package emitter

import (
	"testing"

	"github.com/codewandler/compschema/internal/ir"
)

func TestIRToYAML_Basic(t *testing.T) {
	pkg := ir.NewPackage("test", "test/pkg")
	pkg.Add(&ir.Type{
		Name:        "User",
		Kind:        ir.KindStruct,
		Description: "A user account.",
		Fields: []ir.Field{
			{
				Name:     "Name",
				JSONName: "name",
				Required: true,
				Type:     ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "string"}},
				Constraints: []ir.Constraint{
					{Keyword: "minLength", Value: 1},
				},
			},
			{
				Name:     "Age",
				JSONName: "age",
				Required: false,
				Type:     ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "integer"}},
			},
		},
	})
	pkg.Add(&ir.Type{
		Name:       "Status",
		Kind:       ir.KindEnum,
		EnumType:   "string",
		EnumValues: []any{"active", "inactive"},
	})

	data, err := IRToYAML(pkg)
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(data)

	// Verify key content is present.
	for _, want := range []string{
		"package: test",
		"name: User",
		"kind: struct",
		"json_name: name",
		"required: true",
		"minLength:",
		"name: Status",
		"kind: enum",
		"_hash:",
	} {
		if !contains(yaml, want) {
			t.Errorf("IR YAML missing %q\n\nGot:\n%s", want, yaml)
		}
	}
}

func TestIRToYAML_AllKinds(t *testing.T) {
	pkg := ir.NewPackage("test", "test/pkg")

	// Union
	pkg.Add(&ir.Type{
		Name:          "Shape",
		Kind:          ir.KindUnion,
		Discriminator: "type",
		Variants: []ir.Variant{
			{Name: "Circle", Discriminator: "circle", TypeRef: ir.TypeRef{Name: "Circle"}},
		},
	})
	// List
	pkg.Add(&ir.Type{
		Name:  "Names",
		Kind:  ir.KindList,
		Items: &ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "string"}},
	})
	// Map
	pkg.Add(&ir.Type{
		Name:     "Meta",
		Kind:     ir.KindMap,
		MapValue: &ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "string"}},
	})
	// Ref
	pkg.Add(&ir.Type{
		Name:    "UserRef",
		Kind:    ir.KindRef,
		RefName: "User",
	})
	// Nullable
	pkg.Add(&ir.Type{
		Name:  "OptName",
		Kind:  ir.KindNullable,
		Inner: &ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "string"}},
	})

	data, err := IRToYAML(pkg)
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(data)

	for _, want := range []string{
		"kind: union",
		"discriminator: type",
		"discriminator_value: circle",
		"kind: list",
		"kind: map",
		"kind: ref",
		"ref: User",
		"kind: nullable",
	} {
		if !contains(yaml, want) {
			t.Errorf("IR YAML missing %q\n\nGot:\n%s", want, yaml)
		}
	}
}

func TestKindString(t *testing.T) {
	tests := []struct {
		kind ir.Kind
		want string
	}{
		{ir.KindStruct, "struct"},
		{ir.KindEnum, "enum"},
		{ir.KindUnion, "union"},
		{ir.KindList, "list"},
		{ir.KindMap, "map"},
		{ir.KindScalar, "scalar"},
		{ir.KindRef, "ref"},
		{ir.KindNullable, "nullable"},
		{ir.Kind(99), "unknown(99)"},
	}
	for _, tt := range tests {
		got := kindString(tt.kind)
		if got != tt.want {
			t.Errorf("kindString(%d) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
