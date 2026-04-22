# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [2.3.0] - 2026-04-23

### Added
- **Example validation tests** — `--examples` now generates a table-driven `TestCompschema_ExamplesValidate` test that validates every generated example against its JSON Schema. Proves all examples are schema-compliant.
- **Auto-detect discriminator from const fields** — when all `oneOf` variants share a field with different `const` values, the discriminator is automatically detected. Works in both the analyzer (Go → IR) and the JSON Schema parser (import path). Previously only `json:"type"` was checked.

### Fixed
- Example generator now passes field-level constraints (pattern, format, minimum) to the scalar example generator. Previously constraints on struct fields were ignored, producing values like `"example"` that didn't match the field's `pattern` constraint.
- Discriminator detection in the analyzer no longer hardcodes `"type"` as the only discriminator field name — any field with `const` values across variants is detected.

## [2.2.0] - 2026-04-23

### Added
- **`--examples` flag on `generate`** — adds generated `examples` arrays to JSON Schema `$defs`. The example generator produces realistic values based on type names, constraints (format, minimum, pattern, minLength, minItems), and enum values.
- **`--tags` flag on `import`** — emits additional struct tags on generated Go structs (e.g. `--tags yaml` adds `yaml:"field_name"` alongside `json:"field_name"`). Supports multiple tags: `--tags yaml,db`.
- `Examples` field on config Action for pipeline usage
- `Tags` field on config Action / importer Config for pipeline usage
- `emitter.EmitOptions` struct with `Examples` flag
- `emitter.JSONSchemaWithOptions()` — `JSONSchema()` with configurable options
- `emitter.GenerateExample()` — produces realistic JSON example values from IR types
- `emitter.AddExamples()` — enriches `$defs` map with generated examples
- `importer.GenerateGoWithConfig()` — `GenerateGo()` with Config (tags, etc.)
- 7 new example generator tests + 1 AddExamples test

## [2.1.1] - 2026-04-23

### Changed
- `Schema.JSONSchemaBytes()` return type changed from `[]byte` to `json.RawMessage` — embeds correctly in JSON marshaling instead of being base64-encoded
- All generated `JSONSchemaBytes()` methods and `compschemaDefBytes()` helper updated to match

### Breaking Changes
- `compschema.Schema` interface: `JSONSchemaBytes()` returns `json.RawMessage` instead of `[]byte`. Since `json.RawMessage` is `[]byte` underneath, most code works unchanged — only explicit `[]byte` type assertions on the return value need updating.
## [2.1.0] - 2026-04-23

### Added
- **`compschema run` multi-source pipelines** — array `source` fields expand the pipeline per source with `{name}`, `{source}`, `{hash}` template variables
- **`--test` flag on `generate`** — runs generated tests via `go test -json`, reports pass/fail/skip counts
- **`--emit-ir` flag on `generate`** — writes `schema.gen.ir.yaml` with human-readable IR and Merkle hashes
- **`--no-cache` flag on `generate`** — forces regeneration, skipping the hash cache
- **Pipeline reports** — structured metrics (schemas, types, defs, test results, duration) printed after every pipeline and generate run
- **`--report` flag on `run`** — writes pipeline report to a YAML file
- **Source abstraction** (`internal/source/`) — pluggable sources for pipeline inputs:
  - `FileSource` — local files with SHA-256 content hashing
  - `HTTPSource` — URL fetching with content hashing
  - `GitSource` — git clone + file extraction with commit hash tracking
- **Merkle hashing** (`internal/ir/hash.go`) — deterministic content hashing on all IR nodes (Package, Type, Field, TypeRef, Variant, Constraint) with lazy caching via `sync.Once`
- **Hash cache** (`internal/cache/`) — `.compschema.cache.json` stores per-package IR hashes and per-type hashes for skip-if-unchanged semantics
- **IR YAML emitter** (`internal/emitter/ir_yaml.go`) — serializes IR Package to YAML with `_hash` annotations per type
- **Template expansion** (`internal/config/template.go`) — `{name}`, `{source}`, `{hash}` variable substitution in pipeline actions
- **`.compschema.yaml`** — pipeline config for `openai` and `specs` pipelines, replacing Makefile + run_all.sh
- `build:bin` and `generate:config-schema` Taskfile tasks

