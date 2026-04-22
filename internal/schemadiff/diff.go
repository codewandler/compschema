// Package schemadiff structurally compares two JSON Schema documents
// and produces a completeness report.
//
// It classifies mismatches into:
//   - Structural gaps: missing oneOf, wrong type, missing properties
//   - Annotation gaps: missing description, default, examples
//   - Naming artifacts: types present under different names
package schemadiff

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Report is the result of comparing two JSON Schema documents.
type Report struct {
	GroundTruthDefs int
	GeneratedDefs   int
	MatchedDefs     int

	// Per-def comparison results for matched defs.
	Defs []DefReport

	// Defs only in ground truth.
	MissingDefs []string
	// Defs only in generated (extra types from Go codegen).
	ExtraDefs []string

	// Aggregate stats.
	TotalKeywords   int
	MatchedKeywords int
	MissingKeywords int
	ExtraKeywords   int
}

// DefReport is the comparison result for a single $def.
type DefReport struct {
	Name       string
	Matches    []string // keywords that match
	Missing    []string // keywords in GT but not in ours
	Extra      []string // keywords in ours but not in GT
	Mismatches []Mismatch
}

// Mismatch is a single keyword where both schemas have it but values differ.
type Mismatch struct {
	Path     string
	Keyword  string
	Expected any
	Got      any
	Category string // "structural", "annotation", "value"
}

// structural keywords that affect validation behavior
var structuralKeywords = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true,
	"items": true, "oneOf": true, "anyOf": true, "allOf": true, "not": true,
	"enum": true, "const": true, "minimum": true, "maximum": true,
	"exclusiveMinimum": true, "exclusiveMaximum": true, "multipleOf": true,
	"minLength": true, "maxLength": true, "pattern": true,
	"minItems": true, "maxItems": true, "uniqueItems": true,
	"minProperties": true, "maxProperties": true,
	"if": true, "then": true, "else": true,
	"dependentSchemas": true, "dependentRequired": true,
	"patternProperties": true, "propertyNames": true,
	"contains": true, "minContains": true, "maxContains": true,
	"prefixItems": true, "unevaluatedItems": true, "unevaluatedProperties": true,
	"$ref": true, "format": true,
	"contentEncoding": true, "contentMediaType": true,
	"discriminator": true, "x-discriminator": true,
}

// annotation keywords that don't affect validation
var annotationKeywords = map[string]bool{
	"title": true, "description": true, "default": true,
	"examples": true, "deprecated": true, "readOnly": true,
	"writeOnly": true, "$comment": true, "$id": true,
	"$anchor": true, "$schema": true,
}

// Compare loads two JSON Schema files and produces a diff report.
func Compare(groundTruthPath, generatedPath string) (*Report, error) {
	gt, err := loadDefs(groundTruthPath)
	if err != nil {
		return nil, fmt.Errorf("load ground truth: %w", err)
	}
	gen, err := loadDefs(generatedPath)
	if err != nil {
		return nil, fmt.Errorf("load generated: %w", err)
	}

	r := &Report{
		GroundTruthDefs: len(gt),
		GeneratedDefs:   len(gen),
	}

	// Find matched, missing, extra defs.
	gtNames := sortedKeys(gt)
	genNames := sortedKeys(gen)
	gtSet := toSet(gtNames)
	genSet := toSet(genNames)

	for _, name := range gtNames {
		if _, ok := genSet[name]; ok {
			r.MatchedDefs++
			dr := compareDef(name, gt[name], gen[name])
			r.Defs = append(r.Defs, dr)
			r.TotalKeywords += len(dr.Matches) + len(dr.Missing) + len(dr.Mismatches)
			r.MatchedKeywords += len(dr.Matches)
			r.MissingKeywords += len(dr.Missing)
			r.ExtraKeywords += len(dr.Extra)
		} else {
			r.MissingDefs = append(r.MissingDefs, name)
		}
	}
	for _, name := range genNames {
		if _, ok := gtSet[name]; !ok {
			r.ExtraDefs = append(r.ExtraDefs, name)
		}
	}

	return r, nil
}

func compareDef(name string, gt, gen map[string]any) DefReport {
	dr := DefReport{Name: name}

	allKeys := map[string]bool{}
	for k := range gt {
		allKeys[k] = true
	}
	for k := range gen {
		allKeys[k] = true
	}

	for k := range allKeys {
		// Skip x-* extensions in comparison (OpenAPI artifacts).
		if strings.HasPrefix(k, "x-") {
			continue
		}

		gtVal, inGT := gt[k]
		genVal, inGen := gen[k]

		if inGT && !inGen {
			dr.Missing = append(dr.Missing, k)
		} else if !inGT && inGen {
			dr.Extra = append(dr.Extra, k)
		} else {
			// Both present — compare values.
			match := valuesEqual(gtVal, genVal)
			// For annotation strings, use whitespace-normalized comparison.
			if !match && annotationKeywords[k] {
				if gs, ok1 := gtVal.(string); ok1 {
					if os, ok2 := genVal.(string); ok2 {
						match = normalizeWS(gs) == normalizeWS(os)
					}
				}
			}
			if match {
				dr.Matches = append(dr.Matches, k)
			} else {
				cat := "structural"
				if annotationKeywords[k] {
					cat = "annotation"
				}
				dr.Mismatches = append(dr.Mismatches, Mismatch{
					Path:     name,
					Keyword:  k,
					Expected: gtVal,
					Got:      genVal,
					Category: cat,
				})
			}
		}
	}

	sort.Strings(dr.Matches)
	sort.Strings(dr.Missing)
	sort.Strings(dr.Extra)
	sort.Slice(dr.Mismatches, func(i, j int) bool {
		return dr.Mismatches[i].Keyword < dr.Mismatches[j].Keyword
	})

	return dr
}

