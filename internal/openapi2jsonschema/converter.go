// Package openapi2jsonschema converts OpenAPI 3.x Schema Objects into
// self-contained JSON Schema (draft 2020-12) documents.
//
// OpenAPI Schema Objects are a superset of JSON Schema with extra keywords
// (discriminator, xml, nullable, etc.). This converter strips the OpenAPI-only
// bits, resolves $ref chains, and produces spec-compliant JSON Schema.
package openapi2jsonschema

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/pb33f/libopenapi"
	v3base "github.com/pb33f/libopenapi/datamodel/high/base"
	v3high "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"
)

// Converter holds the parsed OpenAPI model and extracted $defs.
type Converter struct {
	doc  *v3high.Document
	defs *orderedmap.Map[string, any] // collected $defs
	seen map[string]bool              // cycle detection
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
		doc:  &model.Model,
		defs: orderedmap.New[string, any](),
		seen: make(map[string]bool),
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

	// Build the top-level document.
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
func (c *Converter) convertSchema(name string, schema *v3base.Schema) *orderedmap.Map[string, any] {
	if schema == nil {
		return orderedmap.New[string, any]()
	}

	out := orderedmap.New[string, any]()

	// Handle allOf
	if len(schema.AllOf) > 0 {
		allOf := make([]any, 0, len(schema.AllOf))
		for _, proxy := range schema.AllOf {
			allOf = append(allOf, c.convertProxy(proxy))
		}
		out.Set("allOf", allOf)
	}

	// Handle oneOf
	if len(schema.OneOf) > 0 {
		oneOf := make([]any, 0, len(schema.OneOf))
		for _, proxy := range schema.OneOf {
			oneOf = append(oneOf, c.convertProxy(proxy))
		}
		out.Set("oneOf", oneOf)
	}

	// Handle anyOf
	if len(schema.AnyOf) > 0 {
		anyOf := make([]any, 0, len(schema.AnyOf))
		for _, proxy := range schema.AnyOf {
			anyOf = append(anyOf, c.convertProxy(proxy))
		}
		out.Set("anyOf", anyOf)
	}

	// Type — handle nullable (OpenAPI 3.0 nullable → JSON Schema type array with "null")
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
		out.Set("type", "null")
	}

	// Format
	if schema.Format != "" {
		out.Set("format", schema.Format)
	}

	// Title / Description
	if schema.Title != "" {
		out.Set("title", schema.Title)
	}
	if schema.Description != "" {
		out.Set("description", schema.Description)
	}

	// Enum
	if len(schema.Enum) > 0 {
		enums := make([]any, len(schema.Enum))
		for i, v := range schema.Enum {
			enums[i] = v.Value
		}
		out.Set("enum", enums)
	}

	// Const
	if schema.Const != nil {
		out.Set("const", schema.Const.Value)
	}

	// Default
	if schema.Default != nil {
		out.Set("default", schema.Default.Value)
	}

	// Properties
	if schema.Properties != nil && schema.Properties.Len() > 0 {
		props := orderedmap.New[string, any]()
		for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
			props.Set(pair.Key, c.convertProxy(pair.Value))
		}
		out.Set("properties", props)
	}

	// Required
	if len(schema.Required) > 0 {
		out.Set("required", schema.Required)
	}

	// AdditionalProperties — DynamicValue[*SchemaProxy, bool]
	if schema.AdditionalProperties != nil {
		if schema.AdditionalProperties.IsA() {
			// It's a schema
			out.Set("additionalProperties", c.convertProxy(schema.AdditionalProperties.A))
		} else {
			// It's a boolean
			out.Set("additionalProperties", schema.AdditionalProperties.B)
		}
	}

	// Items — DynamicValue[*SchemaProxy, bool]
	if schema.Items != nil {
		if schema.Items.IsA() {
			out.Set("items", c.convertProxy(schema.Items.A))
		} else {
			out.Set("items", schema.Items.B)
		}
	}

	// Numeric constraints
	if schema.Minimum != nil {
		out.Set("minimum", *schema.Minimum)
	}
	if schema.Maximum != nil {
		out.Set("maximum", *schema.Maximum)
	}
	if schema.ExclusiveMinimum != nil {
		if schema.ExclusiveMinimum.IsB() {
			out.Set("exclusiveMinimum", schema.ExclusiveMinimum.B)
		}
	}
	if schema.ExclusiveMaximum != nil {
		if schema.ExclusiveMaximum.IsB() {
			out.Set("exclusiveMaximum", schema.ExclusiveMaximum.B)
		}
	}
	if schema.MultipleOf != nil {
		out.Set("multipleOf", *schema.MultipleOf)
	}

	// String constraints
	if schema.MinLength != nil {
		out.Set("minLength", *schema.MinLength)
	}
	if schema.MaxLength != nil {
		out.Set("maxLength", *schema.MaxLength)
	}
	if schema.Pattern != "" {
		out.Set("pattern", schema.Pattern)
	}

	// Array constraints
	if schema.MinItems != nil {
		out.Set("minItems", *schema.MinItems)
	}
	if schema.MaxItems != nil {
		out.Set("maxItems", *schema.MaxItems)
	}
	if schema.UniqueItems != nil && *schema.UniqueItems {
		out.Set("uniqueItems", true)
	}

	// Register named schemas in $defs
	if name != "" {
		c.defs.Set(name, out)
	}

	return out
}

// convertProxy handles a SchemaProxy — either resolving a $ref or inlining the schema.
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