### Changed
- `generate` CLI now prints a summary report after completion (types, defs, cached count, test results)
- `run` pipeline generate uses caching — skips packages whose IR hash is unchanged
- Taskfile `pipeline` and `specs` tasks now use `compschema run` instead of manual shell commands
- `Source` field on `config.Action` is polymorphic (`string | map | array`), excluded from JSON Schema validation via `stripSourceFields`
- Config schema regenerated with `Test` and `EmitIR` fields on Action

## [2.0.1] - 2026-04-22

### Fixed
- `type Foo any` aliases no longer get methods generated on them (was causing `invalid receiver type` compile errors)
- Union wrapper types for named enums (e.g. `ToolChoiceOptions` string enum as a `ToolChoiceParam` variant) now get wrapper structs + `UnmarshalJSON`/`MarshalJSON`, enabling correct JSON round-trip for string union variants like `"auto"`
- Wrapper type round-trip tests now skip auto-fixture (wrapper MarshalJSON serializes as inner value, not struct form)

## [2.0.0] - 2026-04-22

### Added
- `compschema run <pipeline>` — config-driven pipeline runner with `.compschema.yaml`
- `internal/config/` — pipeline config format (File, Pipeline, Action) with YAML parser
- Config schema validation: config files are validated against a JSON Schema generated by compschema itself (dogfooding)
- `--rename` flag on `compschema import` — rename types at IR level (e.g. `--rename CompactionBody=CompactionItem`)
- `--exclude` flag on `compschema import` — glob patterns to skip types (e.g. `--exclude 'Response*Event'`)
- `internal/importer/config.go` — `Config` struct with `ApplyConfig()` for rename/exclude IR transforms
- `UnmarshalX([]byte) (X, error)` dispatcher functions for all union interfaces
- `func (s *Struct) UnmarshalJSON([]byte) error` for structs with interface-typed fields
- `MarshalJSON` for primitive wrapper types (e.g. `ToolChoiceString`)
- `ir.Package.Dir` field — populated from `packages.NeedFiles` for correct output directory
- `.agents/plans/feature-0.md` — roadmap for source abstraction, IR emission, Merkle hashing, pipeline reports

### Fixed
- **Bug A+B**: Removed legacy `uniongen` CLI — `compschema import` already generates sealed interfaces, marker methods, and unmarshal dispatchers
- **Bug C**: `compschema generate` now writes to the package source directory by default (was writing to cwd)
- **Bug C**: `--out` directory is created automatically with `os.MkdirAll`
- **Bug D**: Missing `$defs` for named slice types (`type Pipeline []Action`) — `convertType` now passes underlying type to `resolveTypeRef`
- **Bug D**: Missing `$defs` for named pointer types (`type Foo *string`) — added `*types.Pointer` case in `convertType`
- **Bug D**: Missing `$defs` for named empty interfaces (`type Foo interface{}`) — now emitted as `KindScalar/"any"`
- Pointer-to-interface fields (`*SomeInterface`) no longer generated — interfaces are already reference types
- Union unmarshal dispatchers skip non-struct variants (enums, other interfaces) to avoid `&val` on non-addressable types
- Go name collisions between structs and unions detected in `isStructVariant` — prevents invalid pointer receivers
- 11/11 third-party API specs now compile clean (was 9/11)

### Changed
- `internal/uniongen/` rewritten as a reusable package with `TypeResolver` interface — no longer tied to CLI
- `EmitUnion()` now generates sealed interface + marker methods + unmarshal dispatcher + wrapper MarshalJSON in one call
- `GenerateGo()` in importer now emits `import ("encoding/json" "fmt")` only when unions are present
- `GenerateGo()` fourth pass: emits `UnmarshalJSON` for structs with interface fields
- Makefile simplified — uses `compschema import` instead of go-jsonschema + uniongen
- Analyzer loads `packages.NeedFiles` for source directory resolution

