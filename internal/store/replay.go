package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// turnSelection picks one run per turn. exclude lists turns dropped from the
// provider payload (state='never'); it is nil on the display path — the
// filter is a provider-path-only parameter, never a shared-helper default.
func (s *Store) turnSelection(convID, sqlOrderedRuns string, currentRunID string, exclude map[string]struct{}) ([]string, error) {
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
		if _, skip := exclude[turn]; skip {
			continue
		}
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
	exclude, err := s.neverTurnsForReplay(convID, currentRunID)
	if err != nil {
		return nil, fmt.Errorf("override selection: %w", err)
	}
	runs, err := s.turnSelection(convID,
		`SELECT id FROM runs
          WHERE turn_id = ? AND active_for_replay = 1 AND status = 'completed'
          LIMIT 1`, currentRunID, exclude)
	if err != nil {
		return nil, fmt.Errorf("provider replay selection: %w", err)
	}
	return s.eventsForRunsPlusUserMessages(convID, runs, currentRunID, exclude)
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
          LIMIT 1`, "", nil)
	if err != nil {
		return nil, fmt.Errorf("display selection: %w", err)
	}
	events, err := s.eventsForRunsPlusUserMessages(convID, runs, "", nil)
	if err != nil {
		return nil, err
	}
	// A run error lives on the run record, not as an event. Append a synthetic
	// run_error event for any selected run that errored, so a reopened
	// conversation shows it (mirrors the live chat:run_errored rendering).
	for _, runID := range runs {
		run, gerr := s.GetRun(runID)
		if gerr != nil || run.Status != "errored" {
			continue
		}
		events = append(events, ConversationEvent{
			ConversationID: convID,
			TurnID:         run.TurnID,
			RunID:          runID,
			PersonaID:      run.PersonaID,
			Model:          run.Model,
			Kind:           "run_error",
			Text:           runErrorDisplayText(run),
		})
	}
	return events, nil
}

// runErrorDisplayText formats an errored run's code+message the same way the
// live chat:run_errored handler renders it ("[code] message").
func runErrorDisplayText(run Run) string {
	code := run.ErrorCode.String
	if code == "" {
		code = "error"
	}
	return "[" + code + "] " + run.ErrorMessage.String
}

func (s *Store) eventsForRunsPlusUserMessages(convID string, runIDs []string, currentRunID string, exclude map[string]struct{}) ([]ConversationEvent, error) {
	runSet := map[string]struct{}{}
	for _, id := range runIDs {
		runSet[id] = struct{}{}
	}
	if currentRunID != "" {
		runSet[currentRunID] = struct{}{}
	}
	// turn_context_overrides has turn_id as its PRIMARY KEY, so the LEFT JOIN
	// contributes at most one row per event — no fan-out.
	rows, err := s.db.Query(
		`SELECT e.id, e.conversation_id, e.turn_id, COALESCE(e.run_id,''),
                e.sequence_index, e.kind, COALESCE(e.text,''),
                COALESCE(e.tool_call_id,''), COALESCE(e.tool_name,''),
                COALESCE(e.tool_input,''), COALESCE(e.tool_metadata,''),
                COALESCE(e.tool_result_hash,''),
                COALESCE(e.tool_latency_ms,0), COALESCE(e.image_hash,''),
                e.is_error, e.created_at,
                COALESCE(r.persona_id,''), COALESCE(r.model,''),
                COALESCE(o.state,'')
           FROM conversation_events e
           LEFT JOIN runs r ON r.id = e.run_id
           LEFT JOIN turn_context_overrides o ON o.turn_id = e.turn_id
          WHERE e.conversation_id = ?
          ORDER BY e.sequence_index`, convID)
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
			&ev.ToolResultHash, &ev.ToolLatencyMs, &ev.ImageHash, &isErrInt, &ev.CreatedAt,
			&ev.PersonaID, &ev.Model, &ev.ContextOverride,
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
			// A never turn's user_message goes too — a dangling question
			// invites the model to re-answer it. exclude is nil on the
			// display path, so this drop is provider-path-only.
			if _, skip := exclude[ev.TurnID]; skip {
				continue
			}
			out = append(out, ev)
			continue
		}
		if _, ok := runSet[ev.RunID]; ok {
			out = append(out, ev)
		}
	}
	return out, rows.Err()
}
