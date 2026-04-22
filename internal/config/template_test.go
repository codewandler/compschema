package config

import "testing"

func TestExpandTemplate(t *testing.T) {
	vars := map[string]string{
		"name":   "petstore",
		"source": "testdata/specs/petstore.yaml",
		"hash":   "abc12345",
	}

	tests := []struct {
		input string
		want  string
	}{
		{"/tmp/{name}/schema.json", "/tmp/petstore/schema.json"},
		{"{name}", "petstore"},
		{"no-vars", "no-vars"},
		{"/tmp/{name}/{hash}/out", "/tmp/petstore/abc12345/out"},
		{"{source}", "testdata/specs/petstore.yaml"},
	}

	for _, tt := range tests {
		got := ExpandTemplate(tt.input, vars)
		if got != tt.want {
			t.Errorf("ExpandTemplate(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExpandAction(t *testing.T) {
	vars := map[string]string{
		"name": "petstore",
	}

	a := Action{
		Action:   "generate",
		Out:      "/tmp/{name}/",
		Packages: []string{"/tmp/{name}/"},
		Package:  "{name}",
	}

	expanded := ExpandAction(a, vars)

	if expanded.Out != "/tmp/petstore/" {
		t.Errorf("Out: got %q", expanded.Out)
	}
	if expanded.Packages[0] != "/tmp/petstore/" {
		t.Errorf("Packages[0]: got %q", expanded.Packages[0])
	}
	if expanded.Package != "petstore" {
		t.Errorf("Package: got %q", expanded.Package)
	}
	// Original should be unchanged.
	if a.Out != "/tmp/{name}/" {
		t.Error("original action was mutated")
	}
}