### Removed
- `compschema uniongen` CLI command (legacy, replaced by `compschema import`)
- `internal/uniongen/patch.go` (go-jsonschema patching utilities)
- `internal/uniongen/uniongen_test.go` (tests for removed code)
- `testdata/openai/generated/` directory (legacy build artifacts)

### Breaking Changes
- `compschema uniongen` command removed — use `compschema import` which generates sealed interfaces inline
- `internal/uniongen` package API changed: `EmitUnion()` now requires a `TypeResolver` interface
- `importer.Import()` signature unchanged but `ImportWithConfig()` and `ImportFromFileWithConfig()` added

## [1.1.0] - 2026-04-22

### Added
- `Taskfile.yml` with 12 tasks (`task check` runs fmt → vet → lint → test)
- `.golangci.yml` with 10 linters + 2 formatters — 0 issues
- `LICENSE` (MIT)
- `CONTRIBUTING.md` (dev workflow, code style, testing layers)
- `AGENTS.md` (AI agent guidelines: structure, concepts, pitfalls, workflows)
- `.editorconfig`
- `.github/SECURITY.md`
- `.github/ISSUE_TEMPLATE/` (bug + feature templates)
- Generated tests now run in multi-spec pipeline (`testdata/specs/run_all.sh`)

### Fixed
- Kubernetes test failure: inline `KindMap` in fixture generator produced `""` instead of `{}`
- 16 lint issues (unchecked errors, unused code, formatting)

## [1.0.0] - 2026-04-22

### Added
- **11/11 real-world API specs pass** the full round-trip pipeline with 0 failures
- Multi-API test suite: Petstore, Twilio, Spotify, Asana, OpenAI, Kubernetes, Box, GitHub, Discord, Plaid, Stripe, Cloudflare
- 34,987 fields validated across 9,288 schemas and 14,791 Go types
- Cycle detection in analyzer (`resolving` map) and emitter (`visiting` map) — fixes stack overflows on self-referencing types (Box, Cloudflare)
- Stripe support: 1,382 schemas compile and achieve 91% match
  - Self-referencing union variants resolved by Go name scan
  - Inline `KindRef` variants resolve before emitting markers
  - Single-variant unions collapsed to direct refs
  - Enum const name collisions: skip types, fallback to `any`
- IR diff fuzzy name matching: `io.k8s.api.v1.Pod` matches `IoK8sAPIV1Pod`
- `toGoName` handles `$`, `<`, `>`, `=`, `!`, `~`, `%` and numeric prefixes
- Integer enum consts emitted as `= 1` not `= "1"`
- Graceful circular `$ref` handling in OpenAPI converter (warnings, not fatal)
- GitHub Actions CI with Go 1.22/1.23 + full round-trip assertion
- `go:generate` verified working end-to-end
- `compschema generate` prints `go get` hint for runtime dependency

### Changed
- Consolidated docs: PRD + design decisions → `docs/DESIGN.md`
- Removed `RELEASES.md` (release notes on GitHub)

### Removed
- Swagger v2 specs (DigitalOcean, NYTimes) — OpenAPI v2 support added to roadmap

## [0.9.0] - 2026-04-22

### Added
- Recursive string enum resolution in JSON Schema → IR parser: follows `$ref` chains through nested `anyOf` to collapse all-string unions into single enums
- Union-of-union normalization in IR diff: `variantSubsumed` and `variantExistsInBoth` handle cases where Go type system can't express union composition
- Union/enum kind equivalence in IR diff: `anyOf(all-string-enums)` ≈ `KindEnum`

### Changed
- **IR field match rate: 100.0%** — 600 fields matched, 0 missing, 0 extra, 0 differ
- Full round-trip: OpenAPI → extract → JSON Schema → import → Go → generate → JSON Schema → diff = perfect structural match

