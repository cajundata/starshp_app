package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// openTestStore opens a Store backed by a temp-file DB. A file (not :memory:)
// is required because database/sql pools connections and modernc.org/sqlite
// gives each connection its own :memory: database — multi-statement tests
// would otherwise see missing tables/rows intermittently.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestAppendUserMessage_AssignsTurnIDAndSequence(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	ev1, err := st.AppendUserMessage(conv.ID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if ev1.TurnID != ev1.ID {
		t.Fatalf("turn_id should equal user_message id; got %q vs %q", ev1.TurnID, ev1.ID)
	}
	if ev1.SequenceIndex != 0 {
		t.Fatalf("first event seq should be 0, got %d", ev1.SequenceIndex)
	}
	ev2, _ := st.AppendUserMessage(conv.ID, "second")
	if ev2.SequenceIndex != 1 {
		t.Fatalf("second event seq should be 1, got %d", ev2.SequenceIndex)
	}
	if ev2.TurnID == ev1.TurnID {
		t.Fatal("second user message should start a new turn")
	}
}

func TestAppendAssistantText_PreservesOrder(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	user, _ := st.AppendUserMessage(conv.ID, "q")
	runID := "r1"
	if err := st.CreateRun(conv.ID, user.TurnID, runID, "openai", "gpt-x",
		"auto_grounded_default", ""); err != nil {
		t.Fatal(err)
	}
	a1, err := st.AppendAssistantText(conv.ID, user.TurnID, runID, "first")
	if err != nil {
		t.Fatal(err)
	}
	a2, err := st.AppendAssistantText(conv.ID, user.TurnID, runID, "second")
	if err != nil {
		t.Fatal(err)
	}
	if a1.SequenceIndex >= a2.SequenceIndex {
		t.Fatalf("expected ascending seq; got %d then %d", a1.SequenceIndex, a2.SequenceIndex)
	}
}

func TestAppendAssistantToolCall_PersistsInputJSON(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	user, _ := st.AppendUserMessage(conv.ID, "q")
	runID := "r1"
	_ = st.CreateRun(conv.ID, user.TurnID, runID, "openai", "gpt-x", "auto_grounded_default", "")
	input := json.RawMessage(`{"query":"realization principle"}`)
	ev, err := st.AppendAssistantToolCall(conv.ID, user.TurnID, runID, "call_1",
		"search_textbook", input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ev.ToolName != "search_textbook" || ev.ToolCallID != "call_1" {
		t.Fatalf("metadata mismatch: %+v", ev)
	}
	if string(ev.ToolInput) != string(input) {
		t.Fatalf("input mismatch: want %s, got %s", input, ev.ToolInput)
	}
	if ev.ToolMetadata != nil {
		t.Fatalf("ToolMetadata = %s, want nil when no metadata passed", ev.ToolMetadata)
	}
}

// TestAppendAssistantToolCall_ThoughtSignatureRoundTrips is the store-level
// half of the Gemini thought_signature invariant: metadata written at append
// time comes back byte-identical through the provider replay read path.
func TestAppendAssistantToolCall_ThoughtSignatureRoundTrips(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	user, _ := st.AppendUserMessage(conv.ID, "q")
	runID := "r1"
	_ = st.CreateRun(conv.ID, user.TurnID, runID, "gemini", "gemini-3-pro", "auto_grounded_default", "")
	input := json.RawMessage(`{"expr":"2+2"}`)
	meta := json.RawMessage(`{"thought_signature":"c2lnLWJ5dGVzLTE="}`)
	ev, err := st.AppendAssistantToolCall(conv.ID, user.TurnID, runID, "call_1",
		"safe_math", input, meta)
	if err != nil {
		t.Fatal(err)
	}
	if string(ev.ToolMetadata) != string(meta) {
		t.Fatalf("append-time ToolMetadata mismatch: want %s, got %s", meta, ev.ToolMetadata)
	}
	if _, err := st.AppendToolResult(conv.ID, user.TurnID, runID, "call_1",
		"safe_math", "4", nil, false, 3); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteRun(runID, RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}
	events, err := st.GetProviderReplayEvents(conv.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Kind == EventKindAssistantToolCall && e.ToolCallID == "call_1" {
			found = true
			if string(e.ToolMetadata) != string(meta) {
				t.Fatalf("replayed ToolMetadata = %s, want %s", e.ToolMetadata, meta)
			}
		}
	}
	if !found {
		t.Fatal("assistant_tool_call event not found in replay")
	}
}

func TestAppendToolResult_PersistsMetadataAndHash(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	user, _ := st.AppendUserMessage(conv.ID, "q")
	runID := "r1"
	_ = st.CreateRun(conv.ID, user.TurnID, runID, "openai", "gpt-x", "auto_grounded_default", "")
	_, _ = st.AppendAssistantToolCall(conv.ID, user.TurnID, runID, "call_1",
		"safe_math", json.RawMessage(`{"expression":"1+1"}`), nil)
	meta := json.RawMessage(`{"normalized_expression":"1+1"}`)
	ev, err := st.AppendToolResult(conv.ID, user.TurnID, runID, "call_1",
		"safe_math", "2", meta /*isError*/, false /*latencyMs*/, 3)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Text != "2" || ev.IsError {
		t.Fatalf("payload mismatch: %+v", ev)
	}
	if ev.ToolResultHash == "" {
		t.Fatal("tool_result_hash should be populated")
	}
	if string(ev.ToolMetadata) != string(meta) {
		t.Fatalf("metadata mismatch: %s", ev.ToolMetadata)
	}
}
