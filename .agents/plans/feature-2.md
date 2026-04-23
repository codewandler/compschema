# Feature 2: Constructors — generated type-safe instantiation

## Status: Brainstorming

## The pain

Using compschema-generated types today is tedious and error-prone. Every struct has fields with constraints, const discriminators, and required/optional semantics — but constructing them is raw struct literals:

```go
// Creating a Circle — you have to know "type" must be "circle"
shape := &Circle{
    Type:   "circle",   // easy to typo, no compile-time check
    Radius: 5.0,
}

// Creating an event — you have to know the const value
event := ResponseCreatedEvent{
    Type:     ResponseCreatedEventTypeResponseCreated,  // verbose, discoverable only via docs
    Response: resp,
}

// Creating a Request with optional fields — awkward pointer dance
notes := "rush order"
order := Order{
    ID:     "ord-123",
    Items:  []LineItem{{SKU: "ABC-123", Qty: 2}},
    Status: OrderPending,
    Notes:  &notes,       // can't inline string literal as pointer
}
```

Problems:
1. **Const fields are footguns** — 86 types in the OpenAI spec have a `Type` field with a single valid value. Users must set it manually or get runtime validation errors.
2. **Optional fields require pointer ceremony** — `*string` means you can't just write `Notes: "rush"`.
3. **No guidance on required vs optional** — struct literal compiles fine with zero values for required fields.
4. **Union construction is unguided** — creating a `Shape` means knowing which concrete types implement it.

## Design options

### Option A: Simple constructors (`New` functions)

Generate a `NewT(required...) T` function per struct:

```go
// Generated:
func NewCircle(radius float64) *Circle {
    return &Circle{
        Type:   "circle",
        Radius: radius,
    }
}

func NewOrder(id string, items []LineItem, status OrderStatus) Order {
    return Order{
        ID:     id,
        Items:  items,
        Status: status,
    }
}
```

**Pros:**
- Dead simple, idiomatic Go
- Const fields auto-filled — impossible to forget or typo
- Required fields are function params — compiler enforces them
- Optional fields use `With` setters or direct field assignment after construction
- Zero new concepts

**Cons:**
- Functions with many required params get unwieldy (`NewResponse` would have 10+ params)
- No guided optional field setting
- Breaking change if a field goes from optional to required (param added)

### Option B: Functional options (`With` pattern)

```go
func NewOrder(id string, items []LineItem, status OrderStatus, opts ...OrderOption) Order
func WithNotes(notes string) OrderOption

// Usage:
order := NewOrder("ord-123", items, OrderPending, WithNotes("rush"))
```

**Pros:**
- Optional fields are clean — `WithNotes("rush")` instead of pointer dance
- Extensible without breaking API
- Standard Go pattern

**Cons:**
- More generated code (option type + `With` func per optional field)
- Indirection (options are closures)
- Overkill for simple types

### Option C: Builder pattern

```go
order := NewOrderBuilder().
    ID("ord-123").
    Items(items).
    Status(OrderPending).
    Notes("rush").
    Build()
```

**Pros:**
- Discoverability via autocomplete
- Fluent API

**Cons:**
- Very un-idiomatic Go
- Huge codegen surface (builder struct + method per field)
- `Build()` can't return compile-time errors for missing required fields
- Runtime validation or panics

### Option D: Required-params constructor + direct field mutation

```go
// Generated: only required fields as params, const fields auto-filled
func NewCircle(radius float64) *Circle

// Optional fields: just set them directly
order := NewOrder("ord-123", items, OrderPending)
order.Notes = ptr("rush")  // or use generated helper

// Generated helper for pointer fields:
func Ptr[T any](v T) *T { return &v }
```

**Pros:**
- Minimal generated code
- Required fields enforced at compile time
- Const fields auto-filled
- Optional fields use normal Go (direct assignment)
- `Ptr[T]` helper solves the pointer ceremony universally
- No new patterns to learn

**Cons:**
- Direct field mutation means no validation at construction time (but `Validate()` catches it)
- `Ptr()` is a pattern, not generated per-field

---

## Recommendation: Option D (constructors + Ptr helper)

Option D hits the sweet spot: minimal API surface, maximum value for the two biggest pain points (const fields and required params), and zero new patterns. It's what you'd write by hand.

### What gets generated

#### 1. `NewT(required...) *T` per struct

```go
// Auto-fills: Type = "circle" (from const constraint)
// Required params: Radius (required, no default)
func NewCircle(radius float64) *Circle {
    return &Circle{
        Type:   "circle",
        Radius: radius,
    }
}
```

**Rules:**
- **Const fields** → auto-filled, NOT a param (the whole point)
- **Required fields** (no `omitempty`) → function params, in declaration order
- **Optional fields** (`omitempty`) → omitted, set via direct field access after construction
- **Fields with `default`** → auto-filled with default value, NOT a param
- **Return type** → pointer for struct types that implement union interfaces (needed for marker method), value otherwise. Actually: always pointer — consistent, enables interface satisfaction, avoids copy.

