# Feature 4: Unified pipeline — IR as single pivot, eliminate import/generate split

## Status: Planning

## Problem

compschema has two parallel code generation paths that share logic but diverge in confusing ways:

```
Path A (external schema):
  OpenAPI → extract → JSON Schema → jsonschema2ir → IR → importer → types.gen.go
                                                                          ↓
Path B (Go types):
  Go source → analyzer → IR → emitter → compschema.gen.go + schema.gen.json + tests
```

This causes real problems:

1. **Duplicate dispatch logic** — The importer generates `UnmarshalX()` (raw dispatch, 19 functions in openai), the emitter generates `DecodeX()` (validated dispatch, 138 functions). Both use the same IR but produce independent switch/case blocks. When they disagree — as with `UnmarshalTool` using `"windows"` while `DecodeTool` correctly uses `"computer_use_preview"` — bugs hide.

2. **Duplicate `UnmarshalJSON` generation** — Both the importer and emitter can generate `UnmarshalJSON` for structs with union fields (via shared `uniongen.EmitStructUnmarshalJSON`). The emitter checks `HasUnmarshalJSON` to skip types the importer already handled. Today: 32 `UnmarshalJSON` methods in `types.gen.go`, 0 in `compschema.gen.go`.

3. **IR information loss** — The importer renders IR to Go text, then the analyzer re-parses that Go text back into IR. The wrapper-type schema bug was exactly this: the importer's IR knew `ComparisonFilterValueString` wraps a scalar, but after rendering to Go and re-analyzing, the emitter had to rediscover wrapper-ness via a heuristic (`isWrapperType`).

4. **Confusing config** — A pipeline needs three actions (`extract → import → generate`) where `generate` re-analyzes Go source that `import` just wrote. The user must understand this two-pass design to configure it.

5. **Two constructor generators** — `emitImporterConstructor` (importer) and `emitConstructor` (emitter), doing nearly the same thing.

## Solution: IR as the single pivot

```
Source A: OpenAPI      → extract → IR
Source B: JSON Schema  → extract → IR
Source C: Go types     → extract → IR    (analyzer is just another extractor)

                          IR
                          ↓
                      generate
                          ↓
            ┌─────────────┼───────────────┐──────────────┐
      types.gen.go   compschema.gen.go   schema.gen.json   tests
     (if needed)    (all runtime code)   (JSON Schema)
```

### Core principles

1. **IR is the single source of truth** — Every generation operation consumes IR, never Go source text or raw JSON Schema.
2. **`extract` is polymorphic** — Detects source type (OpenAPI, JSON Schema, Go package) and always produces IR.
3. **`import` disappears** — Replaced by `generate` knowing how to emit type definitions when they don't already exist.
4. **`generate` is source-aware** — If the source was Go code, types already exist; only emit schema + runtime code. If the source was OpenAPI/JSON Schema, also emit type definitions.
5. **One dispatch path per union** — `UnmarshalJSON` and public `DecodeX` share the same switch/case logic. No independent code paths that can diverge.
6. **Clean file boundary** — `types.gen.go` has type definitions + wrapper marshal (no schema dependency). `compschema.gen.go` has all runtime code that depends on the schema (Validate, Decode, UnmarshalJSON, JSONSchemaBytes).

---

## Architecture changes

### IR gains provenance tracking

```go
type Type struct {
    // ... existing fields ...

    // SourceDefined is true when the type already exists in Go source code
    // (discovered by the Go analyzer). When false, the generate step must
    // emit a type definition.
    SourceDefined bool
}
```

`SourceDefined` is metadata — excluded from the Merkle hash (two IRs that differ only in provenance should hash the same).

### File output boundary

The split between output files becomes a clean architectural boundary:

