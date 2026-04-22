package main

import (
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "compschema",
		Short: "Compile-time JSON Schema generator for Go",
		Long: `compschema generates JSON Schema documents, Decode, and Validate functions
from Go types using static analysis (go/ast + go/types) — zero reflection at runtime.

It also includes tooling for importing schemas from OpenAPI specs.`,
	}

	root.AddCommand(
		newExtractCmd(),
		newSchemasCmd(),
		newGenerateCmd(),
		newUniongenCmd(),
		newDiffCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
