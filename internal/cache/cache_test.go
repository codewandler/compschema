package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_NonExistent(t *testing.T) {
	c := Load("/tmp/nonexistent-compschema-cache.json")
	if c.Version != Version {
		t.Errorf("version: got %d, want %d", c.Version, Version)
	}
	if len(c.Entries) != 0 {
		t.Errorf("entries: got %d, want 0", len(c.Entries))
	}
}

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	c := Load(path)

	c.Set("examples/openai", &Entry{
		IRHash:     "abc123",
		SourceHash: "sha256:def456",
		OutputHash: "sha256:ghi789",
	})

	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	// Verify file contents.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["entries"]; !ok {
		t.Error("missing 'entries' key in cache file")
	}

	// Reload and verify.
	c2 := Load(path)
	entry := c2.Get("examples/openai")
	if entry == nil {
		t.Fatal("entry not found after reload")
	}
	if entry.IRHash != "abc123" {
		t.Errorf("ir_hash: got %q, want %q", entry.IRHash, "abc123")
	}
	if entry.SourceHash != "sha256:def456" {
		t.Errorf("source_hash: got %q", entry.SourceHash)
	}
}

func TestIsStale(t *testing.T) {
	c := Load(filepath.Join(t.TempDir(), "cache.json"))

	// Not cached → stale.
	if !c.IsStale("pkg", "hash1") {
		t.Error("uncached entry should be stale")
	}

	// Cache it.
	c.Set("pkg", &Entry{IRHash: "hash1"})

	// Same hash → not stale.
	if c.IsStale("pkg", "hash1") {
		t.Error("matching hash should not be stale")
	}

	// Different hash → stale.
	if !c.IsStale("pkg", "hash2") {
		t.Error("different hash should be stale")
	}
}

func TestLoad_IncompatibleVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	data := []byte(`{"version": 999, "entries": {"pkg": {"ir_hash": "old"}}}`)
	os.WriteFile(path, data, 0644)

	c := Load(path)
	if len(c.Entries) != 0 {
		t.Error("incompatible version should produce empty cache")
	}
}

func TestLoad_CorruptJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	os.WriteFile(path, []byte(`{invalid json`), 0644)

	c := Load(path)
	if len(c.Entries) != 0 {
		t.Error("corrupt JSON should produce empty cache")
	}
}
