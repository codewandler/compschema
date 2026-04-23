# Feature 1: `embed` — extract repeated fields into embedded structs

## Status: Planned

## Problem

When importing types from an OpenAPI spec, compschema generates flat structs that match the spec 1:1. But real-world Go code often needs **struct embedding** — a group of related types sharing a common set of fields should embed a shared base type instead of repeating those fields.

**Concrete example:** The OpenAI Responses API defines 53 event types. Every one has `type` (string const) and `sequence_number` (integer). In idiomatic Go, these should be an embedded `EventMeta` struct:

```go
// What the spec gives us (53 times):
type ResponseCreatedEvent struct {
    Type           string   `json:"type" jsonschema:"const=response.created"`
    SequenceNumber int      `json:"sequence_number"`
    Response       Response `json:"response"`
}

// What we actually want:
type ResponseCreatedEvent struct {
    EventMeta                              // provides type + sequence_number
    Response  Response `json:"response"`
}
```

Today this requires hand-writing all 53 structs (270+ lines) solely to get embedding.

## Design: IR-level transform, not schema patching

The previous proposal (`inject`) tried to solve this by patching JSON Schema before import — adding `$ref` fragments and removing properties. That's the wrong abstraction layer: struct embedding is a Go concept, JSON Schema has no notion of it. The schema manipulation created "opaque refs" to types that don't exist in the schema, broke schema integrity, and required two config fields (`schema` + `remove`) to express one intent.

**`embed` operates at the IR level instead** — the same layer as `rename` and `exclude`. The schema is parsed normally into flat IR types, then an IR transform extracts repeated fields into a shared type and replaces them with an embedded field.

```
schema JSON → parse → IR (flat) → embed transform → rename → exclude → emit Go
```

This keeps the schema untouched, uses a single directive to express intent, and composes cleanly with existing IR transforms.

---

## Config syntax

```yaml
- action: import
  schema: responses.schema.json
  package: responses
  out: types.gen.go
  embed:
    - type: EventMeta
      fields: [type, sequence_number]
      into: "*Event"
```

| Key | Type | Description |
|-----|------|-------------|
| `type` | `string` | Name of the embedded struct to create (or reference if it already exists) |
| `fields` | `[]string` | JSON property names to extract from matched types |
| `into` | `string` | Glob pattern matching type names that should receive the embed |

### Multiple embeds

```yaml
embed:
  - type: EventMeta
    fields: [type, sequence_number]
    into: "*Event"
  - type: Timestamps
    fields: [created_at, updated_at]
    into: "*Resource"
```

Multiple embed rules are applied sequentially. A single type can receive multiple embeds.

---

## Implementation plan

### Phase 1: IR support for embedded fields

#### Step 1.1 — Add `Embedded` flag to `ir.Field`
**Files:** `internal/ir/types.go`
**Effort:** 5 min

Add one field:

```go
type Field struct {
    // ... existing fields ...
    Embedded bool // true for struct embedding (no json tag, promoted fields)
}
```

This flag is already implicitly handled in the **forward** direction — the analyzer detects `field.Embedded()` and flattens fields (analyzer.go:246). Adding the flag to the IR lets the **reverse** direction (import) express embeds that the emitter can emit.

**Verification:** `go build ./... && go test ./...` — no behavior change yet.

---

#### Step 1.2 — Update hash computation
**Files:** `internal/ir/hash.go`
**Effort:** 10 min

Include `f.Embedded` in the field hash:

```go
// In hashField():
writeUint8(h, boolByte(f.Embedded))
```

**Verification:** `go test ./internal/ir/...`

---

### Phase 2: Embed transform

#### Step 2.1 — Implement `applyEmbeds` in importer config
**Files:** `internal/importer/config.go`
**Effort:** 2 hr

Add the config type and transform function:

```go
// EmbedRule defines a field extraction + embedding rule.
type EmbedRule struct {
    Type   string   // name of the embed struct (e.g. "EventMeta")
    Fields []string // JSON property names to extract (e.g. ["type", "sequence_number"])
    Into   string   // glob pattern for target types (e.g. "*Event")
}

// Config gains a new field:
type Config struct {
    // ... existing fields ...
    Embed []EmbedRule
}
```

