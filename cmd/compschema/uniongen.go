package main

import (
	"fmt"
	"os"

	"github.com/codewandler/compschema/internal/uniongen"
	"github.com/spf13/cobra"
)

func newUniongenCmd() *cobra.Command {
	var (
		schemaPath      string
		pkg             string
		outPath         string
		patchPath       string
		capitalizations []string
	)

	cmd := &cobra.Command{
		Use:   "uniongen",
		Short: "Generate sealed interfaces for JSON Schema oneOf/anyOf unions",
		Long: `Analyze a JSON Schema file for oneOf/anyOf union types and generate
Go sealed interfaces with discriminator-based UnmarshalJSON functions.

This replaces 'type X interface{}' declarations produced by go-jsonschema
with proper sealed interfaces that enforce type safety at compile time.

If --patch is specified, conflicting interface{} declarations are removed
from that file.`,
		Example: `  compschema uniongen --schema responses.schema.json --package myapi --out unions.gen.go --patch types.go`,
		RunE: func(cmd *cobra.Command, args []string) error {
			unions, err := uniongen.AnalyzeSchema(schemaPath)
			if err != nil {
				return fmt.Errorf("analyze: %w", err)
			}

			code := uniongen.GenerateUnionCode(pkg, unions, capitalizations...)

			if outPath != "" {
				if err := os.WriteFile(outPath, []byte(code), 0644); err != nil {
					return fmt.Errorf("write output: %w", err)
				}
				fmt.Fprintf(os.Stderr, "✓ generated %d unions → %s (%d bytes)\n", len(unions), outPath, len(code))
			} else {
				os.Stdout.Write([]byte(code))
			}

			// Patch the types file to remove conflicting interface{} declarations
			if patchPath != "" {
				generated := uniongen.GeneratedUnionNames(unions)
				typesData, err := os.ReadFile(patchPath)
				if err != nil {
					return fmt.Errorf("read patch target: %w", err)
				}
				patched := uniongen.PatchGeneratedTypes(string(typesData), generated)
				if err := os.WriteFile(patchPath, []byte(patched), 0644); err != nil {
					return fmt.Errorf("write patch: %w", err)
				}
				fmt.Fprintf(os.Stderr, "✓ patched %s (removed %d interface{} declarations)\n", patchPath, len(generated))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&schemaPath, "schema", "", "path to JSON Schema file")
	cmd.Flags().StringVar(&pkg, "package", "", "Go package name")
	cmd.Flags().StringVar(&outPath, "out", "", "output file (default: stdout)")
	cmd.Flags().StringVar(&patchPath, "patch", "", "Go source file to patch (removes conflicting interface{} decls)")
	cmd.Flags().StringSliceVar(&capitalizations, "capitalization", nil, "Go capitalization rules (e.g. URL, ID, API)")
	_ = cmd.MarkFlagRequired("schema")
	_ = cmd.MarkFlagRequired("package")

	return cmd
}
