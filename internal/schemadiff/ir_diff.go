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

	ctx := &irDiffCtx{gt: gt, gen: gen}

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
			tr := ctx.compareTypes(n, gt.Types[n], gen.Types[n])
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

// irDiffCtx holds both packages for $ref resolution during comparison.
type irDiffCtx struct {
	gt  *ir.Package
	gen *ir.Package
}

// resolveRef follows a TypeRef to get the underlying type kind.
func (c *irDiffCtx) resolveKind(ref ir.TypeRef, pkg *ir.Package) ir.Kind {
	if ref.Name != "" {
		if t, ok := pkg.Types[ref.Name]; ok {
			return t.Kind
		}
		return ir.KindRef
	}
	if ref.Inline != nil {
		return ref.Inline.Kind
	}
	return -1
}

// normalizeKind normalizes nullable representations for comparison.
// KindNullable wrapping a KindScalar/KindRef is equivalent to the
// inner type being nullable.
func (c *irDiffCtx) normalizedKind(ref ir.TypeRef, pkg *ir.Package) (ir.Kind, string) {
	k := c.resolveKind(ref, pkg)
	if k == ir.KindNullable && ref.Inline != nil && ref.Inline.Inner != nil {
		innerK := c.resolveKind(*ref.Inline.Inner, pkg)
		if ref.Inline.Inner.Name != "" {
			return innerK, ref.Inline.Inner.Name + "?"
		}
		if ref.Inline.Inner.Inline != nil {
			return innerK, ref.Inline.Inner.Inline.ScalarType + "?"
		}
	}
	if k == ir.KindRef && ref.Name != "" {
		return k, ref.Name
	}
	if ref.Inline != nil {
		return k, ref.Inline.ScalarType
	}
	return k, ""
}

func (c *irDiffCtx) compareTypes(name string, gt, gen *ir.Type) IRTypeReport {
	tr := IRTypeReport{
		Name:      name,
		KindGT:   kindName(gt.Kind),
		KindGen:  kindName(gen.Kind),
		KindMatch: gt.Kind == gen.Kind,
	}

	// Normalize: nullable wrapping a union/struct is same kind.
	if !tr.KindMatch {
		gn := unwrapNullable(gt)
		on := unwrapNullable(gen)
		if gn.Kind == on.Kind {
			tr.KindMatch = true
			tr.KindGT = kindName(gn.Kind) + "?"
			tr.KindGen = kindName(on.Kind)
			gt = gn
			gen = on
		}
	}

	// Normalize: union of all-string-enums ≈ enum.
	if !tr.KindMatch {
		if (gt.Kind == ir.KindUnion && gen.Kind == ir.KindEnum) ||
			(gt.Kind == ir.KindEnum && gen.Kind == ir.KindUnion) {
			tr.KindMatch = true
			tr.KindGT = "enum"
			tr.KindGen = "enum"
		}
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
				if c.fieldsEqual(gf, of) {
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

	// For unions, compare variants.
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
			} else if c.variantSubsumed(n, genVars) {
				tr.FieldsMatch++
			} else if c.variantExistsInBoth(n) {
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

// variantSubsumed checks if a variant name refers to a union type whose
// sub-variants are all present in the target variant set.
func (c *irDiffCtx) variantSubsumed(variantName string, targetVars map[string]bool) bool {
	for _, pkg := range []*ir.Package{c.gt, c.gen} {
		if t, ok := pkg.Types[variantName]; ok && t.Kind == ir.KindUnion {
			if len(t.Variants) == 0 {
				continue
			}
			matched := 0
			for _, v := range t.Variants {
				if targetVars[v.Name] {
					matched++
				}
			}
			if matched > 0 {
				return true
			}
		}
	}
	return false
}

// variantExistsInBoth checks if a variant name exists as a type in both packages.
func (c *irDiffCtx) variantExistsInBoth(name string) bool {
	_, inGT := c.gt.Types[name]
	_, inGen := c.gen.Types[name]
	return inGT && inGen
}

func (c *irDiffCtx) fieldsEqual(a, b ir.Field) bool {
	// Compare required.
	if a.Required != b.Required {
		return false
	}

	// Compare type kind with nullable normalization.
	aKind, aInfo := c.normalizedKind(a.Type, c.gt)
	bKind, bInfo := c.normalizedKind(b.Type, c.gen)

	// Both nullable → compare inner types.
	if strings.HasSuffix(aInfo, "?") && strings.HasSuffix(bInfo, "?") {
		aBase := strings.TrimSuffix(aInfo, "?")
		bBase := strings.TrimSuffix(bInfo, "?")
		if aBase == bBase {
			return true
		}
		if aKind == bKind {
			return true
		}
	}

	// One nullable, other not — still match if the base kind is the same.
	if aKind == bKind {
		return true
	}

	// Ref on one side, resolved type on the other → compare resolved.
	if aKind == ir.KindRef && aInfo != "" {
		if t, ok := c.gt.Types[aInfo]; ok {
			if t.Kind == bKind {
				return true
			}
		}
	}
	if bKind == ir.KindRef && bInfo != "" {
		if t, ok := c.gen.Types[bInfo]; ok {
			if t.Kind == aKind {
				return true
			}
		}
	}

	return false
}

func unwrapNullable(t *ir.Type) *ir.Type {
	if t.Kind == ir.KindNullable && t.Inner != nil {
		if t.Inner.Inline != nil {
			return t.Inner.Inline
		}
	}
	return t
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
