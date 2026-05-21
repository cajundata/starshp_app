package store

// GetActiveItems returns the library item filenames active for a conversation,
// ordered by filename for deterministic results.
func (s *Store) GetActiveItems(convID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT item_name FROM conversation_library_items WHERE conversation_id=? ORDER BY item_name`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// SetActiveItems replaces the full active set for a conversation in one
// transaction (replace-all semantics).
func (s *Store) SetActiveItems(convID string, names []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM conversation_library_items WHERE conversation_id=?`, convID); err != nil {
		return err
	}
	for _, n := range names {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO conversation_library_items(conversation_id,item_name) VALUES(?,?)`, convID, n); err != nil {
			return err
		}
	}
	return tx.Commit()
}
