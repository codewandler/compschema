package emitter

import (
	"fmt"
	"sort"

	"github.com/codewandler/compschema/internal/ir"
	"go.yaml.in/yaml/v4"
)

// IRToYAML serializes an IR Package to a human-readable YAML document.
// This is the canonical representation of what compschema understood
// from the Go types — useful for review, diffing, and debugging.
//
// Structure:
//
//	package: mypackage
//	hash: <package merkle hash>
//	hashes:
//	  TypeA: <hash>
//	  TypeB: <hash>
//	types:
//	  - name: TypeA
//	    kind: struct
//	    fields: [...]
func IRToYAML(pkg *ir.Package) ([]byte, error) {
	doc := make(map[string]any, 4)
	doc["package"] = pkg.Name
	doc["hash"] = fmt.Sprintf("%x", pkg.Hash())

	// Per-type hashes as a flat map at the top level.
	hashes := make(map[string]string, len(pkg.Order))
	for _, name := range pkg.Order {
		t := pkg.Types[name]
		hashes[name] = fmt.Sprintf("%x", t.Hash())
	}
	doc["hashes"] = hashes

	// Types without embedded hashes.
	types := make([]any, 0, len(pkg.Order))
	for _, name := range pkg.Order {
		t := pkg.Types[name]
		types = append(types, typeToIR(t))
	}
	doc["types"] = types

	return yaml.Marshal(doc)
}

func typeToIR(t *ir.Type) map[string]any {
	m := map[string]any{
		"name": t.Name,
		"kind": kindString(t.Kind),
	}

	if t.Description != "" {
		m["description"] = t.Description
	}

	switch t.Kind {
	case ir.KindStruct:
		if len(t.Fields) > 0 {
			fields := make([]map[string]any, len(t.Fields))
			for i, f := range t.Fields {
				fields[i] = fieldToIR(f)
			}
			m["fields"] = fields
		}

	case ir.KindEnum:
		m["enum_type"] = t.EnumType
		m["values"] = t.EnumValues

	case ir.KindUnion:
		if t.Discriminator != "" {
			m["discriminator"] = t.Discriminator
		}
		variants := make([]map[string]any, len(t.Variants))
		for i, v := range t.Variants {
			variants[i] = variantToIR(v)
		}
		m["variants"] = variants

	case ir.KindList:
		if t.Items != nil {
			m["items"] = typeRefToIR(*t.Items)
		}

	case ir.KindMap:
		if t.MapValue != nil {
			m["map_value"] = typeRefToIR(*t.MapValue)
		}

	case ir.KindScalar:
		m["scalar_type"] = t.ScalarType

	case ir.KindRef:
		m["ref"] = t.RefName

	case ir.KindNullable:
		if t.Inner != nil {
			m["inner"] = typeRefToIR(*t.Inner)
		}
	}

	if len(t.Constraints) > 0 {
		m["constraints"] = constraintsToIR(t.Constraints)
	}

	return m
}

func fieldToIR(f ir.Field) map[string]any {
	m := map[string]any{
		"name":      f.Name,
		"json_name": f.JSONName,
		"type":      typeRefToIR(f.Type),
	}
	if f.Required {
		m["required"] = true
	}
	if f.Description != "" {
		m["description"] = f.Description
	}
	if len(f.Constraints) > 0 {
		m["constraints"] = constraintsToIR(f.Constraints)
	}
	if len(f.Tags) > 0 {
		m["tags"] = f.Tags
	}
	return m
}

func typeRefToIR(ref ir.TypeRef) any {
	if ref.Name != "" {
		return ref.Name
	}
	if ref.Inline != nil {
		return typeToIR(ref.Inline)
	}
	return "unknown"
}

func variantToIR(v ir.Variant) map[string]any {
	m := map[string]any{
		"name": v.Name,
		"type": typeRefToIR(v.TypeRef),
	}
	if len(v.DiscriminatorValues) > 0 {
		if len(v.DiscriminatorValues) == 1 {
			m["discriminator_value"] = v.DiscriminatorValues[0]
		} else {
			m["discriminator_values"] = v.DiscriminatorValues
		}
	}
	return m
}

func constraintsToIR(cs []ir.Constraint) []map[string]any {
	// Sort constraints by keyword for deterministic output.
	sorted := make([]ir.Constraint, len(cs))
	copy(sorted, cs)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Keyword < sorted[j].Keyword
	})

	result := make([]map[string]any, len(sorted))
	for i, c := range sorted {
		result[i] = map[string]any{
			c.Keyword: c.Value,
		}
	}
	return result
}

func kindString(k ir.Kind) string {
	switch k {
	case ir.KindStruct:
		return "struct"
	case ir.KindEnum:
		return "enum"
	case ir.KindUnion:
		return "union"
	case ir.KindList:
		return "list"
	case ir.KindMap:
		return "map"
	case ir.KindScalar:
		return "scalar"
	case ir.KindRef:
		return "ref"
	case ir.KindNullable:
		return "nullable"
	default:
		return fmt.Sprintf("unknown(%d)", k)
	}
}
