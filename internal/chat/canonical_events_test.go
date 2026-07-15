package chat

import (
	"encoding/json"
	"testing"

	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/google/uuid"
)

// completedTurn persists one full turn: a user message, then a completed run
// for personaID/model with an optional tool round and one assistant_text per
// entry in texts. Returns the turn ID.
func completedTurn(t *testing.T, st *store.Store, convID, userText, personaID, model string, withTool bool, texts ...string) string {
	t.Helper()
	u, err := st.AppendUserMessage(convID, userText)
	if err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	if err := st.CreateRun(convID, u.TurnID, runID, "openai", model, "auto_grounded_default", personaID); err != nil {
		t.Fatal(err)
	}
	if withTool {
		callID := "call-" + runID[:8]
		if _, err := st.AppendAssistantToolCall(convID, u.TurnID, runID, callID,
			"safemath", json.RawMessage(`{"expression":"2+2"}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := st.AppendToolResult(convID, u.TurnID, runID, callID,
			"safemath", "4", nil, false, 3); err != nil {
			t.Fatal(err)
		}
	}
	for _, txt := range texts {
		if _, err := st.AppendAssistantText(convID, u.TurnID, runID, txt); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CompleteRun(runID, store.RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}
	return u.TurnID
}

// currentTurn persists the in-flight turn: a user message plus an in_progress
// run, exactly the state runLoop sees on its first provider call.
func currentTurn(t *testing.T, st *store.Store, convID, userText, personaID, model string) (turnID, runID string) {
	t.Helper()
	u, err := st.AppendUserMessage(convID, userText)
	if err != nil {
		t.Fatal(err)
	}
	runID = uuid.NewString()
	if err := st.CreateRun(convID, u.TurnID, runID, "openai", model, "auto_grounded_default", personaID); err != nil {
		t.Fatal(err)
	}
	return u.TurnID, runID
}

// legacyCanonical is the pre-Spec-2 mapping, inlined verbatim as the
// byte-identical reference: every selected row passes through the six-field
// whitelist. If canonicalEvents diverges from this on a single-persona or
// no-persona thread, an existing conversation replays differently — the one
// failure this feature must never cause.
func legacyCanonical(rows []store.ConversationEvent) []provider.Event {
	out := make([]provider.Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, provider.Event{
			Kind: r.Kind, Text: r.Text,
			ToolCallID: r.ToolCallID, ToolName: r.ToolName,
			ToolInput: r.ToolInput, IsError: r.IsError,
		})
	}
	return out
}

func marshalEvents(t *testing.T, evs []provider.Event) string {
	t.Helper()
	b, err := json.Marshal(evs)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCanonicalEvents_SinglePersonaThreadIsByteIdenticalToLegacy(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("guard")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "q1", "scout", "m1", true, "a1")
	completedTurn(t, st, conv.ID, "q2", "scout", "m1", false, "a2")
	turnID, runID := currentTurn(t, st, conv.ID, "q3", "scout", "m1")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := marshalEvents(t, canonicalEvents(rows, turnID, "scout", nil))
	want := marshalEvents(t, legacyCanonical(rows))
	if got != want {
		t.Errorf("single-persona payload diverged from legacy:\n got %s\nwant %s", got, want)
	}
}

func TestCanonicalEvents_LegacyNoPersonaRunsAreByteIdenticalToLegacy(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("guard-legacy")
	if err != nil {
		t.Fatal(err)
	}
	// Runs recorded before personas existed: persona_id empty. They are the
	// current persona's own voice, never relabeled "From (unknown)".
	completedTurn(t, st, conv.ID, "q1", "", "m1", true, "a1")
	completedTurn(t, st, conv.ID, "q2", "", "m1", false, "a2")
	turnID, runID := currentTurn(t, st, conv.ID, "q3", "scout", "m2")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := marshalEvents(t, canonicalEvents(rows, turnID, "scout", nil))
	want := marshalEvents(t, legacyCanonical(rows))
	if got != want {
		t.Errorf("legacy no-persona payload diverged:\n got %s\nwant %s", got, want)
	}
}