Implement `applyEmbeds(pkg *ir.Package, rules []EmbedRule)`:

1. **For each rule**, iterate `pkg.Types` and collect types matching the `Into` glob.

2. **Build the embed type** from the first matched type:
   - Extract fields matching the `Fields` list (by `JSONName`).
   - Create a new `ir.Type{Name: rule.Type, Kind: KindStruct, Fields: extractedFields}`.
   - Add it to `pkg.Types` and `pkg.Order` (if not already present).
   - If the type already exists in the package (user-defined or from a previous rule), validate that its fields are compatible — same JSON names and types. If incompatible, return an error.

3. **Transform each matched type:**
   - Remove the extracted fields from `t.Fields`.
   - Prepend a single embedded field:
     ```go
     ir.Field{
         Name:     rule.Type,
         JSONName: "",        // embedded fields have no json tag
         Embedded: true,
         Required: true,
         Type:     ir.TypeRef{Name: rule.Type},
     }
     ```

4. **Ordering:** Insert the embed type before the first type that uses it in `pkg.Order`.

**Edge cases:**
- A matched type is missing some of the specified fields → skip that type (don't embed), log a warning.
- A matched type has *all* fields extracted → the struct becomes just the embed (valid, but unusual). Allow it.
- Fields have different constraints across types (e.g., different `const` values for `type`) → extract the field without constraints. The per-variant `const` moves to the matched type's `//compschema:generate` annotation or remains on the field if it's the only one left.

**Verification:**
```go
func TestApplyEmbeds_Basic(t *testing.T) {
    // Create IR with 3 event types, each having type + sequence_number + one unique field.
    // Apply embed rule: EventMeta{type, sequence_number} into "*Event".
    // Assert: EventMeta type created, each event type has 1 embedded field + 1 unique field.
}

func TestApplyEmbeds_MissingFields(t *testing.T) {
    // Type matches glob but doesn't have all specified fields → skip embed for that type.
}

func TestApplyEmbeds_NoMatch(t *testing.T) {
    // No types match glob → no changes, no error.
}
```

---

#### Step 2.2 — Wire into `ApplyConfig`
**Files:** `internal/importer/config.go`
**Effort:** 10 min

Add embed application to the transform pipeline:

```go
func ApplyConfig(pkg *ir.Package, cfg Config) {
    if cfg.Package != "" {
        pkg.Name = cfg.Package
    }
    if len(cfg.Embed) > 0 {
        applyEmbeds(pkg, cfg.Embed)    // before exclude/rename
    }
    if len(cfg.Exclude) > 0 {
        applyExcludes(pkg, cfg.Exclude)
    }
    if len(cfg.Rename) > 0 {
        applyRenames(pkg, cfg.Rename)
    }
}
```

**Order rationale:** Embeds run first because they operate on field names from the schema (pre-rename). Excludes run after because you might exclude types that the embed created. Renames run last as before.

---

### Phase 3: Go emission

#### Step 3.1 — Emit embedded fields in importer
**Files:** `internal/importer/importer.go`
**Effort:** 30 min

Update `emitStruct` to handle `f.Embedded`:

```go
for _, f := range t.Fields {
    if f.Embedded {
        // Emit as promoted field (no json tag, no type annotation).
        b.WriteString(fmt.Sprintf("\t%s\n", toGoName(f.Name)))
        continue
    }
    // ... existing field emission logic ...
}
```

**Verification:** Generate Go code from an IR with embedded fields, compile it.

---

#### Step 3.2 — Handle embeds in the analyzer (forward path)
**Files:** `internal/analyzer/analyzer.go`
**Effort:** 30 min

The analyzer currently flattens embedded fields (line 246). Update it to **also** set the `Embedded` flag when it encounters an embedded struct, so the IR preserves embedding information for the round-trip:

```go
if field.Embedded() {
    embType := field.Type()
    // ... existing flattening logic stays ...
    
    // Also record the embed itself as a field in the IR:
    irType.Fields = append(irType.Fields, ir.Field{
        Name:     field.Name(),
        Embedded: true,
        Type:     ir.TypeRef{Name: field.Name()},
    })
}
```

**Decision:** This step is optional for the initial implementation. The flattening behavior is correct for schema generation (JSON Schema has no embeds). The `Embedded` flag is primarily useful for the import path. If round-trip fidelity through the analyzer becomes important, this step adds it.

**Status:** Deferred — implement only if needed for `generate` round-trip tests.

---

### Phase 4: CLI + pipeline config

#### Step 4.1 — Add `--embed` flag to `compschema import`
**Files:** `cmd/compschema/importcmd.go`
**Effort:** 30 min

Add a repeatable `--embed` flag:

```bash
compschema import --package responses --out types.go \
  --embed "EventMeta:type,sequence_number:*Event" \
  schema.json
```

Format: `TypeName:field1,field2:GlobPattern`

Parse into `[]EmbedRule` and pass to `ImportWithConfig`.

---

#### Step 4.2 — Add `embed` to pipeline config
**Files:** `internal/config/config.go`
**Effort:** 15 min

Add to the `Action` struct:

```go
type EmbedRule struct {
    Type   string   `json:"type" yaml:"type" jsonschema:"description=Name of the embedded struct to create or reference"`
    Fields []string `json:"fields" yaml:"fields" jsonschema:"description=JSON property names to extract"`
    Into   string   `json:"into" yaml:"into" jsonschema:"description=Glob pattern for target types"`
}

type Action struct {
    // ... existing fields ...
    Embed []EmbedRule `json:"embed,omitempty" yaml:"embed,omitempty" jsonschema:"description=Extract shared fields into embedded structs"`
}
```

---

#### Step 4.3 — Wire into `runImportAction`
**Files:** `cmd/compschema/run.go`
**Effort:** 15 min

Pass `Embed` rules from the action config to `importer.Config`:

```go
cfg := importer.Config{
    Package: a.Package,
    Rename:  a.Rename,
    Exclude: a.Exclude,
    Tags:    a.Tags,
    Embed:   convertEmbedRules(a.Embed),
}
```

---

#### Step 4.4 — Regenerate config schema
**Files:** `internal/config/schema.gen.json`, `internal/config/compschema.gen.go`, `internal/config/compschema.gen_test.go`
**Effort:** 5 min

```bash
task generate:config-schema
```

---

### Phase 5: Testing

#### Step 5.1 — Unit tests for embed transform
**Files:** `internal/importer/config_test.go` (new or extend)
**Effort:** 1 hr

| Test | Description |
|------|-------------|
| `TestApplyEmbeds_Basic` | 3 types with shared fields → embed created, fields replaced |
| `TestApplyEmbeds_MissingFields` | Matched type missing some fields → skipped |
| `TestApplyEmbeds_NoMatch` | Glob matches nothing → no changes |
| `TestApplyEmbeds_MultipleRules` | Two embed rules applied to different type groups |
| `TestApplyEmbeds_OverlappingRules` | Same type matched by two rules → receives two embeds |
| `TestApplyEmbeds_ExistingType` | Embed type already exists in IR → reuse, don't create duplicate |
| `TestApplyEmbeds_AllFieldsExtracted` | All fields extracted → struct is just the embed |
| `TestApplyEmbeds_DifferentConstraints` | Shared fields have different `const` values → extract without constraints |

---

#### Step 5.2 — Integration test with OpenAI events
**Files:** `cmd/compschema/run.go` or a test file
**Effort:** 30 min

Run the full pipeline with embed config against the OpenAI spec:

```yaml
embed:
  - type: EventMeta
    fields: [type, sequence_number]
    into: "*Event"
```

Verify:
- `EventMeta` type is generated with `Type string` and `SequenceNumber int` fields
- All 53 event types have `EventMeta` embedded
- Generated code compiles
- Generated tests pass

---

## Future: auto-detect mode

Once the basic embed transform works, an auto-detect mode becomes possible:

```yaml
embed:
  - type: EventMeta
    into: "*Event"
    # fields omitted → auto-detect from existing Go type
```

When `fields` is omitted:
1. Use `go/packages` to load the target package
2. Find the type named `EventMeta`
3. Extract its field JSON names
4. Use those as the `fields` list

This is a natural extension but not required for the initial implementation. The explicit `fields` list is clear, predictable, and doesn't require package loading.

---

## Future: `allOf` → embed detection

OpenAPI specs that use `allOf` for composition already express the embed relationship in the schema:

```json
{
  "ResponseCreatedEvent": {
    "allOf": [
      { "$ref": "#/$defs/EventBase" },
      { "properties": { "response": { "$ref": "#/$defs/Response" } } }
    ]
  }
}
```

The JSON Schema → IR parser could detect this pattern and set `Embedded: true` on the base type's fields automatically. This would require changes to `internal/jsonschema2ir/parser.go` — when an `allOf` has exactly one `$ref` + one inline object, treat the `$ref` as an embed rather than flattening.

This is orthogonal to the `embed` config directive and can be implemented independently.

---

## Execution order

```
Phase 1 (IR support) — 15 min
  1.1  Add Embedded flag to ir.Field        5 min
  1.2  Update hash computation             10 min

Phase 2 (Transform) — 2.5 hr
  2.1  Implement applyEmbeds                2 hr
  2.2  Wire into ApplyConfig               10 min

Phase 3 (Emission) — 30 min
  3.1  Emit embedded fields in importer    30 min

Phase 4 (CLI + config) — 1 hr
  4.1  --embed CLI flag                    30 min
  4.2  Pipeline config types               15 min
  4.3  Wire into runImportAction           15 min
  4.4  Regenerate config schema             5 min

Phase 5 (Testing) — 1.5 hr
  5.1  Unit tests                           1 hr
  5.2  Integration test                    30 min
```

**Total effort:** ~5.5 hr
**Critical path:** 1.1 → 2.1 → 3.1 → 5.2

---

## File inventory

### New files

| File | Phase |
|------|-------|
| `internal/importer/embed.go` | 2.1 |
| `internal/importer/embed_test.go` | 5.1 |

### Modified files

| File | Phase | Change |
|------|-------|--------|
| `internal/ir/types.go` | 1.1 | Add `Embedded bool` to `Field` |
| `internal/ir/hash.go` | 1.2 | Include `Embedded` in field hash |
| `internal/importer/config.go` | 2.1, 2.2 | Add `EmbedRule`, `applyEmbeds`, wire into `ApplyConfig` |
| `internal/importer/importer.go` | 3.1 | Handle `f.Embedded` in `emitStruct` |
| `cmd/compschema/importcmd.go` | 4.1 | Add `--embed` flag |
| `internal/config/config.go` | 4.2 | Add `EmbedRule` + `Embed` field to `Action` |
| `cmd/compschema/run.go` | 4.3 | Pass embed rules to importer config |
| `internal/config/schema.gen.json` | 4.4 | Regenerated |
| `internal/config/compschema.gen.go` | 4.4 | Regenerated |
| `internal/config/compschema.gen_test.go` | 4.4 | Regenerated |

---

## Why this is better than `inject`

| | `inject` (superseded) | `embed` (this plan) |
|---|---|---|
| **Layer** | JSON Schema (pre-parse) | IR (post-parse) |
| **Config** | `schema` fragment + `remove` list | `type` + `fields` + `into` |
| **Phantom types** | Yes — "opaque refs" for non-existent `$defs` | No — creates real IR types |
| **Schema integrity** | Modified before import | Never touched |
| **Intent** | Add this ref, remove those fields | Extract these fields into an embed |
| **Composability** | Separate from rename/exclude pipeline | Same pipeline, same level |
| **Error surface** | Schema fragment must be valid JSON Schema | Field names are just strings |
