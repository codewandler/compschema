# Design: Validate, Decode, and JSONSchemaBytes

Status: **Decided** — not yet implemented.

## Decisions

| # | Question | Decision |
|---|----------|----------|
| 1 | Schema file granularity | One file per package (`schema.gen.json`) |
| 2 | `JSONSchemaBytes()` returns | The `$defs` entry for that type (just the type's own schema) |
| 3 | Validation library | `santhosh-tekuri/jsonschema/v6` (already a dependency) |
| 4 | Decode strategy | Validate-then-unmarshal (two-pass); optimize later |
| 5 | API surface | Methods for `Validate`/`JSONSchemaBytes`, free function for `Decode` |
| 6 | Interface contract | `compschema.Schema` interface |
| 7 | Schema compilation | Lazy via `sync.Once` on first `Validate`/`Decode` call |

## Generated files

For a package with annotated types, `compschema generate` produces:

```
models/
  order.go                  ← user-written types
  schema.gen.json           ← JSON Schema document (all types as $defs)
  compschema.gen.go         ← Validate, Decode, JSONSchemaBytes
```

### `schema.gen.json`

Single file per package. Standard JSON Schema draft 2020-12 with every annotated type as a `$defs` entry:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$defs": {
    "Order": {
      "type": "object",
      "properties": {
        "id": { "type": "string" },
        "items": { "type": "array", "items": { "$ref": "#/$defs/LineItem" }, "minItems": 1 },
        "status": { "$ref": "#/$defs/OrderStatus" }
      },
      "required": ["id", "items", "status"],
      "additionalProperties": false
    },
    "LineItem": { ... },
    "OrderStatus": { "type": "string", "enum": ["pending", "confirmed", "shipped"] }
  }
}
```

This file is:
- Committable and diffable in version control
- Validated against the meta-schema during generation
- The single source of truth for all downstream consumers

### `compschema.gen.go`

```go
package models

import (
    _ "embed"
    "encoding/json"
    "sync"

    "github.com/santhosh-tekuri/jsonschema/v6"
)

// ── Schema embedding ───────────────────────────────────────────────

//go:embed schema.gen.json
var compschemaJSON []byte

var compschemaOnce sync.Once
var compschemaCompiled *jsonschema.Schema

func compschemaSchema() *jsonschema.Schema {
    compschemaOnce.Do(func() {
        c := jsonschema.NewCompiler()
        if err := c.AddResource("schema.json", bytes.NewReader(compschemaJSON)); err != nil {
            panic("compschema: compile schema: " + err.Error())
        }
        sch, err := c.Compile("schema.json")
        if err != nil {
            panic("compschema: compile schema: " + err.Error())
        }
        compschemaCompiled = sch
    })
    return compschemaCompiled
}

// compileDef compiles a single $defs entry for validation.
func compileDef(name string) *jsonschema.Schema {
    c := jsonschema.NewCompiler()
    c.AddResource("schema.json", bytes.NewReader(compschemaJSON))
    sch, err := c.Compile("schema.json#/$defs/" + name)
    if err != nil {
        panic("compschema: compile " + name + ": " + err.Error())
    }
    return sch
}

// ── Per-type: JSONSchemaBytes ──────────────────────────────────────

// defBytes lazily extracts and caches the $defs/<name> subtree as JSON.
var defCache sync.Map

func defBytes(name string) []byte {
    if v, ok := defCache.Load(name); ok {
        return v.([]byte)
    }
    var full struct {
        Defs map[string]json.RawMessage `json:"$defs"`
    }
    json.Unmarshal(compschemaJSON, &full)
    b := full.Defs[name]
    defCache.Store(name, []byte(b))
    return b
}

// JSONSchemaBytes returns the JSON Schema definition for this type.
// This is the $defs entry — the type's own schema without the document wrapper.
// For the full document (with all $defs), use compschemaJSON directly.
func (Order) JSONSchemaBytes() []byte     { return defBytes("Order") }
func (LineItem) JSONSchemaBytes() []byte  { return defBytes("LineItem") }

// ── Per-type: Validate ────────────────────────────────────────────

var orderValidator     *jsonschema.Schema
var orderValidatorOnce sync.Once

// Validate checks whether raw JSON conforms to the Order schema.
func (Order) Validate(data []byte) error {
    orderValidatorOnce.Do(func() {
        orderValidator = compileDef("Order")
    })
    var v any
    if err := json.Unmarshal(data, &v); err != nil {
        return err
    }
    return orderValidator.Validate(v)
}

// ── Per-type: Decode ──────────────────────────────────────────────

