// Package analyzer inspects Go packages via go/types and builds a Schema IR.
//
// It looks for types annotated with //compschema:generate and transitively
// resolves all referenced types (struct fields, enum const blocks, sealed
// interface implementors).
package analyzer

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/codewandler/compschema/internal/ir"
	"golang.org/x/tools/go/packages"
)

// Analyze loads and analyzes the given Go package patterns,
// returning an IR Package for each Go package that contains
// annotated types (or all exported types if allTypes is true).
func Analyze(allTypes bool, patterns ...string) ([]*ir.Package, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedSyntax |
			packages.NeedDeps,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("load packages: %w", err)
	}

	var result []*ir.Package
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			return nil, fmt.Errorf("package %s: %v", pkg.PkgPath, pkg.Errors[0])
		}
		irPkg := analyzePackage(pkg, allTypes)
		if irPkg != nil && len(irPkg.Types) > 0 {
			result = append(result, irPkg)
		}
	}
	return result, nil
}

func analyzePackage(pkg *packages.Package, allTypes bool) *ir.Package {
	a := &pkgAnalyzer{
		pkg:       pkg,
		irPkg:     ir.NewPackage(pkg.Name, pkg.PkgPath),
		seen:      make(map[string]bool),
		resolving: make(map[string]bool),
		enumMap:   buildEnumMap(pkg),
		fieldDocs: buildFieldDocMap(pkg),
	}

	// Derive source directory from the first Go file.
	if len(pkg.GoFiles) > 0 {
		a.irPkg.Dir = filepath.Dir(pkg.GoFiles[0])
	}

	// Find root types.
	var roots []string
	if allTypes {
		roots = a.findAllExportedTypes()
	} else {
		roots = a.findAnnotatedTypes()
	}
	if len(roots) == 0 {
		return nil
	}

	// Analyze each root and its transitive dependencies.
	for _, name := range roots {
		a.ensureType(name)
	}

	return a.irPkg
}

type pkgAnalyzer struct {
	pkg       *packages.Package
	irPkg     *ir.Package
	seen      map[string]bool // for ensureType
	resolving map[string]bool // for resolveTypeRef cycle detection
	enumMap   map[string][]any
	fieldDocs map[string]string
}

// findAnnotatedTypes scans comments for //compschema:generate directives.
func (a *pkgAnalyzer) findAnnotatedTypes() []string {
	var names []string
	for _, file := range a.pkg.Syntax {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			if !hasAnnotation(gd.Doc) {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				names = append(names, ts.Name.Name)
			}
		}
	}
	return names
}

// findAllExportedTypes returns all exported type names in the package.
func (a *pkgAnalyzer) findAllExportedTypes() []string {
	var names []string
	scope := a.pkg.Types.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if _, ok := obj.(*types.TypeName); ok && obj.Exported() {
			names = append(names, name)
		}
	}
	return names
}

func hasAnnotation(cg *ast.CommentGroup) bool {
	if cg == nil {
		return false
	}
	for _, c := range cg.List {
		if strings.Contains(c.Text, "compschema:generate") {
			return true
		}
	}
	return false
}

// ensureType resolves a named type and adds it to the IR if not already present.
func (a *pkgAnalyzer) ensureType(name string) {
	if a.seen[name] {
		return
	}
	a.seen[name] = true

	obj := a.pkg.Types.Scope().Lookup(name)
	if obj == nil {
		return
	}
	tn, ok := obj.(*types.TypeName)
	if !ok {
		return
	}

	// Skip type aliases (type X = Y) — methods would conflict.
	if tn.IsAlias() {
		return
	}

	t := a.convertType(name, tn.Type())
	if t != nil {
		// Check if the type already has UnmarshalJSON defined.
		if t.Kind == ir.KindStruct && hasMethod(tn.Type(), "UnmarshalJSON") {
			t.HasUnmarshalJSON = true
		}
		a.irPkg.Add(t)
	}
}

// hasMethod checks if a type has a method with the given name.
func hasMethod(typ types.Type, name string) bool {
	mset := types.NewMethodSet(types.NewPointer(typ))
	for i := 0; i < mset.Len(); i++ {
		if mset.At(i).Obj().Name() == name {
			return true
		}
	}
	return false
}

