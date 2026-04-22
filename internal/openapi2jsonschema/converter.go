// Package openapi2jsonschema converts OpenAPI 3.x Schema Objects into
// self-contained JSON Schema (draft 2020-12) documents.
//
// OpenAPI Schema Objects are a superset of JSON Schema with extra keywords
// (discriminator, xml, nullable, etc.). This converter strips the OpenAPI-only
// bits, resolves $ref chains, and produces spec-compliant JSON Schema.
//
// Coverage: every field on pb33f/libopenapi's high-level Schema struct is
// explicitly handled — either converted to its JSON Schema equivalent,
// intentionally stripped (OpenAPI-only), or documented as a conscious skip.
package openapi2jsonschema

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	v3base "github.com/pb33f/libopenapi/datamodel/high/base"
	v3high "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"

	"github.com/pb33f/libopenapi"
)

// Converter holds the parsed OpenAPI model and extracted $defs.
type Converter struct {
	doc       *v3high.Document
	defs      *orderedmap.Map[string, any] // collected $defs
	seen      map[string]bool              // cycle detection
	nameStack []string                     // tracks current named schema for recursive $ref resolution
}

// New parses an OpenAPI spec from a file and returns a Converter.
func New(path string) (*Converter, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}
	doc, err := libopenapi.NewDocument(data)
	if err != nil {
		return nil, fmt.Errorf("parse openapi: %w", err)
	}
	model, err := doc.BuildV3Model()
	if err != nil {
		return nil, fmt.Errorf("build v3 model: %w", err)
	}
	return &Converter{
		doc:       &model.Model,
		defs:      orderedmap.New[string, any](),
		seen:      make(map[string]bool),
		nameStack: nil,
	}, nil
}

// ExtractSchema extracts a named component schema (e.g. "CreateResponse")
// and returns a self-contained JSON Schema document with $defs.
func (c *Converter) ExtractSchema(name string) ([]byte, error) {
	schemas := c.doc.Components.Schemas
	if schemas == nil {
		return nil, fmt.Errorf("no component schemas found")
	}

	proxy, ok := schemas.Get(name)
	if !ok {
		return nil, fmt.Errorf("schema %q not found in components", name)
	}

	schema, err := proxy.BuildSchema()
	if err != nil {
		return nil, fmt.Errorf("build schema %q: %w", name, err)
	}

	c.convertSchema(name, schema)

	root := orderedmap.New[string, any]()
	root.Set("$schema", "https://json-schema.org/draft/2020-12/schema")
	root.Set("$id", name)
	root.Set("$ref", "#/$defs/"+name)
	if c.defs.Len() > 0 {
		root.Set("$defs", c.defs)
	}

	return json.MarshalIndent(root, "", "  ")
}