#### 2. `Ptr[T any](v T) *T` helper

```go
// Generated once per package in compschema.gen.go:
func compschemaPtr[T any](v T) *T { return &v }
```

Solves `order.Notes = compschemaPtr("rush")` without import. Short name since it's package-private.

Actually — export it. Users need it too:

```go
// Ptr returns a pointer to v. Useful for setting optional fields.
func Ptr[T any](v T) *T { return &v }
```

#### 3. Union convenience constructors (optional, high value)

For discriminated unions, generate constructors that return the interface:

```go
// Creates a Shape from a Circle.
func NewCircleShape(radius float64) Shape {
    return NewCircle(radius)
}

// Or just use NewCircle directly — it returns *Circle which implements Shape.
```

Actually, `NewCircle` already returns `*Circle` which satisfies `Shape`. No extra constructors needed for unions — the type system handles it. The value is that `NewCircle` auto-fills `Type: "circle"`, so the user doesn't need to know the discriminator value.

### What does NOT get generated

- **No builders** — un-idiomatic, massive codegen, no compile-time required field safety
- **No functional options** — overkill for most types, and `Validate()` already catches errors
- **No constructors for enums** — they're already type-safe constants
- **No constructors for unions** — the variant constructors return the interface implicitly
- **No constructors for types with zero required fields** — nothing to enforce

---

## Examples

### Before (today)

```go
// 1. Must know const value, easy to typo
event := ResponseCreatedEvent{
    Type:     ResponseCreatedEventTypeResponseCreated,
    Response: resp,
}

// 2. Optional string field requires pointer
notes := "rush order"
order := Order{
    ID:     "ord-123",
    Items:  []LineItem{{SKU: "ABC-123", Qty: 2}},
    Status: OrderPending,
    Notes:  &notes,
}

// 3. Circle — must remember to set Type
circle := &Circle{
    Type:   "circle",
    Radius: 5.0,
}
```

### After (with constructors)

```go
// 1. Const auto-filled, only meaningful fields as params
event := NewResponseCreatedEvent(resp)

// 2. Clean optional field setting
order := NewOrder("ord-123", []LineItem{NewLineItem("ABC-123", 2)}, OrderPending)
order.Notes = Ptr("rush order")

// 3. Circle — Type is auto-filled
circle := NewCircle(5.0)
```

---

## Implementation plan

### Phase 1: Constructor generation

#### Step 1.1 — Add `Ptr[T]` helper to codegen
**Files:** `internal/emitter/emitter.go`
**Effort:** 10 min

Emit once per package, before per-type methods:

```go
// Ptr returns a pointer to v. Useful for optional fields on generated types.
func Ptr[T any](v T) *T { return &v }
```

Guard with a check: only emit if any struct has optional pointer fields.

**Verification:** `go build ./...`

---

#### Step 1.2 — Identify const/default fields from IR
**Files:** `internal/emitter/emitter.go`
**Effort:** 30 min

Write helper functions:

```go
// constValue returns the const constraint value for a field, or nil.
func constValue(f ir.Field) any

// defaultValue returns the default constraint value for a field, or nil.
func defaultValue(f ir.Field) any

// isAutoFilled returns true if the field should be auto-filled in the constructor.
func isAutoFilled(f ir.Field) bool  // has const OR has default

// constructorParams returns the required, non-auto-filled fields in declaration order.
func constructorParams(t *ir.Type) []ir.Field
```

**Verification:** Unit test with Circle (const on Type), Order (required + optional fields).

---

#### Step 1.3 — Emit `NewT()` constructors
**Files:** `internal/emitter/emitter.go`
**Effort:** 2 hr

For each `KindStruct` type with at least one required field or one const field:

```go
func emitConstructor(b *strings.Builder, name string, t *ir.Type, pkg *ir.Package)
```

Logic:
1. Collect `constructorParams(t)` → the function signature params.
2. Collect auto-filled fields (const, default) → assigned inside the body.
3. If no params AND no auto-filled fields → skip (nothing useful to generate).
4. If ONLY auto-filled fields and no required params → still generate (e.g., `NewResponseCreatedEvent()` that fills only `Type`).

Emit:

```go
func New{Name}({params}) *{Name} {
    return &{Name}{
        {AutoFilled}: {constOrDefaultValue},
        {Param1}: {param1},
        ...
    }
}
```

**Param naming:** Use the Go field name in camelCase: `func NewOrder(id string, items []LineItem, status OrderStatus)`. If that collides with a Go keyword, prefix with `_`.

**Edge cases:**
- Struct with all optional fields → only emit if there are const/default fields to auto-fill.
- Struct with 10+ required params → still generate, but log a note. The alternative (not generating) is worse.
- Nullable required fields (`*T` but no omitempty) → param type is `T`, constructor wraps with `&`.
- Field type is an interface (union) → param type is the interface.

**Verification:**
```bash
compschema generate --all ./examples/basic
go build ./examples/basic/...
go test ./examples/basic/...
```

---

