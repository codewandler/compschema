// Package uniongen generates Go sealed interfaces for oneOf/anyOf unions
// from the Schema IR. It produces:
//   - A sealed interface with an unexported marker method
//   - Pointer-receiver marker methods on each struct variant
//   - Wrapper types for primitive variants (string, []T, etc.)
//   - UnmarshalX([]byte) (X, error) dispatcher for discriminated unions
//   - MarshalJSON for wrapper types
//
// This package is used by the importer but is kept separate so union
// generation can be reused by other pipelines (e.g. codegen from Go types).
package uniongen

import (
	"fmt"
	"strings"

	"github.com/codewandler/compschema/internal/ir"
)

// TypeResolver maps IR names and type refs to Go source fragments.
// The importer supplies one; other callers can provide their own.
type TypeResolver interface {
	GoName(irName string) string       // schema name → Go identifier
	GoType(ref *ir.TypeRef) string     // type ref → Go type expression
	IsStruct(irName string) bool       // true if the named type is a struct
	Comment(description string) string // sanitize for // comment
}

// UnionAccessor describes an accessor method to generate on all struct
// variants of a union and include in the union interface.
type UnionAccessor struct {
	Method     string // Go method name (e.g. "EventType")
	Field      string // JSON field name to read (e.g. "type")
	ReturnType string // Go return type (e.g. "string")
}

