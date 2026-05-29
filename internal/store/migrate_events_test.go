package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

// openTestDBRaw opens a temp-file SQLite without applying schemaSQL, so a test
// can install a legacy schema first. A file (not :memory:) is required because
// database/sql pools connections and modernc.org/sqlite gives each connection
// its own :memory: database.
func openTestDBRaw(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "raw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrateMessages_EventsAndRunsSynthesized(t *testing.T) {
	db := openTestDBRaw(t)
	// Apply legacy schema (without conversation_events).
	legacySchema := `
        CREATE TABLE conversations (id TEXT PRIMARY KEY, title TEXT NOT NULL,
            created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
            pinned_model TEXT);
        CREATE TABLE messages (id TEXT PRIMARY KEY,
            conversation_id TEXT NOT NULL,
            role TEXT NOT NULL, content TEXT NOT NULL, model TEXT,
            created_at INTEGER NOT NULL,
            rag_context TEXT, rag_sources TEXT,
            input_tokens INTEGER, output_tokens INTEGER, cached_input_tokens INTEGER);
    `
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec(`INSERT INTO conversations(id,title,created_at,updated_at) VALUES('c1','t',0,0)`)
	_, _ = db.Exec(`INSERT INTO messages(id,conversation_id,role,content,model,created_at) VALUES('u1','c1','user','hi',NULL,1)`)
	_, _ = db.Exec(`INSERT INTO messages(id,conversation_id,role,content,model,rag_context,rag_sources,input_tokens,output_tokens,cached_input_tokens,created_at) VALUES('a1','c1','assistant','hello','gpt-x','## src...','[{"book":"A","chapter":1,"chunkId":"cid"}]',12,4,2,2)`)
	_, _ = db.Exec(`INSERT INTO messages(id,conversation_id,role,content,model,created_at) VALUES('u2','c1','user','trailing',NULL,3)`)

	// Now apply the full schema + migration.
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}

	// conversation_events should have user_message + assistant_text + user_message (trailing).
	rows, err := db.Query(`SELECT kind, COALESCE(text,'') FROM conversation_events
        WHERE conversation_id='c1' ORDER BY sequence_index`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []struct{ Kind, Text string }
	for rows.Next() {
		var k, txt string
		if err := rows.Scan(&k, &txt); err != nil {
			t.Fatal(err)
		}
		got = append(got, struct{ Kind, Text string }{k, txt})
	}
	if len(got) != 3 || got[0].Kind != "user_message" || got[1].Kind != "assistant_text" ||
		got[2].Kind != "user_message" {
		t.Fatalf("events mismatch: %+v", got)
	}

	// Synthesized run for the first turn.
	var (
		runID, status string
		active        int
		meta          string
		inputTok      int64
	)
	err = db.QueryRow(`SELECT id, status, active_for_replay, COALESCE(grounding_meta,''), total_input_tokens
        FROM runs WHERE conversation_id='c1' ORDER BY started_at LIMIT 1`).
		Scan(&runID, &status, &active, &meta, &inputTok)
	if err != nil {
		t.Fatal(err)
	}
	if status != "completed" || active != 1 {
		t.Fatalf("run not active+completed: status=%s active=%d", status, active)
	}
	if inputTok != 12 {
		t.Fatalf("input tokens not migrated: %d", inputTok)
	}
	var groundingMeta map[string]any
	_ = json.Unmarshal([]byte(meta), &groundingMeta)
	if groundingMeta["status"] != "ready" {
		t.Fatalf("grounding_meta status want ready; got %v", groundingMeta["status"])
	}

	// messages table should be dropped.
	var name string
	err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='messages'`).Scan(&name)
	if err == nil {
		t.Fatal("messages table should be dropped after migration")
	}
}