// convertType converts a go/types.Type into an IR Type.
func (a *pkgAnalyzer) convertType(name string, typ types.Type) *ir.Type {
	switch t := typ.Underlying().(type) {
	case *types.Struct:
		return a.convertStruct(name, typ, t)
	case *types.Interface:
		if t.NumMethods() == 0 {
			// Named empty interface (e.g. type Foo interface{}) — accepts any JSON.
			return &ir.Type{
				Name:        name,
				Kind:        ir.KindScalar,
				ScalarType:  "any",
				Description: a.typeDoc(name),
			}
		}
		u := a.convertUnion(name, t)
		if u != nil && u.Kind == ir.KindUnion && len(u.Variants) == 0 {
			// Union with no implementors — skip.
			return nil
		}
		return u
	case *types.Basic:
		// Check if it's an enum.
		if vals, ok := a.enumMap[name]; ok {
			return a.convertEnum(name, t, vals)
		}
		// Named scalar type.
		return &ir.Type{
			Name:       name,
			Kind:       ir.KindScalar,
			ScalarType: basicToScalar(t),
		}
	case *types.Map:
		ref := a.resolveTypeRef(t) // use underlying, not named type
		if ref.Inline != nil {
			ref.Inline.Name = name
			ref.Inline.Description = a.typeDoc(name)
			return ref.Inline
		}
		return nil
	case *types.Slice:
		ref := a.resolveTypeRef(t) // use underlying, not named type
		if ref.Inline != nil {
			ref.Inline.Name = name
			ref.Inline.Description = a.typeDoc(name)
			return ref.Inline
		}
		return nil
	case *types.Pointer:
		ref := a.resolveTypeRef(t) // use underlying, not named type
		if ref.Inline != nil {
			ref.Inline.Name = name
			ref.Inline.Description = a.typeDoc(name)
			return ref.Inline
		}
		return nil
	default:
		return nil
	}
}

// convertStruct builds a KindStruct IR type.
func (a *pkgAnalyzer) convertStruct(name string, named types.Type, st *types.Struct) *ir.Type {
	irType := &ir.Type{
		Name: name,
		Kind: ir.KindStruct,
	}

	// Extract doc comment.
	irType.Description = a.typeDoc(name)

	for i := 0; i < st.NumFields(); i++ {
		field := st.Field(i)

		// Handle embedded structs — flatten their fields into this struct.
		if field.Embedded() {
			embType := field.Type()
			if ptr, ok := embType.(*types.Pointer); ok {
				embType = ptr.Elem()
			}
			if embSt, ok := embType.Underlying().(*types.Struct); ok {
				for j := 0; j < embSt.NumFields(); j++ {
					ef := embSt.Field(j)
					if !ef.Exported() {
						continue
					}
					etag := reflect.StructTag(embSt.Tag(j))
					ejsonTag := etag.Get("json")
					if ejsonTag == "-" {
						continue
					}
					ejsonName, eopts := parseJSONTag(ejsonTag)
					if ejsonName == "" {
						ejsonName = ef.Name()
					}
					ff := ir.Field{
						Name:     ef.Name(),
						JSONName: ejsonName,
						Required: !eopts.contains("omitempty") && !eopts.contains("omitzero"),
						Type:     a.resolveTypeRef(ef.Type()),
					}
					if jsTag := etag.Get("jsonschema"); jsTag != "" {
						ff.Constraints, ff.Description = parseConstraintsAndMeta(jsTag)
					}
					irType.Fields = append(irType.Fields, ff)
				}
			}
			continue
		}

		if !field.Exported() {
			continue
		}

		tag := reflect.StructTag(st.Tag(i))
		jsonTag := tag.Get("json")
		if jsonTag == "-" {
			continue
		}

		jsonName, opts := parseJSONTag(jsonTag)
		if jsonName == "" {
			jsonName = field.Name()
		}

		f := ir.Field{
			Name:     field.Name(),
			JSONName: jsonName,
			Required: !opts.contains("omitempty") && !opts.contains("omitzero"),
			Type:     a.resolveTypeRef(field.Type()),
			Tags:     parseStructTags(tag),
		}

		// Parse jsonschema tag for constraints and metadata.
		if jsTag := tag.Get("jsonschema"); jsTag != "" {
			f.Constraints, f.Description = parseConstraintsAndMeta(jsTag)
		}

		// Fall back to field comment if no description from tag.
		if f.Description == "" {
			f.Description = a.fieldDocs[name+"."+field.Name()]
		}

		irType.Fields = append(irType.Fields, f)
	}

	return irType
}