// EmitUnion writes a sealed interface, marker methods, optional wrapper
// types, and an UnmarshalX dispatcher for a single union into b.
// If accessors is non-empty, accessor methods are added to the interface
// and implemented on every struct variant.
// The emittedAccessorMethods map tracks which (type, method) pairs have
// already been emitted to avoid duplicates when a variant belongs to
// multiple unions. Pass nil to disable dedup (single-union usage).
func EmitUnion(b *strings.Builder, goName string, t *ir.Type, pkg *ir.Package, r TypeResolver, emittedAccessorMethods map[string]bool, accessors ...UnionAccessor) {
	marker := fmt.Sprintf("is%s", goName)

	if t.Description != "" {
		b.WriteString(fmt.Sprintf("// %s %s\n", goName, r.Comment(t.Description)))
	}
	if t.Discriminator != "" {
		b.WriteString(fmt.Sprintf("// Discriminated by %q field.\n", t.Discriminator))
	}

	// Interface — marker method + any accessor methods.
	b.WriteString(fmt.Sprintf("//\n//compschema:generate\ntype %s interface {\n\t%s()\n", goName, marker))
	for _, acc := range accessors {
		b.WriteString(fmt.Sprintf("\t%s() %s\n", acc.Method, acc.ReturnType))
	}
	b.WriteString("}\n\n")

	// Track which wrapper types we create (for MarshalJSON later).
	wrapperTypes := map[string]string{} // wrapperName → primType

	// Marker methods + wrapper types for primitives.
	emittedWrappers := make(map[string]bool)
	for _, v := range t.Variants {
		vName := r.GoName(v.Name)
		if vName == goName {
			continue // skip self-reference
		}

		canHaveMethods := true
		primType := ""

		if v.TypeRef.Name != "" {
			vGoName := r.GoName(v.TypeRef.Name)
			canHaveMethods = false
			if r.IsStruct(v.TypeRef.Name) {
				canHaveMethods = true
			} else {
				for _, pt := range pkg.Types {
					if r.GoName(pt.Name) == vGoName && pt.Kind == ir.KindStruct {
						canHaveMethods = true
						break
					}
				}
			}
			// For non-struct named types (enums, scalars), create a wrapper.
			if !canHaveMethods {
				primType = vGoName
			}
		} else if v.TypeRef.Inline != nil {
			switch v.TypeRef.Inline.Kind {
			case ir.KindScalar:
				canHaveMethods = false
				primType = r.GoType(&ir.TypeRef{Inline: v.TypeRef.Inline})
			case ir.KindList:
				canHaveMethods = false
				primType = r.GoType(&ir.TypeRef{Inline: v.TypeRef.Inline})
			case ir.KindMap:
				canHaveMethods = false
			case ir.KindRef:
				if r.IsStruct(v.TypeRef.Inline.RefName) {
					vName = r.GoName(v.TypeRef.Inline.RefName)
				} else {
					canHaveMethods = false
				}
			default:
				canHaveMethods = false
			}
		}

		if canHaveMethods && vName != "" {
			for _, pt := range pkg.Types {
				if r.GoName(pt.Name) == vName && pt.Kind != ir.KindStruct {
					canHaveMethods = false
					break
				}
			}
		}

		if canHaveMethods && vName != "" {
			b.WriteString(fmt.Sprintf("func (*%s) %s() {}\n", vName, marker))
		} else if primType != "" {
			wrapperSuffix := r.GoName(primType)
			if vName != "" {
				wrapperSuffix = vName
			}
			wrapperName := goName + wrapperSuffix
			if emittedWrappers[wrapperName] {
				continue
			}
			emittedWrappers[wrapperName] = true
			wrapperTypes[wrapperName] = primType
			b.WriteString(fmt.Sprintf("\n// %s wraps a %s value as a %s variant.\n", wrapperName, primType, goName))
			b.WriteString(fmt.Sprintf("type %s struct { Value %s }\n", wrapperName, primType))
			b.WriteString(fmt.Sprintf("func (*%s) %s() {}\n", wrapperName, marker))
		}
	}
	b.WriteString("\n")

	// Accessor methods on struct variants.
	if len(accessors) > 0 {
		for _, v := range t.Variants {
			vName := r.GoName(v.Name)
			if vName == goName || vName == "" {
				continue
			}
			// Resolve the variant's struct type to find fields.
			var variantType *ir.Type
			if v.TypeRef.Name != "" {
				variantType = pkg.Types[v.TypeRef.Name]
			} else if v.TypeRef.Inline != nil {
				switch v.TypeRef.Inline.Kind {
				case ir.KindRef:
					variantType = pkg.Types[v.TypeRef.Inline.RefName]
				case ir.KindStruct:
					variantType = v.TypeRef.Inline
				}
			}
			if variantType == nil || variantType.Kind != ir.KindStruct {
				continue
			}
			for _, acc := range accessors {
				key := vName + "." + acc.Method
				if emittedAccessorMethods != nil && emittedAccessorMethods[key] {
					continue // already emitted by another union
				}
				for _, f := range variantType.Fields {
					if f.JSONName == acc.Field {
						fieldGoName := r.GoName(f.JSONName)
						fieldGoType := r.GoType(&f.Type)
						// The importer emits non-required fields as *T (pointer).
						// The IR field type doesn't include the pointer — check Required.
						isPtr := strings.HasPrefix(fieldGoType, "*") || !f.Required
						baseType := strings.TrimPrefix(fieldGoType, "*")
						needsConvert := baseType != acc.ReturnType

						var body string
						switch {
						case isPtr && needsConvert:
							body = fmt.Sprintf("if x.%s == nil { var zero %s; return zero }; return %s(*x.%s)",
								fieldGoName, acc.ReturnType, acc.ReturnType, fieldGoName)
						case isPtr:
							body = fmt.Sprintf("if x.%s == nil { var zero %s; return zero }; return *x.%s",
								fieldGoName, acc.ReturnType, fieldGoName)
						case needsConvert:
							body = fmt.Sprintf("return %s(x.%s)", acc.ReturnType, fieldGoName)
						default:
							body = fmt.Sprintf("return x.%s", fieldGoName)
						}
						b.WriteString(fmt.Sprintf("func (x *%s) %s() %s { %s }\n",
							vName, acc.Method, acc.ReturnType, body))
						if emittedAccessorMethods != nil {
							emittedAccessorMethods[key] = true
						}
						break
					}
				}
			}
		}
		b.WriteString("\n")

		// Accessor methods on wrapper types.
		for wrapperName, primType := range wrapperTypes {
			for _, acc := range accessors {
				key := wrapperName + "." + acc.Method
				if emittedAccessorMethods != nil && emittedAccessorMethods[key] {
					continue
				}
				var body string
				if primType == "string" || primType == acc.ReturnType {
					body = fmt.Sprintf("return %s(w.Value)", acc.ReturnType)
				} else {
					body = fmt.Sprintf("return fmt.Sprintf(\"%%v\", w.Value)")
				}
				b.WriteString(fmt.Sprintf("func (w *%s) %s() %s { %s }\n", wrapperName, acc.Method, acc.ReturnType, body))
				if emittedAccessorMethods != nil {
					emittedAccessorMethods[key] = true
				}
			}
		}
	}

	// MarshalJSON + UnmarshalJSON for wrapper types.
	for wrapperName, primType := range wrapperTypes {
		b.WriteString(fmt.Sprintf("func (w %s) MarshalJSON() ([]byte, error) {\n", wrapperName))
		b.WriteString("\treturn json.Marshal(w.Value)\n")
		b.WriteString("}\n\n")
		b.WriteString(fmt.Sprintf("func (w *%s) UnmarshalJSON(data []byte) error {\n", wrapperName))
		b.WriteString("\treturn json.Unmarshal(data, &w.Value)\n")
		b.WriteString("}\n\n")
		_ = primType
	}

	// Union helper functions — convenience constructors returning the union interface.
	emitUnionHelpers(b, goName, t, pkg, r, wrapperTypes)

	// UnmarshalX dispatcher.
	emitUnmarshalFunc(b, goName, t, pkg, r, wrapperTypes)
}

