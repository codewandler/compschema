// Package compschema provides the Schema interface implemented by
// types with generated JSON Schema support.
package compschema

import "encoding/json"

// Schema is satisfied by types that have compschema-generated
// JSON Schema, Validate, and Decode support.
//
// Use it for generic handlers:
//
//	func Handle[T compschema.Schema](data []byte) (T, error) {
//	    var zero T
//	    if err := zero.Validate(data); err != nil {
//	        return zero, err
//	    }
//	    var result T
//	    json.Unmarshal(data, &result)
//	    return result, nil
//	}
type Schema interface {
	// JSONSchemaBytes returns the JSON Schema definition for this type
	// as raw JSON (the $defs entry, not the full document).
	//
	// The return type is json.RawMessage so it embeds correctly when
	// marshalled inside another JSON structure (instead of being
	// base64-encoded as a plain []byte would be).
	JSONSchemaBytes() json.RawMessage

	// Validate checks whether raw JSON conforms to this type's schema
	// without unmarshalling into a Go struct.
	Validate(data []byte) error
}
