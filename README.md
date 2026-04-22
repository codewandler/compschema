# compschema

**Compile-time JSON Schema generator for Go.**

compschema generates JSON Schema documents, `Decode`, and `Validate` functions from Go types using static analysis (`go/ast` + `go/types`) — zero reflection at runtime.

> **Status**: Working. `compschema generate` produces JSON Schema, `Validate`, `Decode`, and smoke tests from Go types. Validated against the OpenAI Responses API (261 types, 702 tests, 0 failures).

## Quick start

```bash
# Install
go install github.com/codewandler/compschema/cmd/compschema@latest

# Extract JSON Schema from an OpenAPI spec
compschema extract --spec openapi.yaml --path /responses --validate --out responses.schema.json

# List available component schemas
compschema schemas --spec openapi.yaml

# Generate sealed interfaces for unions
compschema uniongen --schema responses.schema.json --package myapi --out unions.gen.go --patch types.go

# Generate JSON Schema + Validate + Decode + tests from Go types
compschema generate ./...

# Generate for all exported types (no annotation needed)
compschema generate --all ./...

# Compare two JSON Schemas
compschema diff ground-truth.json generated.json
```

## CLI commands

### `compschema extract`

Converts OpenAPI 3.x Schema Objects into self-contained JSON Schema (draft 2020-12) documents.

```bash
# Extract all schemas for an endpoint path prefix
compschema extract --spec openapi.yaml --path /responses --validate --out responses.schema.json

# Extract a single named component schema
compschema extract --spec openapi.yaml --schema CreateResponse --validate
```

| Flag | Description |
|------|-------------|
| `--spec` | Path to OpenAPI YAML/JSON file (required) |
| `--path` | API path prefix to extract (e.g. `/responses`) |
| `--schema` | Single component schema name to extract |
| `--validate` | Validate output against JSON Schema draft 2020-12 meta-schema |
| `--out` | Output file (default: stdout) |

The converter resolves all `$ref` chains, collects transitive dependencies into `$defs`, and performs several transformations:

- **Nullable simplification**: `anyOf(T, {type: null})` → `type: ["T", "null"]`
- **allOf flattening**: Merges all-object `allOf` compositions into single structs with combined properties
- **`$recursiveRef` resolution**: Detects empty schemas from unresolved `$recursiveRef` and replaces with self-referencing `$ref`
- **OpenAPI 3.0 → 2020-12 conversion**: `nullable` → type arrays, `exclusiveMinimum: true` → number form, `example` → `examples` array

### `compschema schemas`

Lists all component schema names in an OpenAPI spec, grouped by prefix.

```bash
compschema schemas --spec openapi.yaml
```

### `compschema uniongen`

Generates sealed Go interfaces for JSON Schema `oneOf`/`anyOf` unions.

```bash
compschema uniongen \
  --schema responses.schema.json \
  --package openairesponses \
  --out unions.gen.go \
  --patch types.go \
  --capitalization ID,URL,API
```

| Flag | Description |
|------|-------------|
| `--schema` | Path to JSON Schema file (required) |
| `--package` | Go package name (required) |
| `--out` | Output file for generated unions (default: stdout) |
| `--patch` | Go source file to patch — removes conflicting `type X interface{}` declarations |
| `--capitalization` | Comma-separated capitalization rules to match go-jsonschema naming (e.g. `ID,URL,API`) |

For each union type, `uniongen` generates:

1. **Sealed interface** with unexported marker method:
   ```go
   type Tool interface { isTool() }
   func (*FileSearchTool) isTool() {}
   func (*FunctionTool) isTool() {}
   ```

2. **Discriminator-based `UnmarshalX`** (when a discriminator field is detected):
   ```go
   func UnmarshalTool(data []byte) (Tool, error) {
       // dispatches on "type" field → file_search, function, ...
   }
   ```

