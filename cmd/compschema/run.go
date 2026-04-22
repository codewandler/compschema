package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/codewandler/compschema/internal/analyzer"
	"github.com/codewandler/compschema/internal/cache"
	"github.com/codewandler/compschema/internal/config"
	"github.com/codewandler/compschema/internal/emitter"
	"github.com/codewandler/compschema/internal/importer"
	"github.com/codewandler/compschema/internal/ir"
	"github.com/codewandler/compschema/internal/openapi2jsonschema"
	"github.com/codewandler/compschema/internal/report"
	"github.com/codewandler/compschema/internal/source"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"
)

func newRunCmd() *cobra.Command {
	var (
		configPath string
		reportPath string
	)

	cmd := &cobra.Command{
		Use:   "run <pipeline>",
		Short: "Run a named pipeline from a config file",
		Long: `Execute a named pipeline defined in .compschema.yaml (or --config).

Each pipeline is an ordered list of actions (extract, import, generate, diff)
with their flags specified in the config file.`,
		Example: `  # Run the "openai" pipeline from .compschema.yaml
  compschema run openai

  # Use a custom config file
  compschema run --config my-config.yaml openai

  # Write a report file
  compschema run --report report.yaml openai`,
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

			// Check for multi-source expansion.
			multiSources := findMultiSources(pipeline)
			if len(multiSources) > 0 {
				return runMultiSourcePipeline(pipelineName, pipeline, multiSources, reportPath)
			}

			// Single-source pipeline: run steps sequentially.
			pr, err := runSinglePipeline(pipelineName, pipeline)
			if err != nil {
				return err
			}

			// Write report file if requested.
			if reportPath != "" {
				data, err := pr.YAML()
				if err != nil {
					return fmt.Errorf("marshal report: %w", err)
				}
				if err := os.WriteFile(reportPath, data, 0644); err != nil {
					return fmt.Errorf("write report: %w", err)
				}
				fmt.Fprintf(os.Stderr, "✓ report written to %s\n", reportPath)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path (default: auto-detect)")
	cmd.Flags().StringVar(&reportPath, "report", "", "write pipeline report to file (YAML)")

	return cmd
}

func runAction(a config.Action) (*report.StepReport, error) {
	start := time.Now()
	sr := report.NewStepReport(a.Action)

	var err error
	switch a.Action {
	case "extract":
		err = runExtractAction(a, sr)
	case "import":
		err = runImportAction(a, sr)
	case "generate":
		err = runGenerateAction(a, sr)
	default:
		err = fmt.Errorf("unknown action %q", a.Action)
	}

	sr.Duration = time.Since(start)
	if err != nil {
		return nil, err
	}
	return sr, nil
}

func runExtractAction(a config.Action, sr *report.StepReport) error {
	specPath := a.Spec

	// Resolve source if set (overrides spec).
	if a.Source != nil {
		path, meta, isTemp, err := fetchSourceToTemp(a.Source)
		if err != nil {
			return fmt.Errorf("fetch source: %w", err)
		}
		if isTemp {
			defer os.Remove(path)
		}
		specPath = path
		sr.Set("source_hash", meta.Hash)
	}

	if specPath == "" {
		return fmt.Errorf("'spec' or 'source' is required")
	}
	if a.Out == "" {
		return fmt.Errorf("'out' is required")
	}
	if a.Path == "" {
		return fmt.Errorf("'path' is required")
	}

	conv, err := openapi2jsonschema.New(specPath)
	if err != nil {
		return err
	}

	result, err := conv.ExtractEndpointSchemas(a.Path)
	if err != nil {
		return err
	}

	// Count $defs for metrics.
	var doc map[string]json.RawMessage
	if json.Unmarshal(result, &doc) == nil {
		if defs, ok := doc["$defs"]; ok {
			var defsMap map[string]json.RawMessage
			if json.Unmarshal(defs, &defsMap) == nil {
				sr.Set("schemas", len(defsMap))
			}
		}
	}
	sr.Set("bytes", len(result))

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

func runImportAction(a config.Action, sr *report.StepReport) error {
	schemaPath := a.Schema

	// Resolve source if set (overrides schema).
	if a.Source != nil {
		path, meta, isTemp, err := fetchSourceToTemp(a.Source)
		if err != nil {
			return fmt.Errorf("fetch source: %w", err)
		}
		if isTemp {
			defer os.Remove(path)
		}
		schemaPath = path
		sr.Set("source_hash", meta.Hash)
	}

	if schemaPath == "" {
		return fmt.Errorf("'schema' or 'source' is required")
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

	if err := importer.ImportFromFileWithConfig(schemaPath, a.Out, cfg); err != nil {
		return err
	}

	// Collect metrics from the generated file.
	if data, err := os.ReadFile(a.Out); err == nil {
		types := 0
		for _, line := range splitLines(data) {
			if len(line) > 5 && string(line[:5]) == "type " {
				types++
			}
		}
		sr.Set("types", types)
	}
	sr.Set("renamed", len(a.Rename))
	sr.Set("excluded_patterns", len(a.Exclude))

	return nil
}

func runGenerateAction(a config.Action, sr *report.StepReport) error {
	packages := a.Packages
	if len(packages) == 0 {
		packages = []string{"./..."}
	}

	// If the first package path is absolute and outside the current module,
	// set up a temporary Go module so analysis works.
	if len(packages) > 0 && filepath.IsAbs(packages[0]) {
		dir := packages[0]
		goModPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goModPath); os.IsNotExist(err) {
			// Derive module name from directory base.
			modName := filepath.Base(dir)
			goMod := fmt.Sprintf("module %s\n\ngo 1.24\n", modName)
			if err := os.WriteFile(goModPath, []byte(goMod), 0644); err != nil {
				return fmt.Errorf("create go.mod: %w", err)
			}
			// Build first to verify compilation.
			buildCmd := exec.Command("go", "build", "./...")
			buildCmd.Dir = dir
			if out, err := buildCmd.CombinedOutput(); err != nil {
				return fmt.Errorf("go build: %s: %w", out, err)
			}
			// Set packages to relative for the module.
			packages = []string{"."}
			// Don't clean up go.mod — keep it for subsequent cached runs.
		}
	}

	// If package is absolute, we need to run analyzer from that directory.
	// We use a subprocess-style approach to avoid os.Chdir which is process-global.
	var pkgs []*ir.Package
	var err error
	if len(a.Packages) > 0 && filepath.IsAbs(a.Packages[0]) {
		origDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		if err := os.Chdir(a.Packages[0]); err != nil {
			return fmt.Errorf("chdir to %s: %w", a.Packages[0], err)
		}
		defer os.Chdir(origDir)
		pkgs, err = analyzer.Analyze(a.All, packages...)
	} else {
		pkgs, err = analyzer.Analyze(a.All, packages...)
	}
	if err != nil {
		return fmt.Errorf("analyze: %w", err)
	}

	if len(pkgs) == 0 {
		fmt.Fprintf(os.Stderr, "  no types found\n")
		return nil
	}

	totalTypes := 0
	totalDefs := 0
	cachedTypes := 0
	var testResults *report.TestResults

	// Load cache for pipeline generate.
	c := cache.Load(cache.DefaultPath())

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
		totalTypes += len(pkg.Types)

		// Check cache — skip if IR hash unchanged.
		irHash := fmt.Sprintf("%x", pkg.Hash())
		cacheKey := dir
		if !c.IsStale(cacheKey, irHash) {
			fmt.Fprintf(os.Stderr, "    ✓ unchanged (%d types cached)\n", len(pkg.Types))
			cachedTypes += len(pkg.Types)
			continue
		}

		schemaJSON, inlined, err := emitter.JSONSchema(pkg)
		if err != nil {
			return fmt.Errorf("emit schema: %w", err)
		}

		// Count $defs.
		var doc map[string]json.RawMessage
		if json.Unmarshal(schemaJSON, &doc) == nil {
			if defs, ok := doc["$defs"]; ok {
				var defsMap map[string]json.RawMessage
				if json.Unmarshal(defs, &defsMap) == nil {
					totalDefs += len(defsMap)
				}
			}
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

		// IR YAML (optional)
		if a.EmitIR {
			irYAML, err := emitter.IRToYAML(pkg)
			if err != nil {
				return fmt.Errorf("emit IR: %w", err)
			}
			irPath := filepath.Join(dir, "schema.gen.ir.yaml")
			if err := os.WriteFile(irPath, irYAML, 0644); err != nil {
				return fmt.Errorf("write IR: %w", err)
			}
			fmt.Fprintf(os.Stderr, "    ✓ %s\n", irPath)
		}

		// Run tests (optional)
		if a.Test {
			passed, failed, skipped, err := runGeneratedTests(dir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "    ⚠ test runner error: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "    tests: %d passed, %d failed, %d skipped\n", passed, failed, skipped)
				total := passed + failed + skipped
				rate := float64(0)
				if total > 0 {
					rate = float64(passed) / float64(total) * 100
				}
				testResults = &report.TestResults{
					Total:   total,
					Passed:  passed,
					Failed:  failed,
					Skipped: skipped,
					Rate:    rate,
				}
			}
		}

		// Update cache.
		typeHashes := make(map[string]string, len(pkg.Types))
		for name, t := range pkg.Types {
			typeHashes[name] = fmt.Sprintf("%x", t.Hash())
		}
		c.Set(cacheKey, &cache.Entry{IRHash: irHash, TypeHashes: typeHashes})
	}

	// Save cache.
	if err := c.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ cache save: %v\n", err)
	}

	sr.Set("types", totalTypes)
	sr.Set("defs", totalDefs)
	if cachedTypes > 0 {
		sr.Set("cached_types", cachedTypes)
	}
	if testResults != nil {
		sr.Set("tests", *testResults)
	}

	return nil
}

// splitLines splits byte data into lines (helper for counting types).
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

// fetchSourceToTemp parses a raw source value, fetches the data, and writes
// it to a temp file. Returns the temp file path and metadata.
// The caller must remove the temp file when done (unless it's a FileSource,
// which returns the original path).
func fetchSourceToTemp(raw any) (path string, meta source.Meta, isTemp bool, err error) {
	sources, err := source.Parse(raw)
	if err != nil {
		return "", source.Meta{}, false, err
	}
	if len(sources) == 0 {
		return "", source.Meta{}, false, fmt.Errorf("no sources parsed")
	}
	if len(sources) > 1 {
		return "", source.Meta{}, false, fmt.Errorf("multi-source not supported here; use pipeline multi-source expansion")
	}

	src := sources[0]

	// For file sources, just return the path directly (no temp file needed).
	if fs, ok := src.(*source.FileSource); ok {
		_, meta, err := fs.Fetch()
		if err != nil {
			return "", source.Meta{}, false, err
		}
		return fs.Path, meta, false, nil
	}

	data, meta, err := src.Fetch()
	if err != nil {
		return "", source.Meta{}, false, err
	}

	tmpFile, err := os.CreateTemp("", "compschema-source-*")
	if err != nil {
		return "", source.Meta{}, false, fmt.Errorf("create temp file: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", source.Meta{}, false, fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	fmt.Fprintf(os.Stderr, "  ✓ fetched %s → %s (%d bytes, %s)\n", src, tmpFile.Name(), len(data), meta.Hash[:15]+"...")
	return tmpFile.Name(), meta, true, nil
}

// runSinglePipeline runs a single pipeline (no multi-source expansion).
func runSinglePipeline(name string, pipeline []config.Action) (*report.PipelineReport, error) {
	pipelineStart := time.Now()
	pr := &report.PipelineReport{Pipeline: name}

	for i, action := range pipeline {
		fmt.Fprintf(os.Stderr, "\n── step %d: %s ──\n", i+1, action.Action)
		stepReport, err := runAction(action)
		if err != nil {
			return nil, fmt.Errorf("step %d (%s): %w", i+1, action.Action, err)
		}
		if stepReport != nil {
			pr.Steps = append(pr.Steps, *stepReport)
		}
	}

	pr.Duration = time.Since(pipelineStart)
	fmt.Fprintf(os.Stderr, "\n✓ pipeline %q complete\n", name)
	pr.Print(os.Stderr)
	return pr, nil
}

// findMultiSources finds the first step with a Source that is an array.
// Returns the parsed source list, or nil if no multi-source is found.
func findMultiSources(pipeline []config.Action) []source.Source {
	for _, action := range pipeline {
		if action.Source == nil {
			continue
		}
		if arr, ok := action.Source.([]any); ok && len(arr) > 0 {
			sources, err := source.Parse(action.Source)
			if err != nil {
				continue
			}
			return sources
		}
	}
	return nil
}

// runMultiSourcePipeline expands a pipeline for each source in the multi-source
// list and runs each expanded pipeline sequentially.
func runMultiSourcePipeline(name string, pipeline []config.Action, sources []source.Source, reportPath string) error {
	fmt.Fprintf(os.Stderr, "  multi-source: %d sources\n", len(sources))

	overallStart := time.Now()
	var allReports []*report.PipelineReport
	passCount, failCount := 0, 0

	for _, src := range sources {
		srcName := source.Name(src)
		hashShort := ""

		// For file sources, get hash upfront for the template.
		if fs, ok := src.(*source.FileSource); ok {
			_, meta, err := fs.Fetch()
			if err == nil && len(meta.Hash) > 15 {
				hashShort = meta.Hash[7:15] // skip "sha256:" prefix, take 8 chars
			}
		}

		vars := map[string]string{
			"name":   srcName,
			"source": src.String(),
			"hash":   hashShort,
		}

		fmt.Fprintf(os.Stderr, "\n━━━ %s ━━━\n", srcName)

		// Expand all actions with template vars.
		expanded := make([]config.Action, len(pipeline))
		for i, action := range pipeline {
			expanded[i] = config.ExpandAction(action, vars)
			// For the step that had the multi-source, replace with the single source string.
			if action.Source != nil {
				if _, ok := action.Source.([]any); ok {
					expanded[i].Source = src.String()
				}
			}
		}

		subName := fmt.Sprintf("%s/%s", name, srcName)
		pr, err := runSinglePipeline(subName, expanded)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", srcName, err)
			failCount++
			continue
		}
		passCount++
		allReports = append(allReports, pr)
	}

	totalDuration := time.Since(overallStart)

	fmt.Fprintf(os.Stderr, "\n╔══════════════════════════════════════════════════╗\n")
	fmt.Fprintf(os.Stderr, "║  Multi-Source: %-34s║\n", name)
	fmt.Fprintf(os.Stderr, "║  Sources: %-4d  Pass: %-4d  Fail: %-4d           ║\n", len(sources), passCount, failCount)
	fmt.Fprintf(os.Stderr, "║  Duration: %-38s║\n", totalDuration.Round(time.Millisecond))
	fmt.Fprintf(os.Stderr, "╚══════════════════════════════════════════════════╝\n")

	// Write combined report if requested.
	if reportPath != "" && len(allReports) > 0 {
		// Combine all sub-reports into a map.
		reportMap := make(map[string]*report.PipelineReport, len(allReports))
		for _, r := range allReports {
			reportMap[r.Pipeline] = r
		}
		data, err := yaml.Marshal(reportMap)
		if err != nil {
			return fmt.Errorf("marshal report: %w", err)
		}
		if err := os.WriteFile(reportPath, data, 0644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		fmt.Fprintf(os.Stderr, "✓ report written to %s\n", reportPath)
	}

	if failCount > 0 {
		return fmt.Errorf("%d/%d sources failed", failCount, len(sources))
	}
	return nil
}
