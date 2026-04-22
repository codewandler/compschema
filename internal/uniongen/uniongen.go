// Package uniongen post-processes generated Go types and a JSON Schema to
// replace `type X interface{}` with proper sealed interfaces for oneOf/anyOf unions.
//
// It reads the JSON Schema to determine variant types and discriminator fields,
// then rewrites the Go source to generate:
//   - A sealed interface with an unexported marker method
//   - Variant type assertions via the marker method
//   - An UnmarshalJSON that dispatches on the discriminator
package uniongen

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Union describes a detected oneOf/anyOf union in the JSON Schema.
type Union struct {
	Name          string    // Go type name (e.g. "Tool")
	Keyword       string    // "oneOf" or "anyOf"
	Discriminator string    // property name used to discriminate (e.g. "type"), or ""
	Variants      []Variant // the union members
}

// Variant is one arm of a union.
type Variant struct {
	RefName       string // name of the $ref target (e.g. "FileSearchTool"), or "" for inline
	TypeConstVal  string // value of the discriminator const/enum, if known
	InlineType    string // for non-$ref variants: "string", "number", etc.
}

// Schema is a minimal JSON Schema representation for union analysis.
type Schema struct {
	Defs map[string]SchemaDef `json:"$defs"`
}

// SchemaDef is a single schema definition.
type SchemaDef struct {
	OneOf            []SchemaRef       `json:"oneOf,omitempty"`
	AnyOf            []SchemaRef       `json:"anyOf,omitempty"`
	AllOf            []SchemaRef       `json:"allOf,omitempty"`
	Type             any               `json:"type,omitempty"`
	Properties       map[string]any    `json:"properties,omitempty"`
	XDiscriminator   *Discriminator    `json:"x-discriminator,omitempty"`
	Enum             []any             `json:"enum,omitempty"`
}

