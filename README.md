<p align="center">
  <h1 align="center">compschema</h1>
  <p align="center">
    <strong>Compile-time JSON Schema for Go — zero reflection, full type safety.</strong>
  </p>
  <p align="center">
    <a href="#install">Install</a> · <a href="#quick-start">Quick Start</a> · <a href="#why-compschema">Why compschema</a> · <a href="#cli-commands">CLI</a> · <a href="#multi-api-round-trip-results">Benchmarks</a>
  </p>
</p>

---

compschema generates JSON Schema documents, `Validate()`, and type-safe `Decode()` functions from Go types using static analysis — **zero runtime reflection, zero manual schema files**. It also imports JSON Schema and OpenAPI specs into idiomatic Go with full constraint preservation.

> **100% structural round-trip fidelity** on the OpenAI Responses API — 301 types, 600+ fields, 0 diffs.

```bash
go install github.com/codewandler/compschema/cmd/compschema@latest
```

## Why compschema

### 🚀 Zero reflection, compile-time everything

Unlike `invopop/jsonschema` or `swaggo/swag`, compschema runs at **build time** via `go/ast` + `go/types`. Your binary ships with pre-compiled schemas embedded via `go:embed` — no reflection overhead, no `sync.Once` initialization tax on first request, no surprises in production.

### 🎯 The only Go tool with real union support

Go doesn't have discriminated unions — but your APIs do. compschema detects **sealed interface patterns** and generates proper `oneOf` schemas with automatic discriminator detection. Generated `Decode()` functions use a **3-tier dispatch strategy**:

1. **O(1) discriminator switch** — when variants have distinct `const`/enum values
2. **O(keys) required-set matching** — when variants differ by required properties
3. **O(n) try-each fallback** — only when nothing else works

No other Go schema tool does this.

### 📐 Battle-tested against 35,000 fields

Not toy examples — compschema is validated against **11 real-world OpenAPI specs** including OpenAI, Stripe, Kubernetes, GitHub, and Cloudflare. The full pipeline runs `extract → import → compile → generate → diff` and proves structural correctness at scale.

### 🔄 True bidirectional: Go ↔ JSON Schema ↔ OpenAPI

Most tools go one direction. compschema goes **both ways** — and proves it:

```
Go types ──→ JSON Schema ──→ Go types ──→ JSON Schema
                ↑                              │
                └──── diff: 0 structural gaps ──┘
```

Import any OpenAPI spec, get idiomatic Go. Write Go types, get spec-compliant JSON Schema. Round-trip it. **It matches.**

### ⚡ Smart caching, incremental builds

Every IR node is **Merkle-hashed**. compschema skips regeneration when your types haven't changed — even in monorepos with hundreds of packages. Cache hits are instant.

### 🧪 Generated tests you didn't have to write

Every `generate` run produces a test file alongside your code. Schema validity, constraint enforcement, round-trip decoding — **669+ test functions** generated automatically. Your CI catches schema drift without you lifting a finger.

---

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

### What you get per type

```go
// Schema access — embedded via go:embed, lazily cached via sync.Map
func (Order) JSONSchemaBytes() json.RawMessage

// Validation — compiled schema per type (sync.Once), zero-alloc after first call
func (Order) Validate(data []byte) error

// Type-safe decode — validate-then-unmarshal in one call
func DecodeOrder(data []byte) (Order, error)
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

### Unions — first-class, type-safe

Define a sealed interface, compschema does the rest:

```go
//compschema:generate
type Shape interface { isShape() }

type Circle struct {
    Type   string  `json:"type" jsonschema:"const=circle"`
    Radius float64 `json:"radius" jsonschema:"minimum=0"`
}
func (*Circle) isShape() {}

type Rectangle struct {
    Type   string  `json:"type" jsonschema:"const=rectangle"`
    Width  float64 `json:"width"`
    Height float64 `json:"height"`
}
func (*Rectangle) isShape() {}
```

Generated output:

```go
// O(1) discriminator dispatch — no trial-and-error
func DecodeShape(data []byte) (Shape, error)

