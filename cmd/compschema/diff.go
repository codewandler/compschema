package main

import (
	"fmt"

	"github.com/codewandler/compschema/internal/schemadiff"
	"github.com/spf13/cobra"
)

func newDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff <ground-truth.json> <generated.json>",
		Short: "Compare two JSON Schemas and report structural differences",
		Long: `Structurally compare a ground-truth JSON Schema against a generated one.
Reports missing keywords, value mismatches, and classifies gaps as
structural (affects validation) or annotation (metadata only).`,
		Example: `  compschema diff testdata/openai/responses.schema.json examples/openai/schema.gen.json`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := schemadiff.Compare(args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Print(schemadiff.FormatReport(report))
			return nil
		},
	}
	return cmd
}