// emitUnmarshalFunc generates the UnmarshalX([]byte) (X, error) function.
func emitUnmarshalFunc(b *strings.Builder, goName string, t *ir.Type, pkg *ir.Package, r TypeResolver, wrapperTypes map[string]string) {
	// Check if a variant is a struct (can use &val) vs interface/other.
	isStructVariant := func(typeName string) bool {
		if typeName == "" {
			return false
		}
		if t, ok := pkg.Types[typeName]; ok {
			if t.Kind == ir.KindUnion {
				return false
			}
			if len(t.Variants) > 0 {
				return false
			}
			if t.Kind == ir.KindStruct {
				// Also check if another type with the same Go name is a union
				// (naming collision from extractInlineEnums).
				goN := r.GoName(typeName)
				for _, pt := range pkg.Types {
					if r.GoName(pt.Name) == goN && pt.Kind == ir.KindUnion {
						return false
					}
				}
				return true
			}
		}
		if r.IsStruct(typeName) {
			return true
		}
		return false
	}

	// Collect struct variants with discriminator values.
	type discVariant struct {
		GoName   string
		Values   []string
		IsStruct bool
	}
	var known []discVariant
	for _, v := range t.Variants {
		vName := r.GoName(v.Name)
		if vName == goName {
			continue
		}
		if v.TypeRef.Name != "" {
			gn := r.GoName(v.TypeRef.Name)
			is := isStructVariant(v.TypeRef.Name)
			if v.HasDiscriminator() {
				known = append(known, discVariant{GoName: gn, Values: v.DiscriminatorValues, IsStruct: is})
			}
		}
	}

	b.WriteString(fmt.Sprintf("// Unmarshal%s unmarshals JSON into the correct %s variant.\n", goName, goName))
	if t.Discriminator != "" {
		b.WriteString(fmt.Sprintf("// Dispatches on the %q discriminator field.\n", t.Discriminator))
	}
	b.WriteString(fmt.Sprintf("func Unmarshal%s(data []byte) (%s, error) {\n", goName, goName))

	if t.Discriminator != "" && len(known) > 0 {
		// Discriminator-based dispatch.
		b.WriteString(fmt.Sprintf("\tvar disc struct {\n\t\tD string `json:%q`\n\t}\n", t.Discriminator))
		b.WriteString("\tif err := json.Unmarshal(data, &disc); err != nil {\n")
		b.WriteString("\t\treturn nil, err\n")
		b.WriteString("\t}\n")
		b.WriteString("\tswitch disc.D {\n")

		// Group variants by value set.
		emitted := map[string]bool{}
		for _, kv := range known {
			// Build case label: "case "eq", "gt":" for multi-value.
			var caseValues []string
			for _, val := range kv.Values {
				if emitted[val] {
					continue
				}
				caseValues = append(caseValues, fmt.Sprintf("%q", val))
				emitted[val] = true
			}
			if len(caseValues) == 0 {
				continue
			}

			b.WriteString(fmt.Sprintf("\tcase %s:\n", strings.Join(caseValues, ", ")))
			if !kv.IsStruct {
				b.WriteString(fmt.Sprintf("\t\treturn nil, fmt.Errorf(\"variant %s for %s=%%q is not directly unmarshalable\", disc.D)\n", kv.GoName, t.Discriminator))
			} else {
				b.WriteString(fmt.Sprintf("\t\tvar val %s\n", kv.GoName))
				b.WriteString("\t\tif err := json.Unmarshal(data, &val); err != nil {\n")
				b.WriteString("\t\t\treturn nil, err\n")
				b.WriteString("\t\t}\n")
				b.WriteString("\t\treturn &val, nil\n")
			}
		}
		b.WriteString(fmt.Sprintf("\tdefault:\n\t\treturn nil, fmt.Errorf(\"unknown %s %%q for %s\", disc.D)\n", t.Discriminator, goName))
		b.WriteString("\t}\n")
	} else {
		// No discriminator — try each struct variant in order.
		for _, v := range t.Variants {
			vName := r.GoName(v.Name)
			if vName == goName {
				continue
			}
			goType := vName
			if v.TypeRef.Name != "" {
				goType = r.GoName(v.TypeRef.Name)
			}
			if goType == "" || !isStructVariant(v.TypeRef.Name) {
				continue
			}
			b.WriteString(fmt.Sprintf("\t{\n\t\tvar val %s\n", goType))
			b.WriteString("\t\tif err := json.Unmarshal(data, &val); err == nil {\n")
			b.WriteString("\t\t\treturn &val, nil\n")
			b.WriteString("\t\t}\n\t}\n")
		}
		// Also try wrapper types (for enum/scalar variants).
		for wrapperName := range wrapperTypes {
			b.WriteString(fmt.Sprintf("\t{\n\t\tvar val %s\n", wrapperName))
			b.WriteString("\t\tif err := json.Unmarshal(data, &val); err == nil {\n")
			b.WriteString("\t\t\treturn &val, nil\n")
			b.WriteString("\t\t}\n\t}\n")
		}
		b.WriteString(fmt.Sprintf("\treturn nil, fmt.Errorf(\"no matching variant for %s\")\n", goName))
	}

	b.WriteString("}\n\n")
}

