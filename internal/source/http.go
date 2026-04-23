package source

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPSource fetches content from an HTTP/HTTPS URL.
type HTTPSource struct {
	URL string
}

func (s *HTTPSource) Fetch() ([]byte, Meta, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", s.URL, nil)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("http source: %w", err)
	}
	req.Header.Set("User-Agent", "compschema/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("http source %s: %w", s.URL, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, Meta{}, fmt.Errorf("http source %s: status %d", s.URL, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("http source %s: read body: %w", s.URL, err)
	}

	hash := sha256.Sum256(data)
	meta := Meta{
		Origin:    s.URL,
		Hash:      "sha256:" + hex.EncodeToString(hash[:]),
		FetchedAt: time.Now(),
	}
	return data, meta, nil
}

func (s *HTTPSource) String() string {
	return s.URL
}
