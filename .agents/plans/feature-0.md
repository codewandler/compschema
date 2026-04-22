# Feature 0: compschema build system evolution

## Status: Planning

## Context

All items from the agentapis feature request are complete (Bugs A–D, UnmarshalJSON, rename, exclude, config pipelines). This plan covers the next evolution: making compschema a self-contained build system that replaces the Makefile, run_all.sh, and manual workflows.

---

## 1. Source abstraction

The `import` action's `schema` field becomes a polymorphic `source`:

```yaml
# File (current behavior)
source: ./responses.schema.json

# HTTP URL — fetched, content-hashed
source: https://raw.githubusercontent.com/openai/openai-openapi/main/openapi.yaml

# Git repo — cloned/fetched, commit-hashed
source:
  git: https://github.com/openai/openai-openapi.git
  ref: v2.3.0          # tag, branch, hash — default: main
  path: openapi.yaml    # file within the repo

# Multi-source — pipeline runs once per source
source:
  - https://petstore3.swagger.io/api/v3/openapi.json
  - ./local-spec.yaml
  - git: https://github.com/stripe/openapi.git
    ref: master
    path: openapi/spec3.json
```

### Hashing

Every fetched source gets a content hash (SHA-256 of the raw bytes). For Git sources, we also record the resolved commit hash. These are stored as metadata in the pipeline report (not embedded in the schema itself).

### Implementation

New package: `internal/source/`

```go
type Source interface {
    Fetch() (data []byte, meta Meta, err error)
}

type Meta struct {
    Origin    string // original URL/path/git URI
    Hash      string // sha256 of content
    CommitRef string // resolved git commit (empty for file/http)
    FetchedAt time.Time
}

type FileSource struct { Path string }
type HTTPSource struct { URL string }
type GitSource  struct { Repo, Ref, Path string }
```

The config parser resolves `source` into one or more `Source` values. Multi-source produces multiple pipeline runs with `{name}` template substitution.

---

## 2. IR emission

Flag `--emit-ir` (or config `emit_ir: true`) writes the Schema IR alongside generated output:

```
schema.gen.json        # JSON Schema (existing)
schema.gen.ir.yaml     # Schema IR (new)
```

The IR YAML is the canonical human-readable representation of the analyzed types. Useful for:
- Reviewing what compschema understood from the Go types
- Diffing structural changes between versions
- Debugging round-trip mismatches

### Implementation

Add `IRToYAML(pkg *ir.Package) []byte` to `internal/emitter/`. Emit in the `generate` action when `emit_ir` is set.

---

## 3. Merkle hashing on IR nodes

Each IR node computes a deterministic hash from its own semantic content plus the **hashes** of its children — not the children's full content. Standard Merkle tree structure.

```
Type("Response").Hash() = H(
    kind=struct,
    name="Response",
    field("output").Hash(),     // child hash
    field("status").Hash(),
    ...constraints
)

Field("output").Hash() = H(
    json_name="output",
    required=true,
    type_ref.Hash()
)

TypeRef.Hash() = H(
    name="OutputItem"           // leaf
)
```

### Key properties

- **No stored hash field** — computed on the fly from the node's semantic content, or cached lazily. Never persisted as a field in the IR struct.
- **Own hash excluded** — it's what we're computing.
- **Children included as hashes** — not their full content. Each leaf (scalar type, ref name, constraint value) hashes its own content directly.
- **Format-independent** — hash is over Go struct values, not over any serialized representation.
- **Deterministic** — same semantic content always produces the same hash regardless of field ordering, whitespace, etc.

### Benefits

- **O(1) change detection** — compare root hashes of two IR packages
- **O(changed nodes) diff** — walk down only where hashes diverge, skip matching subtrees
- **Structural sharing** — identical subtrees have the same hash regardless of position
- **Incremental compilation** — only re-emit `$defs` entries whose hash changed
- **CI cache keys** — skip pipeline if source hash → IR hash → output hash chain is unchanged

### Implementation

Methods on IR types, not stored fields:

```go
func (t *Type) Hash() [32]byte
func (f *Field) Hash() [32]byte
func (r *TypeRef) Hash() [32]byte
func (v *Variant) Hash() [32]byte
func (c *Constraint) Hash() [32]byte
func (p *Package) Hash() [32]byte  // root = H(name, sorted type hashes)
```

Same principle as Git trees / IPFS / Merkle-DAGs.

---

## 4. Pipeline reports + metrics

Each pipeline step returns a `Report`. After completion, the report is printed to stderr and optionally written to a file.

```yaml
report:
  source: https://...openapi.yaml
  source_hash: sha256:abc123...
  steps:
    - action: extract
      schemas: 124
      duration: 0.3s
    - action: import
      types: 261
      unions: 14
      excluded: 36
      renamed: 3
      duration: 0.1s
    - action: generate
      types: 252
      defs: 176
      field_match: 100.0%
      any_count: 3
      tests:
        total: 587
        passed: 585
        failed: 2
        pass_rate: 99.7%
      duration: 1.2s
```

