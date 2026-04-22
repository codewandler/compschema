package emitter

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codewandler/compschema/internal/ir"
)

// GenerateExample creates a realistic example JSON value for a type.
// Returns nil if the type cannot be exampled (e.g., pure interface types).
func GenerateExample(t *ir.Type, pkg *ir.Package) any {
	visiting := make(map[string]bool)
	val, ok := exampleValue(t, pkg, visiting)
	if !ok {
		return nil
	}
	return val
}

// AddExamples enriches a JSON Schema defs map with "examples" arrays.
// Each $def that can produce a valid example gets examples: [<example>].
func AddExamples(defs map[string]any, pkg *ir.Package) {
	for _, name := range pkg.Order {
		t, ok := pkg.Types[name]
		if !ok {
			continue
		}
		defSchema, ok := defs[name]
		if !ok {
			continue
		}
		m, ok := defSchema.(map[string]any)
		if !ok {
			continue
		}

		ex := GenerateExample(t, pkg)
		if ex == nil {
			continue
		}
		m["examples"] = []any{ex}
	}
}

func exampleValue(t *ir.Type, pkg *ir.Package, visiting map[string]bool) (any, bool) {
	if t == nil {
		return nil, false
	}

	switch t.Kind {
	case ir.KindStruct:
		if isWrapperType(t) {
			return nil, false
		}
		fields := make(map[string]any)
		for _, f := range t.Fields {
			val, ok := exampleField(f, pkg, visiting)
			if !ok {
				// Use a null placeholder for infeasible fields so the
				// overall object is still a valid example shape.
				if f.Required {
					fields[f.JSONName] = nil
				}
				continue
			}
			fields[f.JSONName] = val
		}
		return fields, true

	case ir.KindEnum:
		if len(t.EnumValues) > 0 {
			return t.EnumValues[0], true
		}
		return nil, false

	case ir.KindUnion:
		// Pick the first struct variant that we can example.
		for _, v := range t.Variants {
			val, ok := exampleTypeRef(v.TypeRef, pkg, visiting)
			if ok {
				return val, true
			}
		}
		return nil, false

	case ir.KindScalar:
		if t.ScalarType == "any" {
			return "example", true
		}
		return exampleScalar(t.ScalarType, t.Name, t.Constraints), true

	case ir.KindList:
		if t.Items != nil {
			item, ok := exampleTypeRef(*t.Items, pkg, visiting)
			if ok {
				return []any{item}, true
			}
		}
		return []any{}, true

	case ir.KindMap:
		if t.MapValue != nil {
			val, ok := exampleTypeRef(*t.MapValue, pkg, visiting)
			if ok {
				return map[string]any{"key1": val}, true
			}
		}
		return map[string]any{}, true

	case ir.KindNullable:
		if t.Inner != nil {
			return exampleTypeRef(*t.Inner, pkg, visiting)
		}
		return nil, true

	case ir.KindRef:
		if visiting[t.RefName] {
			return nil, false
		}
		visiting[t.RefName] = true
		defer delete(visiting, t.RefName)
		if ref, ok := pkg.Types[t.RefName]; ok {
			return exampleValue(ref, pkg, visiting)
		}
		return nil, false
	}

	return nil, false
}

func exampleField(f ir.Field, pkg *ir.Package, visiting map[string]bool) (any, bool) {
	// Const constraint → use the const value.
	for _, c := range f.Constraints {
		if c.Keyword == "const" {
			return c.Value, true
		}
	}

	// Check for format constraint on the field (e.g. format=email on a string field).
	for _, c := range f.Constraints {
		if c.Keyword == "format" {
			val := exampleScalar("string", f.Name, f.Constraints)
			return val, true
		}
	}

	val, ok := exampleTypeRef(f.Type, pkg, visiting)
	if !ok {
		return nil, false
	}

	// Adjust value to satisfy constraints.
	val = applyConstraints(val, f.Constraints, f.Name)
	return val, true
}

func exampleTypeRef(ref ir.TypeRef, pkg *ir.Package, visiting map[string]bool) (any, bool) {
	if ref.Name != "" {
		if visiting[ref.Name] {
			return nil, false
		}
		visiting[ref.Name] = true
		defer delete(visiting, ref.Name)
		if t, ok := pkg.Types[ref.Name]; ok {
			return exampleValue(t, pkg, visiting)
		}
		return nil, false
	}
	if ref.Inline != nil {
		return exampleValue(ref.Inline, pkg, visiting)
	}
	return nil, false
}

// exampleScalar returns a realistic example value for a scalar type.
// Uses the type/field name to generate contextual values.
func exampleScalar(scalarType, name string, constraints []ir.Constraint) any {
	switch scalarType {
	case "string":
		return exampleString(name, constraints)
	case "integer":
		return exampleInteger(constraints)
	case "number":
		return exampleNumber(constraints)
	case "boolean":
		return true
	default:
		return "example"
	}
}

