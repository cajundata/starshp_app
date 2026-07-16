package store

import "database/sql"

// migrate brings a pre-library / pre-token-tracking dev database up to the
// current schema. All operations are idempotent.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`DROP TABLE IF EXISTS presets`); err != nil {
		return err
	}
	has, err := columnExists(db, "conversations", "preset_id")
	if err != nil {
		return err
	}
	if has {
		if _, err := db.Exec(`ALTER TABLE conversations DROP COLUMN preset_id`); err != nil {
			return err
		}
	}
	// Legacy token-column backfill: only relevant when a pre-token messages
	// table still exists. messages is no longer part of the live schema, so on
	// fresh databases this loop is skipped; on legacy databases it ensures the
	// columns exist before migrateMessagesToEvents reads them.
	if msgs, err := tableExists(db, "messages"); err != nil {
		return err
	} else if msgs {
		for _, col := range []string{"input_tokens", "output_tokens", "cached_input_tokens"} {
			has, err := columnExists(db, "messages", col)
			if err != nil {
				return err
			}
			if !has {
				if _, err := db.Exec(`ALTER TABLE messages ADD COLUMN ` + col + ` INTEGER`); err != nil {
					return err
				}
			}
		}
	}

	has, err = columnExists(db, "conversations", "retrieval_mode")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE conversations ADD COLUMN retrieval_mode TEXT NOT NULL DEFAULT 'auto_grounded_default'`); err != nil {
			return err
		}
	}
	// Accounting removal: the assignment surface is gone. Drop its tables and the
	// conversations column that pointed at them. Idempotent — a fresh database
	// never had them.
	if _, err := db.Exec(`DROP TABLE IF EXISTS assignment_items`); err != nil {
		return err
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS assignments`); err != nil {
		return err
	}
	has, err = columnExists(db, "conversations", "assignment_id")
	if err != nil {
		return err
	}
	if has {
		if _, err := db.Exec(`ALTER TABLE conversations DROP COLUMN assignment_id`); err != nil {
			return err
		}
	}
	// Persona foundation: additive, nullable. Pre-persona runs keep persona_id
	// NULL and render as a neutral bubble carrying only the model chip.
	has, err = columnExists(db, "runs", "persona_id")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN persona_id TEXT`); err != nil {
			return err
		}
	}
	has, err = columnExists(db, "conversations", "pinned_persona")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE conversations ADD COLUMN pinned_persona TEXT`); err != nil {
			return err
		}
	}

	// conversation_events, runs, and their indexes are created by schemaSQL
	// running before migrate(). Convert any legacy messages rows into the
	// canonical event log + synthesized runs, then drop the messages table.
	if err := migrateEventsImageHash(db); err != nil {
		return err
	}
	if err := migrateMessagesToEvents(db); err != nil {
		return err
	}
	if err := sweepInline(db); err != nil {
		return err
	}
	return nil
}

// sweepInline runs the orphan sweep using the *sql.DB the migrate path holds.
// Mirrors Store.SweepOrphanedRuns so migrate does not depend on a fully
// constructed *Store.
func sweepInline(db *sql.DB) error {
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master
        WHERE type='table' AND name='runs'`).Scan(&name)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = db.Exec(
		`UPDATE runs
            SET status='errored', active_for_replay=0,
                ended_at=strftime('%s','now')*1000, terminal_reason='orphaned'
          WHERE status='in_progress'`)
	return err
}

// migrateEventsImageHash rebuilds conversation_events for databases created
// before assistant_image existed. SQLite cannot alter a CHECK constraint, so
// this follows the documented table-rebuild recipe: create-new, copy, drop,
// rename, re-index — in one transaction. image_hash doubles as the marker:
// when the column exists (fresh DBs get it from schemaSQL), nothing runs.
// No other table references conversation_events, so drop+rename is FK-safe.
func migrateEventsImageHash(db *sql.DB) error {
	has, err := columnExists(db, "conversation_events", "image_hash")
	if err != nil || has {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmts := []string{
		`CREATE TABLE conversation_events_new (
  id              TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  turn_id         TEXT NOT NULL,
  run_id          TEXT,
  sequence_index  INTEGER NOT NULL,
  kind            TEXT NOT NULL CHECK (kind IN (
                      'user_message','assistant_text',
                      'assistant_tool_call','tool_result',
                      'assistant_image')),
  text            TEXT,
  tool_call_id    TEXT,
  tool_name       TEXT,
  tool_input      TEXT,
  tool_metadata   TEXT,
  tool_result_hash TEXT,
  tool_latency_ms INTEGER,
  image_hash      TEXT,
  is_error        INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL
)`,
		`INSERT INTO conversation_events_new
            (id, conversation_id, turn_id, run_id, sequence_index, kind, text,
             tool_call_id, tool_name, tool_input, tool_metadata,
             tool_result_hash, tool_latency_ms, image_hash, is_error, created_at)
         SELECT id, conversation_id, turn_id, run_id, sequence_index, kind, text,
             tool_call_id, tool_name, tool_input, tool_metadata,
             tool_result_hash, tool_latency_ms, NULL, is_error, created_at
           FROM conversation_events`,
		`DROP TABLE conversation_events`,
		`ALTER TABLE conversation_events_new RENAME TO conversation_events`,
		`CREATE INDEX IF NOT EXISTS conversation_events_conv_seq
  ON conversation_events(conversation_id, sequence_index)`,
		`CREATE INDEX IF NOT EXISTS conversation_events_turn
  ON conversation_events(turn_id)`,
		`CREATE INDEX IF NOT EXISTS conversation_events_run
  ON conversation_events(run_id)`,
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// columnExists reports whether table has a column named col. The table name is
// an internal constant, never user input, so string interpolation is safe.
func columnExists(db *sql.DB, table, col string) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid         int
			name, ctype string
			notnull, pk int
			dflt        sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}