// convertUnion builds a KindUnion IR type from a sealed interface.
func (a *pkgAnalyzer) convertUnion(name string, iface *types.Interface) *ir.Type {
	irType := &ir.Type{
		Name:        name,
		Kind:        ir.KindUnion,
		Description: a.typeDoc(name),
	}

	// Find the unexported marker method (e.g. isShape).
	var markerName string
	for i := 0; i < iface.NumMethods(); i++ {
		m := iface.Method(i)
		if !m.Exported() {
			markerName = m.Name()
			break
		}
	}

	if markerName == "" {
		return irType
	}

	// Scan all types in the package for implementors.
	scope := a.pkg.Types.Scope()
	for _, scopeName := range scope.Names() {
		obj := scope.Lookup(scopeName)
		tn, ok := obj.(*types.TypeName)
		if !ok || scopeName == name || tn.IsAlias() {
			continue
		}

		// Check if *T implements the interface.
		ptr := types.NewPointer(tn.Type())
		if !types.Implements(ptr, iface) {
			continue
		}

		// Ensure the variant type is analyzed.
		a.ensureType(scopeName)

		v := ir.Variant{
			Name:    scopeName,
			TypeRef: ir.TypeRef{Name: scopeName},
		}

		// Try to find discriminator value from any field:
		// 1. const constraint in jsonschema tag
		// 2. field type is a single-value enum (e.g. ClickType with only "click")
		// 3. field type is a multi-value enum (fallback, only if 1+2 found nothing)
		//
		// When the discriminator is already established from a previous variant,
		// only check the field matching that discriminator name.
		if st, ok := tn.Type().Underlying().(*types.Struct); ok {
			// Pass 1: const constraints and single-value enums (strong signals).
			for i := 0; i < st.NumFields(); i++ {
				f := st.Field(i)
				tag := reflect.StructTag(st.Tag(i))
				jsonName, _ := parseJSONTag(tag.Get("json"))
				if jsonName == "" || jsonName == "-" {
					continue
				}
				// If discriminator already known, only check that field.
				if irType.Discriminator != "" && jsonName != irType.Discriminator {
					continue
				}

				// Check jsonschema:"const=..." tag.
				var discValue string
				if jsTag := tag.Get("jsonschema"); jsTag != "" {
					for _, c := range parseConstraints(jsTag) {
						if c.Keyword == "const" {
							discValue = fmt.Sprintf("%v", c.Value)
							break
						}
					}
				}

				// Check if field type is a single-value enum.
				if discValue == "" {
					discValue = singleEnumValue(f.Type(), a.pkg)
				}

				if discValue != "" {
					v.DiscriminatorValues = []string{discValue}
					if irType.Discriminator == "" {
						irType.Discriminator = jsonName
					}
					break
				}
			}

			// Pass 2: multi-value enums (weaker signal, fallback).
			if len(v.DiscriminatorValues) == 0 {
				for i := 0; i < st.NumFields(); i++ {
					f := st.Field(i)
					tag := reflect.StructTag(st.Tag(i))
					jsonName, _ := parseJSONTag(tag.Get("json"))
					if jsonName == "" || jsonName == "-" {
						continue
					}
					if irType.Discriminator != "" && jsonName != irType.Discriminator {
						continue
					}

					if vals := enumValues(f.Type(), a.pkg); len(vals) > 0 {
						v.DiscriminatorValues = vals
						if irType.Discriminator == "" {
							irType.Discriminator = jsonName
						}
						break
					}
				}
			}
		}

		irType.Variants = append(irType.Variants, v)
	}

	// Validate discriminator: all variant value sets must be disjoint.
	if irType.Discriminator != "" {
		if !variantValuesDisjoint(irType.Variants) {
			irType.Discriminator = ""
		}
	}

	return irType
}

