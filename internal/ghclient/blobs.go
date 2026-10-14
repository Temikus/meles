package ghclient

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// BlobStore caches git blobs by SHA. Blobs are immutable, so a hit needs no request.
type BlobStore struct {
	Dir  string
	hits atomic.Int64
}

// Hits returns how many Gets were served from disk.
func (s *BlobStore) Hits() int64 { return s.hits.Load() }

// Get returns the blob if cached.
func (s *BlobStore) Get(sha string) ([]byte, bool) {
	p, err := s.path(sha)
	if err != nil {
		return nil, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	s.hits.Add(1)
	return b, true
}

// Put stores a blob.
func (s *BlobStore) Put(sha string, b []byte) error {
	p, err := s.path(sha)
	if err != nil {
		return err
	}
	return writeAtomic(p, b)
}

func (s *BlobStore) path(sha string) (string, error) {
	if s.Dir == "" {
		return "", fmt.Errorf("blob store disabled")
	}
	if len(sha) < 2 || strings.Trim(sha, "0123456789abcdef") != "" {
		return "", fmt.Errorf("invalid blob sha %q", sha)
	}
	return filepath.Join(s.Dir, "blobs", sha[:2], sha), nil
}
