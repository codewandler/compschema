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

	data, _, err := JSONSchema(pkg)
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

	// Check expected types (OrderStatus may be inlined as single-use enum).
	for _, name := range []string{"Order", "LineItem", "Shape", "Circle", "Rectangle"} {
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

	// Check status property — should be an enum (inlined or $ref).
	statusVal := props["status"]
	statusMap, _ := statusVal.(map[string]any)
	if statusMap != nil {
		// Check it has enum values (inlined) or $ref.
		if _, hasEnum := statusMap["enum"]; !hasEnum {
			if _, hasRef := statusMap["$ref"]; !hasRef {
				t.Error("Order.status should have enum values or $ref")
			}
		}
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

	code := GoCodegen(pkg, nil)

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

	code := GoTests(pkg, nil)

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

func TestGoCodegen_Constructors(t *testing.T) {
	pkgs, err := analyzer.Analyze(false, "../../testdata/basic")
	if err != nil {
		t.Fatal(err)
	}
	pkg := pkgs[0]

	// Without constructors — no NewT functions.
	code := GoCodegen(pkg, nil)
	if strings.Contains(code, "func NewCircle(") {
		t.Error("default GoCodegen should NOT emit constructors")
	}

	// With constructors.
	code = GoCodegenWithOptions(pkg, nil, EmitOptions{Constructors: true})

	// Circle: has const Type="circle" and required Radius.
	if !strings.Contains(code, "func NewCircle(") {
		t.Error("missing NewCircle constructor")
	}
	if !strings.Contains(code, `Type: "circle"`) {
		t.Error("NewCircle should auto-fill Type with \"circle\"")
	}
	if !strings.Contains(code, "radius float64") {
		t.Error("NewCircle should have radius param")
	}

	// Rectangle: has const Type="rectangle", required Width + Height.
	if !strings.Contains(code, "func NewRectangle(") {
		t.Error("missing NewRectangle constructor")
	}
	if !strings.Contains(code, `Type: "rectangle"`) {
		t.Error("NewRectangle should auto-fill Type with \"rectangle\"")
	}

	// Order: required ID, Items, Status — no const fields.
	if !strings.Contains(code, "func NewOrder(") {
		t.Error("missing NewOrder constructor")
	}
	// Notes is optional (omitempty), should NOT be a param.
	if strings.Contains(code, "notes") {
		t.Error("NewOrder should NOT have notes param (it's optional)")
	}

	// LineItem: required SKU + Qty.
	if !strings.Contains(code, "func NewLineItem(") {
		t.Error("missing NewLineItem constructor")
	}

	// Ptr helper should be emitted (Order has optional *string Notes).
	if !strings.Contains(code, "func Ptr[T any](v T) *T") {
		t.Error("missing Ptr helper")
	}

	// Should NOT emit constructor for Shape (union interface).
	if strings.Contains(code, "func NewShape(") {
		t.Error("should NOT emit constructor for Shape union")
	}

	t.Logf("codegen with constructors: %d bytes", len(code))
}

func TestGoTests_Constructors(t *testing.T) {
	pkgs, err := analyzer.Analyze(false, "../../testdata/basic")
	if err != nil {
		t.Fatal(err)
	}
	pkg := pkgs[0]

	// Without constructors.
	code := GoTests(pkg, nil)
	if strings.Contains(code, "TestCompschema_NewCircle") {
		t.Error("default GoTests should NOT emit constructor tests")
	}

	// With constructors.
	code = GoTestsWithOptions(pkg, nil, EmitOptions{Constructors: true})

	for _, name := range []string{"Circle", "Rectangle", "Order", "LineItem"} {
		if !strings.Contains(code, "TestCompschema_New"+name) {
			t.Errorf("missing constructor test for New%s", name)
		}
	}

	// Constructor tests should validate round-trip.
	if !strings.Contains(code, "v.Validate(data)") {
		t.Error("constructor tests should validate marshaled output")
	}

	t.Logf("tests with constructors: %d bytes", len(code))
}

func TestConstructorHelpers(t *testing.T) {
	// Test paramName.
	tests := []struct {
		in, want string
	}{
		{"ID", "id"},
		{"Name", "name"},
		{"URLPath", "urlPath"},
		{"Status", "status"},
		{"Type", "type_"},
		{"Radius", "radius"},
		{"SKU", "sku"},
	}
	for _, tt := range tests {
		got := paramName(tt.in)
		if got != tt.want {
			t.Errorf("paramName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	// Test goLiteral.
	if goLiteral("circle") != `"circle"` {
		t.Errorf("goLiteral string")
	}
	if goLiteral(42) != "42" {
		t.Errorf("goLiteral int")
	}
	if goLiteral(3.14) != "3.14" {
		t.Errorf("goLiteral float")
	}
	if goLiteral(true) != "true" {
		t.Errorf("goLiteral bool")
	}
}
