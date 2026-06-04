// Package store persists conversations, messages, and library items in SQLite.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(dbPath string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(wal)&_pragma=foreign_keys(on)&_pragma=busy_timeout(5000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }
