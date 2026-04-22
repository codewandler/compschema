package openapi2jsonschema

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// ValidateMetaSchema validates that the given JSON document is a valid
// JSON Schema according to the draft 2020-12 meta-schema.
//
// Returns a list of validation errors, or nil if the document is valid.
func ValidateMetaSchema(document []byte) []string {
	// Compile the meta-schema (built into the library).
	c := jsonschema.NewCompiler()
	metaSchema, err := c.Compile("https://json-schema.org/draft/2020-12/schema")
	if err != nil {
		return []string{fmt.Sprintf("compile meta-schema: %v", err)}
	}

	// Decode the document into a generic value.
	var v any
	if err := json.Unmarshal(document, &v); err != nil {
		return []string{fmt.Sprintf("invalid JSON: %v", err)}
	}

	// Validate.
	err = metaSchema.Validate(v)
	if err == nil {
		return nil
	}

	// Extract individual errors from the validation result.
	validationErr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []string{err.Error()}
	}

	return flattenValidationErrors(validationErr, nil)
}

// flattenValidationErrors recursively collects all leaf error messages
// from a jsonschema.ValidationError tree.
func flattenValidationErrors(ve *jsonschema.ValidationError, out []string) []string {
	if len(ve.Causes) == 0 {
		loc := "/" + strings.Join(ve.InstanceLocation, "/")
		kind := ""
		if ve.ErrorKind != nil {
			p := message.NewPrinter(language.English)
			kind = ve.ErrorKind.LocalizedString(p)
		}
		out = append(out, fmt.Sprintf("%s: %s", loc, kind))
		return out
	}
	for _, cause := range ve.Causes {
		out = flattenValidationErrors(cause, out)
	}
	return out
}
