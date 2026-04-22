# compschema — Compile-Time JSON Schema Generator for Go

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
