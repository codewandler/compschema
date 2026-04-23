# Feature 3: `implement` — generate accessor methods on union variants

## Status: Implementing

## Problem

Discriminated unions generate a sealed interface with a marker method, but consumers often need **accessor methods** on all variants — most commonly to get the discriminator value through a shared interface method. This forces hundreds of lines of boilerplate (see `agentapis/api/responses/stream.go`: 131 lines, 53 variants × 2 methods each).

compschema already knows the discriminator field and its type. It should generate the accessor automatically.

## Design

**Additive** — the existing sealed interface keeps its marker method. Accessor methods are added to the interface and implemented on every struct variant.

### Default behavior (zero config)

For every discriminated union, auto-generate a discriminator accessor:

```go
// Before (today):
type ResponseStreamEvent interface {
    isResponseStreamEvent()
}

// After (auto, additive):
type ResponseStreamEvent interface {
    isResponseStreamEvent()
    DiscriminatorValue() string   // ← auto-generated
}

// On every struct variant:
func (x *ResponseCreatedEvent) DiscriminatorValue() string { return string(x.Type) }
```

Default method name: `DiscriminatorValue`.

### With config (custom method name)

```yaml
- action: import
  implement:
    - union: ResponseStreamEvent
      discriminator_method: EventType
```

Overrides the method name:

```go
type ResponseStreamEvent interface {
    isResponseStreamEvent()
    EventType() string
}
func (x *ResponseCreatedEvent) EventType() string { return string(x.Type) }
```

## Implementation

### Changes

1. **`internal/importer/config.go`** — Add `ImplementRule` type and `Implement` field to `Config`
2. **`internal/uniongen/uniongen.go`** — Accept accessor config, emit accessor methods + augment interface
3. **`internal/config/config.go`** — Add `Implement` field to pipeline `Action`
4. **`cmd/compschema/importcmd.go`** — Add `--implement` CLI flag
5. **`cmd/compschema/run.go`** — Wire implement config through pipeline

### Phases

**Phase 1:** Core codegen in uniongen (~2 hr)
**Phase 2:** Config + CLI wiring (~30 min)
**Phase 3:** Regenerate + test (~30 min)

**Total: ~3 hr**
