# Feature 0: compschema build system evolution

## Status: Complete (all phases implemented)

## Context

All items from the agentapis feature request are complete (Bugs A–D, UnmarshalJSON, rename, exclude, config pipelines). This plan covers the next evolution: making compschema a self-contained build system that replaces the Makefile, run_all.sh, and manual workflows.

---

## Phase 1: Foundation (no dependencies)

### Step 1.1 — Taskfile: build binary + generate config schema
**Files:** `Taskfile.yml`
**Effort:** 10 min

Add two tasks:

```yaml
  build:bin:
    desc: Build compschema binary to ./bin/
    cmds:
      - mkdir -p bin
      - go build -o ./bin/compschema ./cmd/compschema

  generate:config-schema:
    desc: Regenerate config schema (dogfooding compschema on itself)
    deps: [build:bin]
    cmds:
      - ./bin/compschema generate --all --validate --out internal/config ./internal/config/
```

Update existing `build` task to call `build:bin`. Update `generate` to include `generate:config-schema`.

**Verification:** `task build:bin && task generate:config-schema && task test`

---

### Step 1.2 — `--test` flag on generate (CLI + config)
**Files:**
- `cmd/compschema/generate.go` — add `--test` flag
- `cmd/compschema/run.go` — pass `Test` field through `runGenerateAction`
- `internal/config/config.go` — add `Test bool` to `Action` struct

**Effort:** 1 hr

**Substeps:**

1. **Add `Test` field to `config.Action`:**
   ```go
   Test bool `json:"test,omitempty" jsonschema:"description=Run generated tests after code generation"`
   ```

2. **Add `--test` flag to `newGenerateCmd()`:**
   ```go
   var runTests bool
   cmd.Flags().BoolVar(&runTests, "test", false, "run generated tests after writing files")
   ```

3. **Implement test runner function** in `cmd/compschema/generate.go`:
   ```go
   func runGeneratedTests(dir string) (passed, failed, skipped int, err error)
   ```
   - Shell out to `go test -count=1 -json ./dir/`
   - Parse JSON lines: look for `Action: "pass"`, `Action: "fail"`, `Action: "skip"` in `TestEvent` structs
   - Print summary to stderr
   - Return counts (do NOT fail the pipeline on test failures)

4. **Wire into both CLI and `runGenerateAction`** — after writing test files, if `--test`/`Test` is set, call `runGeneratedTests(dir)`.

5. **Regenerate config schema** (since `Action` struct changed): `task generate:config-schema`

**Verification:**
```bash
go build ./... && go test ./...
./bin/compschema generate --all --validate --test --out examples/basic ./examples/basic
# Should print test results summary
```

---

### Step 1.3 — IR emission (`--emit-ir`)
**Files:**
- `internal/emitter/ir_yaml.go` (new)
- `cmd/compschema/generate.go` — add `--emit-ir` flag
- `cmd/compschema/run.go` — pass through `runGenerateAction`
- `internal/config/config.go` — add `EmitIR bool` to `Action`

**Effort:** 1 hr

**Substeps:**

1. **Create `internal/emitter/ir_yaml.go`:**
   ```go
   func IRToYAML(pkg *ir.Package) ([]byte, error)
   ```
   - Walk `pkg.Order` → for each type, emit a YAML-friendly map
   - Use `go.yaml.in/yaml/v4` (already a dependency)
   - Include: kind, fields (name, json_name, type, required, constraints), enum values, variants, discriminator
   - Exclude: Go-specific details (import path, source dir)

2. **Add `EmitIR bool` to `config.Action`:**
   ```go
   EmitIR bool `json:"emit_ir,omitempty" jsonschema:"description=Write IR YAML alongside generated output"`
   ```

3. **Add `--emit-ir` flag to `newGenerateCmd()`** and wire into `runGenerateAction`.

4. **After writing schema.gen.json**, if `emit_ir` is set:
   ```go
   irYAML, err := emitter.IRToYAML(pkg)
   irPath := filepath.Join(dir, "schema.gen.ir.yaml")
   os.WriteFile(irPath, irYAML, 0644)
   ```

5. **Regenerate config schema.**

**Verification:**
```bash
./bin/compschema generate --all --emit-ir --out examples/openai ./examples/openai
cat examples/openai/schema.gen.ir.yaml  # should be readable YAML
```

---

## Phase 2: Pipeline reports (depends on 1.2)

### Step 2.1 — Report types + collection
**Files:**
- `internal/report/report.go` (new package)
- `cmd/compschema/run.go` — refactor `runAction` to return `*report.StepReport`