// DecodeOrder validates and unmarshals JSON into an Order.
func DecodeOrder(data []byte) (Order, error) {
    var o Order
    if err := o.Validate(data); err != nil {
        return Order{}, err
    }
    if err := json.Unmarshal(data, &o); err != nil {
        return Order{}, err
    }
    return o, nil
}
```

## Key design details

### JSONSchemaBytes — returns the `$defs` entry

`JSONSchemaBytes()` returns the type's own schema definition, extracted from the `$defs` section of the package-level schema:

```go
order := Order{}
fmt.Println(string(order.JSONSchemaBytes()))
// {"type":"object","properties":{"id":{"type":"string"},...},"required":["id",...]}
```

This is the **type's own shape** — not a full document. It may contain `$ref`s to sibling types (e.g. `$ref: "#/$defs/LineItem"`). If consumers need a self-contained document, they read the package-level `compschemaJSON` variable or `schema.gen.json` file directly.

**Why this choice over the full document:**
- Smaller, more useful for programmatic inspection ("what fields does Order have?")
- No duplication — every type doesn't carry the full schema blob
- The full document is available when needed via the package-level export
- Aligns with how types relate to `$defs` in JSON Schema

**Caching:** The `$defs` extraction is done lazily on first call per type and cached via `sync.Map`. The raw bytes come from the same `go:embed` blob — zero allocation for the underlying data.

### Validate — per-type compiled schema

Each type gets its own `*jsonschema.Schema` compiled from `schema.json#/$defs/TypeName`. This is lazy (`sync.Once` per type) so unused types pay nothing.

Validation flow:
1. `json.Unmarshal(data, &v)` — parse into `any` (required by the jsonschema library)
2. `compiled.Validate(v)` — walk the tree against the compiled schema

### Decode — two-pass

```
data → Validate(data) → json.Unmarshal(data, &T) → T
```

Two JSON parses. Simple, correct, and the schema compilation cost is amortized. If benchmarks show this is a bottleneck, a single-pass decoder can be generated from the IR later.

### Interface contract

```go
// package compschema

// Schema is satisfied by types with generated JSON Schema support.
type Schema interface {
    JSONSchemaBytes() []byte
    Validate(data []byte) error
}
```

Enables generic code:

```go
func Handle[T compschema.Schema](data []byte) (T, error) {
    var zero T
    if err := zero.Validate(data); err != nil {
        return zero, err
    }
    var result T
    if err := json.Unmarshal(data, &result); err != nil {
        return zero, err
    }
    return result, nil
}
```

## What this depends on

All of the above requires `compschema generate` — the IR analyzer that reads Go types and emits JSON Schema. The generated code shape is known; the missing piece is the Go-types-to-IR-to-JSON-Schema pipeline.

The pipeline for producing these files:

```
go/ast + go/types → Schema IR → schema.gen.json    (JSON Schema emitter)
                               → compschema.gen.go  (Go codegen emitter)
```

Both emitters read the same IR. The JSON Schema emitter produces the `.json` file. The Go codegen emitter produces the `.go` file with `go:embed`, `Validate`, `Decode`, and `JSONSchemaBytes` — referencing the `.json` file by name.

## Generated tests: `compschema_test.gen.go`

`compschema generate` also emits a test file alongside the generated code:

```
models/
  order.go                    ← user-written types
  schema.gen.json             ← JSON Schema
  compschema.gen.go           ← Validate, Decode, JSONSchemaBytes
  compschema_test.gen.go      ← generated smoke tests
```

These tests run as part of normal `go test ./...` — they catch breakage from type changes, schema drift, and codegen bugs without any user effort.

### What gets generated

