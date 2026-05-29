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
	row := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('conversations','conversation_textbooks','conversation_library_items','conversation_events','runs')`)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5 tables, got %d", n)
	}
	// messages is retired by the tool-calling migration.
	var msgs int
	s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='messages'`).Scan(&msgs)
	if msgs != 0 {
		t.Fatal("messages table should not exist after migration")
	}
	// A fresh database must not create the legacy presets table.
	var presets int
	s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='presets'`).Scan(&presets)
	if presets != 0 {
		t.Fatal("fresh DB should not have a presets table")
	}
}
