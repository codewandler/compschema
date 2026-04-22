# compschema — Agent Guidelines

Compile-time JSON Schema generator for Go. Converts Go types → JSON Schema + Validate + Decode via static analysis (`go/ast` + `go/types`). Also imports JSON Schema/OpenAPI → Go structs.

> ⚠️ **Always run `task check` before committing.** It runs fmt → vet → lint → test in one step.

## Build & Test Commands

```bash
# All-in-one check (format, vet, lint, test)
task check

# Individual steps
task build          # go build ./...
task test           # go test ./... -count=1
task lint           # golangci-lint run ./...
task fmt            # goimports + gofmt
task vet            # go vet ./...

# Install CLI
task install        # go install ./cmd/compschema/

# Code generation
task generate       # regenerate examples/basic + examples/openai
task generate:basic
task generate:openai

# Multi-API test suite (11 OpenAPI specs)
task specs          # bash testdata/specs/run_all.sh

# Cleanup
task clean          # remove generated artifacts
```

## Project Structure

```
schema.go                          # compschema.Schema interface (the only exported API)
cmd/compschema/                    # CLI (cobra) — all subcommands
  main.go                          # root command + subcommand registration
  extract.go                       # OpenAPI → JSON Schema
  importcmd.go                     # JSON Schema → Go structs
  generate.go                      # Go types → JSON Schema + Validate + Decode + tests
  diff.go                          # JSON Schema structural comparison
  run.go                           # config-driven pipeline runner
internal/config/                    # Pipeline config file format (.compschema.yaml)
  config.go                        # File, Pipeline, Action types + YAML parser
internal/ir/                       # Schema IR — the core data model
  types.go                         # Type, Field, Constraint, Variant, TypeRef, Kind enum
internal/analyzer/                 # Go source → IR (the forward direction)
  analyzer.go                      # go/packages + go/types → IR Package
  analyzer_test.go
internal/emitter/                  # IR → output files (the reverse direction)
  emitter.go                       # JSONSchema(), GoCodegen(), GoTests() emitters
  emitter_test.go
internal/importer/                 # JSON Schema → Go source (import direction)
  importer.go                      # GenerateGo, emitStruct/Enum/Union, type helpers
  config.go                        # Config struct, ApplyConfig (rename/exclude transforms)
internal/jsonschema2ir/            # JSON Schema → IR parser (shared by importer + diff)
  parser.go
internal/openapi2jsonschema/       # OpenAPI → JSON Schema converter
  converter.go                     # Exhaustive field coverage, all OAS3 keywords
  validate.go                      # Meta-schema validation (draft 2020-12)
  validate_test.go
internal/schemadiff/               # Schema comparison
  diff.go                          # JSON-level keyword diff
  ir_diff.go                       # IR-level structural diff (normalized)
internal/uniongen/                 # Sealed interface generation for oneOf/anyOf unions
  uniongen.go                      # EmitUnion() — IR-based, used by importer (reusable)
examples/basic/                    # Simple example: Order, LineItem, Shape union
examples/openai/                   # Real-world: OpenAI Responses API (261 types)
testdata/basic/                    # Test fixture source for examples/basic
testdata/openai/                   # OpenAI OpenAPI spec + extracted JSON Schema
testdata/specs/                    # Multi-API test suite (11 OpenAPI specs)
  run_all.sh                       # Runs extract → import → compile → generate → diff → test
docs/DESIGN.md                     # Architecture, IR design, validation strategy
```

## Key Concepts

### The Pipeline

```
Go types ──analyzer──▶ IR ──emitter──▶ JSON Schema + Go code + tests
JSON Schema ──jsonschema2ir──▶ IR ──importer──▶ Go structs
OpenAPI ──openapi2jsonschema──▶ JSON Schema
```

Everything flows through the **Schema IR** (`internal/ir/types.go`). The IR has 8 type kinds:
`KindStruct`, `KindEnum`, `KindUnion`, `KindScalar`, `KindList`, `KindMap`, `KindNullable`, `KindRef`.

### Annotations

Types are discovered via `//compschema:generate` comments or `--all` flag. Constraints come from `jsonschema:"..."` struct tags (compatible with `invopop/jsonschema`).

### Generated Files

`compschema generate` produces 3 files per package:
- `schema.gen.json` — JSON Schema draft 2020-12 (all types as `$defs`)
- `compschema.gen.go` — `JSONSchemaBytes()`, `Validate()`, `DecodeT()` per type
- `compschema.gen_test.go` — smoke tests (schema validity, validation, round-trip)

## Code Style Guidelines

- **Lint**: all code must pass `golangci-lint` with `.golangci.yml` — run `task lint`
- **Format**: `goimports` for import ordering, `gofmt -s` for simplification
- **Generated code**: files end in `.gen.go` or `.gen_test.go`, include `DO NOT EDIT` header
- **Errors**: wrap with context (`fmt.Errorf("parse schema: %w", err)`), never ignore non-trivial errors
- **Naming**: follow Go conventions. Use `toGoName()` in importer for JSON → Go name conversion
- **Tests**: unit tests for analyzer + emitter, generated smoke tests for examples, multi-API specs for integration

### Common Pitfalls

- **Self-referencing types**: the analyzer has cycle detection (`resolving` map in `resolveTypeRef`). The emitter has a separate `visiting` map for fixture generation. Both must be maintained.
- **Type aliases**: `type X = Y` must be detected via `tn.IsAlias()` and resolved to the target, not the underlying type.
- **Union marker methods**: only `KindStruct` types can have `func (*T) isUnion()` pointer receiver methods. Check via `pkg.Types` lookup before emitting.
- **Const name collisions**: enum const names (e.g. `SourceTypeCard`) can collide with struct type names. The importer detects and skips colliding types.
- **Inline vs $ref**: the emitter inlines single-use enum/scalar `$defs` for closer match with ground truth schemas. The `replaceRef` function must merge additional keys (description) alongside `$ref`.

## Documentation

- **README.md**: user-facing docs, install, CLI reference, API results table, limitations, roadmap
- **CHANGELOG.md**: version history following Keep a Changelog format
- **docs/DESIGN.md**: architecture decisions, IR design, Validate/Decode/JSONSchemaBytes design
- **CONTRIBUTING.md**: dev workflow for external contributors
- **Code comments**: every exported function has a godoc comment. Every IR type kind is documented.

## GitHub Issue Workflow

1. Read the issue thoroughly
2. Reproduce the problem (or understand the feature request)
3. Make changes on `main` (no feature branches for solo dev)
4. Run `task check` — must pass with 0 lint issues, all tests green
5. For significant changes, regenerate examples: `task generate`
6. For API compatibility changes, run `task specs` to verify multi-API suite
7. Commit with a clear, descriptive message
8. Push to `main`
9. If it's a release-worthy change, update CHANGELOG.md, tag, create GitHub release

## Release & Tagging Workflow

```bash
# 1. Update CHANGELOG.md with new version section
# 2. Commit
git add -A && git commit -m "v1.x.x: description"

# 3. Tag
git tag -a v1.x.x -m "v1.x.x: description"

# 4. Push
git push && git push --tags

# 5. Create GitHub release
gh release create v1.x.x --title "v1.x.x: title" --notes "..."
```

Version scheme: semver. Breaking CLI changes = major. New features = minor. Fixes = patch.
