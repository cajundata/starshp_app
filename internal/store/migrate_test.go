package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigrateDropsLegacyPresets simulates a dev DB created before the library
// feature — it has a `presets` table and a `conversations.preset_id` column —
// and verifies store.Open migrates it: presets table gone, preset_id gone.
func TestMigrateDropsLegacyPresets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy := `
CREATE TABLE presets (id TEXT PRIMARY KEY, name TEXT NOT NULL, system_prompt TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE conversations (id TEXT PRIMARY KEY, title TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, preset_id TEXT, pinned_model TEXT);
`
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(legacy); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	db.Close()

	// Open through the real store — this must run the migration.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='presets'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("presets table should have been dropped")
	}
	has, err := columnExists(s.db, "conversations", "preset_id")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("conversations.preset_id should have been dropped")
	}
}

// TestMigrateAddsTokenColumns simulates a pre-token-tracking DB and verifies
// that Open's migration adds input_tokens, output_tokens, cached_input_tokens
// to the messages table.
func TestMigrateAddsTokenColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	// Pre-token messages schema (no usage columns).
	legacy := `
CREATE TABLE conversations (id TEXT PRIMARY KEY, title TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, pinned_model TEXT);
CREATE TABLE messages (id TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  role TEXT NOT NULL, content TEXT NOT NULL, model TEXT,
  created_at INTEGER NOT NULL, rag_context TEXT, rag_sources TEXT);
`
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(legacy); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	for _, col := range []string{"input_tokens", "output_tokens", "cached_input_tokens"} {
		has, err := columnExists(s.db, "messages", col)
		if err != nil {
			t.Fatal(err)
		}
		if !has {
			t.Fatalf("messages.%s should have been added by migration", col)
		}
	}
}
