package eval

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/eval/fakeprovider"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/cajundata/starshp_app/internal/tools"
)

// openStore uses a temp-file DB rather than ":memory:": database/sql pools
// connections and modernc.org/sqlite gives each connection its own in-memory
// database, which breaks the multi-statement assertions these tests rely on.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "eval.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

type emptyResolver struct{}

func (emptyResolver) Resolve(_ context.Context, _ string) ([]chat.TextbookEntry, error) {
	return nil, nil
}

// writeCheckTool observes, at execution time, whether its own
// assistant_tool_call event has already been persisted to the provider-replay
// timeline for the in-progress run. The loop's write-before-dispatch contract
// guarantees the row is written and committed before Execute is called, so a
// correct loop leaves sawCall=true. A loop that dispatched before persisting
// would leave it false.
type writeCheckTool struct {
	st      *store.Store
	convID  string
	sawCall bool
}

func (w *writeCheckTool) Name() string                 { return "wc" }
func (w *writeCheckTool) Description() string          { return "write-before-dispatch probe" }
func (w *writeCheckTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (w *writeCheckTool) Timeout() time.Duration       { return 0 }

func (w *writeCheckTool) Execute(_ context.Context, ec tools.ExecContext, _ json.RawMessage) (tools.ExecResult, error) {
	events, err := w.st.GetProviderReplayEvents(w.convID, ec.RunID)
	if err != nil {
		return tools.ExecResult{}, err
	}
	for _, e := range events {
		if e.Kind == store.EventKindAssistantToolCall && e.ToolName == "wc" {
			w.sawCall = true
		}
	}
	return tools.ExecResult{Output: "ok"}, nil
}

// TestLoop_WriteBeforeDispatch drives the real Service + store + Registry
// through one tool-calling iteration and asserts the assistant_tool_call row
// was durably persisted before the tool ran.
func TestLoop_WriteBeforeDispatch(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("c")
	if err != nil {
		t.Fatal(err)
	}
	svc := chat.New(st)
	reg := tools.NewRegistry(time.Second)
	wc := &writeCheckTool{st: st, convID: conv.ID}
	if err := reg.Register(wc); err != nil {
		t.Fatal(err)
	}

	prov := &fakeprovider.Scripted{Iterations: [][]provider.Delta{
		{
			{ToolCall: &provider.ToolCall{ID: "c1", Name: "wc", Input: json.RawMessage(`{}`)}},
			{Done: true, StopReason: "tool_use"},
		},
		{{Text: "done"}, {Done: true, StopReason: "end_turn"}},
	}}
	sink := &CaptureSink{}

	if _, err := svc.Send(context.Background(), chat.SendParams{
		ConversationID: conv.ID, UserText: "q", Model: "m",
		Provider: prov, Registry: reg, Resolver: emptyResolver{},
		RetrievalMode: chat.RetrievalAutoGroundedDefault, Sink: sink,
	}, nil); err != nil {
		t.Fatal(err)
	}
	if !wc.sawCall {
		t.Fatal("assistant_tool_call row was not persisted before the tool ran (write-before-dispatch violated)")
	}
	// The harness sink should have observed the full happy-path lifecycle.
	for _, want := range []chat.SinkEventKind{
		chat.SinkRunStarted, chat.SinkToolCall, chat.SinkToolResult,
		chat.SinkRunCompleted,
	} {
		if !sink.Has(want) {
			t.Errorf("expected sink event %s; got %v", want, sink.Kinds())
		}
	}
}

// TestLoop_OneActiveRunPerTurnUnderRegenerate completes a turn, then simulates
// a regeneration of that same turn (a second completed run) and asserts the
// invariant the partial unique index enforces: exactly one run is active for
// replay per turn, the latest completion wins, and provider replay returns only
// the regenerated answer.
func TestLoop_OneActiveRunPerTurnUnderRegenerate(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("c")
	if err != nil {
		t.Fatal(err)
	}
	svc := chat.New(st)
	reg := tools.NewRegistry(time.Second)

	prov := &fakeprovider.Scripted{Iterations: [][]provider.Delta{
		{{Text: "first answer"}, {Done: true, StopReason: "end_turn"}},
	}}
	res, err := svc.Send(context.Background(), chat.SendParams{
		ConversationID: conv.ID, UserText: "q", Model: "m",
		Provider: prov, Registry: reg, Resolver: emptyResolver{},
		RetrievalMode: chat.RetrievalAutoGroundedDefault,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r1, err := st.GetRun(res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !r1.ActiveForReplay {
		t.Fatal("first run should be active after completion")
	}

	// Regenerate the same turn: a fresh run over the same turn_id, completed.
	// (The UI regenerate command is a later milestone; the backend is ready,
	// so we drive the store directly the way that command will.)
	const r2ID = "regen-run-2"
	if err := st.CreateRun(conv.ID, r1.TurnID, r2ID, "openai", "m",
		string(chat.RetrievalAutoGroundedDefault), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendAssistantText(conv.ID, r1.TurnID, r2ID, "second answer"); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteRun(r2ID, store.RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}

	// The prior run must have been demoted; the regenerated run is now active.
	r1, _ = st.GetRun(res.RunID)
	r2, _ := st.GetRun(r2ID)
	if r1.ActiveForReplay {
		t.Fatal("first run should have been demoted by the regeneration")
	}
	if !r2.ActiveForReplay {
		t.Fatal("regenerated run should be active for replay")
	}

	// Exactly one assistant_text survives in the provider-replay timeline for
	// the turn, and it is the regenerated answer.
	replay, err := st.GetProviderReplayEvents(conv.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, e := range replay {
		if e.Kind == store.EventKindAssistantText {
			texts = append(texts, e.Text)
		}
	}
	if len(texts) != 1 || texts[0] != "second answer" {
		t.Fatalf("replay should contain only the regenerated answer; got %v", texts)
	}
}