// EmitStructUnmarshalJSON generates a custom UnmarshalJSON method for a struct
// that has one or more fields typed as union interfaces. The method uses
// json.RawMessage for interface fields and dispatches via UnmarshalX.
func EmitStructUnmarshalJSON(b *strings.Builder, goName string, t *ir.Type, pkg *ir.Package, r TypeResolver, emittedUnions map[string]bool) {
	var ifaceFields []UnionFieldInfo
	for _, f := range t.Fields {
		if uf := DetectUnionField(f, pkg, r, emittedUnions); uf != nil {
			ifaceFields = append(ifaceFields, *uf)
		}
	}

	if len(ifaceFields) == 0 {
		return
	}

	b.WriteString(fmt.Sprintf("func (v *%s) UnmarshalJSON(data []byte) error {\n", goName))
	b.WriteString(fmt.Sprintf("\ttype Alias %s\n", goName))

	// Build the raw struct with json.RawMessage for interface fields.
	b.WriteString("\tvar raw struct {\n")
	b.WriteString("\t\tAlias\n")
	for _, uf := range ifaceFields {
		switch {
		case uf.IsSlice:
			b.WriteString(fmt.Sprintf("\t\t%s []json.RawMessage `json:%q`\n", uf.GoName, uf.JSONName))
		default:
			b.WriteString(fmt.Sprintf("\t\t%s json.RawMessage `json:%q`\n", uf.GoName, uf.JSONName))
		}
	}
	b.WriteString("\t}\n")

	b.WriteString("\tif err := json.Unmarshal(data, &raw); err != nil {\n")
	b.WriteString("\t\treturn err\n")
	b.WriteString("\t}\n")
	b.WriteString(fmt.Sprintf("\t*v = %s(raw.Alias)\n", goName))

	// Dispatch each interface field.
	for _, uf := range ifaceFields {
		if uf.IsSlice {
			if uf.IsPointer {
				// *NamedListType — allocate the named slice, then append to it.
				b.WriteString(fmt.Sprintf("\tif len(raw.%s) > 0 {\n", uf.GoName))
				b.WriteString(fmt.Sprintf("\t\tlist := make(%s, 0, len(raw.%s))\n", uf.SliceType, uf.GoName))
				b.WriteString(fmt.Sprintf("\t\tfor _, item := range raw.%s {\n", uf.GoName))
				b.WriteString(fmt.Sprintf("\t\t\tparsed, err := Unmarshal%s(item)\n", uf.TypeName))
				b.WriteString("\t\t\tif err != nil {\n")
				b.WriteString("\t\t\t\treturn err\n")
				b.WriteString("\t\t\t}\n")
				b.WriteString("\t\t\tlist = append(list, parsed)\n")
				b.WriteString("\t\t}\n")
				b.WriteString(fmt.Sprintf("\t\tv.%s = &list\n", uf.GoName))
				b.WriteString("\t}\n")
			} else {
				b.WriteString(fmt.Sprintf("\tfor _, item := range raw.%s {\n", uf.GoName))
				b.WriteString(fmt.Sprintf("\t\tparsed, err := Unmarshal%s(item)\n", uf.TypeName))
				b.WriteString("\t\tif err != nil {\n")
				b.WriteString("\t\t\treturn err\n")
				b.WriteString("\t\t}\n")
				b.WriteString(fmt.Sprintf("\t\tv.%s = append(v.%s, parsed)\n", uf.GoName, uf.GoName))
				b.WriteString("\t}\n")
			}
		} else {
			b.WriteString(fmt.Sprintf("\tif len(raw.%s) > 0 && string(raw.%s) != \"null\" {\n", uf.GoName, uf.GoName))
			b.WriteString(fmt.Sprintf("\t\tparsed, err := Unmarshal%s(raw.%s)\n", uf.TypeName, uf.GoName))
			b.WriteString("\t\tif err != nil {\n")
			b.WriteString("\t\t\treturn err\n")
			b.WriteString("\t\t}\n")
			b.WriteString(fmt.Sprintf("\t\tv.%s = parsed\n", uf.GoName))
			b.WriteString("\t}\n")
		}
	}

	b.WriteString("\treturn nil\n")
	b.WriteString("}\n\n")
}

