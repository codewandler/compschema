package source

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// GitSource clones a Git repository and reads a file from it.
type GitSource struct {
	Repo string // repository URL
	Ref  string // tag, branch, or commit hash (default: main)
	Path string // file path within the repo
}

func (s *GitSource) Fetch() ([]byte, Meta, error) {
	ref := s.Ref
	if ref == "" {
		ref = "main"
	}

	tmpDir, err := os.MkdirTemp("", "compschema-git-*")
	if err != nil {
		return nil, Meta{}, fmt.Errorf("git source: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Clone with depth=1. Try --branch first (works for tags and branches).
	cloneCmd := exec.Command("git", "clone", "--depth=1", "--branch", ref, s.Repo, tmpDir)
	cloneCmd.Stderr = os.Stderr
	if err := cloneCmd.Run(); err != nil {
		// --branch doesn't work for commit hashes. Fall back to full clone + checkout.
		os.RemoveAll(tmpDir)
		if err := os.MkdirAll(tmpDir, 0755); err != nil {
			return nil, Meta{}, fmt.Errorf("git source: recreate temp dir: %w", err)
		}

		cloneCmd = exec.Command("git", "clone", s.Repo, tmpDir)
		cloneCmd.Stderr = os.Stderr
		if err := cloneCmd.Run(); err != nil {
			return nil, Meta{}, fmt.Errorf("git source: clone %s: %w", s.Repo, err)
		}

		checkoutCmd := exec.Command("git", "-C", tmpDir, "checkout", ref)
		checkoutCmd.Stderr = os.Stderr
		if err := checkoutCmd.Run(); err != nil {
			return nil, Meta{}, fmt.Errorf("git source: checkout %s: %w", ref, err)
		}
	}

	// Read the file.
	filePath := filepath.Join(tmpDir, s.Path)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("git source: read %s in %s: %w", s.Path, s.Repo, err)
	}

	// Get resolved commit hash.
	revParseCmd := exec.Command("git", "-C", tmpDir, "rev-parse", "HEAD")
	commitBytes, err := revParseCmd.Output()
	commitRef := ""
	if err == nil {
		commitRef = strings.TrimSpace(string(commitBytes))
	}

	hash := sha256.Sum256(data)
	meta := Meta{
		Origin:    fmt.Sprintf("%s@%s:%s", s.Repo, ref, s.Path),
		Hash:      "sha256:" + hex.EncodeToString(hash[:]),
		CommitRef: commitRef,
		FetchedAt: time.Now(),
	}
	return data, meta, nil
}

func (s *GitSource) String() string {
	ref := s.Ref
	if ref == "" {
		ref = "main"
	}
	return fmt.Sprintf("git:%s@%s:%s", s.Repo, ref, s.Path)
}