**`types.gen.go`** (only when source is NOT Go — i.e., some types have `SourceDefined=false`):
- Struct definitions, enum consts, sealed interfaces, marker methods
- Wrapper types + wrapper `MarshalJSON` / `UnmarshalJSON` (these are simple `json.Marshal(w.Value)` delegations, no schema dependency)
- Union helper constructors (`ToolFromFileSearchTool`, etc.)
- Accessor methods on variants (e.g., `DiscriminatorValue()`)

**`compschema.gen.go`** (always):
- Schema embedding (`//go:embed schema.gen.json`)
- `compschemaValidator`, `compschemaDefBytes` infrastructure
- `Validate()` methods per type
- `DecodeX()` functions per type (unions: validated dispatch; structs: validate + unmarshal)
- `JSONSchemaBytes()` methods per type
- `UnmarshalJSON` methods for structs with union-typed fields (calls internal `unmarshalXDispatch`)
- Constructors (both paths — since they're package API, they belong with the runtime code)
- `Ptr[T]` helper

This boundary means:
- `types.gen.go` has ZERO dependency on `schema.gen.json` or `jsonschema` library
- `compschema.gen.go` has ALL the runtime behavior
- When source is Go code, `types.gen.go` is not emitted (types exist in user code)

### Unified dispatch: shared switch/case, two entry points

The plan does NOT merge `UnmarshalX` and `DecodeX` into one function. Instead, they share dispatch logic:

```go
// In compschema.gen.go:

// unmarshalToolDispatch is the shared dispatch logic for the Tool union.
// Used by both DecodeTool (public, validates) and UnmarshalJSON (internal).
func unmarshalToolDispatch(data []byte) (Tool, error) {
    var disc struct { D string `json:"type"` }
    if err := json.Unmarshal(data, &disc); err != nil {
        return nil, err
    }
    switch disc.D {
    case "computer_use_preview":
        var val ComputerUsePreviewTool
        // ...
    case "file_search":
        // ...
    }
}

// DecodeTool validates then dispatches. Public API.
func DecodeTool(data []byte) (Tool, error) {
    sch := compschemaValidator("Tool")
    var raw any
    json.Unmarshal(data, &raw)
    if err := sch.Validate(raw); err != nil {
        return nil, err
    }
    return unmarshalToolDispatch(data)
}

// UnmarshalJSON on structs calls the dispatch directly (no validation).
func (v *Response) UnmarshalJSON(data []byte) error {
    // ... alias + raw struct ...
    for _, item := range raw.Tools {
        parsed, err := unmarshalToolDispatch(item)  // fast path
        // ...
    }
}
```

**Why not validate in UnmarshalJSON?** Performance. `json.Unmarshal` is called on every response deserialization. Adding schema validation (which parses JSON a second time into `any`) would double the cost. Users who want validation use `DecodeX` explicitly.

**Why not keep separate `UnmarshalX`?** Because having two independent switch/case blocks is how the `"computer_use_preview"` bug happened. One shared `unmarshalXDispatch` function, two callers.

---

## Implementation plan

### Phase 1: Unify dispatch (eliminate divergence bug class)

The highest-value change — can ship independently. No user-facing config changes.

#### Step 1.1 — Generate shared dispatch functions in the emitter
**Files:** `internal/emitter/emitter.go`
**Effort:** 2 hr

For each union type, the emitter generates three things:

1. `unmarshalXDispatch(data []byte) (X, error)` — unexported, shared switch/case logic (discriminator-based or structural matching). This is essentially the current `emitUnionDecode` minus the validation preamble.

2. `DecodeX(data []byte) (X, error)` — public, validates then calls `unmarshalXDispatch`. Same as today.

3. The `UnmarshalJSON` methods on structs with union fields — calls `unmarshalXDispatch` instead of `UnmarshalX`.

Implement by refactoring `emitUnionDecode`:
```go
func emitUnionDispatch(b *strings.Builder, name string, t *ir.Type, pkg *ir.Package) {
    // Generates: func unmarshal{name}Dispatch(data []byte) ({name}, error)
    // Same switch/case logic as current emitUnionDecode, without validation preamble
}

func emitUnionDecode(b *strings.Builder, name string, t *ir.Type, pkg *ir.Package) {
    // Generates: func Decode{name}(data []byte) ({name}, error)
    // Validates against schema, then calls unmarshal{name}Dispatch
}
```

#### Step 1.2 — Move `UnmarshalJSON` generation from `uniongen` to `emitter`
**Files:** `internal/emitter/emitter.go`, `internal/uniongen/uniongen.go`
**Effort:** 1.5 hr

Currently `EmitStructUnmarshalJSON` in `uniongen` generates `UnmarshalJSON` methods that call `UnmarshalX`. It's used by both the importer and emitter.

Change: the emitter generates its own `UnmarshalJSON` methods that call `unmarshalXDispatch` (the shared dispatch from Step 1.1). These go in `compschema.gen.go`.

For the import-only path (backwards compat during migration), `uniongen` keeps generating `UnmarshalX` and `UnmarshalJSON` that calls it. But once `import` is deprecated, this code path is removed.

#### Step 1.3 — Remove `HasUnmarshalJSON` skip logic
**Files:** `internal/emitter/emitter.go`, `internal/ir/types.go`
**Effort:** 15 min

The emitter currently checks `t.HasUnmarshalJSON` to avoid duplicating `UnmarshalJSON` methods from the importer. Since the emitter now always generates its own `UnmarshalJSON` in `compschema.gen.go`, this check is no longer needed.

But: we need to handle the case where both `types.gen.go` (from import) and `compschema.gen.go` (from generate) coexist. If the importer also generates `UnmarshalJSON`, there's a compile error (duplicate method).

**Solution:** The importer stops generating `UnmarshalJSON` for struct types. It only generates:
- `UnmarshalX` dispatchers (still needed until import is fully deprecated)
- Wrapper `UnmarshalJSON` (for `ComparisonFilterValueString` etc. — these are simple `json.Unmarshal(data, &w.Value)` that don't need dispatch)

The emitter's `UnmarshalJSON` for struct types (which calls `unmarshalXDispatch`) replaces the importer's version.

#### Step 1.4 — Regenerate and verify
**Files:** regenerated examples
**Effort:** 30 min

```bash
task generate && task test
```

Verification checklist:
- `types.gen.go`: has `UnmarshalX` (legacy, will be removed later), NO struct `UnmarshalJSON` methods (only wrapper ones)
- `compschema.gen.go`: has `unmarshalXDispatch`, `DecodeX`, struct `UnmarshalJSON` methods
- Both `UnmarshalX` and `DecodeX` produce identical dispatch for every union
- All 850+ openai tests pass
- `task test` passes (all packages)

---

### Phase 2: IR provenance + unified type emission

#### Step 2.1 — Add `SourceDefined` to IR
**Files:** `internal/ir/types.go`, `internal/ir/hash.go`, `internal/analyzer/analyzer.go`
**Effort:** 30 min

```go
type Type struct {
    // ...
    SourceDefined bool // true when type exists in Go source
}
```

- Analyzer sets `SourceDefined = true` for all discovered types
- `jsonschema2ir` leaves it `false` (default)
- Hash computation excludes `SourceDefined` (it's metadata, not structural)

#### Step 2.2 — Extract type emission into shared `codegen` package
**Files:** `internal/codegen/types.go` (new), `internal/codegen/types_test.go` (new), `internal/importer/importer.go`
**Effort:** 3 hr

Move from `internal/importer/importer.go` to `internal/codegen/`:
- `emitStruct` → `codegen.EmitStruct`
- `emitEnum` → `codegen.EmitEnum`
- `toGoName`, `jsonTagForType`, `goTypeForIR` helper functions
- Wrapper type `MarshalJSON` / `UnmarshalJSON` generation (the simple `json.Marshal(w.Value)` kind)
- `EmitUnion` call (interface + marker methods + wrapper types) — already in `uniongen`, just needs wiring

The importer becomes:
```go
func ImportWithConfig(schemaPath string, cfg Config) (string, error) {
    irPkg, _ := jsonschema2ir.Parse(schemaPath)
    ApplyConfig(irPkg, cfg)      // rename, exclude, embed
    return codegen.EmitTypes(irPkg, codegen.Options{
        Package:      cfg.Package,
        Tags:         cfg.Tags,
        Constructors: cfg.Constructors,
        Implement:    cfg.Implement,
    }), nil
}
```

Write tests for `codegen.EmitStruct`, `codegen.EmitEnum` against known IR inputs.

#### Step 2.3 — Teach the emitter to emit `types.gen.go`
**Files:** `internal/emitter/emitter.go`
**Effort:** 2 hr

New function:
```go
func GoTypes(pkg *ir.Package, opts EmitOptions) string
```

Iterates `pkg.Order`, skips types where `SourceDefined=true`, emits the rest via `codegen.EmitStruct`/`codegen.EmitEnum`/`uniongen.EmitUnion`.

The existing `GoCodegen` function emits `compschema.gen.go` as before but now also emits `UnmarshalJSON` for ALL structs with union fields (not just those without `HasUnmarshalJSON`).

---

### Phase 3: Unified extract + pipeline context

#### Step 3.1 — Pipeline context: pass IR between stages
**Files:** `cmd/compschema/run.go`
**Effort:** 1 hr

Add a pipeline context that carries IR between stages:

```go
type pipelineContext struct {
    pkg *ir.Package // built by extract, consumed by generate
}

func runAction(a config.Action, ctx *pipelineContext) (*report.StepReport, error) {
    switch a.Action {
    case "extract":
        return runExtractAction(a, ctx, sr)
    case "generate":
        return runGenerateAction(a, ctx, sr) // uses ctx.pkg if set
    case "import":
        // legacy: warn deprecated, run as extract+generate internally
    }
}
```

When `ctx.pkg` is set, `runGenerateAction` skips the `analyzer.Analyze` call and uses the IR directly.

#### Step 3.2 — Make `extract` produce IR (not just JSON Schema)
**Files:** `cmd/compschema/run.go`, `cmd/compschema/extract.go`
**Effort:** 2 hr

The `extract` action currently produces only a JSON Schema file. Extend it to also build the IR and store it in the pipeline context:

```go
func runExtractAction(a config.Action, ctx *pipelineContext, sr *report.StepReport) error {
    // 1. OpenAPI → JSON Schema (existing logic)
    // 2. JSON Schema → IR (new)
    irPkg, err := jsonschema2ir.ParseBytes(schemaJSON)
    // 3. Apply transforms from config (rename, exclude, etc.)
    importer.ApplyConfig(irPkg, buildImporterConfig(a))
    // 4. Store in context
    ctx.pkg = irPkg
    // 5. Optionally write JSON Schema file (for diffing/debugging)
    if a.Out != "" {
        os.WriteFile(a.Out, schemaJSON, 0644)
    }
}
```

The `generate` action then checks `ctx.pkg`:
- If set: use it (skip analyzer), determine `SourceDefined` from IR (all false — came from schema)
- If not set: run analyzer as today (all `SourceDefined=true`)

#### Step 3.3 — Source-type detection for `extract`
**Files:** `cmd/compschema/run.go`
**Effort:** 1 hr

Extend `extract` to also accept Go package paths:

```yaml
# Go source → IR
- action: extract
  source: ./examples/basic
```

Detection: if source resolves to a directory containing `.go` files (or matches Go package patterns like `./...`), use `analyzer.Analyze`. Otherwise, treat as OpenAPI/JSON Schema.

This makes `extract` truly polymorphic — any source produces IR.

For the Go path, `extract` also needs the `all` flag to control annotated vs all-exported:

```yaml
- action: extract
  source: ./examples/openai
  all: true   # analyze all exported types
```

---

### Phase 4: Config consolidation + deprecation

#### Step 4.1 — Merge import-specific flags into extract+generate
**Files:** `internal/config/config.go`
**Effort:** 30 min

Move `import`-specific flags to where they belong:

| Flag | Old location | New location | Rationale |
|------|-------------|-------------|-----------|
| `schema` | import | extract (as `source`) | It's an input, not generation config |
| `package` | import | generate | Package name is a generation concern |
| `rename` | import | extract | IR transform, applied before generation |
| `exclude` | import | extract | IR transform, applied before generation |
| `tags` | import | generate | Go-specific output concern |
| `implement` | import | generate | Go-specific output concern |

The `Action` struct gains these fields as shared flags (not action-specific):

```go
type Action struct {
    Action string
    
    // extract flags
    Source any    // polymorphic source
    Spec   string // legacy, deprecated → use source
    Path   string
    
    // IR transform flags (applied during extract, before generate)
    Rename  map[string]string
    Exclude []string
    
    // generate flags
    Package      string
    All          bool
    Out          string
    Tags         []string
    Implement    []ImplementRule
    Validate     bool
    Test         bool
    FailOnTest   bool
    Examples     bool
    Constructors bool
    EmitIR       bool
    
    // legacy (deprecated)
    Schema   string   // → use extract action with source
    Packages []string // → use extract action with source
}
```

#### Step 4.2 — Deprecate `import` action
**Files:** `cmd/compschema/run.go`
**Effort:** 30 min

When `action: import` is encountered:
1. Log: `⚠ "import" action is deprecated, use "extract" + "generate" instead`
2. Internally convert to: extract (with `source: schema`) + generate (with type emission)
3. Works exactly as before — zero breakage

#### Step 4.3 — Update `.compschema.yaml` to new format
**Files:** `.compschema.yaml`
**Effort:** 15 min

```yaml
pipelines:
  openai:
    - action: extract
      source: https://raw.githubusercontent.com/openai/openai-openapi/refs/heads/manual_spec/openapi.yaml
      path: /responses
      validate: true
      rename: {}   # IR transforms happen here
      exclude: []
    - action: generate
      package: openairesponses
      validate: true
      test: true
      examples: true
      constructors: true
      out: examples/openai
      implement:
        - union: ResponseStreamEvent
          discriminator_method: EventType

  specs:
    - action: extract
      source:
        - testdata/specs/petstore.yaml
        - testdata/specs/twilio.yaml
        - testdata/specs/spotify.yaml
        - testdata/specs/stripe.yaml
        - testdata/specs/github.yaml
        - testdata/specs/kubernetes.yaml
        - testdata/specs/discord.yaml
        - testdata/specs/cloudflare.yaml
        - testdata/specs/plaid.yaml
        - testdata/specs/box.yaml
        - testdata/specs/asana.yaml
      path: /
      out: "/tmp/compschema-specs/{name}/schema.json"
    - action: generate
      package: "{name}"
      all: true
      test: true
      out: "/tmp/compschema-specs/{name}"
```

#### Step 4.4 — Update Taskfile
**Files:** `Taskfile.yml`
**Effort:** 15 min

```yaml
generate:openai:
  desc: Regenerate examples/openai from OpenAI spec
  deps: [build:bin]
  cmds:
    - "{{.BIN}} run openai"
```

The `task generate` target runs `generate:basic` (Go source, unchanged), `run openai` (pipeline), and `generate:config-schema` (Go source, unchanged).

---

### Phase 5: CLI unification

#### Step 5.1 — `generate` accepts `--source` flag
**Files:** `cmd/compschema/generate.go`
**Effort:** 1 hr

```bash
# From Go source (current behavior, unchanged):
compschema generate ./examples/basic

# From OpenAPI spec (new — single command replaces extract+import+generate):
compschema generate --source openapi.yaml --path /responses --package responses --out ./responses/

# From JSON Schema (new):
compschema generate --source schema.json --package responses --out ./responses/
```

When `--source` is given, `generate` internally: detects source type → builds IR → runs generation (emitting types.gen.go + compschema.gen.go + schema.gen.json + tests).

When positional args are given (e.g., `./examples/basic`), behavior is unchanged: analyze Go source → generate.

#### Step 5.2 — Deprecate `compschema import` CLI command
**Files:** `cmd/compschema/importcmd.go`
**Effort:** 15 min

Add deprecation notice:
```
⚠ "compschema import" is deprecated. Use "compschema generate --source <schema>" instead.
```

Keep working for backwards compatibility.

---

### Phase 6: Cleanup

#### Step 6.1 — Remove `UnmarshalX` generation from `uniongen`
**Files:** `internal/uniongen/uniongen.go`
**Effort:** 1 hr

After Phase 1 ships and the import action is deprecated, remove `emitUnmarshalFunc` from `uniongen.EmitUnion`. The `EmitUnion` function now only generates:
- Sealed interface definition + marker methods
- Wrapper types + wrapper MarshalJSON/UnmarshalJSON
- Accessor methods (DiscriminatorValue, etc.)
- Union helper constructors (ToolFromFileSearchTool, etc.)

All dispatch logic lives in the emitter (via `emitUnionDispatch` + `emitUnionDecode`).

#### Step 6.2 — Remove importer's Go code generation
**Files:** `internal/importer/importer.go`
**Effort:** 1 hr

The importer becomes a thin pipeline: `jsonschema2ir.Parse → ApplyConfig → return IR`.

Delete: `GenerateGo`, `GenerateGoWithConfig`, `emitStruct`, `emitEnum`, `emitImporterConstructor`, `ImportFromFileWithConfig`, and all Go code emission helpers.

What remains in `internal/importer/`:
- `config.go` — Config, ApplyConfig (rename, exclude, embed transforms)
- `importer.go` — `func ParseSchema(path string, cfg Config) (*ir.Package, error)` — thin wrapper

#### Step 6.3 — Remove `HasUnmarshalJSON` from IR
**Files:** `internal/ir/types.go`, `internal/analyzer/analyzer.go`
**Effort:** 15 min

This flag only existed to coordinate between importer and emitter. With unified emission, it's no longer needed.

#### Step 6.4 — Remove deprecated `import` CLI command
**Files:** `cmd/compschema/importcmd.go`
**Effort:** 15 min

After one release cycle with the deprecation warning, remove the command entirely.

#### Step 6.5 — Update docs
**Files:** `AGENTS.md`, `docs/DESIGN.md`
**Effort:** 30 min

Update architecture diagrams, pipeline descriptions, and config examples.

---

## Migration guide

### Config migration

```yaml
# OLD (3 actions):
- action: extract
  source: openapi.yaml
  path: /responses
  out: schema.json
- action: import
  schema: schema.json
  package: myapi
  out: types.gen.go
  rename: { Foo: Bar }
  exclude: ["*Param"]
- action: generate
  all: true
  test: true
  out: ./myapi/
  packages: ["./myapi/"]

# NEW (2 actions):
- action: extract
  source: openapi.yaml
  path: /responses
  rename: { Foo: Bar }
  exclude: ["*Param"]
- action: generate
  package: myapi
  test: true
  out: ./myapi/
```

### CLI migration

```bash
# OLD:
compschema extract --spec openapi.yaml --path /responses --out schema.json
compschema import --package myapi --out types.go schema.json
compschema generate --all --test ./myapi/

# NEW:
compschema generate --source openapi.yaml --path /responses --package myapi --test --out ./myapi/
```

### File output changes

Before:
- `types.gen.go` — types + `UnmarshalX` + struct `UnmarshalJSON` + wrapper marshal (from import)
- `compschema.gen.go` — `DecodeX` + `Validate` + `JSONSchemaBytes` (from generate)

After:
- `types.gen.go` — types + wrapper marshal only (when source is not Go)
- `compschema.gen.go` — `unmarshalXDispatch` + `DecodeX` + struct `UnmarshalJSON` + `Validate` + `JSONSchemaBytes` + constructors

---

## Execution order and dependencies

```
Phase 1 — Unify dispatch (4.25 hr)           ← ship independently, fixes bug class
  1.1  Shared dispatch functions               2 hr
  1.2  Move UnmarshalJSON to emitter          1.5 hr
  1.3  Remove HasUnmarshalJSON skip           15 min
  1.4  Regenerate + verify                    30 min

Phase 2 — Unified type emission (5.5 hr)      ← depends on Phase 1
  2.1  SourceDefined in IR                    30 min
  2.2  codegen package extraction              3 hr
  2.3  Emitter emits types.gen.go              2 hr

Phase 3 — Unified extract + pipeline (4 hr)   ← depends on Phase 2
  3.1  Pipeline context (IR pass-through)      1 hr
  3.2  Extract produces IR                     2 hr
  3.3  Extract from Go source                  1 hr

Phase 4 — Config consolidation (1.5 hr)       ← depends on Phase 3
  4.1  Merge import flags                     30 min
  4.2  Deprecate import action                30 min
  4.3  Update .compschema.yaml                15 min
  4.4  Update Taskfile                        15 min

Phase 5 — CLI unification (1.25 hr)           ← depends on Phase 3
  5.1  generate --source flag                  1 hr
  5.2  Deprecate import CLI                   15 min

Phase 6 — Cleanup (3 hr)                      ← depends on Phase 4+5
  6.1  Remove UnmarshalX from uniongen         1 hr
  6.2  Remove importer code generation         1 hr
  6.3  Remove HasUnmarshalJSON from IR        15 min
  6.4  Remove import CLI command              15 min
  6.5  Update docs                            30 min
```

**Total: ~19.5 hr**

**Ship points** (each is independently releasable):
1. After Phase 1: dispatch unification, no user-facing changes
2. After Phase 2: emitter can produce types, but config unchanged
3. After Phase 3+4: new 2-action pipeline format, old format deprecated but working
4. After Phase 5: CLI unified, `import` deprecated
5. After Phase 6: deprecated code removed (next major version)

---

## Risk assessment

| Risk | Impact | Mitigation |
|---|---|---|
| `UnmarshalJSON` in compschema.gen.go conflicts with existing one in types.gen.go | Build failure | Phase 1.2: importer stops emitting struct `UnmarshalJSON`; only emits wrapper ones |
| Shared `unmarshalXDispatch` adds function call overhead vs inline switch | Negligible — Go inlines small functions, and it's one function call | Benchmark before/after |
| `codegen` extraction (Phase 2.2) touches many helper functions | Large diff, potential regressions | Move one function at a time; keep importer working as thin wrapper; test each move |
| Breaking change for users with existing `.compschema.yaml` | Workflow disruption | `import` action deprecated with warning, works unchanged for one release cycle |
| `extract` source-type detection (Phase 3.3) guesses wrong | Wrong pipeline | Use explicit flags (`--path` implies OpenAPI, `--schema` implies JSON Schema, directory implies Go). Heuristics only as fallback. |
| Performance: emitter-generated `UnmarshalJSON` might differ subtly from importer's | Behavior change | Phase 1.4: verify all openai tests pass; both use same `uniongen.DetectUnionField` logic for field discovery |

## Non-goals

- Changing the IR data model beyond `SourceDefined` (no new Kind, no new fields)
- Changing JSON Schema output format
- Changing the generated test format
- Supporting non-Go output languages
- IR YAML as persistent interchange format (IR passes in-memory between pipeline stages; YAML serialization remains a debug/inspection tool only)