#### Step 1.4 — Constructor tests
**Files:** `internal/emitter/emitter.go` (test generation section)
**Effort:** 1 hr

For each generated constructor, emit a test:

```go
func TestCompschema_NewCircle(t *testing.T) {
    c := NewCircle(5.0)
    if c.Type != "circle" {
        t.Errorf("Type = %q, want %q", c.Type, "circle")
    }
    if c.Radius != 5.0 {
        t.Errorf("Radius = %v, want %v", c.Radius, 5.0)
    }
    // Validate round-trip
    data, err := json.Marshal(c)
    if err != nil {
        t.Fatal(err)
    }
    if err := c.Validate(data); err != nil {
        t.Errorf("NewCircle output fails validation: %v", err)
    }
}
```

This proves the constructor output is schema-valid — a guarantee no other Go schema tool provides.

---

### Phase 2: CLI + config

#### Step 2.1 — Add `--constructors` flag to `generate`
**Files:** `cmd/compschema/generate.go`, `internal/config/config.go`
**Effort:** 15 min

```go
Constructors bool `json:"constructors,omitempty" jsonschema:"description=Generate NewT constructors for struct types"`
```

Default: `true` (constructors are always useful). Add `--no-constructors` to opt out.

Actually — default `false` for backwards compatibility in v3.x, default `true` in v4.

---

#### Step 2.2 — Add `--constructors` to `import`
**Files:** `cmd/compschema/importcmd.go`
**Effort:** 15 min

When importing types from JSON Schema, also generate constructors in the output. The importer already has the IR — just call `emitConstructor` for each struct.

Wait — the importer generates `types.go`, not `compschema.gen.go`. Constructors should go in the same file as the types since they're part of the type's API.

Decision: emit constructors at the end of the importer output, after all type definitions.

---

#### Step 2.3 — Regenerate config schema + examples
**Files:** regenerated files
**Effort:** 5 min

```bash
task generate:config-schema
task generate:basic
task generate:openai
```

---

### Phase 3: Import-path constructors

#### Step 3.1 — Emit constructors in importer output
**Files:** `internal/importer/importer.go`
**Effort:** 1 hr

After the fourth pass (UnmarshalJSON), add a fifth pass:

```go
// Fifth pass: emit constructors for struct types.
for _, name := range names {
    t, ok := pkg.Types[name]
    if !ok || t.Kind != ir.KindStruct {
        continue
    }
    emitConstructor(&b, toGoName(name), t, pkg)
}
```

Reuse the same `emitConstructor` from Phase 1 (move to shared package or duplicate — small function).

---

## Scope control

### In scope
- `NewT()` constructors for struct types (required params + const/default auto-fill)
- `Ptr[T]` helper
- Constructor tests (round-trip validation)
- Both `generate` and `import` paths

### Out of scope (future)
- Functional options (`WithField()`)
- Builder pattern
- Constructor for enum types (already have const blocks)
- Constructor overloads (e.g., `NewCircleWithOptions`)
- Mutation methods (`SetField()`) — direct field access is fine

---

## Execution order

```
Phase 1 (Constructor generation) — 3.5 hr
  1.1  Ptr[T] helper                      10 min
  1.2  Const/default field helpers         30 min
  1.3  Emit NewT() constructors             2 hr
  1.4  Constructor tests                    1 hr

Phase 2 (CLI + config) — 35 min
  2.1  --constructors flag (generate)      15 min
  2.2  --constructors flag (import)        15 min
  2.3  Regenerate schemas                   5 min

Phase 3 (Import path) — 1 hr
  3.1  Emit constructors in importer        1 hr
```

**Total effort:** ~5 hr
**Critical path:** 1.2 → 1.3 → 1.4

---

## File inventory

### Modified files

| File | Phase | Change |
|------|-------|--------|
| `internal/emitter/emitter.go` | 1.1–1.4 | `Ptr[T]`, `emitConstructor`, constructor tests |
| `internal/importer/importer.go` | 3.1 | Fifth pass: constructors |
| `cmd/compschema/generate.go` | 2.1 | `--constructors` flag |
| `cmd/compschema/importcmd.go` | 2.2 | `--constructors` flag |
| `internal/config/config.go` | 2.1 | `Constructors bool` on Action |

### Regenerated files

| File | Phase |
|------|-------|
| `internal/config/schema.gen.json` | 2.3 |
| `internal/config/compschema.gen.go` | 2.3 |
| `internal/config/compschema.gen_test.go` | 2.3 |
| `examples/basic/compschema.gen.go` | 2.3 |
| `examples/basic/compschema.gen_test.go` | 2.3 |
| `examples/openai/compschema.gen.go` | 2.3 |
| `examples/openai/compschema.gen_test.go` | 2.3 |

---

## Impact

For the OpenAI example alone:
- **86 types** with const `Type` fields get constructors that auto-fill the discriminator
- **112 structs** total get `New` functions with required-only params
- **109 optional fields** across the package become settable with `Ptr()` instead of pointer dance
- Every constructor output is **schema-validated in tests** — the only Go schema tool that proves construction correctness at test time
