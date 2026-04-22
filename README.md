# compschema

**Compile-time JSON Schema generator for Go.**

compschema generates JSON Schema documents, `Validate`, and `Decode` functions from Go types using static analysis (`go/ast` + `go/types`) — zero reflection at runtime. It also imports JSON Schema and OpenAPI specs into Go types with full constraint preservation.

> **100% structural round-trip** on the OpenAI Responses API (124 types, 600 fields, 0 diffs).

## Install

```bash
go install github.com/codewandler/compschema/cmd/compschema@latest
```

## Quick start

```bash
# Generate JSON Schema + Validate + Decode + tests from Go types
compschema generate ./...

# Import JSON Schema into Go structs (with sealed interfaces + constraints)
compschema import --package models --out models/types.go schema.json

# Extract JSON Schema from an OpenAPI spec
compschema extract --spec openapi.yaml --path /responses --validate --out schema.json

# Compare two JSON Schemas (structural diff)
compschema diff --ir ground-truth.json generated.json
```

### Using with `go:generate`

Add a generate directive to your package:

```go
//go:generate compschema generate ./...

//compschema:generate
type Order struct {
    ID     string      `json:"id"`
    Items  []LineItem  `json:"items" jsonschema:"minItems=1"`
    Status OrderStatus `json:"status"`
    Notes  *string     `json:"notes,omitempty"`
}
```

Then run:

```bash
go generate ./...
```

This produces three files:

| File | Contents |
|------|----------|
| `schema.gen.json` | JSON Schema (draft 2020-12) with all types as `$defs` |
| `compschema.gen.go` | `JSONSchemaBytes()`, `Validate()`, `DecodeT()` per type |
| `compschema.gen_test.go` | Smoke tests (schema validity, validation, round-trip) |

## CLI commands

### `compschema generate`

Analyzes Go packages via `go/ast` + `go/types`, builds a Schema IR, and emits JSON Schema + Go code + tests.

```bash
compschema generate ./...                      # annotated types only
compschema generate --all ./...                # all exported types
compschema generate --all --validate ./models/ # with meta-schema validation
```

| Flag | Description |
|------|-------------|
| `--all` | Analyze all exported types, not just `//compschema:generate` annotated |
| `--validate` | Validate generated schema against JSON Schema meta-schema |
| `--out` | Output directory (default: package source dir) |

Generated API per struct type:

```go
func (Order) JSONSchemaBytes() []byte       // $defs entry (from go:embed, lazily cached)
func (Order) Validate(data []byte) error    // compiled schema per type (sync.Once)
func DecodeOrder(data []byte) (Order, error) // validate-then-unmarshal
```

All generated struct types implement `compschema.Schema`:

```go
import "github.com/codewandler/compschema"

func Handle[T compschema.Schema](data []byte) (T, error) {
    var zero T
    if err := zero.Validate(data); err != nil {
        return zero, err
    }
    var result T
    json.Unmarshal(data, &result)
    return result, nil
}
```

### `compschema import`

Generates Go structs from JSON Schema — replaces `go-jsonschema` with full constraint and union preservation.

```bash
compschema import --package models --out models/types.go schema.json
```

| Flag | Description |
|------|-------------|
| `--package` | Go package name (required) |
| `--out` | Output Go file path (required) |

What it generates:

- **Structs** with `json` tags and `jsonschema` constraint tags
- **Sealed interfaces** for `oneOf`/`anyOf` unions with `//compschema:generate` annotations
- **Enum types** with const blocks (extracted from inline enums via `title`)
- **Wrapper types** for primitive union variants (`oneOf(string, number)` → `XString{Value string}`)
- **Field descriptions** in both comments and `jsonschema:"description=..."` tags
- **Nullable** as `*T` pointers

### `compschema extract`

Converts OpenAPI 3.x Schema Objects into self-contained JSON Schema (draft 2020-12).

```bash
compschema extract --spec openapi.yaml --path /responses --validate --out schema.json
compschema extract --spec openapi.yaml --schema CreateResponse --validate
```

| Flag | Description |
|------|-------------|
| `--spec` | Path to OpenAPI YAML/JSON file (required) |
| `--path` | API path prefix to extract (e.g. `/responses`) |
| `--schema` | Single component schema name to extract |
| `--validate` | Validate output against JSON Schema meta-schema |
| `--out` | Output file (default: stdout) |