Discriminators are detected from:
- Explicit `x-discriminator` in the JSON Schema (from OpenAPI's `discriminator` keyword)
- Inferred: if all variants share a property with unique `const`/`enum` values

### `compschema generate`

The core command. Analyzes Go packages via `go/ast` + `go/types`, builds a Schema IR, and emits three files:

```bash
# Generate for annotated types (//compschema:generate)
compschema generate ./models/

# Generate for all exported types
compschema generate --all --validate --out ./models/ ./models/
```

| Flag | Description |
|------|-------------|
| `--all` | Analyze all exported types, not just `//compschema:generate` annotated |
| `--validate` | Validate generated schema against JSON Schema meta-schema |
| `--out` | Output directory (default: package source dir) |

Generated files:

| File | Contents |
|------|----------|
| `schema.gen.json` | JSON Schema (draft 2020-12) with all types as `$defs` |
| `compschema.gen.go` | `JSONSchemaBytes()`, `Validate()`, `DecodeT()` per type |
| `compschema.gen_test.go` | Smoke tests (schema validity, validation, round-trip) |

Generated API per struct type:

```go
// Returns the $defs entry for this type (from go:embed, lazily cached)
func (Order) JSONSchemaBytes() []byte

// Validates raw JSON against the compiled schema (lazy sync.Once per type)
func (Order) Validate(data []byte) error

// Validate-then-unmarshal
func DecodeOrder(data []byte) (Order, error)
```

All generated types implement the `compschema.Schema` interface:

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

### `compschema diff`

Structurally compares two JSON Schema documents and reports gaps.

```bash
compschema diff ground-truth.json generated.json
```

Output:
```
╔══════════════════════════════════════════════════╗
║           JSON Schema Diff Report                ║
╠══════════════════════════════════════════════════╣
║  Keyword match rate:  69.4%                      ║
║    Matched:           308                        ║
║    Structural gaps:   121                        ║
║    Annotation gaps:   58                         ║
╚══════════════════════════════════════════════════╝
```

Classifies gaps as **structural** (affects validation) or **annotation** (metadata only).
Uses order-insensitive comparison for `required`, `enum`, `oneOf` arrays.

## Pipeline

compschema includes a round-trip validation pipeline using the [OpenAI Responses API](https://platform.openai.com/docs/api-reference/responses) as an acceptance test:

```
OpenAI OpenAPI spec                                    compschema
       │                                                    │
       ▼                                                    ▼
  ┌─────────┐    ┌──────────┐    ┌──────────┐    ┌──────────────┐
  │ extract  │───▶│  JSON    │───▶│ Go types │───▶│ JSON Schema  │──▶ diff
  │ --path   │    │  Schema  │    │ + unions │    │ (our output) │
  └─────────┘    └──────────┘    └──────────┘    └──────────────┘
  compschema     ground truth    go-jsonschema     compschema
  extract                       + uniongen         generate (TODO)
```

Run the full pipeline:

```bash
make pipeline
```

```
=== compschema round-trip pipeline ===

  OpenAPI spec:  39,848 lines
  JSON Schema:   136,083 bytes (124 $defs, meta-schema valid ✅)
  Go types:      292 declarations (112 structs, 153 enums)
  Unions:        12 sealed interfaces, 12 unmarshal functions
  compschema:    261 types → 107KB schema → 702 tests (0 failures)
  Diff:          69.4% keyword match rate
```

### Pipeline steps

| Step | Command | Output |
|------|---------|--------|
| Fetch spec | `curl` | `testdata/openai/openapi.yaml` (39,848 lines) |
| Extract | `compschema extract --validate` | `responses.schema.json` (136KB, 124 `$defs`) |
| Go structs | `go-jsonschema --only-models` | `types.go` (304 types, 112 structs) |
| Unions | `compschema uniongen --patch` | `unions.gen.go` (12 sealed interfaces) + patched `types.go` |
| Generate | `compschema generate --all` | `schema.gen.json` (107KB) + `compschema.gen.go` + tests |
| Diff | `compschema diff` | 69.4% keyword match rate |

## Limitations

### Current limitations

- **`compschema generate` not implemented** — the core Go types → JSON Schema pipeline is still in design. The current tooling covers the OpenAPI → Go direction only.
- **Remaining `interface{}`** — 12 usages remain in generated Go code that cannot be resolved by struct generation or sealed interfaces alone:

  | Pattern | Count | Example | Why |
  |---------|-------|---------|-----|
  | Multi-primitive `oneOf` | 1 | `oneOf(string, number, bool)` | Go has no sum types for primitives |
  | Mixed type `anyOf` | 1 | `anyOf(RefType, string)` | Ref + primitive union |
  | `oneOf` in array items | 1 | `items: {oneOf: [...]}` | `go-jsonschema` emits `[]interface{}` |
  | Inline `oneOf` in struct fields | 5 | `oneOf(string, $ref)` or `oneOf(3 $refs)` | Not extracted to named types |
  | Nullable `$ref` | 1 | `anyOf($ref, null)` | `go-jsonschema` doesn't resolve this particular case |
  | Duplicate (merged from allOf) | 3 | `ToolChoice` appears in multiple merged structs | Same underlying `oneOf` |

- **`$recursiveRef`** — handled via heuristic (empty schema → self-reference). Only works for direct self-recursion within the same component schema.
- **Multi-value enum discriminators** — when a variant has multiple enum values (e.g. `["web_search_preview", "web_search_preview_2025_03_11"]`), only the first value is used in the `switch` case.
- **Duplicate discriminator values** — when multiple variants share the same discriminator value (e.g. two types with `type: "message"`), unmarshal tries each in order; the first successful parse wins.

### Design constraints

- Go has no algebraic types / sum types — unions are represented as interfaces with marker methods.
- `go-jsonschema` is a third-party tool with its own limitations — compschema's `generate` command will eventually replace this step entirely.
- OpenAPI Schema Objects are a superset of JSON Schema — some information is lost or transformed during conversion.

## `jsonschema` struct tag

compschema reads the `jsonschema:"..."` struct tag (compatible with `invopop/jsonschema`) for validation constraints and metadata:

```go
type User struct {
    Name  string `json:"name"  jsonschema:"minLength=1,description=The user's full name"`
    Email string `json:"email" jsonschema:"format=email"`
    Age   int    `json:"age"   jsonschema:"minimum=0,maximum=150"`
}
```

### Supported keywords

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
| `minProperties` | `minProperties=1` | `"minProperties": 1` |
| `maxProperties` | `maxProperties=10` | `"maxProperties": 10` |
| `const` | `const=circle` | `"const": "circle"` |
| `title` | `title=User Name` | `"title": "User Name"` |
| `description` | `description=Full name` | `"description": "Full name"` |
| `default` | `default=active` | `"default": "active"` |
| `readOnly` | `readOnly` | `"readOnly": true` |
| `writeOnly` | `writeOnly` | `"writeOnly": true` |
| `deprecated` | `deprecated` | `"deprecated": true` |

Enum types are detected automatically from `const` blocks — no tag needed:

```go
type Status string
const (
    StatusActive  Status = "active"
    StatusPending Status = "pending"
)
// → {"type": "string", "enum": ["active", "pending"]}
```

## OpenAPI → JSON Schema converter

### Supported keywords

Every field on the OpenAPI 3.x Schema Object is explicitly handled — either converted to its JSON Schema equivalent, mapped to an extension, or intentionally stripped with documentation.

#### Composition

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `allOf` | Flattened to single object *or* `allOf` | All-object variants merged into one struct; mixed kept as `allOf` |
| `oneOf` | `oneOf` | `oneOf(T, null)` simplified to nullable type |
| `anyOf` | `anyOf` | `anyOf(T, null)` simplified to nullable type |
| `not` | `not` | |
| `if` / `then` / `else` | `if` / `then` / `else` | 3.1+ / JSON Schema 2020-12 |
| `dependentSchemas` | `dependentSchemas` | 3.1+ |
| `dependentRequired` | `dependentRequired` | 3.1+ |

#### Type & format

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `type` | `type` | Single string or array (3.1) |
| `nullable: true` (3.0) | `type: ["T", "null"]` | Merged into type array per draft 2020-12 |
| `format` | `format` | `date-time`, `uri`, `email`, etc. |

#### Metadata & annotations

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `title` | `title` | |
| `description` | `description` | |
| `$comment` | `$comment` | 3.1+ |
| `deprecated` | `deprecated` | Emitted as `true` when set |
| `readOnly` | `readOnly` | Emitted as `true` when set |
| `writeOnly` | `writeOnly` | Emitted as `true` when set |
| `default` | `default` | |
| `enum` | `enum` | |
| `const` | `const` | |
| `example` (singular, 3.0) | `examples: [value]` | Promoted to array per draft 2020-12 |
| `examples` (array, 3.1) | `examples` | Pass-through |

#### Object keywords

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `properties` | `properties` | `$ref` values resolved to `$defs` |
| `patternProperties` | `patternProperties` | |
| `additionalProperties` | `additionalProperties` | Schema or boolean |
| `unevaluatedProperties` | `unevaluatedProperties` | Schema or boolean, 3.1+ |
| `propertyNames` | `propertyNames` | 3.1+ |
| `required` | `required` | |
| `minProperties` | `minProperties` | |
| `maxProperties` | `maxProperties` | |

#### Array keywords

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `items` | `items` | Schema or boolean (3.1) |
| `prefixItems` | `prefixItems` | Tuple validation, 3.1+ |
| `unevaluatedItems` | `unevaluatedItems` | 3.1+ |
| `contains` | `contains` | 3.1+ |
| `minContains` | `minContains` | 3.1+ |
| `maxContains` | `maxContains` | 3.1+ |
| `minItems` | `minItems` | |
| `maxItems` | `maxItems` | |
| `uniqueItems` | `uniqueItems` | Emitted as `true` when set |

#### Numeric constraints

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `minimum` | `minimum` | |
| `maximum` | `maximum` | |
| `exclusiveMinimum` (3.0 bool) | `exclusiveMinimum` (number) | Converted: `true` + `minimum: N` → `exclusiveMinimum: N` |
| `exclusiveMinimum` (3.1 number) | `exclusiveMinimum` | Pass-through |
| `exclusiveMaximum` (3.0 bool) | `exclusiveMaximum` (number) | Converted: `true` + `maximum: N` → `exclusiveMaximum: N` |
| `exclusiveMaximum` (3.1 number) | `exclusiveMaximum` | Pass-through |
| `multipleOf` | `multipleOf` | |

#### String constraints

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `minLength` | `minLength` | |
| `maxLength` | `maxLength` | |
| `pattern` | `pattern` | |
| `contentEncoding` | `contentEncoding` | JSON Schema content vocabulary |
| `contentMediaType` | `contentMediaType` | JSON Schema content vocabulary |
| `contentSchema` | `contentSchema` | 3.1+ |

#### Identity & references

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `$ref` | `$ref` → `#/$defs/Name` | Resolved from `#/components/schemas/` |
| `$recursiveRef` | `$ref` (self) | Resolved via parent schema context |
| `$id` | `$id` | On sub-schemas only (top-level set by document builder) |
| `$anchor` | `$anchor` | 3.1+ |
| `$dynamicAnchor` | `$dynamicAnchor` | 3.1+ |
| `$dynamicRef` | `$dynamicRef` | 3.1+ |

#### OpenAPI-specific (mapped)

| OpenAPI keyword | Converter output | Notes |
|-----------------|-----------------|-------|
| `discriminator` | `x-discriminator` | Preserved with `propertyName` and `mapping` for downstream union detection |
| `x-*` extensions | `x-*` | All vendor extensions passed through (e.g. `x-stainless-const`, `x-oaiMeta`) |

#### OpenAPI-specific (stripped)

| OpenAPI keyword | Reason |
|-----------------|--------|
| `xml` | XML serialization hint — not relevant for JSON Schema |
| `externalDocs` | OpenAPI documentation link — no JSON Schema equivalent |
| `$schema` (nested) | Dialect declaration — set at document level, not per definition |
| `$vocabulary` | Meta-schema vocabulary — not per-schema |
| `nullable` | Already handled — merged into `type` array |

## Project layout

```
schema.go                      compschema.Schema interface (importable)
cmd/compschema/                CLI (cobra)
  main.go                      Root command
  extract.go                   extract + schemas subcommands
  generate.go                  generate subcommand
  uniongen.go                  uniongen subcommand
  diff.go                      diff subcommand
internal/ir/                   Schema IR types
internal/analyzer/              Go types → IR (go/packages + go/types)
internal/emitter/               IR → JSON Schema + Go codegen + tests
internal/openapi2jsonschema/    OpenAPI → JSON Schema converter
internal/uniongen/              Union sealed interface generator
internal/schemadiff/            JSON Schema structural diff
examples/basic/                Basic example (Order, LineItem, Shape union)
examples/openai/               OpenAI Responses API (261 types, 702 tests)
testdata/openai/               Round-trip pipeline fixtures
```

## License

MIT
