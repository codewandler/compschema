package main

import (
	"fmt"
	"strings"

	"github.com/codewandler/compschema/internal/importer"
	"github.com/spf13/cobra"
)

func newImportCmd() *cobra.Command {
	var (
		pkg          string
		outPath      string
		renames      []string
		excludes     []string
		tags         []string
		implements   []string
		constructors bool
	)

	cmd := &cobra.Command{
		Use:   "import <schema.json>",
		Short: "Generate Go structs from a JSON Schema file",
		Long: `Import a JSON Schema file and generate Go structs with proper
jsonschema:"..." tags, sealed interfaces for oneOf/anyOf unions,
and enum constants. This replaces external JSON Schema importers with a generator
that preserves constraints for perfect round-trip with compschema generate.`,
		Example: `  compschema import --package models --out types.go schema.json
  compschema import --package api --out api.go --rename CompactionBody=CompactionItem responses.schema.json
  compschema import --package api --out api.go --exclude 'Response*Event' --exclude '*Param' schema.json
  compschema import --package api --out api.go --tags yaml schema.json
  compschema import --package api --out api.go --implement 'ResponseStreamEvent=EventType' schema.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			schemaPath := args[0]

			if outPath == "" {
				return fmt.Errorf("--out is required")
			}

			cfg := importer.Config{
				Package:      pkg,
				Exclude:      excludes,
				Tags:         tags,
				Constructors: constructors,
			}

			// Parse --rename Foo=Bar flags into the map.
			if len(renames) > 0 {
				cfg.Rename = make(map[string]string, len(renames))
				for _, r := range renames {
					parts := strings.SplitN(r, "=", 2)
					if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
						return fmt.Errorf("invalid --rename %q: expected From=To", r)
					}
					cfg.Rename[parts[0]] = parts[1]
				}
			}

			// Parse --implement Union=MethodName flags.
			for _, impl := range implements {
				parts := strings.SplitN(impl, "=", 2)
				rule := importer.ImplementRule{Union: parts[0]}
				if len(parts) == 2 {
					rule.DiscriminatorMethod = parts[1]
				}
				cfg.Implement = append(cfg.Implement, rule)
			}

			return importer.ImportFromFileWithConfig(schemaPath, outPath, cfg)
		},
	}

	cmd.Flags().StringVar(&pkg, "package", "main", "Go package name")
	cmd.Flags().StringVar(&outPath, "out", "", "output Go file path")
	cmd.Flags().StringSliceVar(&renames, "rename", nil, "rename types: SchemaName=GoName (repeatable)")
	cmd.Flags().StringArrayVar(&excludes, "exclude", nil, "glob patterns for types to exclude (repeatable)")
	cmd.Flags().StringSliceVar(&tags, "tags", nil, "additional struct tags to emit (e.g. yaml)")
	cmd.Flags().StringArrayVar(&implements, "implement", nil, "accessor on union variants: Union=MethodName (repeatable)")
	cmd.Flags().BoolVar(&constructors, "constructors", false, "generate NewT() constructors for struct types")
	_ = cmd.MarkFlagRequired("out")

	return cmd
}
