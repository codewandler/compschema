# compschema

**Compile-time JSON Schema generator for Go.**

compschema generates JSON Schema documents, `Decode`, and `Validate` functions from Go types using static analysis (`go/ast` + `go/types`) — zero reflection at runtime.

> **Status**: Early development. The OpenAPI → JSON Schema extraction pipeline is functional. The core Go → JSON Schema generator is in design (see [PRD.md](PRD.md)).

## Quick start

```bash
# Install
go install github.com/codewandler/compschema/cmd/compschema@latest

# Extract JSON Schema from an OpenAPI spec
compschema extract --spec openapi.yaml --path /responses --validate --out responses.schema.json

# List available component schemas
compschema schemas --spec openapi.yaml

# Generate from Go types (not yet implemented)
compschema generate ./...
```

## Pipeline

compschema includes a round-trip validation pipeline that uses the [OpenAI Responses API](https://platform.openai.com/docs/api-reference/responses) as an acceptance test:

```
OpenAI OpenAPI YAML → JSON Schema (ground truth) → Go structs → compschema → JSON Schema → diff
```

Run the full pipeline:

```bash
make pipeline
```

```
=== compschema round-trip pipeline ===

  OpenAPI spec:  39,848 lines
  JSON Schema:   122,419 bytes (124 $defs, meta-schema valid ✅)
  Go types:      272 declarations (102 structs, 134 enums)
  compschema:    not yet implemented
```

## OpenAPI → JSON Schema converter

`compschema extract` converts OpenAPI 3.x Schema Objects into self-contained JSON Schema draft 2020-12 documents. It resolves all `$ref` chains, collects transitive dependencies into `$defs`, and optionally validates the output against the JSON Schema meta-schema.

### Supported keywords

Every field on the OpenAPI 3.x Schema Object is explicitly handled — either converted to its JSON Schema equivalent, mapped to an extension, or intentionally stripped with documentation.

#### Composition

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `allOf` | `allOf` | `$ref` entries resolved to `$defs` |
| `oneOf` | `oneOf` | |
| `anyOf` | `anyOf` | |
| `not` | `not` | |
| `if` / `then` / `else` | `if` / `then` / `else` | 3.1+ / JSON Schema 2020-12 |
| `dependentSchemas` | `dependentSchemas` | 3.1+ |
| `dependentRequired` | `dependentRequired` | 3.1+ |

#### Type & format

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `type` | `type` | Single string or array (3.1) |
| `nullable: true` (3.0) | `type: ["T", "null"]` | Merged into type array per draft 2020-12 |
| `format` | `format` | `date-time`, `uri`, `email`, etc. |

#### Metadata & annotations

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `title` | `title` | |
| `description` | `description` | |
| `$comment` | `$comment` | 3.1+ |
| `deprecated` | `deprecated` | Emitted as `true` when set |
| `readOnly` | `readOnly` | Emitted as `true` when set |
| `writeOnly` | `writeOnly` | Emitted as `true` when set |
| `default` | `default` | |
| `enum` | `enum` | |
| `const` | `const` | |
| `example` (singular, 3.0) | `examples: [value]` | Promoted to array per draft 2020-12 |
| `examples` (array, 3.1) | `examples` | Pass-through |

#### Object keywords

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `properties` | `properties` | `$ref` values resolved to `$defs` |
| `patternProperties` | `patternProperties` | |
| `additionalProperties` | `additionalProperties` | Schema or boolean |
| `unevaluatedProperties` | `unevaluatedProperties` | Schema or boolean, 3.1+ |
| `propertyNames` | `propertyNames` | 3.1+ |
| `required` | `required` | |
| `minProperties` | `minProperties` | |
| `maxProperties` | `maxProperties` | |

#### Array keywords

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `items` | `items` | Schema or boolean (3.1) |
| `prefixItems` | `prefixItems` | Tuple validation, 3.1+ |
| `unevaluatedItems` | `unevaluatedItems` | 3.1+ |
| `contains` | `contains` | 3.1+ |
| `minContains` | `minContains` | 3.1+ |
| `maxContains` | `maxContains` | 3.1+ |
| `minItems` | `minItems` | |
| `maxItems` | `maxItems` | |
| `uniqueItems` | `uniqueItems` | Emitted as `true` when set |

#### Numeric constraints

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `minimum` | `minimum` | |
| `maximum` | `maximum` | |
| `exclusiveMinimum` (3.0 bool) | `exclusiveMinimum` (number) | Converted: `true` + `minimum: N` → `exclusiveMinimum: N` |
| `exclusiveMinimum` (3.1 number) | `exclusiveMinimum` | Pass-through |
| `exclusiveMaximum` (3.0 bool) | `exclusiveMaximum` (number) | Converted: `true` + `maximum: N` → `exclusiveMaximum: N` |
| `exclusiveMaximum` (3.1 number) | `exclusiveMaximum` | Pass-through |
| `multipleOf` | `multipleOf` | |

#### String constraints

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `minLength` | `minLength` | |
| `maxLength` | `maxLength` | |
| `pattern` | `pattern` | |
| `contentEncoding` | `contentEncoding` | JSON Schema content vocabulary |
| `contentMediaType` | `contentMediaType` | JSON Schema content vocabulary |
| `contentSchema` | `contentSchema` | 3.1+ |

#### Identity & references

| OpenAPI / JSON Schema keyword | Converter output | Notes |
|-------------------------------|-----------------|-------|
| `$ref` | `$ref` → `#/$defs/Name` | Resolved from `#/components/schemas/` |
| `$id` | `$id` | On sub-schemas only (top-level set by document builder) |
| `$anchor` | `$anchor` | 3.1+ |
| `$dynamicAnchor` | `$dynamicAnchor` | 3.1+ |
| `$dynamicRef` | `$dynamicRef` | 3.1+ |

#### OpenAPI-specific (mapped)

| OpenAPI keyword | Converter output | Notes |
|-----------------|-----------------|-------|
| `discriminator` | `x-discriminator` | Preserved with `propertyName` and `mapping` for downstream union detection |
| `x-*` extensions | `x-*` | All vendor extensions passed through (e.g. `x-stainless-const`, `x-oaiMeta`) |

#### OpenAPI-specific (stripped)

| OpenAPI keyword | Reason |
|-----------------|--------|
| `xml` | XML serialization hint — not relevant for JSON Schema |
| `externalDocs` | OpenAPI documentation link — no JSON Schema equivalent |
| `$schema` (nested) | Dialect declaration — set at document level, not per definition |
| `$vocabulary` | Meta-schema vocabulary — not per-schema |
| `nullable` | Already handled — merged into `type` array |

## Project layout

```
cmd/compschema/              CLI (cobra)
  main.go                    Root command
  extract.go                 extract + schemas subcommands
  generate.go                generate subcommand (stub)
internal/openapi2jsonschema/ OpenAPI → JSON Schema converter
  converter.go               Schema conversion (631 lines, exhaustive field coverage)
  validate.go                Meta-schema validation (draft 2020-12)
  validate_test.go           Validation tests
testdata/openai/             Round-trip pipeline test fixtures
  openapi.yaml               OpenAI OpenAPI spec (39,848 lines)
  responses.schema.json      Extracted JSON Schema (122KB, 124 $defs)
  generated/types.go         Generated Go structs (272 types)
Makefile                     Pipeline orchestration
PRD.md                       Project design document
```

## License

MIT
