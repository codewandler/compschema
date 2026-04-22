// Package cache provides a file-backed hash cache for incremental compilation.
//
// The cache stores source hash → IR hash → output hash chains per package/directory,
// enabling skip-if-unchanged semantics in the generate pipeline.
package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

const Version = 1

// Cache is the in-memory representation of .compschema.cache.json.
type Cache struct {
	Version int               `json:"version"`
	Entries map[string]*Entry `json:"entries"`

	mu   sync.Mutex
	path string // file path, set by Load
}

// Entry stores hashes for a single package/directory.
type Entry struct {
	SourceHash string            `json:"source_hash,omitempty"` // sha256 of input source
	IRHash     string            `json:"ir_hash"`               // hex-encoded Package.Hash()
	OutputHash string            `json:"output_hash,omitempty"` // sha256 of generated schema.gen.json
	TypeHashes map[string]string `json:"type_hashes,omitempty"` // per-type IR hashes for incremental
	Timestamp  time.Time         `json:"timestamp"`
}

// Load reads a cache file. Returns an empty cache if the file doesn't exist
// or is incompatible.
func Load(path string) *Cache {
	c := &Cache{
		Version: Version,
		Entries: make(map[string]*Entry),
		path:    path,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return c // file doesn't exist — fresh cache
	}

	var loaded Cache
	if err := json.Unmarshal(data, &loaded); err != nil {
		return c // corrupt — fresh cache
	}
	if loaded.Version != Version {
		return c // incompatible version — fresh cache
	}
	if loaded.Entries != nil {
		c.Entries = loaded.Entries
	}

	return c
}

// Save writes the cache to disk.
func (c *Cache) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.path == "" {
		return fmt.Errorf("cache: no file path set")
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("cache: marshal: %w", err)
	}
	data = append(data, '\n')

	return os.WriteFile(c.path, data, 0644)
}

// Get returns the cache entry for a key, or nil if not found.
func (c *Cache) Get(key string) *Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Entries[key]
}

// Set stores a cache entry.
func (c *Cache) Set(key string, entry *Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry.Timestamp = time.Now()
	c.Entries[key] = entry
}

// IsStale returns true if the cached entry doesn't match the given hashes,
// meaning the output needs to be regenerated.
func (c *Cache) IsStale(key string, irHash string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.Entries[key]
	if !ok {
		return true // not cached
	}
	return entry.IRHash != irHash
}

// DefaultPath returns the default cache file path.
func DefaultPath() string {
	return ".compschema.cache.json"
}