// UnionFieldInfo describes a struct field that is typed as a union interface.
type UnionFieldInfo struct {
	GoName       string // Go field name
	JSONName     string // JSON key
	TypeName     string // union interface type name (Go)
	SliceType    string // named list type name (Go), set when IsSlice && IsPointer
	IsSlice      bool   // []Interface
	IsPointer    bool   // *Interface or *NamedListType
}

// DetectUnionField checks if a struct field is (or contains) a union interface.
// If emittedUnions is non-nil, only unions in that set are considered.
// Returns nil if the field doesn't need special unmarshal handling.
func DetectUnionField(f ir.Field, pkg *ir.Package, r TypeResolver, emittedUnions map[string]bool) *UnionFieldInfo {
	goFieldName := r.GoName(f.JSONName)

	checkUnion := func(typeName string) bool {
		t, ok := pkg.Types[typeName]
		if !ok || t.Kind != ir.KindUnion {
			return false
		}
		if emittedUnions != nil {
			return emittedUnions[r.GoName(typeName)]
		}
		return true
	}

	// Direct interface reference: Field SomeInterface
	if f.Type.Name != "" && checkUnion(f.Type.Name) {
		return &UnionFieldInfo{goFieldName, f.JSONName, r.GoName(f.Type.Name), "", false, false}
	}

	// Named list type whose items are a union: type FooList []SomeInterface
	// When the field is optional (!Required), the importer renders it as *FooList.
	if f.Type.Name != "" {
		if listType, ok := pkg.Types[f.Type.Name]; ok && listType.Kind == ir.KindList && listType.Items != nil {
			if listType.Items.Name != "" && checkUnion(listType.Items.Name) {
				isPtr := !f.Required
				sliceType := ""
				if isPtr {
					sliceType = r.GoName(f.Type.Name)
				}
				return &UnionFieldInfo{goFieldName, f.JSONName, r.GoName(listType.Items.Name), sliceType, true, isPtr}
			}
		}
	}

	if f.Type.Inline == nil {
		return nil
	}

	// Pointer to interface: *SomeInterface
	if f.Type.Inline.Kind == ir.KindNullable && f.Type.Inline.Inner != nil {
		if f.Type.Inline.Inner.Name != "" && checkUnion(f.Type.Inline.Inner.Name) {
			return &UnionFieldInfo{goFieldName, f.JSONName, r.GoName(f.Type.Inline.Inner.Name), "", false, true}
		}

		// Pointer to named list type whose items are a union: *FooList where type FooList []SomeInterface
		if f.Type.Inline.Inner.Name != "" {
			if listType, ok := pkg.Types[f.Type.Inline.Inner.Name]; ok && listType.Kind == ir.KindList && listType.Items != nil {
				if listType.Items.Name != "" && checkUnion(listType.Items.Name) {
					return &UnionFieldInfo{goFieldName, f.JSONName, r.GoName(listType.Items.Name), r.GoName(f.Type.Inline.Inner.Name), true, true}
				}
			}
		}
	}

	// Slice of interface: []SomeInterface
	if f.Type.Inline.Kind == ir.KindList && f.Type.Inline.Items != nil {
		if f.Type.Inline.Items.Name != "" && checkUnion(f.Type.Inline.Items.Name) {
			return &UnionFieldInfo{goFieldName, f.JSONName, r.GoName(f.Type.Inline.Items.Name), "", true, false}
		}
	}

	return nil
}

