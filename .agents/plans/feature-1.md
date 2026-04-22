# Feature 1: `inject` — merge schema fragments into imported types

## Status: Proposed

## Problem

When importing types from an OpenAPI spec, the generated Go structs are flat — they match the spec 1:1. But consumers often need structural modifications:

- **Embedding a base type** into a group of related types (e.g., all `*Event` types should embed an `EventMeta` struct instead of having flat `type` + `sequence_number` fields)
- **Adding fields** that don't exist in the spec (e.g., a `RawJSON json.RawMessage` field for debugging)
- **Replacing fields** with a shared embedded struct to satisfy a Go interface

Today this forces hundreds of lines of hand-written struct definitions that duplicate the spec. In agentapis, 53 event structs (270+ lines) are hand-written solely because they need `EventMeta` embedding instead of flat fields.

## Proposed solution

Add an `inject` option to the `import` action that merges JSON Schema fragments into matched types before Go code generation.

### Config syntax

```yaml
- action: import
  schema: responses.schema.json
  package: responses
  out: types.gen.go
  inject:
    - match: "*Event"
      schema:
        properties:
          meta:
            $ref: "#/$defs/EventMeta"
        required: [meta]
      remove: [type, sequence_number]
```

### Semantics

1. **`match`** — glob pattern against the JSON Schema `$defs` type name (before renames). Selects which types to modify.

2. **`schema`** — a JSON Schema fragment merged into each matched type's schema. Properties are added (or override existing ones). `required` entries are merged. This is just a schema-level patch — compschema already knows how to turn schema properties into Go struct fields, resolve `$ref`, decide embedding vs regular fields, etc.

3. **`remove`** — list of property names to drop from the matched type's schema before generating Go code. Used to remove fields that the injected schema already provides (e.g., `type` and `sequence_number` are provided by the embedded `EventMeta`).

### Processing order

```
extract → [schema $defs] → inject (match + merge + remove) → [patched $defs] → import → types.gen.go
```

Injection happens after schema extraction but before Go code generation. The patched schema is what gets fed to the importer. This keeps the importer itself unchanged — it just sees a modified schema.

### How compschema resolves the injected schema

The injected `$ref: "#/$defs/EventMeta"` is resolved by the same logic compschema already uses:

- If the `$ref` target exists in the extracted schema's `$defs`, it's resolved as usual.
- If it doesn't exist (e.g., `EventMeta` is a hand-written Go type, not in the spec), compschema should treat it as an **opaque embed** — generate a struct field with the Go type name derived from the `$defs` key, and assume the type is defined elsewhere in the same package.

This "opaque ref" behavior is the key design decision: it lets users reference hand-written types from injected schema fragments without those types needing to be in the spec.

## Use case: agentapis event types

The OpenAI Responses API spec defines 53 event types as flat structs:

```json
{
  "ResponseCreatedEvent": {
    "properties": {
      "type": {"type": "string", "const": "response.created"},
      "sequence_number": {"type": "integer"},
      "response": {"$ref": "#/$defs/Response"}
    }
  }
}
```

Today agentapis hand-writes all 53 as:

```go
type ResponseCreatedEvent struct {
    EventMeta                              // provides type + sequence_number
    Response ResponsePayload `json:"response"`
}
```

With `inject`, the config replaces all 53 hand-written structs:

```yaml
inject:
  - match: "Response*Event"
    schema:
      properties:
        meta:
          $ref: "#/$defs/EventMeta"
      required: [meta]
    remove: [type, sequence_number]
```

Generated output:

```go
type ResponseCreatedEvent struct {
    EventMeta                              // embedded from injected $ref
    Response ResponsePayload `json:"response"`
}
```

## Design notes

- `inject` is purely a schema-level transformation. No Go-specific concepts in the config.
- Multiple `inject` rules can apply to the same type (merged in order).
- `inject` runs after `rename` so patterns match the final names.
- The `schema` fragment uses standard JSON Schema syntax — no new DSL.
- `remove` is optional. If omitted, injected properties are added alongside existing ones.
- If a property in `schema.properties` already exists in the type, it's replaced (override semantics).

## Scope

- `inject` is an import-time feature only. It modifies the schema before Go code generation.
- It does not affect `extract` (the OpenAPI schema extraction is unchanged).
- It does not affect `generate` (the Go type analysis is unchanged).
- No changes to the JSON Schema output (`schema.gen.json`) — it reflects the post-injection schema.
