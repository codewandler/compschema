package importer

import (
	"path/filepath"
	"strings"

	"github.com/codewandler/compschema/internal/ir"
	"github.com/codewandler/compschema/internal/uniongen"
)

// Config controls the import pipeline.
type Config struct {
	Package   string            // Go package name
	Rename    map[string]string // schema name → Go name overrides (e.g. "CompactionBody" → "CompactionItem")
	Exclude   []string          // glob patterns for type names to skip (e.g. "Response*Event", "*Param")
	Tags      []string          // additional struct tags to emit (e.g. ["yaml"] → adds `yaml:"field_name"` tags)
	Implement []ImplementRule   // accessor methods to generate on union variants
}

// ImplementRule configures accessor method generation for a union's variants.
type ImplementRule struct {
	Union               string // union type name (schema name or Go name)
	DiscriminatorMethod string // method name for the discriminator accessor (default: "DiscriminatorValue")
}

// ApplyConfig transforms an IR package according to the config:
// renames types, excludes matched patterns, etc.
func ApplyConfig(pkg *ir.Package, cfg Config) {
	if cfg.Package != "" {
		pkg.Name = cfg.Package
	}
	if len(cfg.Exclude) > 0 {
		applyExcludes(pkg, cfg.Exclude)
	}
	if len(cfg.Rename) > 0 {
		applyRenames(pkg, cfg.Rename)
	}
}

// applyExcludes removes types matching any glob pattern from the package.
// References to excluded types are kept by name (the user provides them).
func applyExcludes(pkg *ir.Package, patterns []string) {
	for name := range pkg.Types {
		if matchesAny(name, patterns) || matchesAny(toGoName(name), patterns) {
			delete(pkg.Types, name)
		}
	}
	// Also filter Order.
	filtered := pkg.Order[:0]
	for _, name := range pkg.Order {
		if _, ok := pkg.Types[name]; ok {
			filtered = append(filtered, name)
		}
	}
	pkg.Order = filtered
}

// applyRenames renames types in the IR. Both the schema-level name (key)
// and the Go-level name can be used as the "from" side:
//
//	--rename CompactionBody=CompactionItem     (schema name)
//	--rename UrlCitationBody=URLCitationAnnotation  (schema name, Go casing applied after)
//
// The rename is applied to:
//   - The type map key
//   - All TypeRef.Name references across the package
//   - Variant names in unions
//   - The type's own Name field
func applyRenames(pkg *ir.Package, renames map[string]string) {
	// Build lookup: both schema name and Go name can be the "from" key.
	// The "to" value is used as-is (already the desired Go name).
	schemaRenames := make(map[string]string) // schema name → new schema name
	for from, to := range renames {
		// Check if "from" matches a schema-level name directly.
		if _, ok := pkg.Types[from]; ok {
			schemaRenames[from] = to
			continue
		}
		// Check if "from" matches the Go name of any type.
		for schemaName := range pkg.Types {
			if toGoName(schemaName) == from {
				schemaRenames[schemaName] = to
				break
			}
		}
	}

	if len(schemaRenames) == 0 {
		return
	}

	// Rename type keys.
	for oldName, newName := range schemaRenames {
		t, ok := pkg.Types[oldName]
		if !ok {
			continue
		}
		t.Name = newName
		delete(pkg.Types, oldName)
		pkg.Types[newName] = t
	}

	// Rename in Order.
	for i, name := range pkg.Order {
		if newName, ok := schemaRenames[name]; ok {
			pkg.Order[i] = newName
		}
	}

	// Rename all TypeRef.Name references across the package.
	for _, t := range pkg.Types {
		renameTypeRefs(t, schemaRenames)
	}
}

func renameTypeRefs(t *ir.Type, renames map[string]string) {
	// Fields (structs).
	for i := range t.Fields {
		renameRef(&t.Fields[i].Type, renames)
	}
	// Variants (unions).
	for i := range t.Variants {
		if newName, ok := renames[t.Variants[i].Name]; ok {
			t.Variants[i].Name = newName
		}
		renameRef(&t.Variants[i].TypeRef, renames)
	}
	// Items (lists).
	if t.Items != nil {
		renameRef(t.Items, renames)
	}
	// MapValue (maps).
	if t.MapValue != nil {
		renameRef(t.MapValue, renames)
	}
	// Inner (nullable).
	if t.Inner != nil {
		renameRef(t.Inner, renames)
	}
}

func renameRef(ref *ir.TypeRef, renames map[string]string) {
	if ref == nil {
		return
	}
	if newName, ok := renames[ref.Name]; ok {
		ref.Name = newName
	}
	if ref.Inline != nil {
		renameTypeRefs(ref.Inline, renames)
	}
}

// matchesAny checks if name matches any of the glob patterns.
func matchesAny(name string, patterns []string) bool {
	for _, p := range patterns {
		// Support simple prefix/suffix globs and exact match.
		if matched, _ := filepath.Match(p, name); matched {
			return true
		}
		// Also try case-insensitive for convenience.
		if matched, _ := filepath.Match(strings.ToLower(p), strings.ToLower(name)); matched {
			return true
		}
	}
	return false
}

// resolveAccessors builds UnionAccessor values for a union type based on
// the implement config rules. If no explicit rule matches but the union
// has a discriminator, a default accessor is generated.
func resolveAccessors(goName, schemaName string, t *ir.Type, rules []ImplementRule) []uniongen.UnionAccessor {
	if t.Discriminator == "" {
		return nil // only discriminated unions get accessors
	}

	methodName := "DiscriminatorValue" // default
	for _, rule := range rules {
		if rule.Union == schemaName || rule.Union == goName || rule.Union == toGoName(schemaName) {
			if rule.DiscriminatorMethod != "" {
				methodName = rule.DiscriminatorMethod
			}
			break
		}
	}

	return []uniongen.UnionAccessor{{
		Method:     methodName,
		Field:      t.Discriminator,
		ReturnType: "string",
	}}
}