**Effort:** 1–2 hr

**Substeps:**

1. **Create `internal/report/report.go`:**
   ```go
   package report

   import "time"

   type PipelineReport struct {
       Pipeline string       `json:"pipeline" yaml:"pipeline"`
       Steps    []StepReport `json:"steps" yaml:"steps"`
       Duration time.Duration `json:"duration" yaml:"duration"`
   }

   type StepReport struct {
       Action   string            `json:"action" yaml:"action"`
       Duration time.Duration     `json:"duration" yaml:"duration"`
       Metrics  map[string]any    `json:"metrics" yaml:"metrics"`
   }

   func (r *PipelineReport) Print(w io.Writer)  // human-readable stderr output
   func (r *PipelineReport) YAML() ([]byte, error)
   ```

2. **Refactor `runAction` signature** in `cmd/compschema/run.go`:
   - Change from `func runAction(a config.Action) error` to `func runAction(a config.Action) (*report.StepReport, error)`
   - Same for `runExtractAction`, `runImportAction`, `runGenerateAction`

3. **Collect metrics in each action:**
   - `runExtractAction`: `schemas` (count `$defs` in output), `bytes` (output size)
   - `runImportAction`: `types` (grep `^type ` in output), `unions`, `excluded`, `renamed`
   - `runGenerateAction`: `types`, `defs` (count `$defs` in schema), `any_count`, test results (from step 1.2)

4. **In the `run` command loop**, collect `StepReport`s into a `PipelineReport`, print summary at end.

5. **Add `--report` flag** to `run` command — write report YAML to a file.

**Verification:**
```bash
./bin/compschema run openai  # must print step-by-step metrics
./bin/compschema run --report report.yaml openai && cat report.yaml
```

**Prerequisite:** Need a `.compschema.yaml` for testing. Create a minimal one:
```yaml
pipelines:
  openai:
    - action: extract
      spec: testdata/openai/openapi.yaml
      path: /responses
      validate: true
      out: testdata/openai/responses.schema.json
    - action: import
      schema: testdata/openai/responses.schema.json
      package: openairesponses
      out: examples/openai/types.go
    - action: generate
      all: true
      validate: true
      test: true
      packages: [./examples/openai/]
```

---

## Phase 3: Source abstraction (no dependencies)

### Step 3.1 — Source interface + file source
**Files:**
- `internal/source/source.go` (new)
- `internal/source/file.go` (new)
- `internal/source/source_test.go` (new)

**Effort:** 30 min

**Substeps:**

1. **Define the interface:**
   ```go
   package source

   import "time"

   type Meta struct {
       Origin    string    `json:"origin" yaml:"origin"`
       Hash      string    `json:"hash" yaml:"hash"`           // sha256:hex
       CommitRef string    `json:"commit_ref,omitempty" yaml:"commit_ref,omitempty"`
       FetchedAt time.Time `json:"fetched_at" yaml:"fetched_at"`
   }

   type Source interface {
       Fetch() (data []byte, meta Meta, err error)
       String() string  // human-readable description
   }
   ```

2. **Implement `FileSource`:**
   ```go
   type FileSource struct { Path string }

   func (s *FileSource) Fetch() ([]byte, Meta, error) {
       data, err := os.ReadFile(s.Path)
       hash := sha256.Sum256(data)
       return data, Meta{Origin: s.Path, Hash: "sha256:" + hex.EncodeToString(hash[:]), FetchedAt: time.Now()}, err
   }
   ```

3. **Write tests** for `FileSource` — fetch a temp file, verify hash is stable.

---

### Step 3.2 — HTTP source
**Files:**
- `internal/source/http.go` (new)
- `internal/source/http_test.go` (new)

**Effort:** 45 min

**Substeps:**

1. **Implement `HTTPSource`:**
   ```go
   type HTTPSource struct { URL string }
   ```
   - `Fetch()` → `http.Get(URL)`, read body, sha256, return with meta
   - Set `User-Agent: compschema/1.0`
   - Timeout: 30s
   - Return clear error on non-2xx status

2. **Write test** — use `httptest.NewServer` to serve a known payload, verify hash.

---

### Step 3.3 — Git source
**Files:**
- `internal/source/git.go` (new)
- `internal/source/git_test.go` (new)

**Effort:** 1 hr

**Substeps:**

