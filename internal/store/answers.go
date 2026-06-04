package store

import (
	"database/sql"
	"encoding/json"
)

// GetSubmittedAnswer returns the input JSON of the latest submit_answer
// assistant_tool_call event for a run, or nil if the run never submitted one.
func (s *Store) GetSubmittedAnswer(runID string) (json.RawMessage, error) {
	var input string
	err := s.db.QueryRow(
		`SELECT COALESCE(tool_input,'')
           FROM conversation_events
          WHERE run_id = ? AND kind = 'assistant_tool_call' AND tool_name = 'submit_answer'
          ORDER BY sequence_index DESC
          LIMIT 1`, runID).Scan(&input)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if input == "" { // row existed but tool_input was NULL — treat as no answer
		return nil, nil
	}
	return json.RawMessage(input), nil
}