// emitUnionHelpers generates convenience functions for constructing union values:
//
//  1. For each struct variant: {UnionName}From{VariantName}(v *Variant) UnionName
//  2. For each wrapper variant: {UnionName}{ShortType}(v InnerType) UnionName
//
// These make it easy to construct union values without knowing wrapper type names.
func emitUnionHelpers(b *strings.Builder, goName string, t *ir.Type, pkg *ir.Package, r TypeResolver, wrapperTypes map[string]string) {
	if len(t.Variants) == 0 {
		return
	}

	emitted := make(map[string]bool)

	// Wrapper variant helpers: New{WrapperName}(v InnerType) UnionName
	for wrapperName, primType := range wrapperTypes {
		funcName := "New" + wrapperName
		if emitted[funcName] {
			continue
		}
		emitted[funcName] = true
		b.WriteString(fmt.Sprintf("// %s creates a %s from a %s value.\n", funcName, goName, primType))
		b.WriteString(fmt.Sprintf("func %s(v %s) %s {\n", funcName, primType, goName))
		b.WriteString(fmt.Sprintf("\treturn &%s{Value: v}\n", wrapperName))
		b.WriteString("}\n\n")
	}

	// Struct variant helpers: {UnionName}From{VariantName}(v *Variant) UnionName
	for _, v := range t.Variants {
		vName := r.GoName(v.Name)
		if vName == "" || vName == goName {
			continue
		}
		// Skip wrapper types (already handled above).
		if _, isWrapper := wrapperTypes[vName]; isWrapper {
			continue
		}

		// Check if the variant is a struct type.
		if v.TypeRef.Name != "" {
			vt, ok := pkg.Types[v.TypeRef.Name]
			if !ok || vt.Kind != ir.KindStruct {
				continue
			}
		} else if v.TypeRef.Inline != nil {
			if v.TypeRef.Inline.Kind == ir.KindRef {
				vt, ok := pkg.Types[v.TypeRef.Inline.RefName]
				if !ok || vt.Kind != ir.KindStruct {
					continue
				}
			} else if v.TypeRef.Inline.Kind != ir.KindStruct {
				continue
			}
		}

		funcName := goName + "From" + vName
		if emitted[funcName] {
			continue
		}
		emitted[funcName] = true
		b.WriteString(fmt.Sprintf("// %s wraps a *%s as a %s union value.\n", funcName, vName, goName))
		b.WriteString(fmt.Sprintf("func %s(v *%s) %s {\n", funcName, vName, goName))
		b.WriteString("\treturn v\n")
		b.WriteString("}\n\n")
	}
}