// variantValuesDisjoint checks that no two variants share a discriminator value.
func variantValuesDisjoint(variants []ir.Variant) bool {
	seen := make(map[string]bool)
	for _, v := range variants {
		for _, val := range v.DiscriminatorValues {
			if seen[val] {
				return false
			}
			seen[val] = true
		}
	}
	return true
}

// convertEnum builds a KindEnum IR type.
func (a *pkgAnalyzer) convertEnum(name string, basic *types.Basic, vals []any) *ir.Type {
	return &ir.Type{
		Name:        name,
		Kind:        ir.KindEnum,
		EnumType:    basicToScalar(basic),
		EnumValues:  vals,
		Description: a.typeDoc(name),
	}
}

// resolveTypeRef converts a go/types.Type to an IR TypeRef.
func (a *pkgAnalyzer) resolveTypeRef(typ types.Type) ir.TypeRef {
	switch t := typ.(type) {
	case *types.Named:
		name := t.Obj().Name()
		// Cycle detection: if we're already resolving this type, emit a ref.
		if a.resolving[name] {
			return ir.TypeRef{Name: name}
		}
		a.resolving[name] = true
		defer func() { delete(a.resolving, name) }()

		tn := t.Obj()
		if tn.IsAlias() {
			// Type alias (type X = Y) — resolve to the alias target.
			// If the target is also a Named type, use its name as $ref.
			if target, ok := types.Unalias(t).(*types.Named); ok {
				targetName := target.Obj().Name()
				a.ensureType(targetName)
				if _, exists := a.irPkg.Types[targetName]; exists {
					return ir.TypeRef{Name: targetName}
				}
			}
			// Fallback: inline the underlying type.
			return a.resolveTypeRef(t.Underlying())
		}
		// Ensure transitively referenced types are analyzed.
		a.ensureType(name)
		// Use $ref regardless of whether the type was added.
		// (Even if it wasn't added, e.g. interface{}, this prevents
		// infinite recursion from self-referencing types.)
		return ir.TypeRef{Name: name}

	case *types.Pointer:
		inner := a.resolveTypeRef(t.Elem())
		return ir.TypeRef{
			Inline: &ir.Type{
				Kind:  ir.KindNullable,
				Inner: &inner,
			},
		}

	case *types.Slice:
		items := a.resolveTypeRef(t.Elem())
		return ir.TypeRef{
			Inline: &ir.Type{
				Kind:  ir.KindList,
				Items: &items,
			},
		}

	case *types.Map:
		if basic, ok := t.Key().(*types.Basic); ok && basic.Kind() == types.String {
			val := a.resolveTypeRef(t.Elem())
			return ir.TypeRef{
				Inline: &ir.Type{
					Kind:     ir.KindMap,
					MapValue: &val,
				},
			}
		}
		// Non-string key maps → treat as opaque.
		return ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "object"}}

	case *types.Basic:
		return ir.TypeRef{
			Inline: &ir.Type{
				Kind:       ir.KindScalar,
				ScalarType: basicToScalar(t),
			},
		}

	case *types.Interface:
		// interface{} / any — opaque, can't generate fixtures.
		return ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "any"}}

	case *types.Struct:
		// Anonymous struct — inline the fields.
		var fields []ir.Field
		for i := 0; i < t.NumFields(); i++ {
			f := t.Field(i)
			if !f.Exported() {
				continue
			}
			tag := reflect.StructTag(t.Tag(i))
			jsonTag := tag.Get("json")
			if jsonTag == "-" {
				continue
			}
			jsonName, opts := parseJSONTag(jsonTag)
			if jsonName == "" {
				jsonName = f.Name()
			}
			field := ir.Field{
				Name:     f.Name(),
				JSONName: jsonName,
				Required: !opts.contains("omitempty") && !opts.contains("omitzero"),
				Type:     a.resolveTypeRef(f.Type()),
			}
			if jsTag := tag.Get("jsonschema"); jsTag != "" {
				field.Constraints, field.Description = parseConstraintsAndMeta(jsTag)
			}
			fields = append(fields, field)
		}
		return ir.TypeRef{
			Inline: &ir.Type{
				Kind:   ir.KindStruct,
				Fields: fields,
			},
		}

	default:
		return ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "any"}}
	}
}

