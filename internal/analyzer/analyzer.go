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
	"reflect"
	"strconv"
	"strings"

	"github.com/codewandler/compschema/internal/ir"
	"golang.org/x/tools/go/packages"
)

// Analyze loads and analyzes the given Go package patterns,
// returning an IR Package for each Go package that contains
// annotated types.
func Analyze(patterns ...string) ([]*ir.Package, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName |
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
		irPkg := analyzePackage(pkg)
		if irPkg != nil && len(irPkg.Types) > 0 {
			result = append(result, irPkg)
		}
	}
	return result, nil
}

func analyzePackage(pkg *packages.Package) *ir.Package {
	a := &pkgAnalyzer{
		pkg:     pkg,
		irPkg:   ir.NewPackage(pkg.Name, pkg.PkgPath),
		seen:    make(map[string]bool),
		enumMap: buildEnumMap(pkg),
	}

	// Find annotated types.
	roots := a.findAnnotatedTypes()
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
	pkg     *packages.Package
	irPkg   *ir.Package
	seen    map[string]bool
	enumMap map[string][]any // type name → const values
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

	t := a.convertType(name, tn.Type())
	if t != nil {
		a.irPkg.Add(t)
	}
}

// convertType converts a go/types.Type into an IR Type.
func (a *pkgAnalyzer) convertType(name string, typ types.Type) *ir.Type {
	switch t := typ.Underlying().(type) {
	case *types.Struct:
		return a.convertStruct(name, typ, t)
	case *types.Interface:
		return a.convertUnion(name, t)
	case *types.Basic:
		// Check if it's an enum.
		if vals, ok := a.enumMap[name]; ok {
			return a.convertEnum(name, t, vals)
		}
		// Named scalar type (type alias).
		return &ir.Type{
			Name:       name,
			Kind:       ir.KindScalar,
			ScalarType: basicToScalar(t),
		}
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
		}

		// Parse jsonschema tag constraints.
		if jsTag := tag.Get("jsonschema"); jsTag != "" {
			f.Constraints = parseConstraints(jsTag)
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
		if !ok || scopeName == name {
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

		// Try to find discriminator const value from the variant's "type" field.
		if st, ok := tn.Type().Underlying().(*types.Struct); ok {
			for i := 0; i < st.NumFields(); i++ {
				f := st.Field(i)
				tag := reflect.StructTag(st.Tag(i))
				jsonName, _ := parseJSONTag(tag.Get("json"))
				if jsonName == "type" {
					if jsTag := tag.Get("jsonschema"); jsTag != "" {
						for _, c := range parseConstraints(jsTag) {
							if c.Keyword == "const" {
								v.Discriminator = fmt.Sprintf("%v", c.Value)
								irType.Discriminator = "type"
							}
						}
					}
				}
				_ = f
			}
		}

		irType.Variants = append(irType.Variants, v)
	}

	return irType
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
		// Ensure transitively referenced types are analyzed.
		a.ensureType(name)
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

	default:
		return ir.TypeRef{Inline: &ir.Type{Kind: ir.KindScalar, ScalarType: "string"}}
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

// parseConstraints parses a jsonschema:"..." tag value into Constraints.
func parseConstraints(tag string) []ir.Constraint {
	var constraints []ir.Constraint
	for _, part := range strings.Split(tag, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			// Boolean keywords like "required"
			constraints = append(constraints, ir.Constraint{Keyword: part, Value: true})
			continue
		}
		keyword := part[:eq]
		valueStr := part[eq+1:]

		var value any
		// Try numeric first.
		if f, err := strconv.ParseFloat(valueStr, 64); err == nil {
			// Use int if it's a whole number.
			if f == float64(int64(f)) {
				value = int64(f)
			} else {
				value = f
			}
		} else if b, err := strconv.ParseBool(valueStr); err == nil {
			value = b
		} else {
			value = valueStr
		}

		constraints = append(constraints, ir.Constraint{Keyword: keyword, Value: value})
	}
	return constraints
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
		return "integer"
	default:
		return "string"
	}
}
