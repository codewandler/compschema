package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newGenerateCmd() *cobra.Command {
	var (
		packages []string
	)

	cmd := &cobra.Command{
		Use:   "generate [packages...]",
		Short: "Generate JSON Schema + Decode/Validate from Go types",
		Long: `Analyze Go packages using go/ast + go/types, build a Schema IR from
types annotated with //compschema:generate, and emit:

  - JSON Schema (draft 2020-12) documents
  - Type-safe Decode([]byte) (T, error) functions
  - Validate([]byte) error functions

This is the core compschema command. It runs at go generate time.`,
		Example: `  # Generate for the current package
  compschema generate ./...

  # Generate for a specific package
  compschema generate ./models/`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			packages = args
			// TODO: implement analyzer → IR → emitters pipeline
			fmt.Printf("generate: analyzing %v (not yet implemented)\n", packages)
			return nil
		},
	}

	return cmd
}