// Type-safe variant extraction — like errors.As
func ShapeAs[T any, P interface{ *T; Shape }](v Shape, target *T) bool
```

```json
{
  "oneOf": [
    { "$ref": "#/$defs/Circle" },
    { "$ref": "#/$defs/Rectangle" }
  ],
  "discriminator": { "propertyName": "type" }
}
```

---

## CLI commands

### `compschema generate`

Analyzes Go packages via `go/ast` + `go/types`, builds a Schema IR, and emits JSON Schema + Go code + tests.

```bash
compschema generate ./...                      # annotated types only
compschema generate --all ./...                # all exported types
compschema generate --all --validate ./models/ # with meta-schema validation
compschema generate --examples --test ./...    # with examples + run tests
```

| Flag | Description |
|------|-------------|
| `--all` | Analyze all exported types, not just `//compschema:generate` annotated |
| `--validate` | Validate generated schema against JSON Schema meta-schema |
| `--examples` | Add generated example values to schema `$defs` |
| `--test` | Run generated tests and report pass/fail/skip |
| `--emit-ir` | Write `schema.gen.ir.yaml` with human-readable IR + Merkle hashes |
| `--no-cache` | Force full regeneration (skip hash cache) |
| `--out` | Output directory (default: package source dir) |

### `compschema import`

Generates Go structs from JSON Schema — with full constraint and union preservation.

```bash
compschema import --package models --out models/types.go schema.json
```

| Flag | Description |
|------|-------------|
| `--package` | Go package name (required) |
| `--out` | Output Go file path (required) |
| `--tags` | Additional struct tags (e.g. `--tags yaml,db`) |
| `--rename` | Rename types (e.g. `--rename OldName=NewName`) |
| `--exclude` | Glob patterns to skip types (e.g. `--exclude 'Response*Event'`) |

What it generates:

- **Structs** with `json` tags and `jsonschema` constraint tags
- **Sealed interfaces** for `oneOf`/`anyOf` unions with `//compschema:generate` annotations
- **Enum types** with const blocks (extracted from inline enums via `title`)
- **Wrapper types** for primitive union variants (`oneOf(string, number)` → `XString{Value string}`)
- **Nullable** as `*T` pointers
- **`UnmarshalJSON`** dispatchers for structs with union fields

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