// convertSchema converts a high-level Schema into a JSON Schema ordered map,
// recording any named types into $defs.
//
// Field coverage matches pb33f/libopenapi v0.36.1 Schema struct exhaustively.
// Each field is handled in a named section with a comment. Fields that are
// intentionally not emitted are documented as "Stripped (OpenAPI-only)".
func (c *Converter) convertSchema(name string, schema *v3base.Schema) *orderedmap.Map[string, any] {
	if schema == nil {
		return orderedmap.New[string, any]()
	}

	// Track named schema context for recursive $ref resolution.
	if name != "" {
		c.nameStack = append(c.nameStack, name)
		defer func() { c.nameStack = c.nameStack[:len(c.nameStack)-1] }()
	}

	out := orderedmap.New[string, any]()

	// ── Composition keywords ────────────────────────────────────────────

	// allOf — attempt to flatten when all variants are objects
	if len(schema.AllOf) > 0 {
		if flattened, ok := c.flattenAllOf(schema.AllOf); ok {
			mergeInto(out, flattened)
		} else {
			items := make([]any, 0, len(schema.AllOf))
			for _, proxy := range schema.AllOf {
				items = append(items, c.convertProxy(proxy))
			}
			out.Set("allOf", items)
		}
	}

	// oneOf — with nullable simplification
	if len(schema.OneOf) > 0 {
		if simplified, ok := c.simplifyNullableComposition(schema.OneOf); ok {
			mergeInto(out, simplified)
		} else {
			items := make([]any, 0, len(schema.OneOf))
			for _, proxy := range schema.OneOf {
				items = append(items, c.convertProxy(proxy))
			}
			out.Set("oneOf", items)
		}
	}

	// anyOf — with nullable simplification
	if len(schema.AnyOf) > 0 {
		if simplified, ok := c.simplifyNullableComposition(schema.AnyOf); ok {
			mergeInto(out, simplified)
		} else {
			items := make([]any, 0, len(schema.AnyOf))
			for _, proxy := range schema.AnyOf {
				items = append(items, c.convertProxy(proxy))
			}
			out.Set("anyOf", items)
		}
	}

	// not
	if schema.Not != nil {
		out.Set("not", c.convertProxy(schema.Not))
	}

	// if / then / else  (3.1+ / JSON Schema 2020-12)
	if schema.If != nil {
		out.Set("if", c.convertProxy(schema.If))
	}
	if schema.Then != nil {
		out.Set("then", c.convertProxy(schema.Then))
	}
	if schema.Else != nil {
		out.Set("else", c.convertProxy(schema.Else))
	}

	// dependentSchemas  (3.1+)
	if schema.DependentSchemas != nil && schema.DependentSchemas.Len() > 0 {
		ds := orderedmap.New[string, any]()
		for pair := schema.DependentSchemas.Oldest(); pair != nil; pair = pair.Next() {
			ds.Set(pair.Key, c.convertProxy(pair.Value))
		}
		out.Set("dependentSchemas", ds)
	}

	// dependentRequired  (3.1+)
	if schema.DependentRequired != nil && schema.DependentRequired.Len() > 0 {
		dr := orderedmap.New[string, any]()
		for pair := schema.DependentRequired.Oldest(); pair != nil; pair = pair.Next() {
			dr.Set(pair.Key, pair.Value)
		}
		out.Set("dependentRequired", dr)
	}

	// ── Type & nullable ─────────────────────────────────────────────────

	// Type — handle OpenAPI 3.0 nullable → JSON Schema type array with "null"
	types := schema.Type
	if len(types) > 0 {
		if schema.Nullable != nil && *schema.Nullable {
			types = append(append([]string{}, types...), "null") // copy to avoid mutating
		}
		if len(types) == 1 {
			out.Set("type", types[0])
		} else {
			out.Set("type", types)
		}
	} else if schema.Nullable != nil && *schema.Nullable {
		// nullable without explicit type — emit just "null"
		out.Set("type", "null")
	}

	// ── Format ──────────────────────────────────────────────────────────

	if schema.Format != "" {
		out.Set("format", schema.Format)
	}

	// ── Metadata ────────────────────────────────────────────────────────

	if schema.Title != "" {
		out.Set("title", schema.Title)
	}
	if schema.Description != "" {
		out.Set("description", schema.Description)
	}

	// $comment  (3.1+)
	if schema.Comment != "" {
		out.Set("$comment", schema.Comment)
	}

	// deprecated — valid in JSON Schema 2019-09+
	if schema.Deprecated != nil && *schema.Deprecated {
		out.Set("deprecated", true)
	}

	// readOnly
	if schema.ReadOnly != nil && *schema.ReadOnly {
		out.Set("readOnly", true)
	}

	// writeOnly
	if schema.WriteOnly != nil && *schema.WriteOnly {
		out.Set("writeOnly", true)
	}

	// ── Enum / Const / Default ──────────────────────────────────────────

	if len(schema.Enum) > 0 {
		enums := make([]any, len(schema.Enum))
		for i, v := range schema.Enum {
			enums[i] = v.Value
		}
		out.Set("enum", enums)
	}

	if schema.Const != nil {
		out.Set("const", schema.Const.Value)
	}

	if schema.Default != nil {
		out.Set("default", schema.Default.Value)
	}

	// ── Object keywords ─────────────────────────────────────────────────

	// properties
	if schema.Properties != nil && schema.Properties.Len() > 0 {
		props := orderedmap.New[string, any]()
		for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
			props.Set(pair.Key, c.convertProxy(pair.Value))
		}
		out.Set("properties", props)
	}

	// patternProperties
	if schema.PatternProperties != nil && schema.PatternProperties.Len() > 0 {
		pp := orderedmap.New[string, any]()
		for pair := schema.PatternProperties.Oldest(); pair != nil; pair = pair.Next() {
			pp.Set(pair.Key, c.convertProxy(pair.Value))
		}
		out.Set("patternProperties", pp)
	}

	// additionalProperties — DynamicValue[*SchemaProxy, bool]
	if schema.AdditionalProperties != nil {
		if schema.AdditionalProperties.IsA() {
			out.Set("additionalProperties", c.convertProxy(schema.AdditionalProperties.A))
		} else {
			out.Set("additionalProperties", schema.AdditionalProperties.B)
		}
	}

	// unevaluatedProperties — DynamicValue[*SchemaProxy, bool]  (3.1+)
	if schema.UnevaluatedProperties != nil {
		if schema.UnevaluatedProperties.IsA() {
			out.Set("unevaluatedProperties", c.convertProxy(schema.UnevaluatedProperties.A))
		} else {
			out.Set("unevaluatedProperties", schema.UnevaluatedProperties.B)
		}
	}

	// propertyNames  (3.1+)
	if schema.PropertyNames != nil {
		out.Set("propertyNames", c.convertProxy(schema.PropertyNames))
	}

	// required
	if len(schema.Required) > 0 {
		out.Set("required", schema.Required)
	}

	// minProperties / maxProperties
	if schema.MinProperties != nil {
		out.Set("minProperties", *schema.MinProperties)
	}
	if schema.MaxProperties != nil {
		out.Set("maxProperties", *schema.MaxProperties)
	}

	// ── Array keywords ──────────────────────────────────────────────────

	// items — DynamicValue[*SchemaProxy, bool]
	if schema.Items != nil {
		if schema.Items.IsA() {
			out.Set("items", c.convertProxy(schema.Items.A))
		} else {
			out.Set("items", schema.Items.B)
		}
	}

	// prefixItems  (3.1+ tuple validation)
	if len(schema.PrefixItems) > 0 {
		items := make([]any, 0, len(schema.PrefixItems))
		for _, proxy := range schema.PrefixItems {
			items = append(items, c.convertProxy(proxy))
		}
		out.Set("prefixItems", items)
	}

	// unevaluatedItems  (3.1+)
	if schema.UnevaluatedItems != nil {
		out.Set("unevaluatedItems", c.convertProxy(schema.UnevaluatedItems))
	}

	// contains / minContains / maxContains  (3.1+)
	if schema.Contains != nil {
		out.Set("contains", c.convertProxy(schema.Contains))
	}
	if schema.MinContains != nil {
		out.Set("minContains", *schema.MinContains)
	}
	if schema.MaxContains != nil {
		out.Set("maxContains", *schema.MaxContains)
	}

	// minItems / maxItems
	if schema.MinItems != nil {
		out.Set("minItems", *schema.MinItems)
	}
	if schema.MaxItems != nil {
		out.Set("maxItems", *schema.MaxItems)
	}

	// uniqueItems
	if schema.UniqueItems != nil && *schema.UniqueItems {
		out.Set("uniqueItems", true)
	}

	// ── Numeric constraints ─────────────────────────────────────────────

	if schema.Minimum != nil {
		out.Set("minimum", *schema.Minimum)
	}
	if schema.Maximum != nil {
		out.Set("maximum", *schema.Maximum)
	}

	// exclusiveMinimum — DynamicValue[bool, float64]
	// OpenAPI 3.0: A=bool (if true, minimum is exclusive) → convert to 2020-12 number form
	// OpenAPI 3.1: B=float64 (the exclusive bound itself)
	if schema.ExclusiveMinimum != nil {
		if schema.ExclusiveMinimum.IsB() {
			// 3.1 form: already a number
			out.Set("exclusiveMinimum", schema.ExclusiveMinimum.B)
		} else if schema.ExclusiveMinimum.A && schema.Minimum != nil {
			// 3.0 form: boolean true + minimum → promote minimum to exclusiveMinimum
			out.Delete("minimum")
			out.Set("exclusiveMinimum", *schema.Minimum)
		}
	}

	// exclusiveMaximum — DynamicValue[bool, float64]
	if schema.ExclusiveMaximum != nil {
		if schema.ExclusiveMaximum.IsB() {
			out.Set("exclusiveMaximum", schema.ExclusiveMaximum.B)
		} else if schema.ExclusiveMaximum.A && schema.Maximum != nil {
			out.Delete("maximum")
			out.Set("exclusiveMaximum", *schema.Maximum)
		}
	}

	if schema.MultipleOf != nil {
		out.Set("multipleOf", *schema.MultipleOf)
	}

	// ── String constraints ──────────────────────────────────────────────

	if schema.MinLength != nil {
		out.Set("minLength", *schema.MinLength)
	}
	if schema.MaxLength != nil {
		out.Set("maxLength", *schema.MaxLength)
	}
	if schema.Pattern != "" {
		out.Set("pattern", schema.Pattern)
	}

	// contentEncoding / contentMediaType  (JSON Schema 2020-12 content vocabulary)
	if schema.ContentEncoding != "" {
		out.Set("contentEncoding", schema.ContentEncoding)
	}
	if schema.ContentMediaType != "" {
		out.Set("contentMediaType", schema.ContentMediaType)
	}

	// contentSchema  (3.1+)
	if schema.ContentSchema != nil {
		out.Set("contentSchema", c.convertProxy(schema.ContentSchema))
	}

	// ── Examples ────────────────────────────────────────────────────────

	// OpenAPI 3.0 "example" (singular) → JSON Schema 2020-12 "examples" (array)
	// OpenAPI 3.1 "examples" (array) → pass through
	if len(schema.Examples) > 0 {
		examples := make([]any, len(schema.Examples))
		for i, node := range schema.Examples {
			examples[i] = node.Value
		}
		out.Set("examples", examples)
	} else if schema.Example != nil {
		// Promote singular example to examples array per 2020-12
		out.Set("examples", []any{schema.Example.Value})
	}

	// ── JSON Schema 2020-12 identity keywords ───────────────────────────

	// $id on sub-schemas (top-level $id is set by the document builder)
	if schema.Id != "" && name == "" {
		out.Set("$id", schema.Id)
	}

	// $anchor
	if schema.Anchor != "" {
		out.Set("$anchor", schema.Anchor)
	}

	// $dynamicAnchor
	if schema.DynamicAnchor != "" {
		out.Set("$dynamicAnchor", schema.DynamicAnchor)
	}

	// $dynamicRef
	if schema.DynamicRef != "" {
		out.Set("$dynamicRef", schema.DynamicRef)
	}

	// ── Discriminator (OpenAPI → JSON Schema mapping) ───────────────────

	// OpenAPI's discriminator isn't a JSON Schema keyword, but it carries
	// semantic information about which property determines the union variant.
	// We preserve it under "x-discriminator" so downstream tools (including
	// compschema's IR) can use it for union detection.
	if schema.Discriminator != nil {
		disc := orderedmap.New[string, any]()
		disc.Set("propertyName", schema.Discriminator.PropertyName)
		if schema.Discriminator.Mapping != nil && schema.Discriminator.Mapping.Len() > 0 {
			mapping := orderedmap.New[string, any]()
			for pair := schema.Discriminator.Mapping.Oldest(); pair != nil; pair = pair.Next() {
				mapping.Set(pair.Key, pair.Value)
			}
			disc.Set("mapping", mapping)
		}
		out.Set("x-discriminator", disc)
	}

	// ── Extensions (x-* keywords) ──────────────────────────────────────

	// Preserve vendor extensions — they may carry semantic information
	// (e.g. x-stainless-const in OpenAI's spec).
	if schema.Extensions != nil {
		for pair := schema.Extensions.Oldest(); pair != nil; pair = pair.Next() {
			if pair.Value != nil {
				out.Set(pair.Key, pair.Value.Value)
			}
		}
	}

	// ── Intentionally stripped (OpenAPI-only, no JSON Schema equivalent) ─
	//
	// - XML              → OpenAPI serialization hint, not relevant for JSON
	// - ExternalDocs     → OpenAPI documentation link, no JSON Schema equivalent
	// - SchemaTypeRef    → nested $schema dialect (set at document level, not per-def)
	// - Vocabulary       → meta-schema vocabulary declaration (not per-schema)
	// - Nullable         → already handled above (merged into type array)
	// - ParentProxy, low → internal libopenapi bookkeeping

	// ── Register named schemas in $defs ─────────────────────────────────

	if name != "" {
		c.defs.Set(name, out)
	}

	return out
}

