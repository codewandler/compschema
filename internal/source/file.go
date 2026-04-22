package source

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"
)

// FileSource reads from a local file path.
type FileSource struct {
	Path string
}

func (s *FileSource) Fetch() ([]byte, Meta, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("file source %s: %w", s.Path, err)
	}

	hash := sha256.Sum256(data)
	meta := Meta{
		Origin:    s.Path,
		Hash:      "sha256:" + hex.EncodeToString(hash[:]),
		FetchedAt: time.Now(),
	}
	return data, meta, nil
}

func (s *FileSource) String() string {
	return s.Path
}