// orderInsensitiveKeys are JSON Schema keywords where array order doesn't matter.
var orderInsensitiveKeys = map[string]bool{
	"required": true, "enum": true, "oneOf": true, "anyOf": true, "allOf": true,
}

func valuesEqual(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) == string(jb) {
		return true
	}
	// For arrays, try order-insensitive comparison.
	var aa, bb []json.RawMessage
	if json.Unmarshal(ja, &aa) == nil && json.Unmarshal(jb, &bb) == nil {
		if len(aa) == len(bb) {
			return sameElementsJSON(aa, bb)
		}
	}
	return false
}

func sameElementsJSON(a, b []json.RawMessage) bool {
	used := make([]bool, len(b))
	for _, av := range a {
		found := false
		for j, bv := range b {
			if !used[j] && string(av) == string(bv) {
				used[j] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// FormatReport produces a human-readable report string.
func FormatReport(r *Report) string {
	var b strings.Builder

	b.WriteString("╔══════════════════════════════════════════════════╗\n")
	b.WriteString("║           JSON Schema Diff Report                ║\n")
	b.WriteString("╠══════════════════════════════════════════════════╣\n")
	fmt.Fprintf(&b, "║  Ground truth $defs:  %-4d                       ║\n", r.GroundTruthDefs)
	fmt.Fprintf(&b, "║  Generated $defs:     %-4d                       ║\n", r.GeneratedDefs)
	fmt.Fprintf(&b, "║  Matched by name:     %-4d                       ║\n", r.MatchedDefs)
	fmt.Fprintf(&b, "║  Missing (in GT only): %-4d                      ║\n", len(r.MissingDefs))
	fmt.Fprintf(&b, "║  Extra (in ours only): %-4d                      ║\n", len(r.ExtraDefs))
	b.WriteString("╠══════════════════════════════════════════════════╣\n")

	// Keyword-level stats.
	total := r.MatchedKeywords + r.MissingKeywords + len(collectMismatches(r, "structural"))
	structMatch := r.MatchedKeywords
	structMissing := 0
	structMismatch := 0
	annoMissing := 0
	annoMismatch := 0
	for _, d := range r.Defs {
		for _, k := range d.Missing {
			if structuralKeywords[k] {
				structMissing++
			} else if annotationKeywords[k] {
				annoMissing++
			}
		}
		for _, m := range d.Mismatches {
			if m.Category == "structural" {
				structMismatch++
			} else {
				annoMismatch++
			}
		}
	}

	pct := float64(0)
	if total > 0 {
		pct = float64(structMatch) / float64(total) * 100
	}
	fmt.Fprintf(&b, "║  Keyword match rate:  %.1f%%                      ║\n", pct)
	fmt.Fprintf(&b, "║    Matched:           %-4d                       ║\n", structMatch)
	fmt.Fprintf(&b, "║    Structural gaps:   %-4d                       ║\n", structMissing+structMismatch)
	fmt.Fprintf(&b, "║    Annotation gaps:   %-4d                       ║\n", annoMissing+annoMismatch)
	b.WriteString("╚══════════════════════════════════════════════════╝\n")

	// Missing defs.
	if len(r.MissingDefs) > 0 {
		b.WriteString("\n── Missing $defs (in ground truth, not in generated) ──\n")
		for _, name := range r.MissingDefs {
			fmt.Fprintf(&b, "  ✗ %s\n", name)
		}
	}

	// Per-def details for defs with issues.
	hasIssues := false
	for _, d := range r.Defs {
		if len(d.Missing) > 0 || len(d.Mismatches) > 0 {
			if !hasIssues {
				b.WriteString("\n── Per-type issues ──\n")
				hasIssues = true
			}
			fmt.Fprintf(&b, "\n  %s:\n", d.Name)
			for _, k := range d.Missing {
				cat := "structural"
				if annotationKeywords[k] {
					cat = "annotation"
				}
				fmt.Fprintf(&b, "    missing %-25s [%s]\n", k, cat)
			}
			for _, m := range d.Mismatches {
				exp := truncate(fmt.Sprintf("%v", m.Expected), 40)
				got := truncate(fmt.Sprintf("%v", m.Got), 40)
				fmt.Fprintf(&b, "    differ  %-25s [%s] expected=%s got=%s\n",
					m.Keyword, m.Category, exp, got)
			}
		}
	}

	return b.String()
}

func collectMismatches(r *Report, category string) []Mismatch {
	var result []Mismatch
	for _, d := range r.Defs {
		for _, m := range d.Mismatches {
			if m.Category == category {
				result = append(result, m)
			}
		}
	}
	return result
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func loadDefs(path string) (map[string]map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Defs map[string]map[string]any `json:"$defs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Defs == nil {
		return nil, fmt.Errorf("no $defs found")
	}
	return doc.Defs, nil
}

func sortedKeys(m map[string]map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func toSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}

func normalizeWS(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}