The converter handles **every** OpenAPI 3.x Schema Object keyword — see [Supported keywords](#openapi--json-schema-converter) below.

### `compschema diff`

Structurally compares two JSON Schema documents.

```bash
compschema diff ground-truth.json generated.json        # JSON-level comparison
compschema diff --ir ground-truth.json generated.json   # IR-level (normalized)
```

| Flag | Description |
|------|-------------|
| `--ir` | Compare via Schema IR — normalizes `$ref` vs inline, ordering, nullable representation |

### `compschema run`

Config-driven multi-source pipelines via `.compschema.yaml`.

```bash
compschema run <pipeline>     # run a named pipeline
compschema run specs          # run multi-API test suite
```

### `compschema schemas`

Lists all component schema names in an OpenAPI spec.

```bash
compschema schemas --spec openapi.yaml
```

---

## Multi-API round-trip results

compschema is tested against **11 real-world OpenAPI specs**. The full pipeline runs:
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
| **Cloudflare** | 4,309 | 5,980 | **79.2%** | 9,267 |

**11 of 11 APIs pass the full pipeline.** 5 achieve 100% structural field match.

**Total: 9,288 schemas → 14,791 Go types → 34,987 fields validated.**

---

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

---

## Supported keywords

### OpenAPI → JSON Schema converter

Every field on the OpenAPI 3.x Schema Object is explicitly handled:

**Converted:** `allOf` (with object flattening), `oneOf`, `anyOf`, `not`, `if`/`then`/`else`, `dependentSchemas`, `dependentRequired`, `type`, `nullable` (→ type array), `format`, `title`, `description`, `$comment`, `deprecated`, `readOnly`, `writeOnly`, `default`, `enum`, `const`, `example` (→ `examples`), `examples`, `properties`, `patternProperties`, `additionalProperties`, `unevaluatedProperties`, `propertyNames`, `required`, `minProperties`, `maxProperties`, `items`, `prefixItems`, `unevaluatedItems`, `contains`, `minContains`, `maxContains`, `minItems`, `maxItems`, `uniqueItems`, `minimum`, `maximum`, `exclusiveMinimum` (3.0 bool → number), `exclusiveMaximum`, `multipleOf`, `minLength`, `maxLength`, `pattern`, `contentEncoding`, `contentMediaType`, `contentSchema`, `$ref`, `$recursiveRef`, `$id`, `$anchor`, `$dynamicAnchor`, `$dynamicRef`, `discriminator` (→ `x-discriminator`), `x-*` extensions.

**Stripped (OpenAPI-only):** `xml`, `externalDocs`, `$schema` (nested), `$vocabulary`, `nullable` (merged into type).

---

## How it works

```
                    ┌─────────────────────────────────────────────┐
                    │              Schema IR                       │
                    │  (language-agnostic, Merkle-hashed)          │
                    └──────────┬──────────────────┬───────────────┘
                               │                  │
              ┌────────────────┤                  ├────────────────┐
              ▼                ▼                  ▼                ▼
        JSON Schema      Go codegen         Test codegen      IR YAML
       (draft 2020-12)   (Validate,         (669+ tests,     (debug +
                          Decode,            auto-generated)   diff)
                          JSONSchemaBytes)
              ▲                                                    ▲
              │                                                    │
    ┌─────────┴─────────┐                              ┌──────────┴──────────┐
    │  Go Analyzer       │                              │  JSON Schema Parser  │
    │  (go/ast + go/types│                              │  (import + diff)     │
    └────────────────────┘                              └─────────────────────┘
```

The **Schema IR** is the secret sauce — a normalized, language-agnostic type graph that decouples analysis from emission. This is why compschema can go both directions (Go → Schema, Schema → Go) and prove correctness via IR-level diff.

---

## Project layout

```
schema.go                      compschema.Schema interface (importable)
cmd/compschema/                CLI (cobra): extract, import, generate, diff, schemas, run
internal/ir/                   Schema IR types + Merkle hashing
internal/analyzer/             Go types → IR (go/packages + go/types)
internal/emitter/              IR → JSON Schema + Go codegen + tests + IR YAML
internal/importer/             JSON Schema → Go structs (with sealed interfaces + constraints)
internal/jsonschema2ir/        JSON Schema → IR parser (for diff + import)
internal/openapi2jsonschema/   OpenAPI → JSON Schema converter (exhaustive field coverage)
internal/uniongen/             Sealed interface generation for oneOf/anyOf unions (IR-based)
internal/schemadiff/           JSON Schema + IR structural diff
internal/config/               Pipeline config format + template expansion
internal/source/               Source abstraction (file, HTTP, Git) with content hashing
internal/report/               Structured pipeline reports with metrics
internal/cache/                File-backed hash cache for incremental compilation
examples/basic/                Basic example (Order, LineItem, Shape union)
examples/openai/               OpenAI Responses API (301 types, 864 generated tests)
testdata/specs/                Multi-API test suite (11 OpenAPI specs)
docs/DESIGN.md                Architecture, IR design, validation strategy
CHANGELOG.md                   Version history
```

## Limitations

### Nullable union-as-variant pattern

When a JSON Schema type is defined as a nullable union (`anyOf` / `oneOf` wrapping other unions), and that type is used as a variant of another union, Go cannot express this. Go interfaces cannot implement other interfaces' marker methods via pointer receivers.

This affects specs with deeply self-referencing types. compschema handles these via cycle detection — self-referencing fields resolve to `$ref` and fixture generation returns `nil` for cycles. All 11 tested APIs pass including Cloudflare (4,309 schemas).

### OpenAPI v2 (Swagger)

Only OpenAPI 3.x is supported. Swagger 2.0 specs need to be converted first using tools like [swagger2openapi](https://github.com/Mermade/oas-kit).

### Circular references in OpenAPI

Deeply circular `$ref` chains in OpenAPI specs may cause warnings from the underlying parser (`libopenapi`). The converter handles these gracefully — the model is still usable — but some schemas may be incomplete.

### Enum const name collisions

When a schema name produces the same Go identifier as an enum constant, the struct type is skipped and fields referencing it fall back to `any`. This primarily affects large specs like Stripe.

---

## Roadmap

- [ ] **Generics** — `type Page[T any] struct { Items []T }` with concrete schema instantiation
- [ ] **Single-pass Decode** — validate during unmarshal for ~2× decode performance
- [ ] **Swagger v2 import** — auto-convert Swagger 2.0 specs
- [ ] **OpenAPI output** — emit OpenAPI 3.1 components from the IR
- [ ] **TypeScript output** — emit TypeScript type definitions from the same IR

---

## License

MIT