1. **Implement `GitSource`:**
   ```go
   type GitSource struct { Repo, Ref, Path string }
   ```
   - `Fetch()`:
     - Create temp dir
     - `git clone --depth=1 --branch=<ref> <repo> <tmpdir>` (or `git clone` + `git checkout <ref>` for commit hashes)
     - Read `<tmpdir>/<path>`
     - Get resolved commit: `git -C <tmpdir> rev-parse HEAD`
     - sha256 of file contents
     - Clean up temp dir
   - Handle `Ref` default to `main`

2. **Write test** — skip if `git` not in PATH. Test with a small known public repo, or mock with a local git repo in temp dir.

---

### Step 3.4 — Source parser (YAML config → Source objects)
**Files:**
- `internal/source/parse.go` (new)
- `internal/source/parse_test.go` (new)

**Effort:** 1 hr

**Substeps:**

1. **Implement `Parse(v any) ([]Source, error)`:**
   - `string` starting with `http://` or `https://` → `HTTPSource`
   - `string` → `FileSource`
   - `map` with `git` key → `GitSource`
   - `[]any` → recursive, returns multiple sources
   - Error on unrecognized format

2. **Update `config.Action`** — add `Source` field (replaces `Spec`/`Schema` for new-style configs):
   ```go
   Source any `json:"source,omitempty" yaml:"source" jsonschema:"description=Source file/URL/git for the action"`
   ```
   Keep `Spec` and `Schema` for backwards compatibility. In `runAction`, resolve: if `Source` is set, parse it; else fall back to `Spec`/`Schema`.

3. **Write tests** for each source string format.

---

### Step 3.5 — Wire source into extract/import actions
**Files:**
- `cmd/compschema/run.go` — update `runExtractAction`, `runImportAction`

**Effort:** 45 min

**Substeps:**

1. In `runExtractAction`: if `a.Source` is set, parse → `Source.Fetch()` → write to temp file → use as spec path. Record `Meta` in step report.

2. In `runImportAction`: if `a.Source` is set, parse → `Source.Fetch()` → write to temp file → use as schema path.

3. Both: include `source_hash` in the `StepReport.Metrics`.

**Verification:**
```yaml
# Test with HTTP source in .compschema.yaml:
pipelines:
  petstore:
    - action: extract
      source: https://petstore3.swagger.io/api/v3/openapi.json
      path: /
      out: /tmp/petstore-schema.json
```
```bash
./bin/compschema run petstore
```

---

## Phase 4: Multi-source + templating (depends on 3.x + 2.1)

### Step 4.1 — Template variable substitution
**Files:**
- `internal/config/template.go` (new)
- `internal/config/template_test.go` (new)

**Effort:** 30 min

**Substeps:**

1. **Implement `ExpandTemplate(s string, vars map[string]string) string`:**
   - Replace `{name}`, `{source}`, `{hash}` in string values
   - `{name}` = basename without extension (or repo name for git)

2. **Implement `ExpandAction(a Action, vars map[string]string) Action`:**
   - Deep-copy the action, expand all string fields and string elements in slices

3. **Write tests.**

---

### Step 4.2 — Multi-source pipeline expansion
**Files:**
- `cmd/compschema/run.go` — add multi-source loop

**Effort:** 1.5 hr

**Substeps:**

1. **In the `run` command**, before executing steps:
   - Scan all steps for `Source` fields that are arrays
   - If found, expand: for each source in the array, clone the entire pipeline with `ExpandAction` using that source's `{name}`, `{source}`, `{hash}`
   - Execute each expanded pipeline sequentially

2. **For the report**, produce a map: `source → PipelineReport` when multi-source.

3. **Name derivation:**
   - File path: `filepath.Base` sans extension → `{name}`
   - HTTP URL: last path segment sans extension
   - Git: repo name or `Path` basename

**Verification:**
```yaml
pipelines:
  specs:
    - action: extract
      source:
        - testdata/specs/petstore.yaml
        - testdata/specs/twilio.yaml
      out: /tmp/compschema-specs/{name}/schema.json
      path: /
    - action: import
      source: /tmp/compschema-specs/{name}/schema.json
      package: "{name}"
      out: /tmp/compschema-specs/{name}/types.go
    - action: generate
      all: true
      test: true
      packages: [/tmp/compschema-specs/{name}/]
```
```bash
./bin/compschema run specs
# Should process petstore then twilio separately
```

---

## Phase 5: Replace Makefile + run_all.sh (depends on 4.x)

### Step 5.1 — Create `.compschema.yaml`
**Files:**
- `.compschema.yaml` (new)

**Effort:** 15 min

Write the full config covering both `openai` and `specs` pipelines (as specified in the original plan section 7). Include all 11 spec files from `testdata/specs/`.

