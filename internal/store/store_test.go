package store

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesSchema(t *testing.T) {
	s := newTestStore(t)
	var n int
	row := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('conversations','messages','conversation_textbooks','conversation_library_items')`)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n != 4 {
		t.Fatalf("expected 4 tables, got %d", n)
	}
	// A fresh database must not create the legacy presets table.
	var presets int
	s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='presets'`).Scan(&presets)
	if presets != 0 {
		t.Fatal("fresh DB should not have a presets table")
	}
}
