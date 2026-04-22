package openapi2jsonschema

import (
	"testing"
)

func TestValidateMetaSchema_ValidSchema(t *testing.T) {
	valid := []byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {
			"name": { "type": "string" },
			"age":  { "type": "integer", "minimum": 0 }
		},
		"required": ["name"]
	}`)

	errs := ValidateMetaSchema(valid)
	if len(errs) > 0 {
		t.Errorf("expected valid schema, got %d errors: %v", len(errs), errs)
	}
}

func TestValidateMetaSchema_InvalidSchema(t *testing.T) {
	// "type" must be a string or array of strings, not a number
	invalid := []byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": 42
	}`)

	errs := ValidateMetaSchema(invalid)
	if len(errs) == 0 {
		t.Error("expected validation errors for invalid schema, got none")
	}
}

func TestValidateMetaSchema_InvalidJSON(t *testing.T) {
	errs := ValidateMetaSchema([]byte(`{not valid json`))
	if len(errs) == 0 {
		t.Error("expected error for invalid JSON, got none")
	}
}
