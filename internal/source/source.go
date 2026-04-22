// Package source provides pluggable data sources for pipeline inputs.
//
// Sources abstract over local files, HTTP URLs, and Git repositories,
// providing content-hashed fetching with consistent metadata.
package source

import "time"

// Meta describes a fetched source with content hash and origin info.
type Meta struct {
	Origin    string    `json:"origin" yaml:"origin"`
	Hash      string    `json:"hash" yaml:"hash"`                                       // sha256:<hex>
	CommitRef string    `json:"commit_ref,omitempty" yaml:"commit_ref,omitempty"`        // resolved git commit (empty for file/http)
	FetchedAt time.Time `json:"fetched_at" yaml:"fetched_at"`
}

// Source fetches raw data from some origin.
type Source interface {
	// Fetch retrieves the content and returns raw bytes plus metadata.
	Fetch() (data []byte, meta Meta, err error)

	// String returns a human-readable description of the source.
	String() string
}
