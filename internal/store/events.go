package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	EventKindUserMessage       = "user_message"
	EventKindAssistantText     = "assistant_text"
	EventKindAssistantToolCall = "assistant_tool_call"
	EventKindToolResult        = "tool_result"
)

type ConversationEvent struct {
	ID              string          `json:"id"`
	ConversationID  string          `json:"conversationId"`
	TurnID          string          `json:"turnId"`
	RunID           string          `json:"runId,omitempty"`
	PersonaID       string          `json:"personaId,omitempty"`
	Model           string          `json:"model,omitempty"`
	ContextOverride string          `json:"contextOverride,omitempty"`
	SequenceIndex   int64           `json:"sequenceIndex"`
	Kind            string          `json:"kind"`
	Text            string          `json:"text,omitempty"`
	ToolCallID      string          `json:"toolCallId,omitempty"`
	ToolName        string          `json:"toolName,omitempty"`
	ToolInput       json.RawMessage `json:"toolInput,omitempty"`
	ToolMetadata    json.RawMessage `json:"toolMetadata,omitempty"`
	ToolResultHash  string          `json:"toolResultHash,omitempty"`
	ToolLatencyMs   int64           `json:"toolLatencyMs,omitempty"`
	IsError         bool            `json:"isError,omitempty"`
	CreatedAt       int64           `json:"createdAt"`
}

// nextSequenceIndex returns the next monotonic sequence index for a
// conversation. Holes from deleted events are tolerated; the counter never
// regresses.
func nextSequenceIndex(s *Store, convID string) (int64, error) {
	var next int64
	err := s.db.QueryRow(
		`SELECT COALESCE(MAX(sequence_index)+1, 0)
           FROM conversation_events
          WHERE conversation_id = ?`, convID).Scan(&next)
	return next, err
}

func (s *Store) AppendUserMessage(convID, text string) (ConversationEvent, error) {
	id := uuid.NewString()
	seq, err := nextSequenceIndex(s, convID)
	if err != nil {
		return ConversationEvent{}, err
	}
	ev := ConversationEvent{
		ID: id, ConversationID: convID, TurnID: id, // turn_id = user_message id
		SequenceIndex: seq, Kind: EventKindUserMessage, Text: text,
		CreatedAt: time.Now().UnixMilli(),
	}
	_, err = s.db.Exec(
		`INSERT INTO conversation_events
            (id, conversation_id, turn_id, run_id, sequence_index, kind, text,
             tool_call_id, tool_name, tool_input, tool_metadata,
             tool_result_hash, tool_latency_ms, is_error, created_at)
         VALUES (?,?,?,NULL,?,?,?,NULL,NULL,NULL,NULL,NULL,NULL,0,?)`,
		ev.ID, ev.ConversationID, ev.TurnID, ev.SequenceIndex, ev.Kind, ev.Text,
		ev.CreatedAt)
	return ev, err
}

func (s *Store) AppendAssistantText(convID, turnID, runID, text string) (ConversationEvent, error) {
	id := uuid.NewString()
	seq, err := nextSequenceIndex(s, convID)
	if err != nil {
		return ConversationEvent{}, err
	}
	ev := ConversationEvent{
		ID: id, ConversationID: convID, TurnID: turnID, RunID: runID,
		SequenceIndex: seq, Kind: EventKindAssistantText, Text: text,
		CreatedAt: time.Now().UnixMilli(),
	}
	_, err = s.db.Exec(
		`INSERT INTO conversation_events
            (id, conversation_id, turn_id, run_id, sequence_index, kind, text,
             is_error, created_at)
         VALUES (?,?,?,?,?,?,?,0,?)`,
		ev.ID, convID, turnID, runID, ev.SequenceIndex, ev.Kind, ev.Text, ev.CreatedAt)
	return ev, err
}

func (s *Store) AppendAssistantToolCall(
	convID, turnID, runID, toolCallID, toolName string, input json.RawMessage,
) (ConversationEvent, error) {
	id := uuid.NewString()
	seq, err := nextSequenceIndex(s, convID)
	if err != nil {
		return ConversationEvent{}, err
	}
	ev := ConversationEvent{
		ID: id, ConversationID: convID, TurnID: turnID, RunID: runID,
		SequenceIndex: seq, Kind: EventKindAssistantToolCall,
		ToolCallID: toolCallID, ToolName: toolName, ToolInput: input,
		CreatedAt: time.Now().UnixMilli(),
	}
	_, err = s.db.Exec(
		`INSERT INTO conversation_events
            (id, conversation_id, turn_id, run_id, sequence_index, kind,
             tool_call_id, tool_name, tool_input, is_error, created_at)
         VALUES (?,?,?,?,?,?,?,?,?,0,?)`,
		ev.ID, convID, turnID, runID, ev.SequenceIndex, ev.Kind,
		toolCallID, toolName, string(input), ev.CreatedAt)
	return ev, err
}

func (s *Store) AppendToolResult(
	convID, turnID, runID, toolCallID, toolName, output string,
	metadata json.RawMessage, isError bool, latencyMs int64,
) (ConversationEvent, error) {
	id := uuid.NewString()
	seq, err := nextSequenceIndex(s, convID)
	if err != nil {
		return ConversationEvent{}, err
	}
	sum := sha256.Sum256([]byte(output))
	hash := hex.EncodeToString(sum[:])
	ev := ConversationEvent{
		ID: id, ConversationID: convID, TurnID: turnID, RunID: runID,
		SequenceIndex: seq, Kind: EventKindToolResult,
		ToolCallID: toolCallID, ToolName: toolName, Text: output,
		ToolMetadata: metadata, ToolResultHash: hash, ToolLatencyMs: latencyMs,
		IsError: isError, CreatedAt: time.Now().UnixMilli(),
	}
	isErrInt := 0
	if isError {
		isErrInt = 1
	}
	_, err = s.db.Exec(
		`INSERT INTO conversation_events
            (id, conversation_id, turn_id, run_id, sequence_index, kind, text,
             tool_call_id, tool_name, tool_metadata, tool_result_hash,
             tool_latency_ms, is_error, created_at)
         VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ev.ID, convID, turnID, runID, ev.SequenceIndex, ev.Kind, ev.Text,
		toolCallID, toolName, string(metadata), hash, latencyMs, isErrInt,
		ev.CreatedAt)
	if err != nil {
		return ConversationEvent{}, fmt.Errorf("append tool_result: %w", err)
	}
	return ev, err
}
