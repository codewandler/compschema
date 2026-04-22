package main

import (
	"fmt"
	"os"

	"github.com/codewandler/compschema/internal/analyzer"
	"github.com/codewandler/compschema/internal/config"
	"github.com/codewandler/compschema/internal/emitter"
	"github.com/codewandler/compschema/internal/importer"
	"github.com/codewandler/compschema/internal/openapi2jsonschema"
	"github.com/spf13/cobra"
	"path/filepath"
)

func newRunCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "run <pipeline>",
		Short: "Run a named pipeline from a config file",
		Long: `Execute a named pipeline defined in .compschema.yaml (or --config).

Each pipeline is an ordered list of actions (extract, import, generate, diff)
with their flags specified in the config file.`,
		Example: `  # Run the "openai" pipeline from .compschema.yaml
  compschema run openai

  # Use a custom config file
  compschema run --config my-config.yaml openai`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pipelineName := args[0]

			// Find config file.
			if configPath == "" {
				configPath = config.FindConfig()
				if configPath == "" {
					return fmt.Errorf("no config file found (tried %v)", config.DefaultConfigPaths())
				}
			}

			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			pipeline, ok := cfg.Pipelines[pipelineName]
			if !ok {
				names := make([]string, 0, len(cfg.Pipelines))
				for name := range cfg.Pipelines {
					names = append(names, name)
				}
				return fmt.Errorf("pipeline %q not found in %s (available: %v)", pipelineName, configPath, names)
			}

			fmt.Fprintf(os.Stderr, "▶ pipeline %q (%d steps) from %s\n", pipelineName, len(pipeline), configPath)

			for i, action := range pipeline {
				fmt.Fprintf(os.Stderr, "\n── step %d: %s ──\n", i+1, action.Action)
				if err := runAction(action); err != nil {
					return fmt.Errorf("step %d (%s): %w", i+1, action.Action, err)
				}
			}

			fmt.Fprintf(os.Stderr, "\n✓ pipeline %q complete\n", pipelineName)
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path (default: auto-detect)")

	return cmd
}

func runAction(a config.Action) error {
	switch a.Action {
	case "extract":
		return runExtractAction(a)
	case "import":
		return runImportAction(a)
	case "generate":
		return runGenerateAction(a)
	default:
		return fmt.Errorf("unknown action %q", a.Action)
	}
}

func runExtractAction(a config.Action) error {
	if a.Spec == "" {
		return fmt.Errorf("'spec' is required")
	}
	if a.Out == "" {
		return fmt.Errorf("'out' is required")
	}
	if a.Path == "" {
		return fmt.Errorf("'path' is required")
	}

	conv, err := openapi2jsonschema.New(a.Spec)
	if err != nil {
		return err
	}

	result, err := conv.ExtractEndpointSchemas(a.Path)
	if err != nil {
		return err
	}

	if a.ValidateSchema {
		if errs := openapi2jsonschema.ValidateMetaSchema(result); len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintf(os.Stderr, "  schema error: %s\n", e)
			}
			return fmt.Errorf("generated schema is not valid JSON Schema")
		}
		fmt.Fprintf(os.Stderr, "  ✓ valid JSON Schema (draft 2020-12)\n")
	}

	if err := os.MkdirAll(filepath.Dir(a.Out), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(a.Out, result, 0644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "  ✓ wrote %s (%d bytes)\n", a.Out, len(result))
	return nil
}

func runImportAction(a config.Action) error {
	if a.Schema == "" {
		return fmt.Errorf("'schema' is required")
	}
	if a.Out == "" {
		return fmt.Errorf("'out' is required")
	}
	if a.Package == "" {
		return fmt.Errorf("'package' is required")
	}

	cfg := importer.Config{
		Package: a.Package,
		Rename:  a.Rename,
		Exclude: a.Exclude,
	}

	return importer.ImportFromFileWithConfig(a.Schema, a.Out, cfg)
}

func runGenerateAction(a config.Action) error {
	packages := a.Packages
	if len(packages) == 0 {
		packages = []string{"./..."}
	}

	pkgs, err := analyzer.Analyze(a.All, packages...)
	if err != nil {
		return fmt.Errorf("analyze: %w", err)
	}

	if len(pkgs) == 0 {
		fmt.Fprintf(os.Stderr, "  no types found\n")
		return nil
	}

	for _, pkg := range pkgs {
		dir := a.Out
		if dir == "" {
			dir = pkg.Dir
		}
		if dir == "" {
			dir = "."
		}

		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}

		fmt.Fprintf(os.Stderr, "  package %s: %d types\n", pkg.Name, len(pkg.Types))

		schemaJSON, inlined, err := emitter.JSONSchema(pkg)
		if err != nil {
			return fmt.Errorf("emit schema: %w", err)
		}

		if a.ValidateSchema {
			if errs := openapi2jsonschema.ValidateMetaSchema(schemaJSON); len(errs) > 0 {
				for _, e := range errs {
					fmt.Fprintf(os.Stderr, "    schema error: %s\n", e)
				}
				return fmt.Errorf("generated schema is not valid JSON Schema")
			}
			fmt.Fprintf(os.Stderr, "    ✓ schema valid\n")
		}

		schemaPath := filepath.Join(dir, "schema.gen.json")
		if err := os.WriteFile(schemaPath, schemaJSON, 0644); err != nil {
			return fmt.Errorf("write schema: %w", err)
		}
		fmt.Fprintf(os.Stderr, "    ✓ %s\n", schemaPath)

		goCode := emitter.GoCodegen(pkg, inlined)
		goPath := filepath.Join(dir, "compschema.gen.go")
		if err := os.WriteFile(goPath, []byte(goCode), 0644); err != nil {
			return fmt.Errorf("write codegen: %w", err)
		}
		fmt.Fprintf(os.Stderr, "    ✓ %s\n", goPath)

		testCode := emitter.GoTests(pkg, inlined)
		testPath := filepath.Join(dir, "compschema.gen_test.go")
		if err := os.WriteFile(testPath, []byte(testCode), 0644); err != nil {
			return fmt.Errorf("write tests: %w", err)
		}
		fmt.Fprintf(os.Stderr, "    ✓ %s\n", testPath)
	}

	return nil
}
