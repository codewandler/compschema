package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/codewandler/compschema/internal/analyzer"
	"github.com/codewandler/compschema/internal/cache"
	"github.com/codewandler/compschema/internal/emitter"
	"github.com/codewandler/compschema/internal/openapi2jsonschema"
	"github.com/spf13/cobra"
)

func newGenerateCmd() *cobra.Command {
	var (
		outDir      string
		validate    bool
		allTypes    bool
		runTests    bool
		emitIR      bool
		noCache     bool
		addExamples bool
	)

	cmd := &cobra.Command{
		Use:   "generate [packages...]",
		Short: "Generate JSON Schema + Decode/Validate from Go types",
		Long: `Analyze Go packages using go/ast + go/types, build a Schema IR from
types annotated with //compschema:generate, and emit:

  - schema.gen.json        — JSON Schema (draft 2020-12) with all types as $defs
  - compschema.gen.go      — JSONSchemaBytes, Validate, Decode per type
  - compschema.gen_test.go — smoke tests (schema validity, validation, round-trip)
  - schema.gen.ir.yaml     — Schema IR (with --emit-ir)`,
		Example: `  # Generate for the current package
  compschema generate ./...

  # Generate for a specific package, output to a custom dir
  compschema generate --out ./generated ./models/

  # Generate and run tests
  compschema generate --all --validate --test ./models/

  # Force regeneration (skip cache)
  compschema generate --no-cache --all ./models/`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			start := time.Now()

			pkgs, err := analyzer.Analyze(allTypes, args...)
			if err != nil {
				return fmt.Errorf("analyze: %w", err)
			}

			if len(pkgs) == 0 {
				fmt.Fprintf(os.Stderr, "no types with //compschema:generate found\n")
				return nil
			}

			// Load cache.
			var c *cache.Cache
			if !noCache {
				c = cache.Load(cache.DefaultPath())
			}

			// Collect metrics for the report.
			totalTypes := 0
			totalDefs := 0
			cachedTypes := 0
			generatedTypes := 0
			var testPassed, testFailed, testSkipped int

			for _, pkg := range pkgs {
				dir := outDir
				if dir == "" {
					dir = pkg.Dir
				}
				if dir == "" {
					dir = "."
				}

				if err := os.MkdirAll(dir, 0755); err != nil {
					return fmt.Errorf("create output directory: %w", err)
				}

				fmt.Fprintf(os.Stderr, "package %s: %d types\n", pkg.Name, len(pkg.Types))
				totalTypes += len(pkg.Types)

				// Check cache.
				irHash := fmt.Sprintf("%x", pkg.Hash())
				cacheKey := dir
				if c != nil && !c.IsStale(cacheKey, irHash) {
					fmt.Fprintf(os.Stderr, "  ✓ unchanged (%d types cached)\n", len(pkg.Types))
					cachedTypes += len(pkg.Types)
					continue
				}
				generatedTypes += len(pkg.Types)

				// 1. JSON Schema
				schemaJSON, inlined, err := emitter.JSONSchemaWithOptions(pkg, emitter.EmitOptions{
					Examples: addExamples,
				})
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
				goCode := emitter.GoCodegen(pkg, inlined)
				goPath := filepath.Join(dir, "compschema.gen.go")
				if err := os.WriteFile(goPath, []byte(goCode), 0644); err != nil {
					return fmt.Errorf("write codegen: %w", err)
				}
				fmt.Fprintf(os.Stderr, "  ✓ %s\n", goPath)

				// 3. Tests
				testCode := emitter.GoTestsWithOptions(pkg, inlined, emitter.EmitOptions{
					Examples: addExamples,
				})
				testPath := filepath.Join(dir, "compschema.gen_test.go")
				if err := os.WriteFile(testPath, []byte(testCode), 0644); err != nil {
					return fmt.Errorf("write tests: %w", err)
				}
				fmt.Fprintf(os.Stderr, "  ✓ %s\n", testPath)

				// 4. IR YAML (optional)
				if emitIR {
					irYAML, err := emitter.IRToYAML(pkg)
					if err != nil {
						return fmt.Errorf("emit IR: %w", err)
					}
					irPath := filepath.Join(dir, "schema.gen.ir.yaml")
					if err := os.WriteFile(irPath, irYAML, 0644); err != nil {
						return fmt.Errorf("write IR: %w", err)
					}
					fmt.Fprintf(os.Stderr, "  ✓ %s\n", irPath)
				}

				// 5. Run tests (optional)
				if runTests {
					p, f, s, err := runGeneratedTests(dir)
					if err != nil {
						fmt.Fprintf(os.Stderr, "  ⚠ test runner error: %v\n", err)
					} else {
						fmt.Fprintf(os.Stderr, "  tests: %d passed, %d failed, %d skipped\n", p, f, s)
						testPassed += p
						testFailed += f
						testSkipped += s
					}
				}

				// Update cache.
				if c != nil {
					typeHashes := make(map[string]string, len(pkg.Types))
					for name, t := range pkg.Types {
						typeHashes[name] = fmt.Sprintf("%x", t.Hash())
					}
					c.Set(cacheKey, &cache.Entry{
						IRHash:     irHash,
						TypeHashes: typeHashes,
					})
				}
			}

			// Save cache.
			if c != nil {
				if err := c.Save(); err != nil {
					fmt.Fprintf(os.Stderr, "  ⚠ cache save: %v\n", err)
				}
			}

			// Print report.
			duration := time.Since(start)
			fmt.Fprintf(os.Stderr, "\n")
			fmt.Fprintf(os.Stderr, "── generate complete (%s) ──\n", duration.Round(time.Millisecond))
			if cachedTypes == totalTypes {
				// Everything cached — nothing was generated.
				fmt.Fprintf(os.Stderr, "  types: %d  cached: %d/%d types (no changes)\n", totalTypes, cachedTypes, totalTypes)
			} else {
				fmt.Fprintf(os.Stderr, "  types: %d  defs: %d", totalTypes, totalDefs)
				if cachedTypes > 0 {
					fmt.Fprintf(os.Stderr, "  cached: %d/%d types", cachedTypes, totalTypes)
				}
				if generatedTypes > 0 {
					fmt.Fprintf(os.Stderr, "  generated: %d", generatedTypes)
				}
				fmt.Fprintf(os.Stderr, "\n")
				if runTests {
					total := testPassed + testFailed + testSkipped
					rate := float64(0)
					if total > 0 {
						rate = float64(testPassed) / float64(total) * 100
					}
					fmt.Fprintf(os.Stderr, "  tests: %d passed, %d failed, %d skipped (%.1f%%)\n", testPassed, testFailed, testSkipped, rate)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "", "output directory (default: package source dir)")
	cmd.Flags().BoolVar(&validate, "validate", false, "validate generated schema against meta-schema")
	cmd.Flags().BoolVar(&allTypes, "all", false, "analyze all exported types (not just annotated)")
	cmd.Flags().BoolVar(&runTests, "test", false, "run generated tests after writing files")
	cmd.Flags().BoolVar(&emitIR, "emit-ir", false, "write IR YAML alongside generated output")
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "skip cache, force regeneration")
	cmd.Flags().BoolVar(&addExamples, "examples", false, "add generated examples to JSON Schema output")

	return cmd
}

// testEvent matches the JSON output of `go test -json`.
type testEvent struct {
	Action  string `json:"Action"`
	Test    string `json:"Test"`
	Package string `json:"Package"`
	Output  string `json:"Output"`
}

// runGeneratedTests runs `go test -count=1 -json` in the given directory
// and returns pass/fail/skip counts. It does NOT return an error on test
// failures — only on infrastructure errors (e.g., can't run go test).
func runGeneratedTests(dir string) (passed, failed, skipped int, err error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("resolve dir: %w", err)
	}

	// Ensure dependencies are available (needed for external modules).
	getCmd := exec.Command("go", "get", "./...")
	getCmd.Dir = absDir
	_ = getCmd.Run() // best-effort

	cmd := exec.Command("go", "test", "-count=1", "-json", "./...")
	cmd.Dir = absDir
	cmd.Env = append(os.Environ(), "GOFLAGS=")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("pipe stdout: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return 0, 0, 0, fmt.Errorf("start go test: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var ev testEvent
		if json.Unmarshal(scanner.Bytes(), &ev) != nil {
			continue
		}
		// Only count individual test results, not package-level.
		if ev.Test == "" {
			continue
		}
		switch ev.Action {
		case "pass":
			passed++
		case "fail":
			failed++
		case "skip":
			skipped++
		}
	}

	// We don't care about the exit code — test failures are reported as metrics.
	_ = cmd.Wait()

	return passed, failed, skipped, nil
}
