// Package blob is a content-addressed file store on local disk (stands in for S3).
package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

type Store struct{ root string }

func New(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

func Hash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func (s *Store) path(ref string) string {
	return filepath.Join(s.root, ref[:2], ref)
}

// Put stores data and returns its sha256 ref. Idempotent.
func (s *Store) Put(data []byte) (string, error) {
	ref := Hash(data)
	p := s.path(ref)
	if _, err := os.Stat(p); err == nil {
		return ref, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return ref, os.Rename(tmp.Name(), p)
}

func (s *Store) Get(ref string) ([]byte, error) {
	if !ValidRef(ref) {
		return nil, errors.New("blob: invalid ref")
	}
	return os.ReadFile(s.path(ref))
}

// ValidRef reports whether ref is a lowercase hex sha256 (prevents path traversal).
func ValidRef(ref string) bool {
	if len(ref) != 64 {
		return false
	}
	for _, c := range ref {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
