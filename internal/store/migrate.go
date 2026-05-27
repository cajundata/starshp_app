package store

import "database/sql"

// migrate brings a pre-library dev database up to the current schema. It is
// idempotent: on a fresh database both operations are no-ops.
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
	return nil
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
