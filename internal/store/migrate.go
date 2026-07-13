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
	// conversation_events, runs, and their indexes are created by schemaSQL
	// running before migrate(). Convert any legacy messages rows into the
	// canonical event log + synthesized runs, then drop the messages table.
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