## [0.8.0] - 2026-04-22

### Added
- Primitive wrapper types for union variants in `compschema import`:
  `oneOf(string, number, bool)` → `ComparisonFilterValueString{Value string}`, etc.
- Inline union/object extraction: field-level `oneOf`/`anyOf`/object schemas → named Go types
- String enum collapse: `anyOf` of string enums → single enum with combined values
- `$ref` resolution during string enum collapsing
- `parseContext` in JSON Schema → IR parser for cross-def resolution

### Fixed
- Description comma parsing: `description=Text, image, or...` no longer corrupted
- `[]T` naming in wrapper types: `[]InputItem` → `SliceInputItem`
- Skip empty unions (0 implementors) in analyzer

### Changed
- Round-trip match rate (compschema import pipeline): **99.2%**
  - 490 fields: 486 match, 3 differ (model), 1 missing (Item variant)
  - Remaining 0.8%: nested anyOf-of-anyOf (ModelIdsResponses) + union-of-union (Item)

## [0.7.0] - 2026-04-22

### Added
- `compschema import` command — generates Go structs from JSON Schema, replacing `go-jsonschema`:
  - Sealed interfaces for `oneOf`/`anyOf` unions with `//compschema:generate` annotations
  - Enum types with const blocks
  - Constraints preserved in `jsonschema:"..."` tags
  - Descriptions in comments and tags
  - Inline enum extraction from titles
  - Nullable as `*T` pointers
- IR diff normalization: nullable, `$ref` resolution, kind unwrapping
  - IR field match rate: 82.6% → **97.7%** (go-jsonschema pipeline)
  - Only 10 truly structural diffs remain (all `interface{}` / Go type system limitations)
- `internal/importer` package — JSON Schema → IR → Go code generation
- `internal/jsonschema2ir` package — JSON Schema parser into Schema IR

### Fixed
- Skip empty unions (0 implementors) in analyzer
- Backtick sanitization in generated struct tags

### Changed
- Full round-trip without `go-jsonschema`:
  OpenAPI → extract → JSON Schema → import → Go structs → generate → JSON Schema → diff
  Result: 96.3% field match, 0 extra types

## [0.6.0] - 2026-04-22

### Added
- Field comment extraction — Go struct field comments become JSON Schema `description`
- Analyzer tests (8 tests), emitter tests (3 tests)
- Per-property structural comparison in `compschema diff`
- Inline single-use enum/scalar `$defs` at their reference site

### Fixed
- `replaceRef` now merges `description` and other keys alongside `$ref` during inlining

### Changed
- Diff match rate: 70.3% → **82.5%** on OpenAI round-trip
- Structural gaps: 121 → 65
- `$defs` count: 261 → 136 (ground truth: 124)
- Extra `$defs`: 139 → 17
- Tests: 587 pass, 23 skip, 0 fail

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

[Unreleased]: https://github.com/codewandler/compschema/compare/v2.3.0...HEAD
[2.3.0]: https://github.com/codewandler/compschema/compare/v2.2.0...v2.3.0
[2.2.0]: https://github.com/codewandler/compschema/compare/v2.1.1...v2.2.0
[2.1.1]: https://github.com/codewandler/compschema/compare/v2.1.0...v2.1.1
[2.1.0]: https://github.com/codewandler/compschema/compare/v2.0.1...v2.1.0
[2.0.1]: https://github.com/codewandler/compschema/compare/v2.0.0...v2.0.1
[1.1.0]: https://github.com/codewandler/compschema/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/codewandler/compschema/compare/v0.9.0...v1.0.0
[0.9.0]: https://github.com/codewandler/compschema/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/codewandler/compschema/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/codewandler/compschema/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/codewandler/compschema/compare/v0.4.0...v0.6.0
[0.4.0]: https://github.com/codewandler/compschema/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/codewandler/compschema/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/codewandler/compschema/compare/v0.1.0...v0.2.0