### Metrics tracked

| Metric | Source | Purpose |
|--------|--------|---------|
| `schemas` | extract | Number of schema defs extracted |
| `types` | import | Go types generated |
| `unions` | import | Sealed interfaces generated |
| `excluded` | import | Types skipped by `--exclude` |
| `renamed` | import | Types renamed by `--rename` |
| `defs` | generate | `$defs` entries in JSON Schema |
| `field_match` | generate | Percentage of fields round-tripping correctly |
| `any_count` | generate | Number of `interface{}`/`any` fields (lower is better) |
| `tests.total` | generate --test | Total generated tests |
| `tests.passed` | generate --test | Passing tests |
| `tests.failed` | generate --test | Failing tests |
| `tests.pass_rate` | generate --test | Pass percentage |

For multi-source configs, the report is a map: `source → Report`.

### Implementation

```go
// internal/config/report.go
type Report struct {
    Source     string        `json:"source"`
    SourceHash string       `json:"source_hash"`
    Steps      []StepReport `json:"steps"`
}

type StepReport struct {
    Action   string         `json:"action"`
    Duration time.Duration  `json:"duration"`
    Metrics  map[string]any `json:"metrics"`
}
```

Each `runAction` function returns `(*StepReport, error)` instead of just `error`.

---

## 5. `--test` flag on generate

When `generate` has `test: true` (config) or `--test` (CLI), after writing `compschema.gen_test.go`, automatically run:

```bash
go test -count=1 -json ./path/to/package/
```

Parse the JSON test output, count pass/fail/skip, and include in the step report. The pipeline doesn't fail on test failures — it reports them as metrics.

---

## 6. Multi-source + `{name}` templating

When `source` is an array, the pipeline runs once per source. Template variables available:

| Variable | Value |
|----------|-------|
| `{name}` | Derived from source: filename without extension, or repo name |
| `{source}` | Full source string |
| `{hash}` | Content hash (short, 8 chars) |

```yaml
pipelines:
  specs:
    - action: extract
      source:
        - testdata/specs/petstore.yaml
        - testdata/specs/stripe.yaml
        - testdata/specs/twilio.yaml
      out: /tmp/compschema-specs/{name}/schema.json
    - action: import
      source: /tmp/compschema-specs/{name}/schema.json
      package: "{name}"
      out: /tmp/compschema-specs/{name}/types.go
    - action: generate
      all: true
      validate: true
      test: true
      packages: [/tmp/compschema-specs/{name}/]
```

`compschema run specs` replaces `bash testdata/specs/run_all.sh`.

---

## 7. Replace Makefile + run_all.sh

Once the above is implemented, the Makefile and run_all.sh become a single `.compschema.yaml`:

```yaml
pipelines:
  openai:
    - action: extract
      source: testdata/openai/openapi.yaml
      path: /responses
      validate: true
      out: testdata/openai/responses.schema.json
    - action: import
      source: testdata/openai/responses.schema.json
      package: openairesponses
      out: examples/openai/types.go
    - action: generate
      all: true
      validate: true
      test: true
      emit_ir: true
      packages: [./examples/openai/]

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
      out: /tmp/compschema-specs/{name}/schema.json
    - action: import
      source: /tmp/compschema-specs/{name}/schema.json
      package: "{name}"
      out: /tmp/compschema-specs/{name}/types.go
    - action: generate
      all: true
      validate: true
      test: true
      packages: [/tmp/compschema-specs/{name}/]
```

Then delete `Makefile` and `testdata/specs/run_all.sh`.

---

## 8. Taskfile additions

```yaml
  generate:config-schema:
    desc: Regenerate config schema (dogfooding compschema on itself)
    cmds:
      - "{{.COMPSCHEMA}} generate --all --validate --out internal/config ./internal/config/"

  build:
    desc: Build compschema binary + regenerate config schema
    cmds:
      - go build -o ./bin/compschema ./cmd/compschema
      - task: generate:config-schema
```

---

## Implementation order

| # | Item | Depends on | Effort |
|---|------|-----------|--------|
| 1 | Taskfile tasks (build + config schema) | — | 10 min |
| 2 | Source abstraction (file/HTTP/Git + hashing) | — | 2–3 hr |
| 3 | `--test` flag + test metrics | — | 1 hr |
| 4 | Pipeline reports (structured output) | #3 | 1–2 hr |
| 5 | Multi-source + `{name}` templating | #2, #4 | 2 hr |
| 6 | IR emission (`--emit-ir`) | — | 1 hr |
| 7 | Replace Makefile + run_all.sh | #5 | 30 min |
| 8 | Merkle hashing on IR nodes | #6 | 2–3 hr |
| 9 | IR diff with hash-based pruning | #8 | 1–2 hr |
| 10 | Incremental compilation | #8 | 3–4 hr |
