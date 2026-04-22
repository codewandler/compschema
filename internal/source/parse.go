package source

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// Parse converts a raw source value (from YAML config) into one or more Sources.
//
// Accepted formats:
//   - string starting with http:// or https:// → HTTPSource
//   - string → FileSource
//   - map with "git" key → GitSource
//   - []any → multiple sources (recursive)
func Parse(v any) ([]Source, error) {
	switch val := v.(type) {
	case string:
		return []Source{parseString(val)}, nil

	case map[string]any:
		src, err := parseMap(val)
		if err != nil {
			return nil, err
		}
		return []Source{src}, nil

	case []any:
		var sources []Source
		for i, item := range val {
			parsed, err := Parse(item)
			if err != nil {
				return nil, fmt.Errorf("source[%d]: %w", i, err)
			}
			sources = append(sources, parsed...)
		}
		return sources, nil

	default:
		return nil, fmt.Errorf("unsupported source type: %T", v)
	}
}

func parseString(s string) Source {
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return &HTTPSource{URL: s}
	}
	return &FileSource{Path: s}
}

func parseMap(m map[string]any) (Source, error) {
	if gitURL, ok := m["git"]; ok {
		src := &GitSource{
			Repo: fmt.Sprint(gitURL),
		}
		if ref, ok := m["ref"]; ok {
			src.Ref = fmt.Sprint(ref)
		}
		if path, ok := m["path"]; ok {
			src.Path = fmt.Sprint(path)
		}
		return src, nil
	}
	return nil, fmt.Errorf("unrecognized source map (expected 'git' key): %v", m)
}

// Name derives a short name from a Source, suitable for {name} template variable.
func Name(src Source) string {
	switch s := src.(type) {
	case *FileSource:
		return nameFromPath(s.Path)
	case *HTTPSource:
		u, err := url.Parse(s.URL)
		if err != nil {
			return "unknown"
		}
		return nameFromPath(u.Path)
	case *GitSource:
		if s.Path != "" {
			return nameFromPath(s.Path)
		}
		// Use repo name.
		u, err := url.Parse(s.Repo)
		if err != nil {
			return "unknown"
		}
		name := nameFromPath(u.Path)
		name = strings.TrimSuffix(name, ".git")
		return name
	default:
		return "unknown"
	}
}

func nameFromPath(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	if ext != "" {
		base = base[:len(base)-len(ext)]
	}
	return base
}