type SchemaRef struct {
	Ref        string         `json:"$ref,omitempty"`
	Type       any            `json:"type,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
	Const      any            `json:"const,omitempty"`
	Enum       []any          `json:"enum,omitempty"`
	Title      string         `json:"title,omitempty"`
}

type Discriminator struct {
	PropertyName string            `json:"propertyName"`
	Mapping      map[string]string `json:"mapping,omitempty"`
}

// AnalyzeSchema reads a JSON Schema file and detects all union types.
func AnalyzeSchema(schemaPath string) ([]Union, error) {
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}

	var schema Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("parse schema: %w", err)
	}

	var unions []Union

	for name, def := range schema.Defs {
		var keyword string
		var refs []SchemaRef

		if len(def.OneOf) > 0 {
			keyword = "oneOf"
			refs = def.OneOf
		} else if len(def.AnyOf) > 0 {
			keyword = "anyOf"
			refs = def.AnyOf
		} else {
			continue
		}

		// Skip if this has properties (it's an object that happens to have oneOf for validation, not a union)
		if len(def.Properties) > 0 {
			continue
		}
		// Skip if it also has a type (it's a constrained type, not a pure union)
		if def.Type != nil {
			continue
		}

		u := Union{
			Name:    name,
			Keyword: keyword,
		}

		// Determine discriminator
		if def.XDiscriminator != nil && def.XDiscriminator.PropertyName != "" {
			u.Discriminator = def.XDiscriminator.PropertyName
		}

		// Analyze each variant
		for _, ref := range refs {
			v := Variant{}
			if ref.Ref != "" {
				parts := strings.Split(ref.Ref, "/")
				v.RefName = parts[len(parts)-1]

				// Try to find discriminator value from the referenced schema
				if u.Discriminator != "" {
					if refDef, ok := schema.Defs[v.RefName]; ok {
						v.TypeConstVal = extractDiscriminatorValue(refDef, u.Discriminator)
					}
				}
			} else if ref.Type != nil {
				switch t := ref.Type.(type) {
				case string:
					v.InlineType = t
				}
			}
			u.Variants = append(u.Variants, v)
		}

		// If no explicit discriminator, try to infer one from "type" const fields
		if u.Discriminator == "" {
			if disc := inferDiscriminator(schema.Defs, refs); disc != "" {
				u.Discriminator = disc
				// Re-populate variant type values
				for i, v := range u.Variants {
					if v.RefName != "" {
						if refDef, ok := schema.Defs[v.RefName]; ok {
							u.Variants[i].TypeConstVal = extractDiscriminatorValue(refDef, disc)
						}
					}
				}
			}
		}

		unions = append(unions, u)
	}

	// Sort for deterministic output
	sort.Slice(unions, func(i, j int) bool {
		return unions[i].Name < unions[j].Name
	})

	return unions, nil
}

// extractDiscriminatorValue gets the const/single-enum value of a property.
// For multi-value enums, returns the first value (sufficient for discriminator matching).
func extractDiscriminatorValue(def SchemaDef, propName string) string {
	propRaw, ok := def.Properties[propName]
	if !ok {
		return ""
	}
	propBytes, err := json.Marshal(propRaw)
	if err != nil {
		return ""
	}
	var prop struct {
		Const any   `json:"const"`
		Enum  []any `json:"enum"`
	}
	if err := json.Unmarshal(propBytes, &prop); err != nil {
		return ""
	}
	if prop.Const != nil {
		return fmt.Sprintf("%v", prop.Const)
	}
	if len(prop.Enum) >= 1 {
		return fmt.Sprintf("%v", prop.Enum[0])
	}
	return ""
}

// inferDiscriminator checks if all $ref variants share a common property
// with a const/single-enum value, making it a natural discriminator.
func inferDiscriminator(defs map[string]SchemaDef, refs []SchemaRef) string {
	// Collect all candidate property names from first ref variant
	var candidates []string
	for _, ref := range refs {
		if ref.Ref == "" {
			continue
		}
		parts := strings.Split(ref.Ref, "/")
		refName := parts[len(parts)-1]
		refDef, ok := defs[refName]
		if !ok {
			continue
		}
		for propName := range refDef.Properties {
			candidates = append(candidates, propName)
		}
		break
	}

	// Check each candidate: does every $ref variant have a const/single-enum for it?
	for _, candidate := range candidates {
		allHaveConst := true
		values := map[string]bool{}
		refCount := 0
		for _, ref := range refs {
			if ref.Ref == "" {
				// Inline variants (e.g. string) can't have discriminators
				allHaveConst = false
				break
			}
			parts := strings.Split(ref.Ref, "/")
			refName := parts[len(parts)-1]
			refDef, ok := defs[refName]
			if !ok {
				allHaveConst = false
				break
			}
			val := extractDiscriminatorValue(refDef, candidate)
			if val == "" {
				allHaveConst = false
				break
			}
			if values[val] {
				// Duplicate value — not a valid discriminator
				allHaveConst = false
				break
			}
			values[val] = true
			refCount++
		}
		if allHaveConst && refCount == len(refs) {
			return candidate
		}
	}

	return ""
}
// buildCapitalizationReplacer creates a function that applies Go capitalization
// rules (e.g. "Url" → "URL", "Id" → "ID") to schema names.
func buildCapitalizationReplacer(capitalizations []string) func(string) string {
	if len(capitalizations) == 0 {
		return func(s string) string { return s }
	}

	return func(name string) string {
		for _, cap := range capitalizations {
			titleCase := strings.ToUpper(cap[:1]) + strings.ToLower(cap[1:])
			name = strings.ReplaceAll(name, titleCase, cap)
		}
		return name
	}
}

// GenerateUnionCode generates Go source code for sealed interfaces.
// capitalizations maps schema names to their Go-idiomatic forms (e.g. "Url" → "URL").
func GenerateUnionCode(pkg string, unions []Union, capitalizations ...string) string {
	// Build capitalization replacer
	replacer := buildCapitalizationReplacer(capitalizations)

	var b strings.Builder

	b.WriteString("// Code generated by compschema uniongen. DO NOT EDIT.\n\n")
	b.WriteString(fmt.Sprintf("package %s\n\n", pkg))
	b.WriteString("import (\n\t\"encoding/json\"\n\t\"fmt\"\n)\n\n")

	for _, u := range unions {
		// Skip unions where not all variants are $refs (can’t generate clean interfaces)
		allRefs := true
		for _, v := range u.Variants {
			if v.RefName == "" {
				allRefs = false
				break
			}
		}
		if !allRefs {
			b.WriteString(fmt.Sprintf("// %s: skipped (has inline/non-$ref variants)\n\n", u.Name))
			continue
		}

		goTypeName := replacer(u.Name)
		markerMethod := fmt.Sprintf("is%s", goTypeName)

		// Interface
		b.WriteString(fmt.Sprintf("// %s is a sealed interface for %s union.\n", goTypeName, u.Keyword))
		if u.Discriminator != "" {
			b.WriteString(fmt.Sprintf("// Discriminated by %q field.\n", u.Discriminator))
		}
		b.WriteString(fmt.Sprintf("type %s interface {\n", goTypeName))
		b.WriteString(fmt.Sprintf("\t%s()\n", markerMethod))
		b.WriteString("}\n\n")

		// Marker method on each variant
		for _, v := range u.Variants {
			b.WriteString(fmt.Sprintf("func (*%s) %s() {}\n", replacer(v.RefName), markerMethod))
		}
		b.WriteString("\n")

		// UnmarshalJSON with discriminator dispatch
		if u.Discriminator != "" {
			generateUnmarshalJSON(&b, u, replacer)
		}
	}

	return b.String()
}

func allDiscriminated(u Union) bool {
	for _, v := range u.Variants {
		if v.TypeConstVal == "" {
			return false
		}
	}
	return true
}

func generateUnmarshalJSON(b *strings.Builder, u Union, goName func(string) string) {
	// Collect variants with known discriminator values
	type discVariant struct {
		Value   string
		RefName string
		GoName  string
	}
	var known []discVariant

	for _, v := range u.Variants {
		if v.TypeConstVal != "" && v.RefName != "" {
			known = append(known, discVariant{
				Value:   v.TypeConstVal,
				RefName: v.RefName,
				GoName:  goName(v.RefName),
			})
		}
	}

	if len(known) == 0 {
		return
	}

	// Detect duplicate discriminator values (e.g. multiple variants with type="message")
	valCount := map[string]int{}
	for _, kv := range known {
		valCount[kv.Value]++
	}
	hasDuplicates := false
	for _, count := range valCount {
		if count > 1 {
			hasDuplicates = true
			break
		}
	}

	typeName := goName(u.Name)

	b.WriteString(fmt.Sprintf("// Unmarshal%s unmarshals JSON into the correct %s variant\n", typeName, typeName))
	b.WriteString(fmt.Sprintf("// based on the %q discriminator field.\n", u.Discriminator))
	b.WriteString(fmt.Sprintf("func Unmarshal%s(data []byte) (%s, error) {\n", typeName, typeName))
	b.WriteString(fmt.Sprintf("\tvar disc struct {\n\t\tD string `json:%q`\n\t}\n", u.Discriminator))
	b.WriteString("\tif err := json.Unmarshal(data, &disc); err != nil {\n")
	b.WriteString("\t\treturn nil, err\n")
	b.WriteString("\t}\n")

	if hasDuplicates {
		// For duplicate discriminator values, try each variant in order
		b.WriteString("\tswitch disc.D {\n")

		// Group by value
		emitted := map[string]bool{}
		for _, kv := range known {
			if emitted[kv.Value] {
				continue
			}
			emitted[kv.Value] = true

			// Collect all variants for this value
			var variants []discVariant
			for _, kv2 := range known {
				if kv2.Value == kv.Value {
					variants = append(variants, kv2)
				}
			}

			b.WriteString(fmt.Sprintf("\tcase %q:\n", kv.Value))
			if len(variants) == 1 {
				b.WriteString(fmt.Sprintf("\t\tvar val %s\n", variants[0].GoName))
				b.WriteString("\t\tif err := json.Unmarshal(data, &val); err != nil {\n")
				b.WriteString("\t\t\treturn nil, err\n")
				b.WriteString("\t\t}\n")
				b.WriteString("\t\treturn &val, nil\n")
			} else {
				// Try each in order — first successful unmarshal wins
				for i, v := range variants {
					varName := fmt.Sprintf("v%d", i)
					b.WriteString(fmt.Sprintf("\t\tvar %s %s\n", varName, v.GoName))
					b.WriteString(fmt.Sprintf("\t\tif err := json.Unmarshal(data, &%s); err == nil {\n", varName))
					b.WriteString(fmt.Sprintf("\t\t\treturn &%s, nil\n", varName))
					b.WriteString("\t\t}\n")
				}
				b.WriteString(fmt.Sprintf("\t\treturn nil, fmt.Errorf(\"no matching variant for %s=%%q in %s\", disc.D)\n", u.Discriminator, typeName))
			}
		}
		b.WriteString(fmt.Sprintf("\tdefault:\n\t\treturn nil, fmt.Errorf(\"unknown %s %%q for %s\", disc.D)\n", u.Discriminator, typeName))
		b.WriteString("\t}\n")
	} else {
		// Simple case — unique discriminator values
		b.WriteString("\tswitch disc.D {\n")
		for _, kv := range known {
			b.WriteString(fmt.Sprintf("\tcase %q:\n", kv.Value))
			b.WriteString(fmt.Sprintf("\t\tvar val %s\n", kv.GoName))
			b.WriteString("\t\tif err := json.Unmarshal(data, &val); err != nil {\n")
			b.WriteString("\t\t\treturn nil, err\n")
			b.WriteString("\t\t}\n")
			b.WriteString("\t\treturn &val, nil\n")
		}
		b.WriteString(fmt.Sprintf("\tdefault:\n\t\treturn nil, fmt.Errorf(\"unknown %s %%q for %s\", disc.D)\n", u.Discriminator, typeName))
		b.WriteString("\t}\n")
	}
	b.WriteString("}\n\n")
}
