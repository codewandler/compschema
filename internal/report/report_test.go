package report

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestPipelineReport_Print(t *testing.T) {
	r := &PipelineReport{
		Pipeline: "openai",
		Duration: 2 * time.Second,
		Steps: []StepReport{
			{
				Action:   "extract",
				Duration: 100 * time.Millisecond,
				Metrics: map[string]any{
					"schemas": 124,
					"bytes":   136083,
				},
			},
			{
				Action:   "generate",
				Duration: 1900 * time.Millisecond,
				Metrics: map[string]any{
					"types": 268,
					"defs":  146,
					"tests": TestResults{
						Total: 578, Passed: 578, Failed: 0, Skipped: 0, Rate: 100.0,
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	r.Print(&buf)
	out := buf.String()

	// Verify structure.
	for _, want := range []string{
		"Pipeline: openai",
		"extract",
		"schemas=124",
		"bytes=136083",
		"generate",
		"types=268",
		"defs=146",
		"tests: 578p/0f/0s (100.0%)",
		"Total:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Print output missing %q\n\nGot:\n%s", want, out)
		}
	}
}

func TestPipelineReport_PrintDeterministic(t *testing.T) {
	r := &PipelineReport{
		Pipeline: "test",
		Duration: time.Second,
		Steps: []StepReport{
			{
				Action:   "extract",
				Duration: 50 * time.Millisecond,
				Metrics: map[string]any{
					"zebra": 1,
					"alpha": 2,
					"middle": 3,
				},
			},
		},
	}

	var buf1, buf2 bytes.Buffer
	r.Print(&buf1)
	r.Print(&buf2)

	if buf1.String() != buf2.String() {
		t.Error("Print output is not deterministic")
	}

	// Verify alphabetical order.
	out := buf1.String()
	alphaIdx := strings.Index(out, "alpha=")
	middleIdx := strings.Index(out, "middle=")
	zebraIdx := strings.Index(out, "zebra=")
	if alphaIdx > middleIdx || middleIdx > zebraIdx {
		t.Errorf("metrics not in alphabetical order: alpha@%d middle@%d zebra@%d", alphaIdx, middleIdx, zebraIdx)
	}
}

func TestPipelineReport_YAML(t *testing.T) {
	r := &PipelineReport{
		Pipeline: "test",
		Duration: time.Second,
		Steps: []StepReport{
			{Action: "generate", Duration: 500 * time.Millisecond, Metrics: map[string]any{"types": 10}},
		},
	}

	data, err := r.YAML()
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(data)

	for _, want := range []string{"pipeline: test", "generate", "types: 10"} {
		if !strings.Contains(yaml, want) {
			t.Errorf("YAML missing %q\n\nGot:\n%s", want, yaml)
		}
	}
}

func TestNewStepReport(t *testing.T) {
	sr := NewStepReport("extract")
	if sr.Action != "extract" {
		t.Errorf("Action: got %q", sr.Action)
	}
	if sr.Metrics == nil {
		t.Error("Metrics should be initialized")
	}

	sr.Set("count", 42)
	if sr.Metrics["count"] != 42 {
		t.Errorf("Set: got %v", sr.Metrics["count"])
	}
}
