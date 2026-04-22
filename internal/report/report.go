// Package report defines structured pipeline reports with metrics.
package report

import (
	"fmt"
	"io"
	"sort"
	"time"

	"go.yaml.in/yaml/v4"
)

// PipelineReport is the result of running a named pipeline.
type PipelineReport struct {
	Pipeline string        `json:"pipeline" yaml:"pipeline"`
	Steps    []StepReport  `json:"steps" yaml:"steps"`
	Duration time.Duration `json:"duration" yaml:"duration"`
}

// StepReport is the result of a single pipeline step.
type StepReport struct {
	Action   string         `json:"action" yaml:"action"`
	Duration time.Duration  `json:"duration" yaml:"duration"`
	Metrics  map[string]any `json:"metrics,omitempty" yaml:"metrics,omitempty"`
}

// TestResults holds test execution metrics.
type TestResults struct {
	Total   int     `json:"total" yaml:"total"`
	Passed  int     `json:"passed" yaml:"passed"`
	Failed  int     `json:"failed" yaml:"failed"`
	Skipped int     `json:"skipped" yaml:"skipped"`
	Rate    float64 `json:"pass_rate" yaml:"pass_rate"` // 0.0–100.0
}

// NewStepReport creates a StepReport for the given action.
func NewStepReport(action string) *StepReport {
	return &StepReport{
		Action:  action,
		Metrics: make(map[string]any),
	}
}

// Set adds a metric to the step report.
func (s *StepReport) Set(key string, value any) {
	s.Metrics[key] = value
}

// Print writes a human-readable summary of the pipeline report to w.
func (r *PipelineReport) Print(w io.Writer) {
	fmt.Fprintf(w, "\n╔══════════════════════════════════════════════════╗\n")
	fmt.Fprintf(w, "║  Pipeline: %-38s║\n", r.Pipeline)
	fmt.Fprintf(w, "╠══════════════════════════════════════════════════╣\n")

	for _, step := range r.Steps {
		fmt.Fprintf(w, "║  %-8s  %s", step.Action, step.Duration.Round(time.Millisecond))

		// Sort metric keys for deterministic output.
		keys := make([]string, 0, len(step.Metrics))
		for k := range step.Metrics {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			v := step.Metrics[k]
			switch val := v.(type) {
			case TestResults:
				fmt.Fprintf(w, "\n║    tests: %dp/%df/%ds (%.1f%%)", val.Passed, val.Failed, val.Skipped, val.Rate)
			default:
				fmt.Fprintf(w, "  %s=%v", k, val)
			}
		}
		fmt.Fprintf(w, "\n")
	}

	fmt.Fprintf(w, "╠══════════════════════════════════════════════════╣\n")
	fmt.Fprintf(w, "║  Total: %-41s║\n", r.Duration.Round(time.Millisecond).String())
	fmt.Fprintf(w, "╚══════════════════════════════════════════════════╝\n")
}

// YAML serializes the report as YAML.
func (r *PipelineReport) YAML() ([]byte, error) {
	return yaml.Marshal(r)
}
