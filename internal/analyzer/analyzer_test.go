package analyzer

import (
	"testing"

	"github.com/codewandler/compschema/internal/ir"
)

func TestAnalyze_BasicPackage(t *testing.T) {
	pkgs, err := Analyze(false, "../../testdata/basic")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}

	pkg := pkgs[0]
	if pkg.Name != "basic" {
		t.Errorf("package name = %q, want %q", pkg.Name, "basic")
	}

	// Should find annotated types + transitive deps.
	expectedTypes := []string{"LineItem", "Order", "OrderStatus", "Circle", "Rectangle", "Shape"}
	for _, name := range expectedTypes {
		if _, ok := pkg.Types[name]; !ok {
			t.Errorf("missing type %q", name)
		}
	}
	t.Logf("found %d types: %v", len(pkg.Types), pkg.Order)
}

func TestAnalyze_EnumDetection(t *testing.T) {
	pkgs, err := Analyze(false, "../../testdata/basic")
	if err != nil {
		t.Fatal(err)
	}
	pkg := pkgs[0]

	os := pkg.Types["OrderStatus"]
	if os == nil {
		t.Fatal("OrderStatus not found")
	}
	if os.Kind != 1 { // KindEnum
		t.Errorf("OrderStatus.Kind = %d, want KindEnum (1)", os.Kind)
	}
	if len(os.EnumValues) != 3 {
		t.Errorf("OrderStatus has %d enum values, want 3", len(os.EnumValues))
	}
	t.Logf("OrderStatus enum values: %v", os.EnumValues)
}

func TestAnalyze_StructFields(t *testing.T) {
	pkgs, err := Analyze(false, "../../testdata/basic")
	if err != nil {
		t.Fatal(err)
	}
	pkg := pkgs[0]

	order := pkg.Types["Order"]
	if order == nil {
		t.Fatal("Order not found")
	}

	// Check field count.
	if len(order.Fields) != 4 {
		t.Fatalf("Order has %d fields, want 4", len(order.Fields))
	}

	// Check field names and required status.
	expected := map[string]bool{
		"id": true, "items": true, "status": true, "notes": false,
	}
	for _, f := range order.Fields {
		wantReq, ok := expected[f.JSONName]
		if !ok {
			t.Errorf("unexpected field %q", f.JSONName)
			continue
		}
		if f.Required != wantReq {
			t.Errorf("field %q: required = %v, want %v", f.JSONName, f.Required, wantReq)
		}
	}
}

func TestAnalyze_UnionDetection(t *testing.T) {
	pkgs, err := Analyze(false, "../../testdata/basic")
	if err != nil {
		t.Fatal(err)
	}
	pkg := pkgs[0]

	shape := pkg.Types["Shape"]
	if shape == nil {
		t.Fatal("Shape not found")
	}
	if shape.Kind != 2 { // KindUnion
		t.Errorf("Shape.Kind = %d, want KindUnion (2)", shape.Kind)
	}
	if len(shape.Variants) != 2 {
		t.Fatalf("Shape has %d variants, want 2", len(shape.Variants))
	}
	if shape.Discriminator != "type" {
		t.Errorf("Shape.Discriminator = %q, want %q", shape.Discriminator, "type")
	}

	varNames := map[string]bool{}
	for _, v := range shape.Variants {
		varNames[v.Name] = true
	}
	for _, want := range []string{"Circle", "Rectangle"} {
		if !varNames[want] {
			t.Errorf("missing variant %q", want)
		}
	}
}

func TestAnalyze_Constraints(t *testing.T) {
	pkgs, err := Analyze(false, "../../testdata/basic")
	if err != nil {
		t.Fatal(err)
	}
	pkg := pkgs[0]

	li := pkg.Types["LineItem"]
	if li == nil {
		t.Fatal("LineItem not found")
	}

	for _, f := range li.Fields {
		switch f.JSONName {
		case "sku":
			if len(f.Constraints) == 0 {
				t.Error("sku should have constraints")
			}
			found := false
			for _, c := range f.Constraints {
				if c.Keyword == "pattern" {
					found = true
				}
			}
			if !found {
				t.Error("sku missing pattern constraint")
			}
		case "qty":
			found := false
			for _, c := range f.Constraints {
				if c.Keyword == "minimum" {
					found = true
				}
			}
			if !found {
				t.Error("qty missing minimum constraint")
			}
		}
	}
}

func TestAnalyze_FieldDescription(t *testing.T) {
	pkgs, err := Analyze(false, "../../testdata/basic")
	if err != nil {
		t.Fatal(err)
	}
	pkg := pkgs[0]

	// LineItem has no field comments (defined with jsonschema tags, not comments).
	// Let's check the type-level description.
	li := pkg.Types["LineItem"]
	if li == nil {
		t.Fatal("LineItem not found")
	}
	if li.Description == "" {
		t.Error("LineItem should have a description from doc comment")
	}
	t.Logf("LineItem.Description = %q", li.Description)
}

func TestAnalyze_AllTypes(t *testing.T) {
	pkgs, err := Analyze(true, "../../testdata/basic")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}

	// With --all, should find ALL exported types.
	pkg := pkgs[0]
	if len(pkg.Types) < 6 {
		t.Errorf("expected at least 6 types with --all, got %d", len(pkg.Types))
	}
}

func TestAnalyze_EmptyInterface(t *testing.T) {
	// Named empty interfaces (type Foo interface{}) should be included
	// as KindScalar/"any" so they get a $defs entry.
	pkgs, err := Analyze(true, "../../examples/openai")
	if err != nil {
		t.Fatal(err)
	}
	pkg := pkgs[0]

	// ModelIdsResponses is `interface{}` — should be in the IR as a scalar "any".
	typ, ok := pkg.Types["ModelIdsResponses"]
	if !ok {
		t.Fatal("ModelIdsResponses (empty interface) should be in the IR")
	}
	if typ.Kind != ir.KindScalar || typ.ScalarType != "any" {
		t.Errorf("ModelIdsResponses should be KindScalar/any, got %v/%v", typ.Kind, typ.ScalarType)
	}
}
