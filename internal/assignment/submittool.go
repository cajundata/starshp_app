package assignment

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cajundata/starshp_app/internal/tools"
)

// SubmitAnswerName is the tool name the solver must call.
const SubmitAnswerName = "submit_answer"

// SubmitAnswer is a per-question tool whose input IS the structured answer.
// The orchestrator recovers the answer from the persisted assistant_tool_call
// event, so Execute only returns a confirmation that ends the model's turn.
type SubmitAnswer struct {
	schema json.RawMessage
}

func NewSubmitAnswer(q Question) *SubmitAnswer {
	return &SubmitAnswer{schema: BuildSubmitAnswerSchema(q)}
}

func (s *SubmitAnswer) Name() string                 { return SubmitAnswerName }
func (s *SubmitAnswer) InputSchema() json.RawMessage { return s.schema }
func (s *SubmitAnswer) Timeout() time.Duration       { return 5 * time.Second }

func (s *SubmitAnswer) Description() string {
	return "Submit your final structured answer for this question. Call exactly once, then stop."
}

func (s *SubmitAnswer) Execute(_ context.Context, _ tools.ExecContext, _ json.RawMessage) (tools.ExecResult, error) {
	return tools.ExecResult{Output: `{"status":"answer_recorded"}`}, nil
}
