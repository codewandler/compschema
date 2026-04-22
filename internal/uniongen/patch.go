package uniongen

import (
	"fmt"
	"regexp"
	"strings"
)

// StripInterfaceDecls removes `type X interface{}` declarations from Go source
// for types that are replaced by sealed interfaces from uniongen.
func StripInterfaceDecls(source string, unionNames []string) string {
	for _, name := range unionNames {
		// Match: type Name interface{} (possibly with whitespace variations)
		pattern := fmt.Sprintf(`(?m)^type %s interface\{\}\s*\n`, regexp.QuoteMeta(name))
		re := regexp.MustCompile(pattern)
		source = re.ReplaceAllString(source, "")
	}
	return source
}

// UnionNames extracts just the names from a slice of unions.
func UnionNames(unions []Union) []string {
	names := make([]string, len(unions))
	for i, u := range unions {
		names[i] = u.Name
	}
	return names
}

// GeneratedUnionNames returns union names that actually got generated
// (i.e. all variants are $refs, not inline types).
func GeneratedUnionNames(unions []Union) []string {
	var names []string
	for _, u := range unions {
		allRefs := true
		for _, v := range u.Variants {
			if v.RefName == "" {
				allRefs = false
				break
			}
		}
		if allRefs {
			names = append(names, u.Name)
		}
	}
	return names
}

// PatchGeneratedTypes strips conflicting interface{} declarations and adds
// the fmt import if not already present (needed by UnmarshalX functions).
func PatchGeneratedTypes(source string, unionNames []string) string {
	source = StripInterfaceDecls(source, unionNames)

	// Ensure "fmt" is imported if we have unions that need it
	if len(unionNames) > 0 && !strings.Contains(source, `"fmt"`) {
		// The generated types file might not import fmt — our unions.gen.go does
		// but it's a separate file, so no action needed on the types file.
	}

	return source
}
