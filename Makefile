# compschema — development pipeline
#
# The round-trip validation pipeline:
#   OpenAI OpenAPI YAML → JSON Schema (ground truth) → Go structs → [compschema] → JSON Schema → diff
#
# Usage:
#   make pipeline       # run all pipeline steps
#   make fetch          # download OpenAI OpenAPI spec
#   make jsonschema     # extract + validate JSON Schema from OpenAPI spec
#   make gotypes        # generate Go structs from JSON Schema
#   make stats          # print pipeline stats
#   make clean          # remove generated artifacts

SPEC_URL    := https://raw.githubusercontent.com/openai/openai-openapi/refs/heads/manual_spec/openapi.yaml
SPEC_FILE   := testdata/openai/openapi.yaml
SCHEMA_OUT  := testdata/openai/responses.schema.json
PATH_PREFIX := /responses

COMPSCHEMA  := go run ./cmd/compschema

.PHONY: pipeline fetch jsonschema gotypes stats clean

pipeline: fetch jsonschema gotypes stats

# Step 0: Download the OpenAI OpenAPI spec
fetch: $(SPEC_FILE)
$(SPEC_FILE):
	@mkdir -p $(dir $@)
	curl -sL -o $@ '$(SPEC_URL)'
	@echo "✓ fetched spec → $@ ($$(wc -l < $@) lines)"

# Step 1: OpenAPI → JSON Schema (with meta-schema validation)
jsonschema: $(SCHEMA_OUT)
$(SCHEMA_OUT): $(SPEC_FILE) cmd/compschema/*.go internal/openapi2jsonschema/*.go
	@mkdir -p $(dir $@)
	$(COMPSCHEMA) extract --spec $(SPEC_FILE) --path $(PATH_PREFIX) --validate --out $@

# Step 2: JSON Schema → Go structs (using compschema import — includes sealed interfaces)
gotypes: $(SCHEMA_OUT)
	$(COMPSCHEMA) import --package openairesponses --out examples/openai/types.go $(SCHEMA_OUT)

# Step 3: Go structs → JSON Schema (compschema core — TODO)
# compschema-roundtrip: examples/openai/types.go
# 	$(COMPSCHEMA) generate ./examples/openai/...

# Step 4: diff (TODO)
# diff: ...

# Stats
stats:
	@echo ""
	@echo "=== compschema round-trip pipeline ==="
	@echo ""
	@test -f $(SPEC_FILE) && echo "  OpenAPI spec:  $$(wc -l < $(SPEC_FILE)) lines" || echo "  OpenAPI spec:  not fetched"
	@test -f $(SCHEMA_OUT) && echo "  JSON Schema:   $$(wc -c < $(SCHEMA_OUT)) bytes" || echo "  JSON Schema:   not generated"
	@test -f examples/openai/types.go && echo "  Go types:      $$(grep -c '^type ' examples/openai/types.go) declarations" || echo "  Go types:      not generated"
	@test -f examples/openai/types.go && echo "    lines:       $$(wc -l < examples/openai/types.go)"
	@echo "  compschema:    not yet implemented"
	@echo ""

clean:
	rm -f $(SCHEMA_OUT) examples/openai/types.go
	@echo "✓ cleaned generated artifacts"
