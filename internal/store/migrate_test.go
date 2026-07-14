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

// TestMigratePreTokenMessagesNoError simulates a pre-token-tracking DB (a
// messages table without the usage columns) and verifies Open migrates it
// cleanly: the token-column ALTER runs before migrateMessagesToEvents reads the
// rows, the rows convert to events, and the messages table is dropped — all
// without a "no such column" error.
func TestMigratePreTokenMessagesNoError(t *testing.T) {
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
	if _, err := db.Exec(`INSERT INTO conversations(id,title,created_at,updated_at) VALUES('c1','t',0,0);
		INSERT INTO messages(id,conversation_id,role,content,model,created_at) VALUES('u1','c1','user','hi',NULL,1);
		INSERT INTO messages(id,conversation_id,role,content,model,created_at) VALUES('a1','c1','assistant','hello','claude-x',2);`); err != nil {
		t.Fatalf("seed rows: %v", err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// messages dropped; the pair migrated into the event log.
	if has, _ := columnExists(s.db, "messages", "id"); has {
		t.Fatal("messages table should be dropped after migration")
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM conversation_events WHERE conversation_id='c1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 migrated events, got %d", n)
	}
}

func TestMigrate_CreatesConversationEvents(t *testing.T) {
	db := openTestDB(t)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	cols := readTableColumns(t, db, "conversation_events")
	for _, want := range []string{
		"id", "conversation_id", "turn_id", "run_id", "sequence_index",
		"kind", "text", "tool_call_id", "tool_name", "tool_input",
		"tool_metadata", "tool_result_hash", "tool_latency_ms", "is_error",
		"created_at",
	} {
		if _, ok := cols[want]; !ok {
			t.Errorf("conversation_events missing column %q", want)
		}
	}
}

func TestMigrate_CreatesRunsAndPartialIndex(t *testing.T) {
	db := openTestDB(t)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	cols := readTableColumns(t, db, "runs")
	for _, want := range []string{
		"id", "conversation_id", "turn_id", "status", "active_for_replay",
		"provider", "model", "retrieval_mode", "grounding_meta",
		"started_at", "ended_at", "terminal_reason", "error_code",
		"error_message", "total_input_tokens", "total_output_tokens",
		"total_cached_input_tokens", "total_tool_calls", "total_iterations",
	} {
		if _, ok := cols[want]; !ok {
			t.Errorf("runs missing column %q", want)
		}
	}
	// Partial unique index enforces one active run per turn.
	if !indexExists(t, db, "runs_one_active_per_turn") {
		t.Error("expected partial unique index runs_one_active_per_turn")
	}
}

func TestMigrate_AddsRetrievalModeToConversations(t *testing.T) {
	db := openTestDB(t)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	cols := readTableColumns(t, db, "conversations")
	if _, ok := cols["retrieval_mode"]; !ok {
		t.Fatal("conversations missing retrieval_mode column")
	}
	// Insert with the default and read it back.
	if _, err := db.Exec(`INSERT INTO conversations(id,title,created_at,updated_at)
        VALUES('c1','t',0,0)`); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := db.QueryRow(`SELECT retrieval_mode FROM conversations WHERE id='c1'`).
		Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "auto_grounded_default" {
		t.Fatalf("default mode want auto_grounded_default, got %q", mode)
	}
}

// openTestDB opens a raw *sql.DB with the current schema applied (mirroring
// what store.Open does before calling migrate). Tests then call migrate(db)
// explicitly to exercise the idempotent upgrade path.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "migrate.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

func readTableColumns(t *testing.T, db *sql.DB, table string) map[string]struct{} {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var (
			cid         int
			name, ctype string
			notnull, pk int
			dflt        sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		out[name] = struct{}{}
	}
	return out
}

func indexExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var got string
	err := db.QueryRow(`SELECT name FROM sqlite_master
        WHERE type='index' AND name=?`, name).Scan(&got)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return got == name
}

// TestMigrateDropsLegacyAssignments simulates a dev DB created before the
// accounting removal — it has `assignments` / `assignment_items` tables and a
// `conversations.assignment_id` column — and verifies store.Open migrates it:
// both tables and the column are gone.
func TestMigrateDropsLegacyAssignments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy_assignments.db")
	legacy := `
CREATE TABLE conversations (id TEXT PRIMARY KEY, title TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, pinned_model TEXT,
  assignment_id TEXT);
CREATE TABLE assignments (id TEXT PRIMARY KEY, source_dir TEXT NOT NULL,
  title TEXT NOT NULL, manifest_hash TEXT NOT NULL, model TEXT NOT NULL,
  grounding_scope TEXT, library_items TEXT, status TEXT NOT NULL,
  total_items INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL);
CREATE TABLE assignment_items (id TEXT PRIMARY KEY,
  assignment_id TEXT NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL, source_path TEXT NOT NULL, type TEXT NOT NULL,
  title TEXT, run_id TEXT, conversation_id TEXT, status TEXT NOT NULL,
  confidence TEXT, answer_json TEXT, flags_json TEXT, answer_path TEXT,
  error TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
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

	for _, table := range []string{"assignments", "assignment_items"} {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s table should have been dropped", table)
		}
	}
	has, err := columnExists(s.db, "conversations", "assignment_id")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("conversations.assignment_id should have been dropped")
	}
}

// TestMigrateLegacyDatabaseGainsPersonaColumns simulates a dev DB created
// before personas existed — and still carrying the retired assignment surface
// dropped by TestMigrateDropsLegacyAssignments above — and verifies store.Open
// migrates it cleanly and additively: both new persona columns appear, and the
// pre-existing conversation row survives untouched. The assignment-drop
// behavior itself is already covered by TestMigrateDropsLegacyAssignments;
// this test only asserts the persona columns.
func TestMigrateLegacyDatabaseGainsPersonaColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE conversations (
  id TEXT PRIMARY KEY, title TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  pinned_model TEXT, assignment_id TEXT
);
CREATE TABLE assignments (id TEXT PRIMARY KEY, title TEXT NOT NULL);
CREATE TABLE assignment_items (id TEXT PRIMARY KEY, assignment_id TEXT NOT NULL);
INSERT INTO conversations(id,title,created_at,updated_at) VALUES('c1','old',1,1);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open (which runs schema+migrate): %v", err)
	}
	defer s.Close()

	for _, tc := range []struct{ table, col string }{
		{"runs", "persona_id"},
		{"conversations", "pinned_persona"},
	} {
		has, err := columnExists(s.db, tc.table, tc.col)
		if err != nil {
			t.Fatal(err)
		}
		if !has {
			t.Errorf("%s.%s was not added by migrate", tc.table, tc.col)
		}
	}

	// The pre-existing conversation survives and is listable.
	convs, err := s.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	if len(convs) != 1 || convs[0].ID != "c1" {
		t.Errorf("ListConversations = %+v, want the legacy row", convs)
	}
}
