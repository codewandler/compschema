package ir

import (
	"testing"
)

func TestTypeHash_Deterministic(t *testing.T) {
	makeType := func() *Type {
		return &Type{
			Name: "User",
			Kind: KindStruct,
			Fields: []Field{
				{Name: "Name", JSONName: "name", Required: true, Type: TypeRef{Inline: &Type{Kind: KindScalar, ScalarType: "string"}}},
				{Name: "Age", JSONName: "age", Required: false, Type: TypeRef{Inline: &Type{Kind: KindScalar, ScalarType: "integer"}}},
			},
		}
	}

	h1 := makeType().Hash()
	h2 := makeType().Hash()
	if h1 != h2 {
		t.Error("same type should produce same hash")
	}
}

func TestTypeHash_Sensitivity(t *testing.T) {
	base := &Type{
		Name: "User",
		Kind: KindStruct,
		Fields: []Field{
			{Name: "Name", JSONName: "name", Required: true, Type: TypeRef{Inline: &Type{Kind: KindScalar, ScalarType: "string"}}},
		},
	}

	// Different name.
	different := &Type{
		Name: "Admin",
		Kind: KindStruct,
		Fields: []Field{
			{Name: "Name", JSONName: "name", Required: true, Type: TypeRef{Inline: &Type{Kind: KindScalar, ScalarType: "string"}}},
		},
	}

	if base.Hash() == different.Hash() {
		t.Error("different type names should produce different hashes")
	}
}

func TestTypeHash_FieldChange(t *testing.T) {
	t1 := &Type{
		Name: "User",
		Kind: KindStruct,
		Fields: []Field{
			{Name: "Name", JSONName: "name", Required: true, Type: TypeRef{Inline: &Type{Kind: KindScalar, ScalarType: "string"}}},
		},
	}

	t2 := &Type{
		Name: "User",
		Kind: KindStruct,
		Fields: []Field{
			{Name: "Name", JSONName: "name", Required: false, Type: TypeRef{Inline: &Type{Kind: KindScalar, ScalarType: "string"}}},
		},
	}

	if t1.Hash() == t2.Hash() {
		t.Error("changing required should change hash")
	}
}

func TestTypeHash_Constraint(t *testing.T) {
	t1 := &Type{
		Name:       "Score",
		Kind:       KindScalar,
		ScalarType: "integer",
		Constraints: []Constraint{
			{Keyword: "minimum", Value: 0},
		},
	}

	t2 := &Type{
		Name:       "Score",
		Kind:       KindScalar,
		ScalarType: "integer",
		Constraints: []Constraint{
			{Keyword: "minimum", Value: 1},
		},
	}

	if t1.Hash() == t2.Hash() {
		t.Error("different constraint values should produce different hashes")
	}
}

func TestTypeRefHash(t *testing.T) {
	ref1 := &TypeRef{Name: "User"}
	ref2 := &TypeRef{Name: "User"}
	ref3 := &TypeRef{Name: "Admin"}

	if ref1.Hash() != ref2.Hash() {
		t.Error("same ref should produce same hash")
	}
	if ref1.Hash() == ref3.Hash() {
		t.Error("different ref should produce different hash")
	}
}

func TestPackageHash_Deterministic(t *testing.T) {
	makePkg := func() *Package {
		pkg := NewPackage("test", "test/pkg")
		pkg.Add(&Type{Name: "A", Kind: KindScalar, ScalarType: "string"})
		pkg.Add(&Type{Name: "B", Kind: KindScalar, ScalarType: "integer"})
		return pkg
	}

	h1 := makePkg().Hash()
	h2 := makePkg().Hash()
	if h1 != h2 {
		t.Error("same package should produce same hash")
	}
}

func TestPackageHash_OrderIndependent(t *testing.T) {
	pkg1 := NewPackage("test", "test/pkg")
	pkg1.Add(&Type{Name: "A", Kind: KindScalar, ScalarType: "string"})
	pkg1.Add(&Type{Name: "B", Kind: KindScalar, ScalarType: "integer"})

	pkg2 := NewPackage("test", "test/pkg")
	pkg2.Add(&Type{Name: "B", Kind: KindScalar, ScalarType: "integer"})
	pkg2.Add(&Type{Name: "A", Kind: KindScalar, ScalarType: "string"})

	if pkg1.Hash() != pkg2.Hash() {
		t.Error("package hash should be independent of insertion order")
	}
}

