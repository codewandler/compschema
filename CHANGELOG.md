# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.4.0] - 2026-04-22

### Added
- Full `jsonschema:"..."` struct tag support (compatible with invopop/jsonschema):
  description, title, format, default, examples, readOnly, writeOnly,
  deprecated, uniqueItems, multipleOf, minProperties, maxProperties,
  exclusiveMinimum, exclusiveMaximum
- `compschema.Schema` interface (`schema.go`) — `JSONSchemaBytes() []byte` + `Validate([]byte) error`. Enables `Handle[T compschema.Schema]` generic patterns.
- Struct embedding support — embedded fields are flattened into parent struct
- `compschema diff` — structural JSON Schema comparison with per-keyword reporting
- Title emission from Go type names on structs and enums
- Empty schema `{}` for `interface{}`/`any` types in JSON Schema output
- Whitespace-normalized description comparison in diff
- Comprehensive README with CLI docs, tag reference, pipeline numbers

### Changed
- Diff match rate: 57.6% → 69.4% on OpenAI Responses API round-trip
- Annotation gaps reduced from 113 → 58
- All 702 tests passing, 0 failures

## [0.3.0] - 2026-04-22

### Added
- `compschema generate` command — the core pipeline is working end-to-end:
  - Analyzer: reads Go packages via `go/packages` + `go/types`, builds Schema IR
  - Detects `//compschema:generate` annotations and `--all` flag for all exported types
  - Resolves transitive dependencies, enum const blocks, sealed interface unions
  - Handles type aliases, named maps, named slices, `interface{}`/any
- Schema IR (`internal/ir`): Type, Field, Constraint, Variant, TypeRef with Struct, Enum, Union, Scalar, List, Map, Nullable, Ref kinds
- JSON Schema emitter: IR → `schema.gen.json` (draft 2020-12, all types as `$defs`)
- Go codegen emitter: IR → `compschema.gen.go`
  - `(T).JSONSchemaBytes()` — returns `$defs` entry via lazy cache from `go:embed`
  - `(T).Validate([]byte) error` — compiled schema per type via `sync.Once`
  - `DecodeT([]byte) (T, error)` — validate-then-unmarshal
- Test emitter: IR → `compschema.gen_test.go`
  - Schema validity, JSONSchemaBytes, Validate rejects invalid/wrong-type/empty, round-trip
  - Constraint-aware fixture generation (minimum, pattern, minItems)
  - Infeasible fixture detection (union fields, interface{} types) → `t.Skip`
- `compschema diff` command — structurally compares two JSON Schemas
  - Order-insensitive comparison for `required`, `enum`, `oneOf`, `anyOf`, `allOf`
  - Classifies gaps as structural vs annotation
  - Reports per-type keyword match/missing/mismatch
- OpenAI Responses API round-trip: 261 types, 702 tests passing, 23 skipped, 0 failing
- `examples/basic/` — Order, LineItem, OrderStatus, Shape union (23 tests, all pass)
- `examples/openai/` — full OpenAI Responses API (697 tests pass, 23 skip)

### Changed
- `compschema uniongen` output moved from `testdata/` to `examples/`

## [0.1.0] - 2026-04-22

### Added
- `compschema extract` command — extracts JSON Schema (draft 2020-12) from OpenAPI 3.x specs.
- `compschema schemas` command — lists component schema names in an OpenAPI spec.
- `compschema generate` command — stub for Go types → JSON Schema generation.
- `internal/openapi2jsonschema` package — full OpenAPI Schema Object → JSON Schema converter with exhaustive field coverage (see README for keyword tables).
- Meta-schema validation (`--validate` flag) using `santhosh-tekuri/jsonschema/v6`.
- Nullable `anyOf`/`oneOf` simplification: `anyOf(T, {type: null})` → `type: ["T", "null"]`.
- `allOf` object composition flattening: merges all-object `allOf` variants into a single struct.
- OpenAI Responses API round-trip pipeline (`make pipeline`):
  - Fetches OpenAI OpenAPI spec (39,848 lines).
  - Extracts JSON Schema (124 `$defs`, 136KB, meta-schema valid).
  - Generates Go structs via `go-jsonschema` (304 types, 112 structs, 153 enums).
- Cobra CLI with help, examples, and flag validation.
- `Makefile` for pipeline orchestration.
- `PRD.md` — project design document with scope, IR design, and validation strategy.

[Unreleased]: https://github.com/codewandler/compschema/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/codewandler/compschema/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/codewandler/compschema/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/codewandler/compschema/compare/v0.1.0...v0.2.0