// convertProxy handles a SchemaProxy — either resolving a $ref or inlining.
func (c *Converter) convertProxy(proxy *v3base.SchemaProxy) any {
	if proxy == nil {
		return orderedmap.New[string, any]()
	}

	ref := proxy.GetReference()
	if ref != "" {
		refName := refToName(ref)
		c.ensureDef(refName)
		refMap := orderedmap.New[string, any]()
		refMap.Set("$ref", "#/$defs/"+refName)
		return refMap
	}

	s, err := proxy.BuildSchema()
	if err != nil {
		return orderedmap.New[string, any]()
	}

	// Detect empty schemas — these are typically $recursiveRef: "#" that
	// libopenapi can't resolve. Replace with a $ref to the nearest named
	// parent schema (self-reference).
	if c.isEmptySchema(s) && len(c.nameStack) > 0 {
		parentName := c.nameStack[len(c.nameStack)-1]
		refMap := orderedmap.New[string, any]()
		refMap.Set("$ref", "#/$defs/"+parentName)
		return refMap
	}

	return c.convertSchema("", s)
}

// ensureDef resolves a component schema name and adds it to $defs if not already present.
func (c *Converter) ensureDef(name string) {
	if c.seen[name] {
		return
	}
	c.seen[name] = true

	if c.doc.Components == nil || c.doc.Components.Schemas == nil {
		return
	}

	compProxy, ok := c.doc.Components.Schemas.Get(name)
	if !ok {
		return
	}

	schema, err := compProxy.BuildSchema()
	if err != nil {
		return
	}
	c.convertSchema(name, schema)
}
// isEmptySchema returns true if a schema has no meaningful content — typically
// the result of an unresolved $recursiveRef in OpenAPI 3.0 specs.
func (c *Converter) isEmptySchema(s *v3base.Schema) bool {
	if s == nil {
		return true
	}
	return len(s.Type) == 0 &&
		len(s.AllOf) == 0 &&
		len(s.OneOf) == 0 &&
		len(s.AnyOf) == 0 &&
		s.Not == nil &&
		(s.Properties == nil || s.Properties.Len() == 0) &&
		s.Items == nil &&
		s.AdditionalProperties == nil &&
		len(s.Enum) == 0 &&
		s.Const == nil &&
		s.If == nil &&
		s.Title == "" &&
		s.Description == "" &&
		s.Format == ""
}

