# compschema — Design Document

This document captures the original design decisions, architecture, validation strategy, and implementation details for compschema.

## Problem

Existing Go JSON schema libraries (e.g. `invopop/jsonschema`) rely on runtime reflection to inspect struct types and produce JSON Schema documents. This has several downsides:

- **Runtime cost** — reflection is slow and allocates; schemas are rebuilt on every call or cached with sync overhead.
- **No compile-time safety** — schema mismatches (unsupported types, bad tags) surface only at runtime.
- **Limited union support** — Go's type system has no native unions; reflection-based tools struggle to represent discriminated unions cleanly.
- **No integrated decoding/validation** — schema generation and data validation are separate concerns wired together manually.

## Solution

**compschema** is a Go code generator that runs at `go generate` time. It parses Go source via `go/ast` + `go/types`, extracts type information, and emits:

1. **JSON Schema documents** — spec-compliant (draft 2020-12) `.json` files, one per annotated type.
2. **`Decode([]byte) (T, error)`** — a generated, type-safe function that deserializes JSON into the target struct.
3. **`Validate([]byte) error`** — a generated function that validates raw JSON against the schema without unmarshalling.

All three artifacts are produced at compile time — zero reflection at runtime.

### Schema IR — Intermediate Representation

The generator does **not** go directly from `go/types` → JSON Schema. Instead it builds a **Schema IR** — a self-contained, Go-agnostic type graph that models everything the output stages need:

```
  go/ast + go/types               Schema IR                  Emitters
 ┌────────────────┐          ┌──────────────────┐       ┌──────────────────┐
 │  Analyzer       │─────────▶│  IR Graph         │──────▶│  JSON Schema     │
 │  (Go-specific)  │          │  (language-agnostic│      │  Decode codegen  │
 │                 │          │   type model)     │      │  Validate codegen│
 └────────────────┘          └──────────────────┘       └──────────────────┘
```

**Why an IR?**

- **Separation of concerns** — the analyzer deals with Go's type system quirks (unexported fields, embedding, pointer semantics, generics instantiation); emitters deal only with the IR's clean type model.
- **Testability** — the IR can be snapshot-tested, diffed, and inspected independently of any output format.
- **Multiple backends** — JSON Schema today, but the same IR could emit OpenAPI components, TypeScript types, or protobuf messages tomorrow without touching the analyzer.
- **Generics** — the IR can model type parameters and instantiation sites cleanly, even before the emitters fully support them.
- **Validation pipeline** — the diff step in our OpenAI round-trip test can compare IR graphs directly, not just serialized JSON.

**IR node types (planned):**

| IR Node | Maps from Go | Maps to JSON Schema |
|---------|-------------|---------------------|
| `Struct` | `struct { ... }` | `type: "object"`, `properties`, `required` |
| `Field` | struct field + tags | property entry + constraints |
| `Enum` | `type T string` + const block | `type: "string"`, `enum: [...]` |
| `Union` | sealed interface + implementors | `oneOf: [...]`, optional `discriminator` |
| `List` | `[]T` | `type: "array"`, `items` |
| `Map` | `map[string]T` | `type: "object"`, `additionalProperties` |
| `Ref` | named type / cross-package | `$ref: "#/$defs/..."` |
| `Scalar` | `string`, `int`, `float64`, `bool` | `type: "string"` / `"integer"` / `"number"` / `"boolean"` |
| `Nullable` | `*T` | `type: ["T", "null"]` or `oneOf` with `null` |
| `Generic` | `type Page[T any] struct{...}` | template — expanded at instantiation sites |
| `Constraint` | `jsonschema:"minimum=1"` tag | `minimum`, `pattern`, `maxLength`, etc. |

## Key Design Principles

| Principle | Implication |
|---|---|
| **AST + types, not reflection** | Analysis happens via `go/packages` / `go/types` during code generation. The binary ships no schema logic. |
| **First-class union support** | Sealed interface patterns (`type Shape interface { isShape() }`) are detected and mapped to JSON Schema `oneOf` with an optional discriminator. |
| **Single source of truth** | Go types *are* the schema. No separate `.json` files to maintain; they are generated outputs. |
| **Incremental & fast** | Only re-generate when source types change. Leverage `go/packages` caching. |
| **Minimal API surface** | One annotation (`//compschema:generate`), one CLI/generator entry point, predictable output. |

## Scope

### In Scope

- Structs, nested structs, pointers, slices, maps, type aliases.
- Generic types — `type Page[T any] struct { Items []T }` must produce a concrete schema when instantiated (e.g. `Page[User]`). The IR models generic type definitions as templates with type parameters; instantiation sites are resolved during analysis. Full support may land incrementally, but the IR and analyzer must not preclude generics.
- Discriminated unions via sealed interfaces.
- Standard `json` struct tags (`json:"name,omitempty"`).
- **Tag compatibility with `invopop/jsonschema`** — the `jsonschema:"..."` struct tag is the primary annotation surface (e.g. `jsonschema:"minimum=0,maximum=100,pattern=..."`), ensuring existing codebases can adopt compschema without rewriting tags. compschema may extend the tag vocabulary beyond what invopop supports, but never conflicts.
- `jsonschema_extras` tag support for arbitrary schema keywords, matching invopop conventions.
- Enum types (`type Status string` with const block).
- Generated `Decode` and `Validate` per annotated type.
- JSON Schema draft 2020-12 output.

### Out of Scope (v1)

- Non-JSON encodings (YAML, CBOR, etc.).
- OpenAPI / Swagger wrapper generation.
- Runtime schema manipulation API.

## User Experience

