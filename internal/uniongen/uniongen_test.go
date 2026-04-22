package uniongen

import (
	"testing"
)

func TestAnalyzeSchema(t *testing.T) {
	unions, err := AnalyzeSchema("../../testdata/openai/responses.schema.json")
	if err != nil {
		t.Fatalf("AnalyzeSchema: %v", err)
	}

	if len(unions) == 0 {
		t.Fatal("expected unions, got none")
	}

	t.Logf("Found %d unions:", len(unions))
	for _, u := range unions {
		disc := u.Discriminator
		if disc == "" {
			disc = "(none)"
		}
		t.Logf("  %s: %s(%d variants) disc=%s", u.Name, u.Keyword, len(u.Variants), disc)
		for _, v := range u.Variants {
			if v.RefName != "" {
				t.Logf("    - %s [%s]", v.RefName, v.TypeConstVal)
			} else {
				t.Logf("    - inline:%s", v.InlineType)
			}
		}
	}

	// Check specific unions
	found := map[string]bool{}
	for _, u := range unions {
		found[u.Name] = true
	}

	expected := []string{"Tool", "Annotation", "ResponseStreamEvent", "OutputItem", "ComputerAction"}
	for _, name := range expected {
		if !found[name] {
			t.Errorf("expected union %q not found", name)
		}
	}

	// Check that Tool has discriminator
	for _, u := range unions {
		if u.Name == "Tool" {
			if u.Discriminator != "type" {
				t.Errorf("Tool discriminator = %q, want %q", u.Discriminator, "type")
			}
			if len(u.Variants) != 4 {
				t.Errorf("Tool has %d variants, want 4", len(u.Variants))
			}
		}
	}
}

func TestGenerateUnionCode(t *testing.T) {
	unions, err := AnalyzeSchema("../../testdata/openai/responses.schema.json")
	if err != nil {
		t.Fatalf("AnalyzeSchema: %v", err)
	}

	code := GenerateUnionCode("openairesponses", unions, "ID", "URL", "API")
	if len(code) == 0 {
		t.Fatal("generated empty code")
	}

	t.Logf("Generated %d bytes of Go code", len(code))
	// Just check it contains expected patterns
	if !containsAll(code, "type Tool interface", "isTool()", "func (*FileSearchTool) isTool()") {
		t.Error("missing Tool sealed interface")
	}
	if !containsAll(code, "UnmarshalTool", "file_search") {
		t.Error("missing Tool unmarshal function")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