// flattenAllOf merges allOf variants into a single object schema when all
// variants are objects (have properties or resolve to object $refs).
//
// Pattern: allOf: [{$ref: A}, {$ref: B}, {type:object, properties:{...}}]
// → type: object, properties: {merged from A + B + inline}, required: [merged]
//
// This eliminates interface{} in generated Go for struct-composition patterns
// that OpenAPI commonly uses for inheritance/mixins.
func (c *Converter) flattenAllOf(proxies []*v3base.SchemaProxy) (*orderedmap.Map[string, any], bool) {
	// First pass: convert all variants and check they're all object-like.
	var converted []*orderedmap.Map[string, any]
	for _, proxy := range proxies {
		raw := c.convertProxy(proxy)
		m, ok := raw.(*orderedmap.Map[string, any])
		if !ok {
			return nil, false
		}

		// If it's a $ref, we need to check if the referenced schema is object-like.
		// We still keep the $ref in $defs, but we read its resolved properties for merging.
		if refVal, hasRef := m.Get("$ref"); hasRef {
			refStr, _ := refVal.(string)
			// Extract the def name from "#/$defs/Foo"
			parts := strings.Split(refStr, "/")
			defName := parts[len(parts)-1]
			resolved, ok := c.defs.Get(defName)
			if !ok {
				return nil, false
			}
			resolvedMap, ok := resolved.(*orderedmap.Map[string, any])
			if !ok {
				return nil, false
			}
			// Must be object-like.
			if !isObjectLike(resolvedMap) {
				return nil, false
			}
			converted = append(converted, resolvedMap)
		} else {
			// Inline schema — must be object-like.
			if !isObjectLike(m) {
				return nil, false
			}
			converted = append(converted, m)
		}
	}

	// Second pass: merge all properties, required, and other object keywords.
	out := orderedmap.New[string, any]()
	out.Set("type", "object")

	mergedProps := orderedmap.New[string, any]()
	var mergedRequired []string
	requiredSeen := map[string]bool{}

	for _, m := range converted {
		// Merge properties.
		if propsRaw, ok := m.Get("properties"); ok {
			if props, ok := propsRaw.(*orderedmap.Map[string, any]); ok {
				for pair := props.Oldest(); pair != nil; pair = pair.Next() {
					if _, exists := mergedProps.Get(pair.Key); !exists {
						mergedProps.Set(pair.Key, pair.Value)
					}
				}
			}
		}

		// Merge required.
		if reqRaw, ok := m.Get("required"); ok {
			if reqs, ok := reqRaw.([]string); ok {
				for _, r := range reqs {
					if !requiredSeen[r] {
						requiredSeen[r] = true
						mergedRequired = append(mergedRequired, r)
					}
				}
			}
		}

		// Carry over additionalProperties from any variant (last wins).
		if ap, ok := m.Get("additionalProperties"); ok {
			out.Set("additionalProperties", ap)
		}

		// Carry over description from first variant that has one.
		if desc, ok := m.Get("description"); ok {
			if _, alreadySet := out.Get("description"); !alreadySet {
				out.Set("description", desc)
			}
		}

		// Carry over title.
		if title, ok := m.Get("title"); ok {
			if _, alreadySet := out.Get("title"); !alreadySet {
				out.Set("title", title)
			}
		}
	}

	if mergedProps.Len() > 0 {
		out.Set("properties", mergedProps)
	}
	if len(mergedRequired) > 0 {
		out.Set("required", mergedRequired)
	}

	return out, true
}