```go
package models

import (
    "encoding/json"
    "testing"
)

// ── Schema validity ───────────────────────────────────────────────

func TestCompschema_SchemaIsValidJSON(t *testing.T) {
    // The embedded schema.gen.json must parse as valid JSON.
    var v any
    if err := json.Unmarshal(compschemaJSON, &v); err != nil {
        t.Fatalf("schema.gen.json is not valid JSON: %v", err)
    }
}

func TestCompschema_SchemaIsValidJSONSchema(t *testing.T) {
    // The embedded schema must validate against the JSON Schema
    // draft 2020-12 meta-schema.
    if err := compschemaValidateMetaSchema(compschemaJSON); err != nil {
        t.Fatalf("schema.gen.json is not valid JSON Schema: %v", err)
    }
}

// ── Per-type: JSONSchemaBytes ─────────────────────────────────────

func TestCompschema_Order_JSONSchemaBytes(t *testing.T) {
    b := (Order{}).JSONSchemaBytes()
    if len(b) == 0 {
        t.Fatal("JSONSchemaBytes returned empty")
    }
    // Must be valid JSON.
    var v any
    if err := json.Unmarshal(b, &v); err != nil {
        t.Fatalf("JSONSchemaBytes is not valid JSON: %v", err)
    }
    // Must be an object (schema def is always an object).
    if _, ok := v.(map[string]any); !ok {
        t.Fatalf("JSONSchemaBytes is not a JSON object, got %T", v)
    }
}

// ── Per-type: Validate ────────────────────────────────────────────

func TestCompschema_Order_ValidateRejectsEmpty(t *testing.T) {
    // An empty object should fail validation if the type has required fields.
    err := (Order{}).Validate([]byte(`{}`))
    if err == nil {
        t.Fatal("Validate({}) should fail for Order (has required fields)")
    }
}

func TestCompschema_Order_ValidateRejectsInvalidJSON(t *testing.T) {
    err := (Order{}).Validate([]byte(`{not json}`))
    if err == nil {
        t.Fatal("Validate should reject invalid JSON")
    }
}

func TestCompschema_Order_ValidateRejectsWrongType(t *testing.T) {
    err := (Order{}).Validate([]byte(`"a string"`))
    if err == nil {
        t.Fatal("Validate should reject a string for an object type")
    }
}

// ── Per-type: round-trip ──────────────────────────────────────────

func TestCompschema_Order_RoundTrip(t *testing.T) {
    // Marshal a zero value, then Decode it back.
    // This catches mismatches between the Go type and its schema.
    //
    // Note: this only works if the zero value satisfies required fields.
    // For types where it doesn't, the test verifies that Decode returns
    // a validation error (which is also correct behavior).
    original := Order{
        // Populated with required fields — generated from schema "required" list.
        ID:     "test-id",
        Items:  []LineItem{{SKU: "ABC-123", Qty: 1}},
        Status: OrderPending,
    }
    data, err := json.Marshal(original)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    decoded, err := DecodeOrder(data)
    if err != nil {
        t.Fatalf("decode: %v", err)
    }
    // Re-marshal and compare.
    data2, _ := json.Marshal(decoded)
    if string(data) != string(data2) {
        t.Errorf("round-trip mismatch:\n  original: %s\n  decoded:  %s", data, data2)
    }
}
```

### Test categories

| Category | Per-package | Per-type | What it catches |
|----------|:-----------:|:--------:|----------------|
| Schema is valid JSON | ✅ | | Corrupted embed, codegen bug |
| Schema is valid JSON Schema (meta-schema) | ✅ | | Schema structure errors |
| `JSONSchemaBytes()` returns valid JSON object | | ✅ | Wrong def name, extraction bug |
| `Validate` rejects `{}` | | ✅ (if has `required`) | Missing required field detection |
| `Validate` rejects invalid JSON | | ✅ | Parse error propagation |
| `Validate` rejects wrong type | | ✅ | Type constraint enforcement |
| Round-trip (marshal → Decode) | | ✅ | Schema/type mismatch, field naming |

### Round-trip test generation

The round-trip test is the most valuable but also the hardest to generate — it needs a valid instance of the type. Three strategies:

**Strategy A: Zero value + required fields only**
Generate a minimal valid instance by setting required fields to their zero/default values:
```go
original := Order{
    ID:     "",              // required, type string → zero value
    Items:  []LineItem{{}},   // required, type array, minItems=1 → one empty item
    Status: "pending",        // required, type string, enum → first enum value
}
```
Pro: Fully automatic. Con: May not pass validation if constraints like `minLength` exist.

**Strategy B: Example from schema**
If the schema has `examples` or `default`, use those:
```go
original := Order{
    ID: "ord_123",  // from schema examples
}
```
Pro: Realistic data. Con: Not always available.

**Strategy C: Validation-only (no round-trip)**
Skip the round-trip entirely, only test that Validate works:
```go
func TestCompschema_Order_ValidateAcceptsKnownGood(t *testing.T) {
    // Known-good JSON embedded as a test fixture.
    err := (Order{}).Validate(knownGoodOrderJSON)
    if err != nil {
        t.Fatalf("Validate rejected known-good JSON: %v", err)
    }
}
```
Pro: Always correct. Con: Needs a fixture.

**Recommendation**: Generate Strategy A (zero+required) tests by default. They won't always pass validation (e.g. `minLength=1` on a required string), but that's fine — the test catches it and the user can either relax the constraint or provide a fixture. The generator can emit a `TODO` comment for types where the zero value is known to violate constraints:

```go
func TestCompschema_Order_RoundTrip(t *testing.T) {
    // TODO: Order has constraints that the zero value may not satisfy.
    // Provide a valid fixture in order_test.go if this test fails.
    t.Skip("auto-generated zero value may not satisfy constraints")
}
```

### Generating the tests from IR

The test emitter has access to the full Schema IR, so it knows:
- Which types have `required` fields → generate `ValidateRejectsEmpty`
- Which types are objects vs scalars → generate `ValidateRejectsWrongType` with the right wrong-type literal
- Which fields have constraints → decide whether round-trip is feasible
- Which fields are enums → use first enum value in test fixtures

This is the same IR that drives JSON Schema and Validate/Decode codegen — tests are just another emitter.