The converter handles every OpenAPI 3.x Schema Object keyword — see [Supported keywords](#supported-keywords) below.

### `compschema diff`

Structurally compares two JSON Schema documents.

```bash
compschema diff ground-truth.json generated.json        # JSON-level comparison
compschema diff --ir ground-truth.json generated.json   # IR-level (normalized)
```

| Flag | Description |
|------|-------------|
| `--ir` | Compare via Schema IR — normalizes `$ref` vs inline, ordering, nullable representation |

The IR-level diff eliminates noise (array ordering, `$ref` vs inline, whitespace, nullable representation) and reports only true structural differences.

### `compschema schemas`

Lists all component schema names in an OpenAPI spec.

```bash
compschema schemas --spec openapi.yaml
```

## Multi-API round-trip results

compschema is tested against 13 real-world OpenAPI specs. The full pipeline runs:
`extract → import → compile → generate → diff --ir`

| API | Schemas | Go types | IR match | Fields |
|-----|:-------:|:--------:|:--------:|:------:|
| **Petstore** | 6 | 8 | **100.0%** | 27 |
| **Twilio** | 97 | 135 | **100.0%** | 649 |
| **Spotify** | 86 | 214 | **100.0%** | 669 |
| **Asana** | 3 | 4 | **100.0%** | 10 |
| **OpenAI** | 124 | 263 | **100.0%** | 600 |
| **Kubernetes** | 251 | 475 | **99.8%** | 1,133 |
| **GitHub** | 447 | 902 | **98.7%** | 3,635 |
| **Discord** | 402 | 588 | **96.9%** | 2,077 |
| **Plaid** | 2,019 | 2,058 | **96.2%** | 7,394 |
| **Stripe** | 1,382 | 3,010 | **93.2%** | 5,860 |
| Box | 286 | 570 | — | stack overflow (self-ref structs) |
| **Cloudflare** | 4,309 | 5,980 | **79.2%** | 9,267 |

**11 of 11 APIs pass the full pipeline.** 5 achieve 100% structural field match.
**Total: 9,288 schemas, 14,791 Go types, 34,987 fields validated.**

Run the full suite: `bash testdata/specs/run_all.sh`

## `jsonschema` struct tag

compschema reads the `jsonschema:"..."` struct tag (compatible with `invopop/jsonschema`):

```go
type User struct {
    Name  string `json:"name"  jsonschema:"minLength=1,description=The user's full name"`
    Email string `json:"email" jsonschema:"format=email"`
    Age   int    `json:"age"   jsonschema:"minimum=0,maximum=150"`
}
```

### Supported tag keywords

| Keyword | Example | JSON Schema output |
|---------|---------|-------------------|
| `minimum` | `minimum=0` | `"minimum": 0` |
| `maximum` | `maximum=100` | `"maximum": 100` |
| `exclusiveMinimum` | `exclusiveMinimum=0` | `"exclusiveMinimum": 0` |
| `exclusiveMaximum` | `exclusiveMaximum=100` | `"exclusiveMaximum": 100` |
| `multipleOf` | `multipleOf=5` | `"multipleOf": 5` |
| `minLength` | `minLength=1` | `"minLength": 1` |
| `maxLength` | `maxLength=255` | `"maxLength": 255` |
| `pattern` | `pattern=^[a-z]+$` | `"pattern": "^[a-z]+$"` |
| `format` | `format=email` | `"format": "email"` |
| `minItems` | `minItems=1` | `"minItems": 1` |
| `maxItems` | `maxItems=10` | `"maxItems": 10` |
| `uniqueItems` | `uniqueItems` | `"uniqueItems": true` |
| `const` | `const=circle` | `"const": "circle"` |
| `title` | `title=User Name` | `"title": "User Name"` |
| `description` | `description=Full name` | `"description": "Full name"` |
| `default` | `default=active` | `"default": "active"` |
| `readOnly` | `readOnly` | `"readOnly": true` |
| `writeOnly` | `writeOnly` | `"writeOnly": true` |
| `deprecated` | `deprecated` | `"deprecated": true` |

Enum types are detected automatically from `const` blocks:

```go
type Status string
const (
    StatusActive  Status = "active"
    StatusPending Status = "pending"
)
// → {"type": "string", "enum": ["active", "pending"]}
```

Sealed unions are detected from interface patterns:

```go
//compschema:generate
type Shape interface { isShape() }

type Circle struct {
    Type   string  `json:"type" jsonschema:"const=circle"`
    Radius float64 `json:"radius" jsonschema:"minimum=0"`
}
func (*Circle) isShape() {}

// → {"oneOf": [{"$ref": "#/$defs/Circle"}, ...], "discriminator": {"propertyName": "type"}}
```

## Supported keywords

### OpenAPI → JSON Schema converter

Every field on the OpenAPI 3.x Schema Object is explicitly handled:

**Converted:** `allOf` (with object flattening), `oneOf`, `anyOf`, `not`, `if`/`then`/`else`, `dependentSchemas`, `dependentRequired`, `type`, `nullable` (→ type array), `format`, `title`, `description`, `$comment`, `deprecated`, `readOnly`, `writeOnly`, `default`, `enum`, `const`, `example` (→ `examples`), `examples`, `properties`, `patternProperties`, `additionalProperties`, `unevaluatedProperties`, `propertyNames`, `required`, `minProperties`, `maxProperties`, `items`, `prefixItems`, `unevaluatedItems`, `contains`, `minContains`, `maxContains`, `minItems`, `maxItems`, `uniqueItems`, `minimum`, `maximum`, `exclusiveMinimum` (3.0 bool → number), `exclusiveMaximum`, `multipleOf`, `minLength`, `maxLength`, `pattern`, `contentEncoding`, `contentMediaType`, `contentSchema`, `$ref`, `$recursiveRef`, `$id`, `$anchor`, `$dynamicAnchor`, `$dynamicRef`, `discriminator` (→ `x-discriminator`), `x-*` extensions.

**Stripped (OpenAPI-only):** `xml`, `externalDocs`, `$schema` (nested), `$vocabulary`, `nullable` (merged into type).

## Project layout

```
schema.go                      compschema.Schema interface (importable)
cmd/compschema/                CLI (cobra): extract, import, generate, diff, schemas
internal/ir/                   Schema IR types (Type, Field, Constraint, Variant, TypeRef)
internal/analyzer/             Go types → IR (go/packages + go/types)
internal/emitter/              IR → JSON Schema + Go codegen + tests
internal/importer/             JSON Schema → Go structs (with sealed interfaces + constraints)
internal/jsonschema2ir/        JSON Schema → IR parser (for diff + import)
internal/openapi2jsonschema/   OpenAPI → JSON Schema converter (exhaustive field coverage)
internal/uniongen/             Sealed interface generation for oneOf/anyOf unions (IR-based)
internal/schemadiff/           JSON Schema + IR structural diff
examples/basic/                Basic example (Order, LineItem, Shape union — 23 tests)
examples/openai/               OpenAI Responses API (261 types — 587 tests)
testdata/specs/               Multi-API test suite (11 OpenAPI specs)
docs/DESIGN.md                Architecture, IR design, validation strategy
CHANGELOG.md                  Version history
```

## Limitations

### Nullable union-as-variant pattern

When a JSON Schema type is defined as a nullable union (`anyOf` / `oneOf` wrapping other unions), and that type is used as a variant of another union, Go cannot express this. Go interfaces cannot implement other interfaces' marker methods via pointer receivers.

```
// JSON Schema:
// ParentUnion: oneOf(ChildUnion, OtherType)
// ChildUnion: anyOf(StructA, StructB)
//
// Go cannot do:
// type ChildUnion interface { isChildUnion() }
// func (*ChildUnion) isParentUnion() {}  ← invalid: pointer to interface
```

This affects specs with deeply self-referencing types. compschema handles these via cycle detection — self-referencing fields resolve to `$ref` and fixture generation returns `nil` for cycles. All 11 tested APIs now pass including Cloudflare (4,309 schemas).

### OpenAPI v2 (Swagger)

Only OpenAPI 3.x is supported. Swagger 2.0 specs (DigitalOcean, some older APIs) need to be converted to OpenAPI 3.x first using tools like [swagger2openapi](https://github.com/Mermade/oas-kit).

### Circular references in OpenAPI

Deeply circular `$ref` chains in OpenAPI specs may cause warnings from the underlying parser (`libopenapi`). The converter handles these gracefully—the model is still usable—but some schemas may be incomplete.

### Enum const name collisions

When a schema name produces the same Go identifier as an enum constant (e.g. `source_type_ach_credit_transfer` collides with `SourceType` enum value `ach_credit_transfer`), the struct type is skipped and fields referencing it fall back to `any`. This primarily affects large specs like Stripe where naming conventions create ambiguity.

### Type name normalization

Schemas with dotted names (`io.k8s.api.core.v1.Pod`) are normalized to Go identifiers (`IoK8sAPICoreV1Pod`). The IR diff uses fuzzy name matching to handle this, but ~1-3% of schemas may not match across the round-trip due to naming differences.

## Roadmap

### Planned

- [ ] **Swagger v2 import** — convert Swagger 2.0 specs to OpenAPI 3.x for processing. Currently only OpenAPI 3.x is supported.
- [ ] **Generics** — `type Page[T any] struct { Items []T }` should produce a concrete schema when instantiated (e.g. `Page[User]`). The IR already has a `Generic` node type; the analyzer needs `go/types.TypeParam` support.
- [ ] **Single-pass Decode** — currently `DecodeT()` does validate-then-unmarshal (two JSON parses). A generated bespoke decoder could validate constraints during a single `json.Decoder` pass for ~2× performance.
- [ ] **Real-world integration test** — use compschema as the schema layer in an actual API project to validate the developer experience end-to-end.
- [ ] **OpenAPI output** — emit OpenAPI 3.1 components from the IR (the reverse of `extract`). The IR is already language-agnostic; this is a new emitter.
- [ ] **TypeScript output** — emit TypeScript type definitions from the same IR.
- [ ] **Stripe full support** — resolve the nullable-union-as-variant pattern, possibly via union flattening or wrapper struct generation for interface variants.

### Non-goals (v1)

- Non-JSON encodings (YAML, CBOR, etc.)
- Runtime schema manipulation API
- Full JSON Schema test suite compliance (we validate against 13 real-world APIs instead)

## License

MIT
