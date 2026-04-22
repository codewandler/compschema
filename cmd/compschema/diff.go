package main

import (
	"fmt"

	"github.com/codewandler/compschema/internal/schemadiff"
	"github.com/spf13/cobra"
)

func newDiffCmd() *cobra.Command {
	var useIR bool

	cmd := &cobra.Command{
		Use:   "diff <ground-truth.json> <generated.json>",
		Short: "Compare two JSON Schemas and report structural differences",
		Long: `Structurally compare a ground-truth JSON Schema against a generated one.

With --ir, both schemas are parsed into the Schema IR first, which
normalizes away superficial differences like $ref vs inline, array
ordering, nullable representation, and annotation-only differences.`,
		Example: `  compschema diff testdata/openai/responses.schema.json examples/openai/schema.gen.json
  compschema diff --ir testdata/openai/responses.schema.json examples/openai/schema.gen.json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if useIR {
				report, err := schemadiff.CompareIR(args[0], args[1])
				if err != nil {
					return err
				}
				fmt.Print(schemadiff.FormatIRReport(report))
			} else {
				report, err := schemadiff.Compare(args[0], args[1])
				if err != nil {
					return err
				}
				fmt.Print(schemadiff.FormatReport(report))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&useIR, "ir", false, "compare via Schema IR (normalizes $ref, ordering, nullable)")

	return cmd
}
