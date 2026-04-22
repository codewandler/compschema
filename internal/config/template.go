package config

import "strings"

// ExpandTemplate replaces {name}, {source}, {hash} placeholders in a string.
func ExpandTemplate(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

// ExpandAction returns a copy of the Action with all string fields expanded.
func ExpandAction(a Action, vars map[string]string) Action {
	out := a
	out.Spec = ExpandTemplate(a.Spec, vars)
	out.Path = ExpandTemplate(a.Path, vars)
	out.Schema = ExpandTemplate(a.Schema, vars)
	out.Package = ExpandTemplate(a.Package, vars)
	out.Out = ExpandTemplate(a.Out, vars)

	// Expand Packages slice.
	if len(a.Packages) > 0 {
		out.Packages = make([]string, len(a.Packages))
		for i, p := range a.Packages {
			out.Packages[i] = ExpandTemplate(p, vars)
		}
	}

	// Expand Rename keys/values.
	if len(a.Rename) > 0 {
		out.Rename = make(map[string]string, len(a.Rename))
		for k, v := range a.Rename {
			out.Rename[ExpandTemplate(k, vars)] = ExpandTemplate(v, vars)
		}
	}

	// Expand Exclude patterns.
	if len(a.Exclude) > 0 {
		out.Exclude = make([]string, len(a.Exclude))
		for i, e := range a.Exclude {
			out.Exclude[i] = ExpandTemplate(e, vars)
		}
	}

	// Note: Source is not expanded here — it's resolved before templating.

	return out
}
