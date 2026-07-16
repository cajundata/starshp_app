// Package imagestore persists model-generated images as content-addressed
// PNG files (<sha256-hex>.png) under one directory, and serves them to the
// frontend at /appimages/<hash>.png via the Wails asset-server handler.
package imagestore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Store struct{ dir string }

// New returns a store rooted at dir, creating the directory if absent.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("imagestore: %w", err)
	}
	return &Store{dir: dir}, nil
}

var hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Put writes data as <sha256-hex>.png and returns the hash. Identical content
// dedupes to the existing file. The write goes through a temp file + rename so
// a crash never leaves a torn file under a valid hash name.
func (s *Store) Put(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	path := s.path(hash)
	if _, err := os.Stat(path); err == nil {
		return hash, nil
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("imagestore: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("imagestore: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("imagestore: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("imagestore: %w", err)
	}
	return hash, nil
}

// Read returns the stored bytes for hash. A missing file surfaces as an error
// wrapping fs.ErrNotExist (deleted-image callers degrade to a placeholder).
func (s *Store) Read(hash string) ([]byte, error) {
	if !hashRE.MatchString(hash) {
		return nil, fmt.Errorf("imagestore: invalid hash %q", hash)
	}
	return os.ReadFile(s.path(hash))
}

func (s *Store) path(hash string) string { return filepath.Join(s.dir, hash+".png") }

// Handler serves stored images at /appimages/<hash>.png. The hash segment must
// be exactly 64 lowercase hex chars — traversal is rejected by construction.
// Everything else is 404.
func (s *Store) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutPrefix(r.URL.Path, "/appimages/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		hash, ok := strings.CutSuffix(name, ".png")
		if !ok || !hashRE.MatchString(hash) {
			http.NotFound(w, r)
			return
		}
		data, err := s.Read(hash)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(data)
	})
}
