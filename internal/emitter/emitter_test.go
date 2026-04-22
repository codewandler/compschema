package emitter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codewandler/compschema/internal/analyzer"
)

func TestJSONSchema_BasicPackage(t *testing.T) {
	pkgs, err := analyzer.Analyze(false, "../../testdata/basic")
	if err != nil {
		t.Fatal(err)
	}
	pkg := pkgs[0]

	data, err := JSONSchema(pkg)
	if err != nil {
		t.Fatalf("JSONSchema: %v", err)
	}

	// Must be valid JSON.
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Must have $schema.
	if doc["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("wrong $schema: %v", doc["$schema"])
	}

	// Must have $defs.
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatal("no $defs")
	}

	// Check expected types.
	for _, name := range []string{"Order", "LineItem", "OrderStatus", "Shape", "Circle", "Rectangle"} {
		if _, ok := defs[name]; !ok {
			t.Errorf("missing $def: %s", name)
		}
	}

	// Check Order has properties.
	order, _ := defs["Order"].(map[string]any)
	if order["type"] != "object" {
		t.Errorf("Order.type = %v, want object", order["type"])
	}
	props, _ := order["properties"].(map[string]any)
	if _, ok := props["id"]; !ok {
		t.Error("Order missing 'id' property")
	}

	// Check OrderStatus has enum values.
	os, _ := defs["OrderStatus"].(map[string]any)
	enumVals, _ := os["enum"].([]any)
	if len(enumVals) != 3 {
		t.Errorf("OrderStatus has %d enum values, want 3", len(enumVals))
	}

	// Check Shape has oneOf.
	shape, _ := defs["Shape"].(map[string]any)
	oneOf, _ := shape["oneOf"].([]any)
	if len(oneOf) != 2 {
		t.Errorf("Shape has %d oneOf variants, want 2", len(oneOf))
	}

	t.Logf("schema: %d bytes, %d $defs", len(data), len(defs))
}

func TestGoCodegen_BasicPackage(t *testing.T) {
	pkgs, err := analyzer.Analyze(false, "../../testdata/basic")
	if err != nil {
		t.Fatal(err)
	}
	pkg := pkgs[0]

	code := GoCodegen(pkg)

	// Must contain package declaration.
	if !strings.Contains(code, "package basic") {
		t.Error("missing package declaration")
	}

	// Must contain go:embed.
	if !strings.Contains(code, "//go:embed schema.gen.json") {
		t.Error("missing go:embed directive")
	}

	// Must have Validate for structs.
	for _, name := range []string{"Order", "LineItem", "Circle", "Rectangle"} {
		if !strings.Contains(code, "func ("+name+") Validate(") {
			t.Errorf("missing Validate for %s", name)
		}
		if !strings.Contains(code, "func Decode"+name+"(") {
			t.Errorf("missing Decode%s", name)
		}
		if !strings.Contains(code, "func ("+name+") JSONSchemaBytes()") {
			t.Errorf("missing JSONSchemaBytes for %s", name)
		}
	}

	// Union types should have free function, not method.
	if !strings.Contains(code, "func ShapeJSONSchemaBytes()") {
		t.Error("missing ShapeJSONSchemaBytes free function")
	}
	if strings.Contains(code, "func (Shape) Validate(") {
		t.Error("Shape should NOT have Validate method (it's an interface)")
	}

	t.Logf("codegen: %d bytes", len(code))
}

func TestGoTests_BasicPackage(t *testing.T) {
	pkgs, err := analyzer.Analyze(false, "../../testdata/basic")
	if err != nil {
		t.Fatal(err)
	}
	pkg := pkgs[0]

	code := GoTests(pkg)

	// Must have schema validity test.
	if !strings.Contains(code, "TestCompschema_SchemaIsValidJSON") {
		t.Error("missing schema validity test")
	}

	// Must have per-type tests.
	for _, name := range []string{"Order", "LineItem", "Circle", "Rectangle"} {
		if !strings.Contains(code, "TestCompschema_"+name+"_ValidateRejectsInvalidJSON") {
			t.Errorf("missing ValidateRejectsInvalidJSON for %s", name)
		}
		if !strings.Contains(code, "TestCompschema_"+name+"_RoundTrip") {
			t.Errorf("missing RoundTrip for %s", name)
		}
	}

	// Union types should have JSONSchemaBytes test but not Validate.
	if !strings.Contains(code, "TestCompschema_Shape_JSONSchemaBytes") {
		t.Error("missing Shape JSONSchemaBytes test")
	}
	if strings.Contains(code, "TestCompschema_Shape_Validate") {
		t.Error("Shape should NOT have Validate test (it's an interface)")
	}

	t.Logf("tests: %d bytes", len(code))
}
