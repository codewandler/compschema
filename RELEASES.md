# Release Notes

## v0.1.0 — OpenAPI → JSON Schema Pipeline

First release. Establishes the OpenAPI → JSON Schema extraction pipeline with exhaustive field coverage.

### Features
- `compschema extract` — converts OpenAPI 3.x Schema Objects into JSON Schema draft 2020-12
- `compschema schemas` — lists component schema names in an OpenAPI spec
- Meta-schema validation (`--validate` flag)
- Cobra CLI with help, examples, flag validation
- Nullable `anyOf`/`oneOf` simplification: `anyOf(T, {type: null})` → `type: ["T", "null"]`
- `allOf` object composition flattening: merges all-object `allOf` into single structs
- Every OpenAPI 3.x Schema Object keyword explicitly handled (converted, mapped, or documented as stripped)

### Validation
- OpenAI Responses API: 39,848-line spec → 136KB JSON Schema (124 `$defs`, meta-schema valid)
- `make pipeline` orchestrates the full round-trip

---

## v0.2.0 — Union Sealed Interfaces

### Features
- `compschema uniongen` — generates sealed Go interfaces for `oneOf`/`anyOf` unions
- Discriminator detection: explicit `x-discriminator` or inferred from `type` const fields
- `UnmarshalX` functions with discriminator-based dispatch
- `--patch` flag removes conflicting `interface{}` declarations
- `--capitalization` flag matches go-jsonschema naming
- `$recursiveRef` resolution in converter

### Results
- 12 sealed interfaces, 12 unmarshal functions
- `interface{}` reduced from 50 → 12 (76%)

---

## v0.3.0 — compschema generate (Core Pipeline)

The core pipeline works end-to-end: Go types → Schema IR → JSON Schema + Validate + Decode + Tests.

### Features
- `compschema generate` command
- Schema IR (`internal/ir`): Struct, Enum, Union, Scalar, List, Map, Nullable, Ref
- Analyzer (`internal/analyzer`): `go/packages` + `go/types` → IR
- JSON Schema emitter: IR → `schema.gen.json` (draft 2020-12)
- Go codegen emitter: `JSONSchemaBytes()`, `Validate()`, `DecodeT()`
- Test emitter: schema validity, validation rejection, round-trip tests
- `compschema diff` command for structural comparison
- `--all` flag for all exported types

### Results
- OpenAI Responses API: 261 types, 697 tests (674 pass, 23 fail)
- Fixed all failures → 702 pass, 0 fail

---

## v0.4.0 — Full Tag Support + Schema Interface

### Features
- Full `jsonschema:"..."` tag parsing (20+ keywords, invopop-compatible)
- `compschema.Schema` interface: `JSONSchemaBytes()` + `Validate()`
- Struct embedding support
- Title emission from Go type names
- Whitespace-normalized description comparison in diff

### Results
- Diff match rate: 57.6% → 69.4%

---

## v0.5.0 — Field Descriptions + Unit Tests

### Features
- Field comment extraction → JSON Schema `description`
- Analyzer tests (8 tests)
- Emitter tests (3 tests)
- Per-property structural comparison in diff

### Results
- Diff match rate: 69.4% → 70.3%
- 713 total tests

---

## v0.6.0 — Inline Defs + 82.5% Match

### Features
- Inline single-use enum/scalar `$defs` at reference site
- `$ref` merge fix for properties with description alongside `$ref`

### Results
- Diff match rate: 70.3% → **82.5%**
- `$defs` count: 261 → 136 (closer to ground truth's 124)
- Extra `$defs`: 139 → 17

---

## v0.7.0 — compschema import + IR Normalization

### Features
- `compschema import` — generates Go structs from JSON Schema (replaces `go-jsonschema`)
  - Sealed interfaces for unions with `//compschema:generate`
  - Constraint preservation in `jsonschema:"..."` tags
  - Enum types with const blocks
  - Nullable as `*T` pointers
- `compschema diff --ir` — IR-level comparison
- `internal/jsonschema2ir` — JSON Schema → IR parser
- IR diff normalization: nullable, `$ref` resolution, kind unwrapping

### Results
- IR field match: 82.6% → **97.7%** (go-jsonschema pipeline)
- Import pipeline: 96.3% field match, 0 extra types

---

## v0.8.0 — Wrapper Types + 99.2% Match

### Features
- Primitive wrapper types for union variants: `oneOf(string, number)` → `XString{Value string}`
- String enum collapse: `anyOf` of string enums → single enum
- `$ref` resolution during string enum collapsing
- Description comma parsing fix

### Results
- IR field match: 97.7% → **99.2%**
- 490 fields: 486 match, 3 differ, 1 missing

---

## v0.9.0 — 100% IR Field Match 🎉

### Features
- Recursive string enum resolution: follows `$ref` chains through nested `anyOf`
- Union-of-union normalization in IR diff
- Union/enum kind equivalence

### Results
- **100.0% IR field match** — 600 fields, 0 missing, 0 extra, 0 differ
- Full round-trip: OpenAPI → extract → JSON Schema → import → Go → generate → JSON Schema → diff = perfect structural match
