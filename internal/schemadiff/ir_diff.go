package schemadiff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/codewandler/compschema/internal/ir"
	"github.com/codewandler/compschema/internal/jsonschema2ir"
)

// IRReport is the result of an IR-level comparison.
type IRReport struct {
	GroundTruthTypes int
	GeneratedTypes   int
	MatchedTypes     int
	MissingTypes     []string // in GT only
	ExtraTypes       []string // in ours only

	// Per-type results for matched types.
	TypeReports []IRTypeReport

	// Aggregates.
	FieldsTotal   int
	FieldsMatch   int
	FieldsMissing int
	FieldsExtra   int
	FieldsDiffer  int
}

type IRTypeReport struct {
	Name          string
	KindMatch     bool
	KindGT        string
	KindGen       string
	FieldsMatch   int
	FieldsMissing []string
	FieldsExtra   []string
	FieldsDiffer  []string // field names where type/constraints differ
}

// CompareIR parses both JSON Schema files into IR and compares structurally.
func CompareIR(groundTruthPath, generatedPath string) (*IRReport, error) {
	gtPkg, err := jsonschema2ir.Parse(groundTruthPath)
	if err != nil {
		return nil, fmt.Errorf("parse ground truth: %w", err)
	}
	genPkg, err := jsonschema2ir.Parse(generatedPath)
	if err != nil {
		return nil, fmt.Errorf("parse generated: %w", err)
	}

	return compareIRPackages(gtPkg, genPkg), nil
}

func compareIRPackages(gt, gen *ir.Package) *IRReport {
	r := &IRReport{
		GroundTruthTypes: len(gt.Types),
		GeneratedTypes:   len(gen.Types),
	}

	gtNames := make(map[string]bool)
	for _, n := range gt.Order {
		gtNames[n] = true
	}
	genNames := make(map[string]bool)
	for _, n := range gen.Order {
		genNames[n] = true
	}

	for _, n := range gt.Order {
		if genNames[n] {
			r.MatchedTypes++
			tr := compareIRTypes(n, gt.Types[n], gen.Types[n])
			r.TypeReports = append(r.TypeReports, tr)
			r.FieldsMatch += tr.FieldsMatch
			r.FieldsMissing += len(tr.FieldsMissing)
			r.FieldsExtra += len(tr.FieldsExtra)
			r.FieldsDiffer += len(tr.FieldsDiffer)
			r.FieldsTotal += tr.FieldsMatch + len(tr.FieldsMissing) + len(tr.FieldsDiffer)
		} else {
			r.MissingTypes = append(r.MissingTypes, n)
		}
	}
	for _, n := range gen.Order {
		if !gtNames[n] {
			r.ExtraTypes = append(r.ExtraTypes, n)
		}
	}

	sort.Strings(r.MissingTypes)
	sort.Strings(r.ExtraTypes)

	return r
}

func compareIRTypes(name string, gt, gen *ir.Type) IRTypeReport {
	tr := IRTypeReport{
		Name:     name,
		KindGT:   kindName(gt.Kind),
		KindGen:  kindName(gen.Kind),
		KindMatch: gt.Kind == gen.Kind,
	}

	// For structs, compare fields.
	if gt.Kind == ir.KindStruct && gen.Kind == ir.KindStruct {
		gtFields := make(map[string]ir.Field)
		for _, f := range gt.Fields {
			gtFields[f.JSONName] = f
		}
		genFields := make(map[string]ir.Field)
		for _, f := range gen.Fields {
			genFields[f.JSONName] = f
		}

		for fname, gf := range gtFields {
			if of, ok := genFields[fname]; ok {
				if fieldsStructurallyEqual(gf, of) {
					tr.FieldsMatch++
				} else {
					tr.FieldsDiffer = append(tr.FieldsDiffer, fname)
				}
			} else {
				tr.FieldsMissing = append(tr.FieldsMissing, fname)
			}
		}
		for fname := range genFields {
			if _, ok := gtFields[fname]; !ok {
				tr.FieldsExtra = append(tr.FieldsExtra, fname)
			}
		}
	}

	// For unions, compare variant count.
	if gt.Kind == ir.KindUnion && gen.Kind == ir.KindUnion {
		gtVars := make(map[string]bool)
		for _, v := range gt.Variants {
			gtVars[v.Name] = true
		}
		genVars := make(map[string]bool)
		for _, v := range gen.Variants {
			genVars[v.Name] = true
		}
		for n := range gtVars {
			if genVars[n] {
				tr.FieldsMatch++
			} else {
				tr.FieldsMissing = append(tr.FieldsMissing, "variant:"+n)
			}
		}
		for n := range genVars {
			if !gtVars[n] {
				tr.FieldsExtra = append(tr.FieldsExtra, "variant:"+n)
			}
		}
	}

	// For enums, compare values.
	if gt.Kind == ir.KindEnum && gen.Kind == ir.KindEnum {
		gtVals := make(map[string]bool)
		for _, v := range gt.EnumValues {
			gtVals[fmt.Sprintf("%v", v)] = true
		}
		genVals := make(map[string]bool)
		for _, v := range gen.EnumValues {
			genVals[fmt.Sprintf("%v", v)] = true
		}
		for v := range gtVals {
			if genVals[v] {
				tr.FieldsMatch++
			} else {
				tr.FieldsMissing = append(tr.FieldsMissing, "enum:"+v)
			}
		}
		for v := range genVals {
			if !gtVals[v] {
				tr.FieldsExtra = append(tr.FieldsExtra, "enum:"+v)
			}
		}
	}

	sort.Strings(tr.FieldsMissing)
	sort.Strings(tr.FieldsExtra)
	sort.Strings(tr.FieldsDiffer)

	return tr
}

