package emitter

import (
	"testing"

	"github.com/codewandler/compschema/internal/ir"
)

func TestGenerateExample_Struct(t *testing.T) {
	pkg := ir.NewPackage("test", "test/pkg")
	pkg.Add(&ir.Type{
		Name: "User",
		Kind: ir.KindStruct,
		Fields: []ir.Field{
			{Name: "Name", JSONName: "name", Required: true, Type: ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "string"}}},
			{Name: "Age", JSONName: "age", Required: false, Type: ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "integer"}}},
			{Name: "Email", JSONName: "email", Required: true, Type: ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "string"}},
				Constraints: []ir.Constraint{{Keyword: "format", Value: "email"}}},
		},
	})

	ex := GenerateExample(pkg.Types["User"], pkg)
	if ex == nil {
		t.Fatal("expected example, got nil")
	}

	m, ok := ex.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", ex)
	}

	// Required fields should be present.
	if _, ok := m["name"]; !ok {
		t.Error("missing required field 'name'")
	}
	if _, ok := m["email"]; !ok {
		t.Error("missing required field 'email'")
	}
	// Optional field should also be present (examples include all fields).
	if _, ok := m["age"]; !ok {
		t.Error("missing optional field 'age'")
	}

	// Email should have a realistic value.
	if email, ok := m["email"].(string); ok {
		if email != "user@example.com" {
			t.Errorf("email: got %q, want user@example.com", email)
		}
	}
}

func TestGenerateExample_Enum(t *testing.T) {
	pkg := ir.NewPackage("test", "test/pkg")
	pkg.Add(&ir.Type{
		Name:       "Status",
		Kind:       ir.KindEnum,
		EnumType:   "string",
		EnumValues: []any{"active", "inactive"},
	})

	ex := GenerateExample(pkg.Types["Status"], pkg)
	if ex != "active" {
		t.Errorf("expected first enum value, got %v", ex)
	}
}

func TestGenerateExample_ScalarConstraints(t *testing.T) {
	pkg := ir.NewPackage("test", "test/pkg")

	tests := []struct {
		name        string
		scalarType  string
		constraints []ir.Constraint
		check       func(t *testing.T, v any)
	}{
		{
			name:        "minimum",
			scalarType:  "integer",
			constraints: []ir.Constraint{{Keyword: "minimum", Value: 10}},
			check: func(t *testing.T, v any) {
				if n, ok := v.(int64); !ok || n < 10 {
					t.Errorf("expected >= 10, got %v (%T)", v, v)
				}
			},
		},
		{
			name:        "format_email",
			scalarType:  "string",
			constraints: []ir.Constraint{{Keyword: "format", Value: "email"}},
			check: func(t *testing.T, v any) {
				if v != "user@example.com" {
					t.Errorf("expected email, got %v", v)
				}
			},
		},
		{
			name:        "format_uuid",
			scalarType:  "string",
			constraints: []ir.Constraint{{Keyword: "format", Value: "uuid"}},
			check: func(t *testing.T, v any) {
				s, _ := v.(string)
				if len(s) != 36 {
					t.Errorf("expected UUID, got %q", s)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typ := &ir.Type{
				Name:        tt.name,
				Kind:        ir.KindScalar,
				ScalarType:  tt.scalarType,
				Constraints: tt.constraints,
			}
			pkg.Add(typ)

			ex := GenerateExample(typ, pkg)
			if ex == nil {
				t.Fatal("expected example")
			}
			tt.check(t, ex)
		})
	}
}

func TestGenerateExample_Union(t *testing.T) {
	pkg := ir.NewPackage("test", "test/pkg")
	pkg.Add(&ir.Type{
		Name: "Circle",
		Kind: ir.KindStruct,
		Fields: []ir.Field{
			{Name: "Type", JSONName: "type", Required: true, Type: ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "string"}},
				Constraints: []ir.Constraint{{Keyword: "const", Value: "circle"}}},
			{Name: "Radius", JSONName: "radius", Required: true, Type: ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "number"}}},
		},
	})
	pkg.Add(&ir.Type{
		Name: "Shape",
		Kind: ir.KindUnion,
		Variants: []ir.Variant{
			{Name: "Circle", TypeRef: ir.TypeRef{Name: "Circle"}},
		},
	})

	ex := GenerateExample(pkg.Types["Shape"], pkg)
	if ex == nil {
		t.Fatal("expected example for union")
	}
	m, ok := ex.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", ex)
	}
	if m["type"] != "circle" {
		t.Errorf("expected type=circle, got %v", m["type"])
	}
}

func TestGenerateExample_List(t *testing.T) {
	pkg := ir.NewPackage("test", "test/pkg")
	typ := &ir.Type{
		Name:  "Names",
		Kind:  ir.KindList,
		Items: &ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "string"}},
	}
	pkg.Add(typ)

	ex := GenerateExample(typ, pkg)
	arr, ok := ex.([]any)
	if !ok {
		t.Fatalf("expected array, got %T", ex)
	}
	if len(arr) != 1 {
		t.Errorf("expected 1 item, got %d", len(arr))
	}
}

func TestGenerateExample_Map(t *testing.T) {
	pkg := ir.NewPackage("test", "test/pkg")
	typ := &ir.Type{
		Name:     "Meta",
		Kind:     ir.KindMap,
		MapValue: &ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "string"}},
	}
	pkg.Add(typ)

	ex := GenerateExample(typ, pkg)
	m, ok := ex.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", ex)
	}
	if _, ok := m["key1"]; !ok {
		t.Error("expected key1 in map example")
	}
}

func TestAddExamples(t *testing.T) {
	pkg := ir.NewPackage("test", "test/pkg")
	pkg.Add(&ir.Type{
		Name:       "Status",
		Kind:       ir.KindEnum,
		EnumType:   "string",
		EnumValues: []any{"active"},
	})

	defs := map[string]any{
		"Status": map[string]any{"type": "string", "enum": []any{"active"}},
	}

	AddExamples(defs, pkg)

	m := defs["Status"].(map[string]any)
	examples, ok := m["examples"]
	if !ok {
		t.Fatal("expected examples key")
	}
	arr, ok := examples.([]any)
	if !ok || len(arr) != 1 || arr[0] != "active" {
		t.Errorf("unexpected examples: %v", examples)
	}
}