---

### Step 5.2 — Validate replacement
**Effort:** 15 min

1. Run `./bin/compschema run openai` — verify output matches what `make pipeline` produced.
2. Run `./bin/compschema run specs` — verify output matches what `bash testdata/specs/run_all.sh` produced.
3. Compare: same types generated, same test pass rates.

---

### Step 5.3 — Delete Makefile + run_all.sh, update Taskfile
**Files:**
- Delete `Makefile`
- Delete `testdata/specs/run_all.sh`
- `Taskfile.yml` — update `pipeline` and `specs` tasks

**Effort:** 10 min

**Changes to Taskfile:**
```yaml
  pipeline:
    desc: Run the full OpenAI round-trip pipeline
    deps: [build:bin]
    cmds:
      - ./bin/compschema run openai

  specs:
    desc: Run multi-API round-trip test suite
    deps: [build:bin]
    cmds:
      - ./bin/compschema run specs
```

Remove `pipeline:fetch`, `pipeline:extract` tasks (subsumed by `compschema run`).

**Verification:** `task pipeline && task specs && task test`

---

## Phase 6: Merkle hashing + IR diff (depends on 1.3)

### Step 6.1 — Merkle hash methods on IR nodes
**Files:**
- `internal/ir/hash.go` (new)
- `internal/ir/hash_test.go` (new)

**Effort:** 2–3 hr

**Substeps:**

1. **Implement hash methods** (all return `[32]byte`):

   | Method | Hashes over |
   |--------|-------------|
   | `(*Constraint).Hash()` | `keyword + value` |
   | `(*TypeRef).Hash()` | If `Name` → `H(name)`. If `Inline` → `inline.Hash()` |
   | `(*Field).Hash()` | `json_name + required + type.Hash() + sorted(constraints.Hash())` |
   | `(*Variant).Hash()` | `name + discriminator + typeref.Hash()` |
   | `(*Type).Hash()` | `kind + name + kind-specific children hashes + sorted(constraints.Hash())` |
   | `(*Package).Hash()` | `name + sorted(type hashes)` |

2. **Canonical encoding:** Use a deterministic binary encoding for hashing (not JSON/YAML). Write values as `key\x00value\x00` pairs fed to `sha256.New()`. Sort map/slice elements by their hash before including.

3. **Tests:**
   - Same IR → same hash (determinism)
   - Change one field name → different hash (sensitivity)
   - Swap field order → same hash (order independence for sets, but preserve for structs — fields ARE ordered)
   - Actually fields should be order-dependent since JSON Schema `properties` + `required` care about structure not order, but for correctness fields should hash in declaration order

4. **Lazy caching:** Add unexported `hashOnce sync.Once` + `hashVal [32]byte` to each type. Invalidate by convention (hashes are computed on immutable IR snapshots).

---

### Step 6.2 — Hash-based IR diff pruning
**Files:**
- `internal/schemadiff/ir_diff.go` — optimize with hash comparison

**Effort:** 1–2 hr

**Substeps:**

1. **In `CompareIR`**, before doing field-level comparison of two types:
   - If `left.Hash() == right.Hash()` → skip, mark as exact match
   - Only descend into children when hashes differ

2. **Add hash info to `IRReport`:**
   ```go
   HashMatched int // types skipped because hash matched
   ```

3. **Test:** Generate IR from the same source twice → hash diff should skip everything. Change one field → should only descend into that type.

---

### Step 6.3 — Include hashes in IR YAML emission
**Files:**
- `internal/emitter/ir_yaml.go` — add hash annotations

**Effort:** 30 min

When `emit_ir` is set, include `_hash: <short hex>` on each type and field in the YAML output. This makes the IR YAML useful for manual change tracking.

---

## Phase 7: Incremental compilation (depends on 6.1)

### Step 7.1 — Hash cache file
**Files:**
- `internal/cache/cache.go` (new)
- `internal/cache/cache_test.go` (new)

**Effort:** 1 hr

**Substeps:**

1. **Define cache format** — `.compschema.cache.json`:
   ```json
   {
     "version": 1,
     "entries": {
       "examples/openai": {
         "source_hash": "sha256:abc...",
         "ir_hash": "sha256:def...",
         "output_hash": "sha256:ghi...",
         "timestamp": "2025-01-01T00:00:00Z"
       }
     }
   }
   ```

2. **Load/Save functions:**
   ```go
   func Load(path string) (*Cache, error)
   func (c *Cache) Save(path string) error
   func (c *Cache) IsStale(key string, sourceHash, irHash string) bool
   ```