```go
//go:generate compschema ./...

//compschema:generate
type Order struct {
    ID     string      `json:"id"`
    Items  []LineItem  `json:"items"              jsonschema:"minItems=1"`
    Status OrderStatus `json:"status"`
    Notes  *string     `json:"notes,omitempty"`
}

//compschema:generate
type LineItem struct {
    SKU string  `json:"sku"   jsonschema:"pattern=^[A-Z]{3}-[0-9]+$"`
    Qty int     `json:"qty"   jsonschema:"minimum=1"`
}

type OrderStatus string

const (
    OrderPending   OrderStatus = "pending"
    OrderConfirmed OrderStatus = "confirmed"
    OrderShipped   OrderStatus = "shipped"
)
```

Running `go generate` produces:

- `order_schema.gen.json` — full JSON Schema for `Order` (with `$defs` for `LineItem`, `OrderStatus`).
- `order_compschema.gen.go` — contains `OrderDecode([]byte) (Order, error)` and `OrderValidate([]byte) error`.

## Validation Strategy — OpenAI Responses API Round-Trip

To prove completeness against a real-world, complex schema we use the **OpenAI `/v1/responses` API** as our acceptance test:

```
┌──────────────────┐       ┌─────────────────────┐       ┌──────────────────┐
│  OpenAI OpenAPI   │──(1)──▶  JSON Schema (target) │──(2)──▶  Go structs        │
│  spec (YAML)      │       │  (ground truth)       │       │  (generated input) │
└──────────────────┘       └─────────────────────┘       └──────────────────┘
                                                                  │
                                                                (3) compschema
                                                                  │
                                                                  ▼
                                                          ┌──────────────────┐
                                                          │  JSON Schema      │
                                                          │  (our output)     │
                                                          └──────────────────┘
                                                                  │
                                                                (4) diff
                                                                  │
                                                                  ▼
                                                          completeness report
```

### Pipeline Steps

| Step | What | Tooling | Status |
|------|------|---------|--------|
| **(1)** Fetch OpenAI OpenAPI spec | Download `openapi.yaml` from the `manual_spec` branch of `github.com/openai/openai-openapi`. | `curl` → `testdata/openai/openapi.yaml` (39,848 lines) | ✅ Done |
| **(2)** OpenAPI → JSON Schema | Extract `/responses` endpoint request+response schemas and all transitive `$ref` dependencies. Convert OpenAPI 3.0 Schema Objects into JSON Schema draft 2020-12. Resolves `$ref` chains, strips OpenAPI-only keywords, converts `nullable` → `type: ["T", "null"]`. Output is validated against the JSON Schema draft 2020-12 meta-schema. | **Built-in**: `internal/openapi2jsonschema` (Go, uses `pb33f/libopenapi`). CLI: `compschema extract --validate`. Output: 124 `$defs`, 117KB, meta-schema valid ✅. | ✅ Done |
| **(3)** JSON Schema → Go structs | Generate Go types preserving `json` tags, enums, struct nesting. `oneOf`/`anyOf` → `interface{}` (unions not fully expressible in generated Go). | **[`go-jsonschema`](https://github.com/atombender/go-jsonschema)** (`--only-models`). Output: 272 types (102 structs, 134 enum types), 2,426 lines. | ✅ Done |
| **(4)** Go structs → JSON Schema | Run compschema on the generated Go package. **This is the system under test.** | `compschema ./testdata/openai/generated/...` | ⬜ TODO |
| **(5)** Diff & report | Structurally compare step 2 output (ground truth) with step 4 output. Report per-keyword coverage, missing constraints, extra properties. | Custom Go test or `json-diff` CLI. | ⬜ TODO |

All steps are orchestrated via `make pipeline` (see `Makefile`).

### What This Proves

- **Keyword coverage** — every JSON Schema keyword present in the OpenAI spec that survives the Go-struct round-trip must be re-emitted by compschema.
- **Union fidelity** — the Responses API uses heavy `oneOf`/discriminated unions (tool types, content blocks, etc.); this exercises our sealed-interface detection end-to-end.
- **Enum handling** — model names, status values, roles — all must round-trip as `enum` constraints.
- **Real-world scale** — the full Responses schema has 100+ types; this is not a toy test.

### Scoping Note

Perfect round-trip parity is not expected — some information is lost in the OpenAPI→Go→Schema chain (e.g. `description` strings, `example` values, ordering). The diff tool should classify mismatches into:

1. **Structural gaps** (missing `oneOf`, wrong `type`) — bugs in compschema.
2. **Annotation gaps** (missing `description`, `default`) — need richer tags, future work.
3. **Tooling artifacts** (naming differences from go-jsonschema) — expected, ignore.

### Future: Built-In OpenAPI Pipeline

The `internal/openapi2jsonschema` package and `cmd/pipeline` tool are designed to be reusable beyond testing. In the future, compschema could offer a direct `openapi → Go structs + JSON Schema` pipeline:

```
compschema import --openapi openapi.yaml --path /responses --package responses --out ./responses/
```

This would combine steps 1–3 into a single command, generating Go structs with proper `jsonschema:"..."` tags and sealed-interface unions directly from an OpenAPI spec — no intermediate manual step.

## Success Metrics

- **Correctness** — generated schemas pass the JSON Schema Test Suite for all supported keywords.
- **Round-trip coverage** — ≥90% structural keyword match on the OpenAI Responses API schema.
- **Performance** — `Decode` and `Validate` outperform reflection-based equivalents by ≥3×.
- **Adoption friction** — a new user goes from `go install` to first generated schema in < 5 minutes.

---

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
