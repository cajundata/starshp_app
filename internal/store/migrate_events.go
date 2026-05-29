package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// migrateMessagesToEvents is forward-only: every messages row becomes a
// conversation_events row, every user/assistant pair synthesizes a completed
// run with active_for_replay=1, RAG metadata folds into runs.grounding_meta,
// and the messages table is dropped on success.
func migrateMessagesToEvents(db *sql.DB) error {
	has, err := tableExists(db, "messages")
	if err != nil {
		return err
	}
	if !has {
		return nil
	}
	rows, err := db.Query(`SELECT id, conversation_id, role, content,
        COALESCE(model,''), created_at,
        COALESCE(rag_context,''), COALESCE(rag_sources,''),
        COALESCE(input_tokens, 0), COALESCE(output_tokens, 0), COALESCE(cached_input_tokens, 0)
      FROM messages ORDER BY conversation_id, created_at, id`)
	if err != nil {
		return err
	}
	type legacy struct {
		ID, ConvID, Role, Content, Model string
		CreatedAt                        int64
		RAGContext, RAGSources           string
		IT, OT, CT                       int64
	}
	var all []legacy
	for rows.Next() {
		var m legacy
		if err := rows.Scan(&m.ID, &m.ConvID, &m.Role, &m.Content, &m.Model,
			&m.CreatedAt, &m.RAGContext, &m.RAGSources,
			&m.IT, &m.OT, &m.CT); err != nil {
			rows.Close()
			return err
		}
		all = append(all, m)
	}
	rows.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// Per-conversation sequence_index counters.
	seqByConv := map[string]int64{}
	pendingTurn := map[string]string{} // convID -> open turn_id awaiting an assistant
	for _, m := range all {
		seq := seqByConv[m.ConvID]
		switch m.Role {
		case "user":
			// Insert a user_message event; turn_id = event id (matches live writer).
			turnID := m.ID
			_, err := tx.Exec(`INSERT INTO conversation_events
                (id, conversation_id, turn_id, run_id, sequence_index,
                 kind, text, is_error, created_at)
                VALUES (?,?,?,NULL,?,?,?,0,?)`,
				m.ID, m.ConvID, turnID, seq,
				EventKindUserMessage, m.Content, m.CreatedAt)
			if err != nil {
				return fmt.Errorf("insert legacy user_message: %w", err)
			}
			pendingTurn[m.ConvID] = turnID
		case "assistant":
			turnID, ok := pendingTurn[m.ConvID]
			if !ok {
				// Orphan assistant row with no preceding user — skip but
				// consume a sequence number. Should not occur in practice.
				seqByConv[m.ConvID] = seq + 1
				continue
			}
			runID := uuid.NewString()
			providerName := providerFromModel(m.Model)
			mode := "auto_grounded_default"
			// Synthesize a completed run.
			_, err := tx.Exec(`INSERT INTO runs
                (id, conversation_id, turn_id, status, active_for_replay,
                 provider, model, retrieval_mode, grounding_meta,
                 started_at, ended_at, terminal_reason,
                 total_input_tokens, total_output_tokens, total_cached_input_tokens,
                 total_tool_calls, total_iterations)
                VALUES (?,?,?,'completed',1,?,?,?,?,?,?,?,?,?,?,0,1)`,
				runID, m.ConvID, turnID, providerName, m.Model, mode,
				buildLegacyGroundingMeta(m.RAGContext, m.RAGSources),
				m.CreatedAt, m.CreatedAt, "end_turn",
				m.IT, m.OT, m.CT)
			if err != nil {
				return fmt.Errorf("synthesize run: %w", err)
			}
			// Insert the assistant_text event for the assistant row.
			seq++
			seqByConv[m.ConvID] = seq
			_, err = tx.Exec(`INSERT INTO conversation_events
                (id, conversation_id, turn_id, run_id, sequence_index,
                 kind, text, is_error, created_at)
                VALUES (?,?,?,?,?,?,?,0,?)`,
				m.ID, m.ConvID, turnID, runID, seq,
				EventKindAssistantText, m.Content, m.CreatedAt)
			if err != nil {
				return fmt.Errorf("insert legacy assistant_text: %w", err)
			}
			delete(pendingTurn, m.ConvID)
		default:
			// Unknown role — skip but consume a sequence number.
		}
		seqByConv[m.ConvID] = seqByConv[m.ConvID] + 1
	}

	if _, err := tx.Exec(`DROP TABLE messages`); err != nil {
		return fmt.Errorf("drop messages: %w", err)
	}
	return tx.Commit()
}

func buildLegacyGroundingMeta(ragCtx, ragSrc string) sql.NullString {
	if ragCtx == "" && ragSrc == "" {
		meta, _ := json.Marshal(map[string]string{"status": "not_available"})
		return sql.NullString{String: string(meta), Valid: true}
	}
	out := map[string]any{
		"status":         "ready",
		"injected_chars": len(ragCtx),
	}
	if ragSrc != "" {
		var srcs any
		if err := json.Unmarshal([]byte(ragSrc), &srcs); err == nil {
			out["sources"] = srcs
		}
	}
	meta, _ := json.Marshal(out)
	return sql.NullString{String: string(meta), Valid: true}
}

func providerFromModel(model string) string {
	if model == "" {
		return "unknown"
	}
	// Heuristic: anthropic model IDs always begin with "claude-".
	if len(model) >= 7 && model[:7] == "claude-" {
		return "anthropic"
	}
	return "openai"
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var got string
	err := db.QueryRow(`SELECT name FROM sqlite_master
        WHERE type='table' AND name=?`, name).Scan(&got)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
