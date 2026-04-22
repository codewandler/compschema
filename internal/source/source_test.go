package source

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSource(t *testing.T) {
	// Create a temp file with known content.
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	content := []byte(`{"hello": "world"}`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	src := &FileSource{Path: path}
	data, meta, err := src.Fetch()
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != string(content) {
		t.Errorf("data mismatch: got %q, want %q", data, content)
	}
	if meta.Origin != path {
		t.Errorf("origin: got %q, want %q", meta.Origin, path)
	}
	if meta.Hash == "" || meta.Hash[:7] != "sha256:" {
		t.Errorf("hash should start with sha256:, got %q", meta.Hash)
	}

	// Verify determinism — same content → same hash.
	_, meta2, _ := src.Fetch()
	if meta.Hash != meta2.Hash {
		t.Errorf("hash not deterministic: %q vs %q", meta.Hash, meta2.Hash)
	}
}

func TestFileSource_NotFound(t *testing.T) {
	src := &FileSource{Path: "/nonexistent/file.json"}
	_, _, err := src.Fetch()
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestHTTPSource(t *testing.T) {
	content := `{"openapi": "3.0.0"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "compschema/1.0" {
			t.Errorf("unexpected User-Agent: %q", r.Header.Get("User-Agent"))
		}
		w.Write([]byte(content))
	}))
	defer srv.Close()

	src := &HTTPSource{URL: srv.URL + "/spec.yaml"}
	data, meta, err := src.Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("data mismatch: got %q", data)
	}
	if meta.Hash == "" {
		t.Error("expected non-empty hash")
	}
}

func TestHTTPSource_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	src := &HTTPSource{URL: srv.URL + "/missing"}
	_, _, err := src.Fetch()
	if err == nil {
		t.Error("expected error for 404")
	}
}

func TestParse_String(t *testing.T) {
	tests := []struct {
		input string
		typ   string
	}{
		{"./local.yaml", "*source.FileSource"},
		{"https://example.com/spec.yaml", "*source.HTTPSource"},
		{"http://example.com/spec.yaml", "*source.HTTPSource"},
	}

	for _, tt := range tests {
		sources, err := Parse(tt.input)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tt.input, err)
		}
		if len(sources) != 1 {
			t.Fatalf("Parse(%q): got %d sources, want 1", tt.input, len(sources))
		}
		got := fmt.Sprintf("%T", sources[0])
		if got != tt.typ {
			t.Errorf("Parse(%q): type %s, want %s", tt.input, got, tt.typ)
		}
	}
}

func TestParse_GitMap(t *testing.T) {
	m := map[string]any{
		"git":  "https://github.com/example/repo.git",
		"ref":  "v1.0",
		"path": "openapi.yaml",
	}
	sources, err := Parse(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 {
		t.Fatalf("got %d sources, want 1", len(sources))
	}
	gs, ok := sources[0].(*GitSource)
	if !ok {
		t.Fatalf("expected *GitSource, got %T", sources[0])
	}
	if gs.Repo != "https://github.com/example/repo.git" {
		t.Errorf("repo: %q", gs.Repo)
	}
	if gs.Ref != "v1.0" {
		t.Errorf("ref: %q", gs.Ref)
	}
	if gs.Path != "openapi.yaml" {
		t.Errorf("path: %q", gs.Path)
	}
}

func TestParse_Array(t *testing.T) {
	arr := []any{
		"./local.yaml",
		"https://example.com/spec.yaml",
	}
	sources, err := Parse(arr)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("got %d sources, want 2", len(sources))
	}
}

func TestName(t *testing.T) {
	tests := []struct {
		src  Source
		want string
	}{
		{&FileSource{Path: "testdata/specs/petstore.yaml"}, "petstore"},
		{&HTTPSource{URL: "https://example.com/api/v3/openapi.json"}, "openapi"},
		{&GitSource{Repo: "https://github.com/openai/openai-openapi.git", Path: "openapi.yaml"}, "openapi"},
		{&GitSource{Repo: "https://github.com/openai/openai-openapi.git"}, "openai-openapi"},
	}

	for _, tt := range tests {
		got := Name(tt.src)
		if got != tt.want {
			t.Errorf("Name(%s) = %q, want %q", tt.src, got, tt.want)
		}
	}
}
