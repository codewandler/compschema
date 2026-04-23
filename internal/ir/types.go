// Package ir defines the Schema IR — an intermediate representation
// of Go types for JSON Schema generation.
//
// The IR is language-agnostic: it models types, fields, constraints,
// unions, and references without any Go-specific constructs. Multiple
// emitters (JSON Schema, Go codegen, tests) consume the same IR.
package ir

// Package is the root IR node — all types discovered in a Go package.
type Package struct {
	Name  string           // Go package name
	Path  string           // import path
	Dir   string           // source directory on disk (empty if unknown)
	Types map[string]*Type // named types, keyed by Go type name
	Order []string         // insertion order for deterministic output
}

// NewPackage creates an empty Package IR.
func NewPackage(name, path string) *Package {
	return &Package{
		Name:  name,
		Path:  path,
		Types: make(map[string]*Type),
	}
}

// Add registers a named type in the package.
func (p *Package) Add(t *Type) {
	if _, exists := p.Types[t.Name]; !exists {
		p.Order = append(p.Order, t.Name)
	}
	p.Types[t.Name] = t
}

// Kind classifies an IR type node.
type Kind int

const (
	KindStruct   Kind = iota // object with named properties
	KindEnum                 // string or integer with fixed values
	KindUnion                // oneOf — sealed interface with variants
	KindList                 // array with typed items
	KindMap                  // object with additionalProperties
	KindScalar               // string, integer, number, boolean
	KindRef                  // reference to a named type
	KindNullable             // pointer — wraps another type, allows null
)

// Type is a single IR node representing a named or anonymous type.
type Type struct {
	Name        string // Go type name (empty for anonymous/inline types)
	Kind        Kind
	Description string

	cache hashCache // lazy hash cache (unexported, not serialized)

	// KindStruct
	Fields []Field

	// KindEnum
	EnumValues []any  // string or int values
	EnumType   string // "string" or "integer"

	// KindUnion
	Variants      []Variant
	Discriminator string // property name for discriminated unions, or ""

	// KindList
	Items *TypeRef

	// KindMap
	MapValue *TypeRef

	// KindScalar
	ScalarType string // "string", "integer", "number", "boolean"

	// KindRef
	RefName string // name of the referenced type in Package.Types

	// KindNullable
	Inner *TypeRef

	// Constraints (applicable to any kind)
	Constraints []Constraint

	// HasUnmarshalJSON is true if the type already has a custom UnmarshalJSON method
	// defined in the source package. The codegen skips emitting one to avoid duplicates.
	HasUnmarshalJSON bool
}

// Field is a property in a struct type.
type Field struct {
	Name        string // Go field name
	JSONName    string // from json tag
	Description string
	Required    bool // true if no omitempty/omitzero
	Type        TypeRef
	Constraints []Constraint
	Tags        map[string]string // raw struct tags: "json" → "bar,omitempty", "yaml" → "baz", etc.
}

// TypeRef is a reference to a type — either inline (anonymous) or by name.
type TypeRef struct {
	Inline *Type  // inline/anonymous type definition
	Name   string // reference to a named type in Package.Types
}

// Variant is one arm of a union type.
type Variant struct {
	Name                string // Go type name of the variant
	TypeRef             TypeRef
	DiscriminatorValues []string // discriminator field values that map to this variant
}

// DiscriminatorValue returns the single discriminator value, or "" if none/multiple.
// Use DiscriminatorValues for multi-value enum discrimination.
func (v *Variant) DiscriminatorValue() string {
	if len(v.DiscriminatorValues) == 1 {
		return v.DiscriminatorValues[0]
	}
	return ""
}

// HasDiscriminator returns true if this variant has at least one discriminator value.
func (v *Variant) HasDiscriminator() bool {
	return len(v.DiscriminatorValues) > 0
}

// Constraint is a JSON Schema validation keyword.
type Constraint struct {
	Keyword string // "minimum", "maximum", "pattern", "minLength", "maxLength", "minItems", "maxItems", "const", etc.
	Value   any    // the constraint value
}

// HasRequired returns true if the struct has any required fields.
func (t *Type) HasRequired() bool {
	for _, f := range t.Fields {
		if f.Required {
			return true
		}
	}
	return false
}

// RequiredFields returns the names of required fields.
func (t *Type) RequiredFields() []string {
	var r []string
	for _, f := range t.Fields {
		if f.Required {
			r = append(r, f.JSONName)
		}
	}
	return r
}
