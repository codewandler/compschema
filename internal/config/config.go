// Package config defines the compschema pipeline configuration format.
//
// A config file (.compschema.yaml) contains named pipelines, each a
// sequence of actions that map to CLI subcommands:
//
//	pipelines:
//	  openai:
//	    - action: extract
//	      spec: openapi.yaml
//	      path: /responses
//	      validate: true
//	      out: responses.schema.json
//	    - action: import
//	      schema: responses.schema.json
//	      package: responses
//	      out: types.gen.go
//	      rename:
//	        CompactionBody: CompactionItem
//	      exclude:
//	        - "Response*Event"
//	    - action: generate
//	      all: true
//	      validate: true
//	      packages:
//	        - ./api/responses/
package config

import (
	"encoding/json"
	"fmt"
	"os"

	"go.yaml.in/yaml/v4"
)

// File is the top-level config file structure.
//
//compschema:generate
type File struct {
	// Pipelines is a map of named pipelines to execute.
	Pipelines map[string][]Action `json:"pipelines" jsonschema:"description=Named pipelines. Each pipeline is a list of actions."`
}

// Pipeline is an ordered list of actions to execute.
type Pipeline = []Action

// Action is a single step in a pipeline, corresponding to a CLI subcommand.
type Action struct {
	// Action name — must be one of: extract, import, generate, diff.
	Action string `json:"action" jsonschema:"description=CLI subcommand to run,enum=extract,enum=import,enum=generate,enum=diff"`

	// extract flags
	Spec   string `json:"spec,omitempty" jsonschema:"description=Path to the OpenAPI spec file"`
	Path   string `json:"path,omitempty" jsonschema:"description=API path prefix to extract (e.g. /responses)"`
	Source any    `json:"-" yaml:"source"` // parsed separately; polymorphic (string, map, array)

	// import flags
	Schema    string            `json:"schema,omitempty" jsonschema:"description=Path to the JSON Schema file"`
	Package   string            `json:"package,omitempty" jsonschema:"description=Go package name for generated code"`
	Rename    map[string]string `json:"rename,omitempty" jsonschema:"description=Type rename map: SchemaName → GoName"`
	Exclude   []string          `json:"exclude,omitempty" jsonschema:"description=Glob patterns for type names to skip"`
	Tags      []string          `json:"tags,omitempty" jsonschema:"description=Additional struct tags to emit (e.g. yaml)"`
	Implement []ImplementRule   `json:"implement,omitempty" yaml:"implement,omitempty" jsonschema:"description=Accessor methods to generate on union variants"`

	// generate flags
	All          bool     `json:"all,omitempty" jsonschema:"description=Analyze all exported types (not just annotated)"`
	Packages     []string `json:"packages,omitempty" jsonschema:"description=Go package patterns to analyze"`
	Test         bool     `json:"test,omitempty" jsonschema:"description=Run generated tests after code generation"`
	FailOnTest   bool     `json:"fail_on_test,omitempty" jsonschema:"description=Exit with error if any generated test fails (requires test: true)"`
	EmitIR       bool     `json:"emit_ir,omitempty" jsonschema:"description=Write IR YAML alongside generated output"`
	Examples     bool     `json:"examples,omitempty" jsonschema:"description=Add generated examples to JSON Schema output"`
	Constructors bool     `json:"constructors,omitempty" jsonschema:"description=Generate NewT constructors for struct types"`

	// shared flags
	ValidateSchema bool   `json:"validate,omitempty" jsonschema:"description=Validate generated schema against meta-schema"`
	Out            string `json:"out,omitempty" jsonschema:"description=Output file or directory path"`
}

// ImplementRule configures accessor method generation on union variants.
type ImplementRule struct {
	Union               string `json:"union" yaml:"union" jsonschema:"description=Union type name (schema or Go name)"`
	DiscriminatorMethod string `json:"discriminator_method,omitempty" yaml:"discriminator_method,omitempty" jsonschema:"description=Method name for discriminator accessor (default: DiscriminatorValue)"`
}

// Load reads a config file (YAML or JSON).
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(data)
}

// Parse parses and validates config from YAML/JSON bytes.
func Parse(data []byte) (*File, error) {
	// First validate the raw structure against the JSON Schema.
	// Unmarshal as generic data to preserve unknown fields for validation.
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// Validate against the generated JSON Schema.
	// Strip "source" fields before validation (polymorphic, not in schema).
	normalized := normalizeYAML(raw)
	stripSourceFields(normalized)
	jsonData, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal config for validation: %w", err)
	}
	var zero File
	if err := zero.Validate(jsonData); err != nil {
		return nil, fmt.Errorf("config validation: %w", err)
	}

	// Now unmarshal into the typed struct.
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Structural checks that JSON Schema can't express.
	for name, pipeline := range f.Pipelines {
		for i, action := range pipeline {
			if action.Action == "" {
				return nil, fmt.Errorf("pipeline %q step %d: 'action' is required", name, i+1)
			}
		}
	}
	return &f, nil
}

// normalizeYAML converts YAML-decoded data (map[string]any with possible
// map[any]any from YAML) into JSON-compatible form.
func normalizeYAML(v any) any {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, v := range val {
			result[k] = normalizeYAML(v)
		}
		return result
	case map[any]any:
		result := make(map[string]any, len(val))
		for k, v := range val {
			result[fmt.Sprint(k)] = normalizeYAML(v)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, v := range val {
			result[i] = normalizeYAML(v)
		}
		return result
	default:
		return v
	}
}

// stripSourceFields removes "source" keys from action maps before JSON Schema
// validation. The source field is polymorphic (string|map|array) and handled
// separately from the schema-validated fields.
func stripSourceFields(v any) {
	switch val := v.(type) {
	case map[string]any:
		// If this looks like an action (has "action" key), strip "source".
		if _, ok := val["action"]; ok {
			delete(val, "source")
		}
		for _, child := range val {
			stripSourceFields(child)
		}
	case []any:
		for _, item := range val {
			stripSourceFields(item)
		}
	}
}

// DefaultConfigPaths returns the paths to search for a config file.
func DefaultConfigPaths() []string {
	return []string{
		".compschema.yaml",
		".compschema.yml",
		".compschema.json",
		"compschema.yaml",
		"compschema.yml",
		"compschema.json",
	}
}

// FindConfig searches for a config file in the default paths.
// Returns "" if none found.
func FindConfig() string {
	for _, p := range DefaultConfigPaths() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
