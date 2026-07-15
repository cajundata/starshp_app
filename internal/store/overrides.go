package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// Override states for a turn's contribution to the provider payload. auto is
// the absence of a row — it is "stored" by deleting — so only always and
// never persist (see turn_context_overrides in schema.go). Overrides shape
// what the model sees, never what the operator sees: the display path does
// not consult them.
const (
	OverrideAuto   = "auto"
	OverrideAlways = "always"
	OverrideNever  = "never"
)

// ErrUnknownTurn reports an override write against a turn that does not
// exist in the given conversation. appapi maps it to AppError{Code:"config"}.
var ErrUnknownTurn = errors.New("unknown turn")

// SetTurnContextOverride records the operator's per-turn context override.
// For auto, the row is deleted (absence is the default state); for always/never,
// rows are upserted. The turn must be a user_message event of convID for all
// states — turn IDs are user_message event IDs, so this also rejects a turn
// that belongs to another conversation.
func (s *Store) SetTurnContextOverride(convID, turnID, state string) error {
	// Validate state first.
	switch state {
	case OverrideAuto, OverrideAlways, OverrideNever:
	default:
		return fmt.Errorf("invalid override state %q", state)
	}

	// All states require the turn to exist in convID.
	var id string
	err := s.db.QueryRow(
		`SELECT id FROM conversation_events
          WHERE id = ? AND conversation_id = ? AND kind = 'user_message'`,
		turnID, convID).Scan(&id)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: %s", ErrUnknownTurn, turnID)
	}
	if err != nil {
		return err
	}

	// auto deletes the row; always/never upsert.
	if state == OverrideAuto {
		_, err := s.db.Exec(
			`DELETE FROM turn_context_overrides WHERE turn_id = ?`, turnID)
		return err
	}

	_, err = s.db.Exec(
		`INSERT INTO turn_context_overrides (conversation_id, turn_id, state)
         VALUES (?,?,?)
         ON CONFLICT(turn_id) DO UPDATE SET state = excluded.state`,
		convID, turnID, state)
	return err
}

// GetTurnContextOverrides returns turn → state for every override row in the
// conversation, for UI seeding on conversation open. Turns in auto have no
// row and are absent from the map.
func (s *Store) GetTurnContextOverrides(convID string) (map[string]string, error) {
	rows, err := s.db.Query(
		`SELECT turn_id, state FROM turn_context_overrides
          WHERE conversation_id = ?`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var turn, state string
		if err := rows.Scan(&turn, &state); err != nil {
			return nil, err
		}
		out[turn] = state
	}
	return out, rows.Err()
}

// neverTurnsForReplay returns the turns excluded from the provider payload:
// every turn marked never, except the current run's own turn — an override
// governs the turn as history for later turns, never the turn being answered
// (rule 2: a rerun of a never turn still gets its own user message as its
// prompt). Only GetProviderReplayEvents calls this; the display path never
// consults overrides (rule 1).
func (s *Store) neverTurnsForReplay(convID, currentRunID string) (map[string]struct{}, error) {
	rows, err := s.db.Query(
		`SELECT turn_id FROM turn_context_overrides
          WHERE conversation_id = ? AND state = 'never'`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	exclude := map[string]struct{}{}
	for rows.Next() {
		var turn string
		if err := rows.Scan(&turn); err != nil {
			return nil, err
		}
		exclude[turn] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(exclude) == 0 || currentRunID == "" {
		return exclude, nil
	}
	var current string
	err = s.db.QueryRow(`SELECT turn_id FROM runs WHERE id = ?`, currentRunID).Scan(&current)
	if err == sql.ErrNoRows {
		return exclude, nil
	}
	if err != nil {
		return nil, err
	}
	delete(exclude, current)
	return exclude, nil
}
