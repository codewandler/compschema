# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `compschema uniongen` command — generates sealed Go interfaces for JSON Schema `oneOf`/`anyOf` unions with discriminator-based `UnmarshalX` dispatch functions.
- `internal/uniongen` package — schema analysis, discriminator detection (explicit `x-discriminator` or inferred from `type` const fields), Go code generation, and source patching.
- `--patch` flag on `uniongen` to remove conflicting `type X interface{}` declarations from go-jsonschema output.
- `--capitalization` flag on `uniongen` to match go-jsonschema naming (e.g. `Url` → `URL`).
- `$recursiveRef` resolution in converter — detects empty schemas from unresolved `$recursiveRef: "#"` and replaces with self-referencing `$ref`.

### Changed
- Pipeline (`make pipeline`) now includes `uniongen` step: generates `unions.gen.go` and patches `types.go`.
- `interface{}` in generated Go types reduced from 50 → 12 (76% reduction).

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

[Unreleased]: https://github.com/codewandler/compschema/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/codewandler/compschema/releases/tag/v0.1.0