// typeDoc extracts the doc comment for a named type.
func (a *pkgAnalyzer) typeDoc(name string) string {
	for _, file := range a.pkg.Syntax {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Doc == nil {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if ts.Name.Name == name {
					text := gd.Doc.Text()
					// Strip the "TypeName " prefix that godoc conventions add.
					text = strings.TrimPrefix(text, name+" ")
					text = strings.TrimPrefix(text, name+"\n")
					return strings.TrimSpace(text)
				}
			}
		}
	}
	return ""
}

// buildEnumMap collects const blocks that define enum values for named types.
// buildFieldDocMap extracts doc comments for struct fields.
// Key format: "TypeName.FieldName" → comment text.
func buildFieldDocMap(pkg *packages.Package) map[string]string {
	result := make(map[string]string)
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range st.Fields.List {
					if field.Doc == nil || len(field.Names) == 0 {
						continue
					}
					for _, name := range field.Names {
						doc := strings.TrimSpace(field.Doc.Text())
						if doc != "" {
							result[ts.Name.Name+"."+name.Name] = doc
						}
					}
				}
			}
		}
	}
	return result
}
func buildEnumMap(pkg *packages.Package) map[string][]any {
	result := make(map[string][]any)
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, vName := range vs.Names {
					obj := pkg.TypesInfo.ObjectOf(vName)
					if obj == nil {
						continue
					}
					c, ok := obj.(*types.Const)
					if !ok {
						continue
					}
					named, ok := c.Type().(*types.Named)
					if !ok {
						continue
					}
					typeName := named.Obj().Name()
					val := c.Val()
					switch val.Kind() {
					case constant.String:
						s := constant.StringVal(val)
						result[typeName] = append(result[typeName], s)
					case constant.Int:
						if i, ok := constant.Int64Val(val); ok {
							result[typeName] = append(result[typeName], i)
						}
					}
				}
			}
		}
	}
	return result
}

// parseJSONTag splits a json tag into name and options.
func parseJSONTag(tag string) (string, tagOptions) {
	if idx := strings.Index(tag, ","); idx != -1 {
		return tag[:idx], tagOptions(tag[idx+1:])
	}
	return tag, ""
}

type tagOptions string

func (o tagOptions) contains(name string) bool {
	for o != "" {
		var next string
		i := strings.Index(string(o), ",")
		if i >= 0 {
			next = string(o[i+1:])
			o = o[:i]
		} else {
			next = ""
		}
		if string(o) == name {
			return true
		}
		o = tagOptions(next)
	}
	return false
}

// parseStructTags extracts all key:"value" pairs from a reflect.StructTag.
// Returns a map like {"json": "bar,omitempty", "yaml": "baz", "mapstructure": "bar"}.
// The jsonschema tag is excluded (handled separately as constraints).
func parseStructTags(tag reflect.StructTag) map[string]string {
	tags := make(map[string]string)
	raw := string(tag)
	for raw != "" {
		raw = strings.TrimLeft(raw, " ")
		if raw == "" {
			break
		}
		// Find key.
		i := strings.IndexByte(raw, ':')
		if i < 0 {
			break
		}
		key := raw[:i]
		raw = raw[i+1:]
		// Find quoted value.
		if len(raw) == 0 || raw[0] != '"' {
			break
		}
		raw = raw[1:]
		j := strings.IndexByte(raw, '"')
		if j < 0 {
			break
		}
		value := raw[:j]
		raw = raw[j+1:]
		if key != "jsonschema" {
			tags[key] = value
		}
	}
	return tags
}

// Metadata keywords that go to Field.Description / Type properties, not JSON Schema constraints.