func TestVariantHash(t *testing.T) {
	v1 := Variant{Name: "String", DiscriminatorValues: []string{"type"}, TypeRef: TypeRef{Name: "StringValue"}}
	v2 := Variant{Name: "Number", DiscriminatorValues: []string{"type"}, TypeRef: TypeRef{Name: "NumberValue"}}

	if v1.Hash() == v2.Hash() {
		t.Error("different variants should produce different hashes")
	}
}

func TestUnionHash_VariantOrderIndependent(t *testing.T) {
	t1 := &Type{
		Name: "Value",
		Kind: KindUnion,
		Variants: []Variant{
			{Name: "A", TypeRef: TypeRef{Name: "TypeA"}},
			{Name: "B", TypeRef: TypeRef{Name: "TypeB"}},
		},
	}

	t2 := &Type{
		Name: "Value",
		Kind: KindUnion,
		Variants: []Variant{
			{Name: "B", TypeRef: TypeRef{Name: "TypeB"}},
			{Name: "A", TypeRef: TypeRef{Name: "TypeA"}},
		},
	}

	if t1.Hash() != t2.Hash() {
		t.Error("union hash should be independent of variant order")
	}
}

func TestConstraintHash_OrderIndependent(t *testing.T) {
	t1 := &Type{
		Name:       "Range",
		Kind:       KindScalar,
		ScalarType: "integer",
		Constraints: []Constraint{
			{Keyword: "minimum", Value: 0},
			{Keyword: "maximum", Value: 100},
		},
	}

	t2 := &Type{
		Name:       "Range",
		Kind:       KindScalar,
		ScalarType: "integer",
		Constraints: []Constraint{
			{Keyword: "maximum", Value: 100},
			{Keyword: "minimum", Value: 0},
		},
	}

	if t1.Hash() != t2.Hash() {
		t.Error("constraint hash should be independent of order")
	}
}

func TestTypeHash_List(t *testing.T) {
	t1 := &Type{
		Name:  "Names",
		Kind:  KindList,
		Items: &TypeRef{Inline: &Type{Kind: KindScalar, ScalarType: "string"}},
	}
	t2 := &Type{
		Name:  "Names",
		Kind:  KindList,
		Items: &TypeRef{Inline: &Type{Kind: KindScalar, ScalarType: "integer"}},
	}
	if t1.Hash() == t2.Hash() {
		t.Error("different item types should produce different hashes")
	}
}

func TestTypeHash_Map(t *testing.T) {
	t1 := &Type{
		Name:     "Meta",
		Kind:     KindMap,
		MapValue: &TypeRef{Inline: &Type{Kind: KindScalar, ScalarType: "string"}},
	}
	t2 := &Type{
		Name:     "Meta",
		Kind:     KindMap,
		MapValue: &TypeRef{Inline: &Type{Kind: KindScalar, ScalarType: "integer"}},
	}
	if t1.Hash() == t2.Hash() {
		t.Error("different map value types should produce different hashes")
	}
}

func TestTypeHash_Nullable(t *testing.T) {
	t1 := &Type{
		Name:  "OptName",
		Kind:  KindNullable,
		Inner: &TypeRef{Inline: &Type{Kind: KindScalar, ScalarType: "string"}},
	}
	t2 := &Type{
		Name:  "OptName",
		Kind:  KindNullable,
		Inner: &TypeRef{Inline: &Type{Kind: KindScalar, ScalarType: "integer"}},
	}
	if t1.Hash() == t2.Hash() {
		t.Error("different inner types should produce different hashes")
	}
}

func TestTypeHash_Ref(t *testing.T) {
	t1 := &Type{Name: "R", Kind: KindRef, RefName: "User"}
	t2 := &Type{Name: "R", Kind: KindRef, RefName: "Admin"}
	if t1.Hash() == t2.Hash() {
		t.Error("different ref names should produce different hashes")
	}
}

func TestTypeHash_Enum(t *testing.T) {
	t1 := &Type{Name: "S", Kind: KindEnum, EnumType: "string", EnumValues: []any{"a", "b"}}
	t2 := &Type{Name: "S", Kind: KindEnum, EnumType: "string", EnumValues: []any{"a", "c"}}
	if t1.Hash() == t2.Hash() {
		t.Error("different enum values should produce different hashes")
	}
}
