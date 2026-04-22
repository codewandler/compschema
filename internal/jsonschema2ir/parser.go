// Package jsonschema2ir parses JSON Schema documents into the Schema IR.
// This is the reverse of the emitter — it enables IR-level comparison
// and is the foundation for `compschema import`.
package jsonschema2ir

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/codewandler/compschema/internal/ir"
)

// Parse loads a JSON Schema file and returns an IR Package.
func Parse(path string) (*ir.Package, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBytes(data)
}

// ParseBytes parses JSON Schema bytes into an IR Package.
func ParseBytes(data []byte) (*ir.Package, error) {
	var doc struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse schema: %w", err)
	}

	// Pre-parse all defs for cross-reference resolution.
	parsedDefs := make(map[string]*schemaNode)
	for name, raw := range doc.Defs {
		var s schemaNode
		if json.Unmarshal(raw, &s) == nil {
			parsedDefs[name] = &s
		}
	}

	ctx := &parseContext{defs: parsedDefs}
	pkg := ir.NewPackage("schema", "")

	for name, s := range parsedDefs {
		t := ctx.convertNode(name, s)
		if t != nil {
			pkg.Add(t)
		}
	}

	return pkg, nil
}

// parseContext holds the full $defs for cross-reference resolution.
type parseContext struct {
	defs map[string]*schemaNode
}

