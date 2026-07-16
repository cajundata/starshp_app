package store

import (
	"encoding/json"
	"testing"
)

func TestOrphanRun_ExcludedFromProviderReplay(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	u, _ := st.AppendUserMessage(conv.ID, "q")
	_ = st.CreateRun(conv.ID, u.TurnID, "rOrphan", "openai", "gpt", "auto_grounded_default", "")
	_, _ = st.AppendAssistantToolCall(conv.ID, u.TurnID, "rOrphan", "callX",
		"search_textbook", json.RawMessage(`{"query":"x"}`), nil)
	events, err := st.GetProviderReplayEvents(conv.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.RunID == "rOrphan" {
			t.Fatalf("orphan events must not appear in provider replay: %+v", e)
		}
	}
}

func TestSweepOrphans_ReconcilesStatus(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	u, _ := st.AppendUserMessage(conv.ID, "q")
	_ = st.CreateRun(conv.ID, u.TurnID, "rOrphan", "openai", "gpt", "auto_grounded_default", "")
	_, _ = st.AppendAssistantToolCall(conv.ID, u.TurnID, "rOrphan", "callX",
		"search_textbook", json.RawMessage(`{"query":"x"}`), nil)
	if err := st.SweepOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	r, _ := st.GetRun("rOrphan")
	if r.Status != "errored" {
		t.Fatalf("orphan status want errored, got %q", r.Status)
	}
	if !r.TerminalReason.Valid || r.TerminalReason.String != "orphaned" {
		t.Fatalf("terminal_reason want orphaned, got %v", r.TerminalReason)
	}
}

func TestSweepOrphans_TreatsAllInProgressAsOrphan(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	u, _ := st.AppendUserMessage(conv.ID, "q")
	_ = st.CreateRun(conv.ID, u.TurnID, "rPartialText", "openai", "gpt", "auto_grounded_default", "")
	_, _ = st.AppendAssistantText(conv.ID, u.TurnID, "rPartialText", "partial")
	if err := st.SweepOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	r, _ := st.GetRun("rPartialText")
	if r.Status != "errored" {
		t.Fatalf("any in_progress run at sweep time is by definition orphaned (no live writer); got %q", r.Status)
	}
}
