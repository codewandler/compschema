package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/codewandler/compschema/internal/openapi2jsonschema"
	"github.com/spf13/cobra"
)

func newExtractCmd() *cobra.Command {
	var (
		specPath   string
		pathPrefix string
		schemaName string
		outPath    string
		validate   bool
	)

	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract JSON Schema from an OpenAPI spec",
		Long: `Extract component schemas from an OpenAPI 3.x specification and convert
them into self-contained JSON Schema (draft 2020-12) documents.

Schemas can be selected by API path prefix (--path) or by component name (--schema).
All transitive $ref dependencies are resolved and included as $defs.

Use --validate to verify the output against the JSON Schema draft 2020-12 meta-schema.`,
		Example: `  # Extract all schemas for the /responses endpoints
  compschema extract --spec openapi.yaml --path /responses --out responses.schema.json

  # Extract a single named schema
  compschema extract --spec openapi.yaml --schema CreateResponse --out create.schema.json

  # Extract and validate against meta-schema
  compschema extract --spec openapi.yaml --path /responses --validate --out responses.schema.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if specPath == "" {
				return fmt.Errorf("--spec is required")
			}
			if pathPrefix == "" && schemaName == "" {
				return fmt.Errorf("either --path or --schema is required")
			}

			conv, err := openapi2jsonschema.New(specPath)
			if err != nil {
				return fmt.Errorf("parse spec: %w", err)
			}

			var data []byte
			if schemaName != "" {
				data, err = conv.ExtractSchema(schemaName)
			} else {
				data, err = conv.ExtractEndpointSchemas(pathPrefix)
			}
			if err != nil {
				return fmt.Errorf("extract: %w", err)
			}

			// Meta-schema validation
			if validate {
				if errs := openapi2jsonschema.ValidateMetaSchema(data); len(errs) > 0 {
					fmt.Fprintf(os.Stderr, "meta-schema validation failed with %d error(s):\n", len(errs))
					for i, e := range errs {
						fmt.Fprintf(os.Stderr, "  %d. %s\n", i+1, e)
					}
					return fmt.Errorf("output is not valid JSON Schema draft 2020-12")
				}
				fmt.Fprintf(os.Stderr, "✓ valid JSON Schema (draft 2020-12)\n")
			}

			if outPath != "" {
				if err := os.WriteFile(outPath, data, 0644); err != nil {
					return fmt.Errorf("write output: %w", err)
				}
				fmt.Fprintf(os.Stderr, "✓ wrote %s (%d bytes)\n", outPath, len(data))
			} else {
				_, _ = os.Stdout.Write(data)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&specPath, "spec", "", "path to OpenAPI YAML/JSON file")
	cmd.Flags().StringVar(&pathPrefix, "path", "", "API path prefix to extract (e.g. /responses)")
	cmd.Flags().StringVar(&schemaName, "schema", "", "single component schema name to extract")
	cmd.Flags().StringVar(&outPath, "out", "", "output file (default: stdout)")
	cmd.Flags().BoolVar(&validate, "validate", false, "validate output against JSON Schema meta-schema")

	_ = cmd.MarkFlagRequired("spec")

	return cmd
}

func newSchemasCmd() *cobra.Command {
	var specPath string

	cmd := &cobra.Command{
		Use:   "schemas",
		Short: "List component schema names in an OpenAPI spec",
		Example: `  compschema schemas --spec openapi.yaml
  compschema schemas --spec openapi.yaml | grep Response`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if specPath == "" {
				return fmt.Errorf("--spec is required")
			}

			conv, err := openapi2jsonschema.New(specPath)
			if err != nil {
				return fmt.Errorf("parse spec: %w", err)
			}

			names := conv.ListSchemaNames()
			fmt.Printf("Found %d component schemas:\n", len(names))

			groups := map[string][]string{}
			for _, name := range names {
				prefix := "Other"
				for _, p := range []string{"Response", "Create", "Input", "Output"} {
					if strings.HasPrefix(name, p) {
						prefix = p + "*"
						break
					}
				}
				groups[prefix] = append(groups[prefix], name)
			}

			for group, gnames := range groups {
				fmt.Printf("\n  %s (%d):\n", group, len(gnames))
				for _, n := range gnames {
					fmt.Printf("    %s\n", n)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&specPath, "spec", "", "path to OpenAPI YAML/JSON file")
	_ = cmd.MarkFlagRequired("spec")

	return cmd
}
