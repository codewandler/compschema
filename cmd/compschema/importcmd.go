package main

import (
	"fmt"

	"github.com/codewandler/compschema/internal/importer"
	"github.com/spf13/cobra"
)

func newImportCmd() *cobra.Command {
	var (
		pkg    string
		outPath string
	)

	cmd := &cobra.Command{
		Use:   "import <schema.json>",
		Short: "Generate Go structs from a JSON Schema file",
		Long: `Import a JSON Schema file and generate Go structs with proper
jsonschema:"..." tags, sealed interfaces for oneOf/anyOf unions,
and enum constants. This replaces go-jsonschema with a generator
that preserves constraints for perfect round-trip with compschema generate.`,
		Example: `  compschema import --package models --out types.go schema.json
  compschema import --package api --out api.go responses.schema.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			schemaPath := args[0]

			if outPath == "" {
				return fmt.Errorf("--out is required")
			}

			return importer.ImportFromFile(schemaPath, pkg, outPath)
		},
	}

	cmd.Flags().StringVar(&pkg, "package", "main", "Go package name")
	cmd.Flags().StringVar(&outPath, "out", "", "output Go file path")
	_ = cmd.MarkFlagRequired("out")

	return cmd
}