func fieldsStructurallyEqual(a, b ir.Field) bool {
	// Compare type kind.
	aKind := refKind(a.Type)
	bKind := refKind(b.Type)
	if aKind != bKind {
		return false
	}
	// Compare required.
	if a.Required != b.Required {
		return false
	}
	// Compare constraints (set comparison).
	if !constraintsEqual(a.Constraints, b.Constraints) {
		return false
	}
	return true
}

func refKind(ref ir.TypeRef) ir.Kind {
	if ref.Name != "" {
		return ir.KindRef
	}
	if ref.Inline != nil {
		return ref.Inline.Kind
	}
	return -1
}

func constraintsEqual(a, b []ir.Constraint) bool {
	if len(a) != len(b) {
		return false
	}
	am := make(map[string]any)
	for _, c := range a {
		am[c.Keyword] = c.Value
	}
	for _, c := range b {
		av, ok := am[c.Keyword]
		if !ok {
			return false
		}
		if fmt.Sprintf("%v", av) != fmt.Sprintf("%v", c.Value) {
			return false
		}
	}
	return true
}

func kindName(k ir.Kind) string {
	switch k {
	case ir.KindStruct:
		return "struct"
	case ir.KindEnum:
		return "enum"
	case ir.KindUnion:
		return "union"
	case ir.KindScalar:
		return "scalar"
	case ir.KindList:
		return "list"
	case ir.KindMap:
		return "map"
	case ir.KindRef:
		return "ref"
	case ir.KindNullable:
		return "nullable"
	default:
		return fmt.Sprintf("kind(%d)", k)
	}
}

// FormatIRReport produces a human-readable report.
func FormatIRReport(r *IRReport) string {
	var b strings.Builder

	b.WriteString("╔══════════════════════════════════════════════════╗\n")
	b.WriteString("║          IR-Level Schema Diff Report             ║\n")
	b.WriteString("╠══════════════════════════════════════════════════╣\n")
	fmt.Fprintf(&b, "║  Ground truth types:  %-4d                       ║\n", r.GroundTruthTypes)
	fmt.Fprintf(&b, "║  Generated types:     %-4d                       ║\n", r.GeneratedTypes)
	fmt.Fprintf(&b, "║  Matched by name:     %-4d                       ║\n", r.MatchedTypes)
	fmt.Fprintf(&b, "║  Missing:             %-4d                       ║\n", len(r.MissingTypes))
	fmt.Fprintf(&b, "║  Extra:               %-4d                       ║\n", len(r.ExtraTypes))
	b.WriteString("╠══════════════════════════════════════════════════╣\n")

	pct := float64(0)
	if r.FieldsTotal > 0 {
		pct = float64(r.FieldsMatch) / float64(r.FieldsTotal) * 100
	}
	fmt.Fprintf(&b, "║  Field match rate:    %.1f%%                      ║\n", pct)
	fmt.Fprintf(&b, "║    Matched:           %-4d                       ║\n", r.FieldsMatch)
	fmt.Fprintf(&b, "║    Missing:           %-4d                       ║\n", r.FieldsMissing)
	fmt.Fprintf(&b, "║    Extra:             %-4d                       ║\n", r.FieldsExtra)
	fmt.Fprintf(&b, "║    Differ:            %-4d                       ║\n", r.FieldsDiffer)
	b.WriteString("╚══════════════════════════════════════════════════╝\n")

	// Kind mismatches.
	hasKindMismatch := false
	for _, tr := range r.TypeReports {
		if !tr.KindMatch {
			if !hasKindMismatch {
				b.WriteString("\n── Kind mismatches ──\n")
				hasKindMismatch = true
			}
			fmt.Fprintf(&b, "  %s: %s → %s\n", tr.Name, tr.KindGT, tr.KindGen)
		}
	}

	// Types with issues.
	hasIssues := false
	for _, tr := range r.TypeReports {
		if len(tr.FieldsMissing) > 0 || len(tr.FieldsDiffer) > 0 {
			if !hasIssues {
				b.WriteString("\n── Per-type issues ──\n")
				hasIssues = true
			}
			fmt.Fprintf(&b, "\n  %s:\n", tr.Name)
			for _, f := range tr.FieldsMissing {
				fmt.Fprintf(&b, "    missing %s\n", f)
			}
			for _, f := range tr.FieldsDiffer {
				fmt.Fprintf(&b, "    differ  %s\n", f)
			}
		}
	}

	return b.String()
}