3. **Add `.compschema.cache.json` to `.gitignore`.**

---

### Step 7.2 — Skip-if-unchanged in generate
**Files:**
- `cmd/compschema/run.go` — add cache check in `runGenerateAction`
- `cmd/compschema/generate.go` — add `--cache` flag

**Effort:** 2 hr

**Substeps:**

1. Before emitting, compute `pkg.Hash()`. Check cache.
2. If cache hit (same source hash + IR hash) → skip emit, print `✓ unchanged (cached)`.
3. After emit, update cache with new hashes.
4. `--no-cache` flag to force re-generation.

---

### Step 7.3 — Incremental `$defs` emission
**Files:**
- `internal/emitter/emitter.go` — add incremental mode

**Effort:** 2 hr

**Substeps:**

1. Load previous `schema.gen.json` and parse its `$defs`.
2. Parse previous IR (from cache or re-analyze).
3. For each type: if `type.Hash()` matches previous → reuse the existing `$def` entry verbatim.
4. Only re-emit types whose hash changed.
5. This preserves exact byte output for unchanged types (useful for minimal git diffs).

---

## Execution order summary

```
Phase 1 (Foundation) — no dependencies, parallelize freely
  1.1  Taskfile tasks                    10 min
  1.2  --test flag                        1 hr
  1.3  IR emission (--emit-ir)            1 hr

Phase 2 (Reports) — depends on 1.2
  2.1  Report types + collection        1–2 hr

Phase 3 (Sources) — no dependencies, parallelize with Phase 2
  3.1  Source interface + FileSource     30 min
  3.2  HTTP source                       45 min
  3.3  Git source                         1 hr
  3.4  Source parser                       1 hr
  3.5  Wire into extract/import          45 min

Phase 4 (Multi-source) — depends on 3.x + 2.1
  4.1  Template variables                30 min
  4.2  Multi-source expansion           1.5 hr

Phase 5 (Replace scripts) — depends on 4.x
  5.1  Create .compschema.yaml           15 min
  5.2  Validate replacement              15 min
  5.3  Delete Makefile + run_all.sh      10 min

Phase 6 (Merkle) — depends on 1.3
  6.1  Merkle hash methods              2–3 hr
  6.2  Hash-based diff pruning          1–2 hr
  6.3  Hashes in IR YAML                 30 min

Phase 7 (Incremental) — depends on 6.1
  7.1  Hash cache file                    1 hr
  7.2  Skip-if-unchanged                  2 hr
  7.3  Incremental $defs                  2 hr
```

**Critical path:** 1.2 → 2.1 → 4.2 → 5.x (~5 hr)
**Total effort:** ~20 hr
**Parallel lanes:** Phases 1+3 run concurrently. Phase 6 runs concurrently with Phase 4+5.

---

## File inventory (new files)

| File | Phase |
|------|-------|
| `internal/source/source.go` | 3.1 |
| `internal/source/file.go` | 3.1 |
| `internal/source/http.go` | 3.2 |
| `internal/source/git.go` | 3.3 |
| `internal/source/parse.go` | 3.4 |
| `internal/source/source_test.go` | 3.1 |
| `internal/source/http_test.go` | 3.2 |
| `internal/source/git_test.go` | 3.3 |
| `internal/source/parse_test.go` | 3.4 |
| `internal/report/report.go` | 2.1 |
| `internal/emitter/ir_yaml.go` | 1.3 |
| `internal/config/template.go` | 4.1 |
| `internal/config/template_test.go` | 4.1 |
| `internal/ir/hash.go` | 6.1 |
| `internal/ir/hash_test.go` | 6.1 |
| `internal/cache/cache.go` | 7.1 |
| `internal/cache/cache_test.go` | 7.1 |
| `.compschema.yaml` | 5.1 |

## Files modified

| File | Phases |
|------|--------|
| `Taskfile.yml` | 1.1, 5.3 |
| `cmd/compschema/generate.go` | 1.2, 1.3, 7.2 |
| `cmd/compschema/run.go` | 1.2, 1.3, 2.1, 3.5, 4.2, 7.2 |
| `internal/config/config.go` | 1.2, 1.3, 3.4 |
| `internal/schemadiff/ir_diff.go` | 6.2 |
| `internal/emitter/emitter.go` | 7.3 |
| `internal/emitter/ir_yaml.go` | 6.3 |

## Files deleted

| File | Phase |
|------|-------|
| `Makefile` | 5.3 |
| `testdata/specs/run_all.sh` | 5.3 |
