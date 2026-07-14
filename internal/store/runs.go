package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Run struct {
	ID                     string
	ConversationID         string
	TurnID                 string
	Status                 string
	ActiveForReplay        bool
	Provider               string
	Model                  string
	PersonaID              string
	RetrievalMode          string
	GroundingMeta          json.RawMessage
	StartedAt              int64
	EndedAt                sql.NullInt64
	TerminalReason         sql.NullString
	ErrorCode              sql.NullString
	ErrorMessage           sql.NullString
	TotalInputTokens       int64
	TotalOutputTokens      int64
	TotalCachedInputTokens int64
	TotalToolCalls         int64
	TotalIterations        int64
}

type RunTotals struct {
	InputTokens       int64
	OutputTokens      int64
	CachedInputTokens int64
	ToolCalls         int64
	Iterations        int64
}

var ErrRunNotInProgress = errors.New("run is not in_progress (likely cancelled or errored concurrently)")

func (s *Store) CreateRun(convID, turnID, runID, providerName, model, mode, personaID string) error {
	_, err := s.db.Exec(
		`INSERT INTO runs
            (id, conversation_id, turn_id, status, active_for_replay,
             provider, model, persona_id, retrieval_mode, started_at)
         VALUES (?,?,?,'in_progress',0,?,?,?,?,?)`,
		runID, convID, turnID, providerName, model, nullIfEmpty(personaID), mode,
		time.Now().UnixMilli())
	return err
}

func (s *Store) SetRunGroundingMeta(runID string, meta json.RawMessage) error {
	_, err := s.db.Exec(`UPDATE runs SET grounding_meta = ? WHERE id = ?`,
		string(meta), runID)
	return err
}

// CompleteRun atomically demotes any prior active run for the turn and
// activates this run. If the activate UPDATE affects zero rows the run is no
// longer in_progress (concurrent cancel/error) — the transaction rolls back
// and ErrRunNotInProgress is returned.
func (s *Store) CompleteRun(runID string, totals RunTotals, terminalReason string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // safe no-op after a successful commit

	var convID, turnID string
	if err := tx.QueryRow(
		`SELECT conversation_id, turn_id FROM runs WHERE id = ?`, runID,
	).Scan(&convID, &turnID); err != nil {
		return fmt.Errorf("lookup run: %w", err)
	}

	if _, err := tx.Exec(
		`UPDATE runs SET active_for_replay = 0
          WHERE turn_id = ? AND active_for_replay = 1`, turnID); err != nil {
		return fmt.Errorf("demote prior active: %w", err)
	}

	res, err := tx.Exec(
		`UPDATE runs
            SET status='completed', active_for_replay=1,
                ended_at=?, terminal_reason=?,
                total_input_tokens=?, total_output_tokens=?,
                total_cached_input_tokens=?, total_tool_calls=?,
                total_iterations=?
          WHERE id=? AND status='in_progress'`,
		time.Now().UnixMilli(), terminalReason,
		totals.InputTokens, totals.OutputTokens, totals.CachedInputTokens,
		totals.ToolCalls, totals.Iterations, runID)
	if err != nil {
		return fmt.Errorf("activate completing run: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrRunNotInProgress
	}
	return tx.Commit()
}

// MarkRunErrored sets a terminal error state. It never touches
// active_for_replay on any other run.
func (s *Store) MarkRunErrored(runID, terminalReason, errCode, errMsg string) error {
	_, err := s.db.Exec(
		`UPDATE runs
            SET status='errored', active_for_replay=0,
                ended_at=?, terminal_reason=?, error_code=?, error_message=?
          WHERE id=?`,
		time.Now().UnixMilli(), terminalReason, errCode, errMsg, runID)
	return err
}

// MarkRunCancelled sets cancellation state. It never touches
// active_for_replay on any other run.
func (s *Store) MarkRunCancelled(runID, terminalReason string) error {
	_, err := s.db.Exec(
		`UPDATE runs
            SET status='cancelled', active_for_replay=0,
                ended_at=?, terminal_reason=?
          WHERE id=?`,
		time.Now().UnixMilli(), terminalReason, runID)
	return err
}

func (s *Store) GetRun(runID string) (Run, error) {
	var r Run
	var meta sql.NullString
	err := s.db.QueryRow(
		`SELECT id, conversation_id, turn_id, status, active_for_replay,
                provider, model, COALESCE(persona_id,''), retrieval_mode, grounding_meta,
                started_at, ended_at, terminal_reason, error_code, error_message,
                total_input_tokens, total_output_tokens, total_cached_input_tokens,
                total_tool_calls, total_iterations
           FROM runs WHERE id = ?`, runID,
	).Scan(
		&r.ID, &r.ConversationID, &r.TurnID, &r.Status, &r.ActiveForReplay,
		&r.Provider, &r.Model, &r.PersonaID, &r.RetrievalMode, &meta,
		&r.StartedAt, &r.EndedAt, &r.TerminalReason, &r.ErrorCode, &r.ErrorMessage,
		&r.TotalInputTokens, &r.TotalOutputTokens, &r.TotalCachedInputTokens,
		&r.TotalToolCalls, &r.TotalIterations)
	if err != nil {
		return Run{}, err
	}
	if meta.Valid {
		r.GroundingMeta = json.RawMessage(meta.String)
	}
	return r, nil
}
