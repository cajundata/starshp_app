package store

import "time"

// SweepOrphanedRuns reconciles any in_progress runs to errored with
// terminal_reason='orphaned'. Called once at startup after migration.
// Provider replay already excludes orphans at read time (status='completed'
// filter), so this is a follow-up reconciler — not a correctness gate.
func (s *Store) SweepOrphanedRuns() error {
	_, err := s.db.Exec(
		`UPDATE runs
            SET status='errored', active_for_replay=0,
                ended_at=?, terminal_reason='orphaned'
          WHERE status='in_progress'`, time.Now().UnixMilli())
	return err
}