// isObjectLike returns true if a converted schema map represents an object
// (has properties, type:object, or is composed of objects via allOf).
func isObjectLike(m *orderedmap.Map[string, any]) bool {
	if _, ok := m.Get("properties"); ok {
		return true
	}
	if t, ok := m.Get("type"); ok {
		if ts, ok := t.(string); ok && ts == "object" {
			return true
		}
	}
	// allOf of objects is also object-like (recursive composition).
	if _, ok := m.Get("allOf"); ok {
		return true
	}
	return false
}

// simplifyNullableComposition checks if a oneOf/anyOf is just [T, {type:null}]
// and if so, returns a simplified schema where null is merged into the type.
//
// Pattern: anyOf: [{type: "string"}, {type: "null"}]  → type: ["string", "null"]
// Pattern: anyOf: [{$ref: "#/$defs/Foo"}, {type: "null"}] → allOf: [{$ref}] with nullable
//
// This is extremely common in OpenAPI specs that use anyOf for nullable fields
// instead of the nullable keyword.
func (c *Converter) simplifyNullableComposition(proxies []*v3base.SchemaProxy) (*orderedmap.Map[string, any], bool) {
	if len(proxies) != 2 {
		return nil, false
	}

	// Find which is the null variant and which is the concrete variant.
	var nullIdx, concreteIdx int = -1, -1
	for i, proxy := range proxies {
		// Skip $ref proxies — they can't be the null variant.
		if proxy.GetReference() != "" {
			concreteIdx = i
			continue
		}
		s, err := proxy.BuildSchema()
		if err != nil {
			return nil, false
		}
		if len(s.Type) == 1 && s.Type[0] == "null" && s.Properties == nil && len(s.AllOf) == 0 && len(s.OneOf) == 0 && len(s.AnyOf) == 0 {
			nullIdx = i
		} else {
			concreteIdx = i
		}
	}

	if nullIdx == -1 || concreteIdx == -1 {
		return nil, false
	}

	// Convert the concrete variant.
	concrete := c.convertProxy(proxies[concreteIdx])
	concreteMap, ok := concrete.(*orderedmap.Map[string, any])
	if !ok {
		return nil, false
	}

	// If the concrete variant is a $ref, don't simplify — go-jsonschema
	// already handles anyOf($ref, null) correctly by generating a pointer
	// to the referenced type. Our allOf+null wrapping would break that.
	if _, hasRef := concreteMap.Get("$ref"); hasRef {
		return nil, false
	}

	// Concrete has a type — merge null into it.
	if typeVal, hasType := concreteMap.Get("type"); hasType {
		switch t := typeVal.(type) {
		case string:
			concreteMap.Set("type", []any{t, "null"})
		case []any:
			// Check null isn't already there.
			hasNull := false
			for _, v := range t {
				if v == "null" {
					hasNull = true
				}
			}
			if !hasNull {
				concreteMap.Set("type", append(t, "null"))
			}
		case []string:
			hasNull := false
			for _, v := range t {
				if v == "null" {
					hasNull = true
				}
			}
			if !hasNull {
				newTypes := make([]any, len(t)+1)
				for i, v := range t {
					newTypes[i] = v
				}
				newTypes[len(t)] = "null"
				concreteMap.Set("type", newTypes)
			}
		}
		return concreteMap, true
	}

	// No type on concrete — can't simplify cleanly.
	return nil, false
}

