package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/codewandler/compschema/internal/analyzer"
	"github.com/codewandler/compschema/internal/emitter"
	"github.com/codewandler/compschema/internal/openapi2jsonschema"
	"github.com/spf13/cobra"
)

func newGenerateCmd() *cobra.Command {
	var (
		outDir   string
		validate bool
		allTypes bool
	)

	cmd := &cobra.Command{
		Use:   "generate [packages...]",
		Short: "Generate JSON Schema + Decode/Validate from Go types",
		Long: `Analyze Go packages using go/ast + go/types, build a Schema IR from
types annotated with //compschema:generate, and emit:

  - schema.gen.json      — JSON Schema (draft 2020-12) with all types as $defs
  - compschema.gen.go    — JSONSchemaBytes, Validate, Decode per type
  - compschema_test.gen.go — smoke tests (schema validity, validation, round-trip)`,
		Example: `  # Generate for the current package
  compschema generate ./...

  # Generate for a specific package, output to a custom dir
  compschema generate --out ./generated ./models/`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkgs, err := analyzer.Analyze(allTypes, args...)
			if err != nil {
				return fmt.Errorf("analyze: %w", err)
			}

			if len(pkgs) == 0 {
				fmt.Fprintf(os.Stderr, "no types with //compschema:generate found\n")
				return nil
			}

			for _, pkg := range pkgs {
				dir := outDir
				if dir == "" {
					// Default: write to the package's source directory.
					// For patterns like ./..., resolve from the working directory.
					dir = "."
				}

				fmt.Fprintf(os.Stderr, "package %s: %d types\n", pkg.Name, len(pkg.Types))

				// 1. JSON Schema
				schemaJSON, err := emitter.JSONSchema(pkg)
				if err != nil {
					return fmt.Errorf("emit schema: %w", err)
				}

				if validate {
					if errs := openapi2jsonschema.ValidateMetaSchema(schemaJSON); len(errs) > 0 {
						for _, e := range errs {
							fmt.Fprintf(os.Stderr, "  schema error: %s\n", e)
						}
						return fmt.Errorf("generated schema is not valid JSON Schema")
					}
					fmt.Fprintf(os.Stderr, "  ✓ schema valid (meta-schema)\n")
				}

				schemaPath := filepath.Join(dir, "schema.gen.json")
				if err := os.WriteFile(schemaPath, schemaJSON, 0644); err != nil {
					return fmt.Errorf("write schema: %w", err)
				}
				fmt.Fprintf(os.Stderr, "  ✓ %s (%d bytes)\n", schemaPath, len(schemaJSON))

				// 2. Go codegen
				goCode := emitter.GoCodegen(pkg)
				goPath := filepath.Join(dir, "compschema.gen.go")
				if err := os.WriteFile(goPath, []byte(goCode), 0644); err != nil {
					return fmt.Errorf("write codegen: %w", err)
				}
				fmt.Fprintf(os.Stderr, "  ✓ %s\n", goPath)

				// 3. Tests
				testCode := emitter.GoTests(pkg)
				testPath := filepath.Join(dir, "compschema.gen_test.go")
				if err := os.WriteFile(testPath, []byte(testCode), 0644); err != nil {
					return fmt.Errorf("write tests: %w", err)
				}
				fmt.Fprintf(os.Stderr, "  ✓ %s\n", testPath)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "", "output directory (default: package source dir)")
	cmd.Flags().BoolVar(&validate, "validate", false, "validate generated schema against meta-schema")
	cmd.Flags().BoolVar(&allTypes, "all", false, "analyze all exported types (not just annotated)")

	return cmd
}