func exampleString(name string, constraints []ir.Constraint) string {
	lower := strings.ToLower(name)

	// Check format constraint.
	for _, c := range constraints {
		if c.Keyword == "format" {
			switch fmt.Sprint(c.Value) {
			case "email":
				return "user@example.com"
			case "uri", "url":
				return "https://example.com"
			case "date":
				return "2025-01-15"
			case "date-time":
				return "2025-01-15T09:30:00Z"
			case "uuid":
				return "550e8400-e29b-41d4-a716-446655440000"
			case "ipv4":
				return "192.168.1.1"
			case "ipv6":
				return "2001:db8::1"
			case "hostname":
				return "example.com"
			}
		}
	}

	// Check pattern constraint.
	for _, c := range constraints {
		if c.Keyword == "pattern" {
			p := fmt.Sprint(c.Value)
			if strings.Contains(p, "[A-Z]") && strings.Contains(p, "[0-9]") {
				return "ABC-123"
			}
		}
	}

	// Derive from name.
	switch {
	case strings.Contains(lower, "email"):
		return "user@example.com"
	case strings.Contains(lower, "url") || strings.Contains(lower, "uri"):
		return "https://example.com"
	case strings.Contains(lower, "name"):
		return "example-name"
	case strings.Contains(lower, "id"):
		return "id_abc123"
	case strings.Contains(lower, "description") || strings.Contains(lower, "desc"):
		return "An example description."
	case strings.Contains(lower, "title"):
		return "Example Title"
	case strings.Contains(lower, "type"):
		return "example_type"
	case strings.Contains(lower, "status"):
		return "active"
	case strings.Contains(lower, "message") || strings.Contains(lower, "text") || strings.Contains(lower, "content"):
		return "Hello, world!"
	case strings.Contains(lower, "path") || strings.Contains(lower, "file"):
		return "/path/to/file"
	case strings.Contains(lower, "token") || strings.Contains(lower, "key") || strings.Contains(lower, "secret"):
		return "sk_example_token_123"
	case strings.Contains(lower, "date") || strings.Contains(lower, "time"):
		return "2025-01-15T09:30:00Z"
	}

	// Apply minLength.
	result := "example"
	for _, c := range constraints {
		if c.Keyword == "minLength" {
			if ml, ok := toFloat(c.Value); ok && int(ml) > len(result) {
				result = strings.Repeat("x", int(ml))
			}
		}
	}
	return result
}

func exampleInteger(constraints []ir.Constraint) any {
	val := int64(1)
	for _, c := range constraints {
		switch c.Keyword {
		case "minimum":
			if min, ok := toFloat(c.Value); ok && int64(min) > val {
				val = int64(min)
			}
		case "maximum":
			if max, ok := toFloat(c.Value); ok && int64(max) < val {
				val = int64(max)
			}
		case "exclusiveMinimum":
			if min, ok := toFloat(c.Value); ok && int64(min)+1 > val {
				val = int64(min) + 1
			}
		}
	}
	return val
}

func exampleNumber(constraints []ir.Constraint) any {
	val := 1.0
	for _, c := range constraints {
		switch c.Keyword {
		case "minimum":
			if min, ok := toFloat(c.Value); ok && min > val {
				val = min
			}
		case "maximum":
			if max, ok := toFloat(c.Value); ok && max < val {
				val = max
			}
		}
	}
	return val
}

// applyConstraints adjusts a value to satisfy field constraints.
func applyConstraints(val any, constraints []ir.Constraint, fieldName string) any {
	for _, c := range constraints {
		switch c.Keyword {
		case "minimum":
			if min, ok := toFloat(c.Value); ok {
				if cur, ok := toFloat(val); ok && cur < min {
					if min == float64(int64(min)) {
						val = int64(min)
					} else {
						val = min
					}
				}
			}
		case "minLength":
			if ml, ok := toFloat(c.Value); ok && ml > 0 {
				if s, ok := val.(string); ok && len(s) < int(ml) {
					val = strings.Repeat("x", int(ml))
				}
			}
		case "minItems":
			if mi, ok := toFloat(c.Value); ok && mi > 0 {
				if arr, ok := val.([]any); ok && len(arr) < int(mi) {
					for len(arr) < int(mi) {
						if len(arr) > 0 {
							arr = append(arr, arr[0])
						} else {
							arr = append(arr, nil)
						}
					}
					val = arr
				}
			}
		}
	}
	return val
}

// ExamplesToJSON serializes an example value to indented JSON.
func ExamplesToJSON(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