// schemaNode is a minimal JSON Schema representation for parsing.
type schemaNode struct {
	Type                 any                       `json:"type,omitempty"`
	Properties           map[string]json.RawMessage `json:"properties,omitempty"`
	Required             []string                  `json:"required,omitempty"`
	AdditionalProperties any                       `json:"additionalProperties,omitempty"`
	Items                json.RawMessage           `json:"items,omitempty"`
	Enum                 []any                     `json:"enum,omitempty"`
	Const                any                       `json:"const,omitempty"`
	OneOf                []json.RawMessage         `json:"oneOf,omitempty"`
	AnyOf                []json.RawMessage         `json:"anyOf,omitempty"`
	AllOf                []json.RawMessage         `json:"allOf,omitempty"`
	Ref                  string                    `json:"$ref,omitempty"`
	Title                string                    `json:"title,omitempty"`
	Description          string                    `json:"description,omitempty"`
	Format               string                    `json:"format,omitempty"`
	Discriminator        *discNode                 `json:"discriminator,omitempty"`
	XDiscriminator       *discNode                 `json:"x-discriminator,omitempty"`

	// Numeric
	Minimum          *float64 `json:"minimum,omitempty"`
	Maximum          *float64 `json:"maximum,omitempty"`
	ExclusiveMinimum *float64 `json:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum *float64 `json:"exclusiveMaximum,omitempty"`
	MultipleOf       *float64 `json:"multipleOf,omitempty"`

	// String
	MinLength *int64 `json:"minLength,omitempty"`
	MaxLength *int64 `json:"maxLength,omitempty"`
	Pattern   string `json:"pattern,omitempty"`

	// Array
	MinItems    *int64 `json:"minItems,omitempty"`
	MaxItems    *int64 `json:"maxItems,omitempty"`
	UniqueItems *bool  `json:"uniqueItems,omitempty"`

	// Object
	MinProperties *int64 `json:"minProperties,omitempty"`
	MaxProperties *int64 `json:"maxProperties,omitempty"`

	// Metadata
	Default    any   `json:"default,omitempty"`
	Examples   []any `json:"examples,omitempty"`
	ReadOnly   *bool `json:"readOnly,omitempty"`
	WriteOnly  *bool `json:"writeOnly,omitempty"`
	Deprecated *bool `json:"deprecated,omitempty"`
}

type discNode struct {
	PropertyName string `json:"propertyName"`
}

func (ctx *parseContext) convertNode(name string, s *schemaNode) *ir.Type {
	if s == nil {
		return nil
	}

	// $ref → KindRef
	if s.Ref != "" {
		refName := s.Ref
		if strings.HasPrefix(refName, "#/$defs/") {
			refName = strings.TrimPrefix(refName, "#/$defs/")
		}
		return &ir.Type{Name: name, Kind: ir.KindRef, RefName: refName}
	}

	// oneOf/anyOf → KindUnion
	variants := s.OneOf
	keyword := "oneOf"
	if len(variants) == 0 && len(s.AnyOf) > 0 {
		variants = s.AnyOf
		keyword = "anyOf"
	}
	if len(variants) > 0 {
		// Check for nullable pattern: [T, {type:null}]
		if len(variants) == 2 {
			if isNullVariant(variants[0]) || isNullVariant(variants[1]) {
				nonNullIdx := 0
				if isNullVariant(variants[0]) {
					nonNullIdx = 1
				}
				var inner schemaNode
				json.Unmarshal(variants[nonNullIdx], &inner)
				innerType := ctx.convertNode("", &inner)
				if innerType != nil {
					ref := typeToRef(innerType)
					return &ir.Type{
						Name:        name,
						Kind:        ir.KindNullable,
						Inner:       &ref,
						Description: s.Description,
					}
				}
			}
		}

		// Check if all variants are string-typed (enums/scalars) → collapse to single enum.
		allString := true
		var combinedEnums []any
		for _, raw := range variants {
			var vs schemaNode
			json.Unmarshal(raw, &vs)
			if vs.Ref != "" {
				refName := vs.Ref
				if strings.HasPrefix(refName, "#/$defs/") {
					refName = strings.TrimPrefix(refName, "#/$defs/")
				}
				if enums, ok := ctx.resolveStringEnums(refName); ok {
					combinedEnums = append(combinedEnums, enums...)
					continue
				}
				allString = false
				break
			}
			vType := resolveType(vs.Type)
			if vType != "string" {
				allString = false
				break
			}
			if len(vs.Enum) > 0 {
				combinedEnums = append(combinedEnums, vs.Enum...)
			}
		}
		if allString && len(combinedEnums) > 0 {
			return &ir.Type{
				Name:        name,
				Kind:        ir.KindEnum,
				EnumType:    "string",
				EnumValues:  combinedEnums,
				Description: s.Description,
			}
		}

		t := &ir.Type{
			Name:        name,
			Kind:        ir.KindUnion,
			Description: s.Description,
		}
		disc := s.Discriminator
		if disc == nil {
			disc = s.XDiscriminator
		}
		if disc != nil {
			t.Discriminator = disc.PropertyName
		}
		for _, raw := range variants {
			var vs schemaNode
			json.Unmarshal(raw, &vs)
			vt := ctx.convertNode("", &vs)
			if vt != nil {
				v := ir.Variant{TypeRef: typeToRef(vt)}
				if vt.Kind == ir.KindRef {
					v.Name = vt.RefName
				}
				t.Variants = append(t.Variants, v)
			}
		}
		_ = keyword
		return t
	}

	// Determine type string
	typeStr := resolveType(s.Type)

	// Enum
	if len(s.Enum) > 0 && (typeStr == "string" || typeStr == "integer") {
		return &ir.Type{
			Name:        name,
			Kind:        ir.KindEnum,
			EnumType:    typeStr,
			EnumValues:  s.Enum,
			Description: s.Description,
			Constraints: collectConstraints(s),
		}
	}

	// Object with properties → KindStruct
	if typeStr == "object" && len(s.Properties) > 0 {
		t := &ir.Type{
			Name:        name,
			Kind:        ir.KindStruct,
			Description: s.Description,
		}
		reqSet := make(map[string]bool)
		for _, r := range s.Required {
			reqSet[r] = true
		}
		for propName, raw := range s.Properties {
			var ps schemaNode
			json.Unmarshal(raw, &ps)
			propType := ctx.convertNode("", &ps)
			ref := typeToRef(propType)

			f := ir.Field{
				Name:        propName,
				JSONName:    propName,
				Required:    reqSet[propName],
				Description: ps.Description,
				Type:        ref,
				Constraints: collectConstraints(&ps),
			}
			t.Fields = append(t.Fields, f)
		}
		t.Constraints = collectConstraints(s)
		return t
	}

	// Object with additionalProperties → KindMap
	if typeStr == "object" && s.AdditionalProperties != nil {
		t := &ir.Type{Name: name, Kind: ir.KindMap, Description: s.Description}
		if raw, err := json.Marshal(s.AdditionalProperties); err == nil {
			var vs schemaNode
			if json.Unmarshal(raw, &vs) == nil {
				vt := ctx.convertNode("", &vs)
				if vt != nil {
					ref := typeToRef(vt)
					t.MapValue = &ref
				}
			}
		}
		return t
	}

	// Array → KindList
	if typeStr == "array" {
		t := &ir.Type{Name: name, Kind: ir.KindList, Description: s.Description, Constraints: collectConstraints(s)}
		if s.Items != nil {
			var is schemaNode
			json.Unmarshal(s.Items, &is)
			it := ctx.convertNode("", &is)
			if it != nil {
				ref := typeToRef(it)
				t.Items = &ref
			}
		}
		return t
	}

	// Nullable type: type: ["string", "null"]
	if types, ok := s.Type.([]any); ok {
		nonNull := []string{}
		hasNull := false
		for _, t := range types {
			if ts, ok := t.(string); ok {
				if ts == "null" {
					hasNull = true
				} else {
					nonNull = append(nonNull, ts)
				}
			}
		}
		if hasNull && len(nonNull) == 1 {
			inner := &ir.Type{Kind: ir.KindScalar, ScalarType: nonNull[0], Constraints: collectConstraints(s)}
			ref := typeToRef(inner)
			return &ir.Type{Name: name, Kind: ir.KindNullable, Inner: &ref, Description: s.Description}
		}
	}

	// Simple scalar
	if typeStr != "" {
		return &ir.Type{
			Name:        name,
			Kind:        ir.KindScalar,
			ScalarType:  typeStr,
			Description: s.Description,
			Constraints: collectConstraints(s),
		}
	}

	// Empty schema or unknown
	return &ir.Type{Name: name, Kind: ir.KindScalar, ScalarType: "any", Description: s.Description}
}

func resolveType(t any) string {
	switch v := t.(type) {
	case string:
		return v
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s != "null" {
				return s
			}
		}
	}
	return ""
}

func isNullVariant(raw json.RawMessage) bool {
	var s schemaNode
	json.Unmarshal(raw, &s)
	return resolveType(s.Type) == "null"
}

func typeToRef(t *ir.Type) ir.TypeRef {
	if t == nil {
		return ir.TypeRef{}
	}
	if t.Kind == ir.KindRef {
		return ir.TypeRef{Name: t.RefName}
	}
	return ir.TypeRef{Inline: t}
}

func collectConstraints(s *schemaNode) []ir.Constraint {
	var cs []ir.Constraint
	if s.Minimum != nil {
		cs = append(cs, ir.Constraint{Keyword: "minimum", Value: *s.Minimum})
	}
	if s.Maximum != nil {
		cs = append(cs, ir.Constraint{Keyword: "maximum", Value: *s.Maximum})
	}
	if s.ExclusiveMinimum != nil {
		cs = append(cs, ir.Constraint{Keyword: "exclusiveMinimum", Value: *s.ExclusiveMinimum})
	}
	if s.ExclusiveMaximum != nil {
		cs = append(cs, ir.Constraint{Keyword: "exclusiveMaximum", Value: *s.ExclusiveMaximum})
	}
	if s.MultipleOf != nil {
		cs = append(cs, ir.Constraint{Keyword: "multipleOf", Value: *s.MultipleOf})
	}
	if s.MinLength != nil {
		cs = append(cs, ir.Constraint{Keyword: "minLength", Value: *s.MinLength})
	}
	if s.MaxLength != nil {
		cs = append(cs, ir.Constraint{Keyword: "maxLength", Value: *s.MaxLength})
	}
	if s.Pattern != "" {
		cs = append(cs, ir.Constraint{Keyword: "pattern", Value: s.Pattern})
	}
	if s.MinItems != nil {
		cs = append(cs, ir.Constraint{Keyword: "minItems", Value: *s.MinItems})
	}
	if s.MaxItems != nil {
		cs = append(cs, ir.Constraint{Keyword: "maxItems", Value: *s.MaxItems})
	}
	if s.UniqueItems != nil && *s.UniqueItems {
		cs = append(cs, ir.Constraint{Keyword: "uniqueItems", Value: true})
	}
	if s.Format != "" {
		cs = append(cs, ir.Constraint{Keyword: "format", Value: s.Format})
	}
	if s.Const != nil {
		cs = append(cs, ir.Constraint{Keyword: "const", Value: s.Const})
	}
	return cs
}

// resolveStringEnums checks if a named $def is a string enum or an anyOf
// of string enums, and returns the combined enum values.
func (ctx *parseContext) resolveStringEnums(name string) ([]any, bool) {
	def, ok := ctx.defs[name]
	if !ok {
		return nil, false
	}

	// Direct string enum.
	refType := resolveType(def.Type)
	if refType == "string" {
		if len(def.Enum) > 0 {
			return def.Enum, true
		}
		// Plain string (no enum) — still string-typed.
		return nil, true
	}

	// anyOf of strings — recursively resolve.
	variants := def.AnyOf
	if len(variants) == 0 {
		variants = def.OneOf
	}
	if len(variants) == 0 {
		return nil, false
	}

	var combined []any
	for _, raw := range variants {
		var vs schemaNode
		json.Unmarshal(raw, &vs)

		if vs.Ref != "" {
			childRef := vs.Ref
			if strings.HasPrefix(childRef, "#/$defs/") {
				childRef = strings.TrimPrefix(childRef, "#/$defs/")
			}
			if childRef == name {
				return nil, false // self-reference, avoid infinite loop
			}
			childEnums, ok := ctx.resolveStringEnums(childRef)
			if !ok {
				return nil, false
			}
			combined = append(combined, childEnums...)
			continue
		}

		vType := resolveType(vs.Type)
		if vType == "null" {
			continue // nullable variant, skip
		}
		if vType != "string" {
			return nil, false
		}
		if len(vs.Enum) > 0 {
			combined = append(combined, vs.Enum...)
		}
	}

	if len(combined) > 0 {
		return combined, true
	}
	return nil, true // all string, no enums
}
