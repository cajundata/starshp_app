package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

func (s *Store) turnSelection(convID, sqlOrderedRuns string, currentRunID string) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT id FROM conversation_events
          WHERE conversation_id = ? AND kind = 'user_message'
          ORDER BY sequence_index`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var turns []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		turns = append(turns, t)
	}
	var pickedRuns []string
	for _, turn := range turns {
		if currentRunID != "" {
			var got string
			err := s.db.QueryRow(
				`SELECT id FROM runs WHERE id = ? AND turn_id = ? AND status = 'in_progress'`,
				currentRunID, turn).Scan(&got)
			if err == nil {
				pickedRuns = append(pickedRuns, got)
				continue
			} else if err != sql.ErrNoRows {
				return nil, err
			}
		}
		var runID sql.NullString
		err := s.db.QueryRow(sqlOrderedRuns, turn).Scan(&runID)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		if runID.Valid {
			pickedRuns = append(pickedRuns, runID.String)
		}
	}
	return pickedRuns, nil
}

func (s *Store) GetProviderReplayEvents(convID, currentRunID string) ([]ConversationEvent, error) {
	runs, err := s.turnSelection(convID,
		`SELECT id FROM runs
          WHERE turn_id = ? AND active_for_replay = 1 AND status = 'completed'
          LIMIT 1`, currentRunID)
	if err != nil {
		return nil, fmt.Errorf("provider replay selection: %w", err)
	}
	return s.eventsForRunsPlusUserMessages(convID, runs, currentRunID)
}

func (s *Store) GetConversationDisplayEvents(convID string) ([]ConversationEvent, error) {
	runs, err := s.turnSelection(convID,
		`SELECT id FROM runs
          WHERE turn_id = ?
          ORDER BY active_for_replay DESC,
                   CASE status
                       WHEN 'completed' THEN 0
                       WHEN 'cancelled' THEN 1
                       WHEN 'errored'   THEN 2
                       WHEN 'in_progress' THEN 3
                   END,
                   COALESCE(ended_at, 0) DESC,
                   started_at DESC
          LIMIT 1`, "")
	if err != nil {
		return nil, fmt.Errorf("display selection: %w", err)
	}
	return s.eventsForRunsPlusUserMessages(convID, runs, "")
}

func (s *Store) eventsForRunsPlusUserMessages(convID string, runIDs []string, currentRunID string) ([]ConversationEvent, error) {
	runSet := map[string]struct{}{}
	for _, id := range runIDs {
		runSet[id] = struct{}{}
	}
	if currentRunID != "" {
		runSet[currentRunID] = struct{}{}
	}
	rows, err := s.db.Query(
		`SELECT id, conversation_id, turn_id, COALESCE(run_id,''),
                sequence_index, kind, COALESCE(text,''),
                COALESCE(tool_call_id,''), COALESCE(tool_name,''),
                COALESCE(tool_input,''), COALESCE(tool_metadata,''),
                COALESCE(tool_result_hash,''),
                COALESCE(tool_latency_ms,0), is_error, created_at
           FROM conversation_events
          WHERE conversation_id = ?
          ORDER BY sequence_index`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConversationEvent
	for rows.Next() {
		var ev ConversationEvent
		var input, meta string
		var isErrInt int
		if err := rows.Scan(
			&ev.ID, &ev.ConversationID, &ev.TurnID, &ev.RunID,
			&ev.SequenceIndex, &ev.Kind, &ev.Text,
			&ev.ToolCallID, &ev.ToolName, &input, &meta,
			&ev.ToolResultHash, &ev.ToolLatencyMs, &isErrInt, &ev.CreatedAt,
		); err != nil {
			return nil, err
		}
		if input != "" {
			ev.ToolInput = json.RawMessage(input)
		}
		if meta != "" {
			ev.ToolMetadata = json.RawMessage(meta)
		}
		ev.IsError = isErrInt != 0
		if ev.Kind == EventKindUserMessage {
			out = append(out, ev)
			continue
		}
		if _, ok := runSet[ev.RunID]; ok {
			out = append(out, ev)
		}
	}
	return out, rows.Err()
}