// parseConstraintsAndMeta parses a jsonschema:"..." tag into constraints and
// a description string. Compatible with invopop/jsonschema tag format.
//
// Supported keywords:
//   - Numeric: minimum, maximum, exclusiveMinimum, exclusiveMaximum, multipleOf
//   - String: minLength, maxLength, pattern, format
//   - Array: minItems, maxItems, uniqueItems
//   - Object: minProperties, maxProperties
//   - Composition: const, enum (comma-separated in value)
//   - Metadata: title, description, default, examples
//   - Boolean flags: required, readOnly, writeOnly, deprecated
func parseConstraintsAndMeta(tag string) ([]ir.Constraint, string) {
	var constraints []ir.Constraint
	var description string

	// Handle description= specially since it can contain commas.
	// Format: "key1=val1,key2=val2,description=rest of the string"
	descPrefix := "description="
	if idx := strings.Index(tag, descPrefix); idx >= 0 {
		description = tag[idx+len(descPrefix):]
		tag = strings.TrimRight(tag[:idx], ",")
	}

	for _, part := range strings.Split(tag, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			// Boolean keywords.
			switch part {
			case "required":
				// invopop compat: marks field as required (we already handle via omitempty)
				continue
			case "readOnly", "writeOnly", "deprecated", "uniqueItems":
				constraints = append(constraints, ir.Constraint{Keyword: part, Value: true})
			default:
				constraints = append(constraints, ir.Constraint{Keyword: part, Value: true})
			}
			continue
		}

		keyword := part[:eq]
		valueStr := part[eq+1:]

		// Handle metadata keywords separately.
		switch keyword {
		case "description":
			// Should already be handled above, but catch any edge case.
			if description == "" {
				description = valueStr
			}
			continue
		case "title":
			constraints = append(constraints, ir.Constraint{Keyword: "title", Value: valueStr})
			continue
		case "format":
			constraints = append(constraints, ir.Constraint{Keyword: "format", Value: valueStr})
			continue
		case "default":
			constraints = append(constraints, ir.Constraint{Keyword: "default", Value: parseTagValue(valueStr)})
			continue
		case "examples":
			// examples can be a single value; invopop uses multiple examples tags
			constraints = append(constraints, ir.Constraint{Keyword: "examples", Value: []any{parseTagValue(valueStr)}})
			continue
		}

		constraints = append(constraints, ir.Constraint{Keyword: keyword, Value: parseTagValue(valueStr)})
	}

	return constraints, description
}

// parseConstraints is a backward-compatible wrapper that ignores metadata.
func parseConstraints(tag string) []ir.Constraint {
	constraints, _ := parseConstraintsAndMeta(tag)
	return constraints
}

// parseTagValue converts a string value from a struct tag into the appropriate Go type.
func parseTagValue(s string) any {
	// Try numeric.
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		if f == float64(int64(f)) {
			return int64(f)
		}
		return f
	}
	// Try boolean.
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	return s
}

// enumValues returns all const values for a named enum type.
// Returns nil if the type is not a named string/integer type with const values.
func enumValues(t types.Type, pkg *packages.Package) []string {
	named, ok := t.(*types.Named)
	if !ok {
		return nil
	}
	basic, ok := named.Underlying().(*types.Basic)
	if !ok {
		return nil
	}
	if basic.Info()&(types.IsString|types.IsInteger) == 0 {
		return nil
	}

	var values []string
	scope := pkg.Types.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		c, ok := obj.(*types.Const)
		if !ok {
			continue
		}
		if !types.Identical(c.Type(), t) {
			continue
		}
		val := c.Val().ExactString()
		if len(val) >= 2 && val[0] == '"' {
			val = val[1 : len(val)-1]
		}
		values = append(values, val)
	}
	return values
}

// singleEnumValue returns the const value if the type has exactly one.
func singleEnumValue(t types.Type, pkg *packages.Package) string {
	vals := enumValues(t, pkg)
	if len(vals) == 1 {
		return vals[0]
	}
	return ""
}

func basicToScalar(b *types.Basic) string {
	switch {
	case b.Info()&types.IsString != 0:
		return "string"
	case b.Info()&types.IsBoolean != 0:
		return "boolean"
	case b.Info()&types.IsFloat != 0:
		return "number"
	case b.Info()&types.IsInteger != 0:
		// Preserve specific Go integer type for codegen fidelity.
		switch b.Kind() {
		case types.Int:
			return "int"
		case types.Int64:
			return "int64"
		case types.Int32:
			return "int32"
		case types.Int16:
			return "int16"
		case types.Int8:
			return "int8"
		default:
			return "integer"
		}
	default:
		return "string"
	}
}