// mergeInto copies all entries from src into dst.
func mergeInto(dst, src *orderedmap.Map[string, any]) {
	for pair := src.Oldest(); pair != nil; pair = pair.Next() {
		dst.Set(pair.Key, pair.Value)
	}
}

// refToName extracts the schema name from a $ref like "#/components/schemas/Foo".
func refToName(ref string) string {
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1]
}

// ListSchemaNames returns all component schema names in the spec.
func (c *Converter) ListSchemaNames() []string {
	if c.doc.Components == nil || c.doc.Components.Schemas == nil {
		return nil
	}
	var names []string
	for pair := c.doc.Components.Schemas.Oldest(); pair != nil; pair = pair.Next() {
		names = append(names, pair.Key)
	}
	return names
}

// ExtractEndpointSchemas extracts all schemas referenced by a specific path
// prefix (e.g. "/responses") and returns a JSON Schema document with all of
// them as $defs.
func (c *Converter) ExtractEndpointSchemas(pathPrefix string) ([]byte, error) {
	if c.doc.Paths == nil {
		return nil, fmt.Errorf("no paths in spec")
	}

	var rootSchemaNames []string

	for pair := c.doc.Paths.PathItems.Oldest(); pair != nil; pair = pair.Next() {
		path := pair.Key
		if !strings.HasPrefix(path, pathPrefix) {
			continue
		}
		item := pair.Value
		ops := []*v3high.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete}
		for _, op := range ops {
			if op == nil {
				continue
			}
			// Request body schemas
			if op.RequestBody != nil && op.RequestBody.Content != nil {
				for ct := op.RequestBody.Content.Oldest(); ct != nil; ct = ct.Next() {
					if ct.Value.Schema != nil {
						ref := ct.Value.Schema.GetReference()
						if ref != "" {
							name := refToName(ref)
							rootSchemaNames = append(rootSchemaNames, name)
							c.ensureDef(name)
						}
					}
				}
			}
			// Response schemas
			if op.Responses != nil && op.Responses.Codes != nil {
				for code := op.Responses.Codes.Oldest(); code != nil; code = code.Next() {
					resp := code.Value
					if resp.Content != nil {
						for ct := resp.Content.Oldest(); ct != nil; ct = ct.Next() {
							if ct.Value.Schema != nil {
								ref := ct.Value.Schema.GetReference()
								if ref != "" {
									name := refToName(ref)
									rootSchemaNames = append(rootSchemaNames, name)
									c.ensureDef(name)
								}
							}
						}
					}
				}
			}
		}
	}

	if len(rootSchemaNames) == 0 {
		return nil, fmt.Errorf("no schemas found for path prefix %q", pathPrefix)
	}

	root := orderedmap.New[string, any]()
	root.Set("$schema", "https://json-schema.org/draft/2020-12/schema")
	root.Set("$id", "openai-responses-api")
	root.Set("description", fmt.Sprintf("JSON Schema extracted from OpenAI OpenAPI spec for paths matching %q", pathPrefix))

	// Create oneOf referencing all root schemas
	seen := map[string]bool{}
	refs := make([]any, 0, len(rootSchemaNames))
	for _, name := range rootSchemaNames {
		if seen[name] {
			continue
		}
		seen[name] = true
		refMap := orderedmap.New[string, any]()
		refMap.Set("$ref", "#/$defs/"+name)
		refs = append(refs, refMap)
	}
	root.Set("oneOf", refs)

	if c.defs.Len() > 0 {
		root.Set("$defs", c.defs)
	}

	return json.MarshalIndent(root, "", "  ")
}
