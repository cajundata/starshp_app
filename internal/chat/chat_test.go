package chat

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/cajundata/starshp_app/internal/tools"
	"github.com/cajundata/starshp_app/internal/tools/probe"
)

// minimal fake provider: emits one assistant_text delta then Done with end_turn.
type oneShotProvider struct{ text string }

func (o oneShotProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.Delta, error) {
	ch := make(chan provider.Delta, 3)
	ch <- provider.Delta{Text: o.text}
	ch <- provider.Delta{Done: true, StopReason: "end_turn",
		Usage: &provider.Usage{InputTokens: 10, OutputTokens: 5}}
	close(ch)
	return ch, nil
}

type captureSink struct{ events []SinkEvent }

func (c *captureSink) Emit(e SinkEvent) { c.events = append(c.events, e) }

type emptyResolver struct{}

func (emptyResolver) Resolve(_ context.Context, _ string) ([]TextbookEntry, error) {
	return nil, nil
}

// openStore uses a temp-file DB rather than ":memory:": database/sql pools
// connections and modernc.org/sqlite gives each connection its own in-memory
// database, which breaks multi-statement tests.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSend_HappyPath_PersistsEventsAndCompletesRun(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	sink := &captureSink{}
	reg := tools.NewRegistry(5 * time.Second)

	res, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID,
		UserText:       "hi",
		SystemPrompt:   "system",
		Model:          "gpt-x",
		Provider:       oneShotProvider{text: "hello"},
		Registry:       reg,
		Resolver:       emptyResolver{},
		RetrievalMode:  RetrievalAutoGroundedDefault,
		Sink:           sink,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.TerminalReason != "end_turn" {
		t.Fatalf("terminal_reason want end_turn, got %q", res.TerminalReason)
	}
	events, _ := st.GetConversationDisplayEvents(conv.ID)
	if len(events) != 2 || events[0].Kind != store.EventKindUserMessage ||
		events[1].Kind != store.EventKindAssistantText || events[1].Text != "hello" {
		t.Fatalf("event log mismatch: %+v", events)
	}
	run, _ := st.GetRun(res.RunID)
	if !run.ActiveForReplay || run.Status != "completed" {
		t.Fatalf("run not active/completed: %+v", run)
	}
	if run.TotalInputTokens != 10 || run.TotalOutputTokens != 5 {
		t.Fatalf("totals mismatch: %+v", run)
	}
	// Sink should have emitted run_started, run_completed, usage.
	haveKinds := map[SinkEventKind]bool{}
	for _, e := range sink.events {
		haveKinds[e.Kind] = true
	}
	for _, want := range []SinkEventKind{SinkRunStarted, SinkRunCompleted, SinkUsage} {
		if !haveKinds[want] {
			t.Errorf("expected sink event %s", want)
		}
	}
	_ = json.Valid // import guard for json package; used in later tests
}

// scriptedProvider emits a canned sequence of Delta arrays — one per
// iteration. The Nth call to Stream emits the Nth element.
type scriptedProvider struct {
	iterations [][]provider.Delta
	callCount  int
}

func (s *scriptedProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.Delta, error) {
	if s.callCount >= len(s.iterations) {
		return nil, errors.New("scriptedProvider: out of canned iterations")
	}
	deltas := s.iterations[s.callCount]
	s.callCount++
	ch := make(chan provider.Delta, len(deltas))
	for _, d := range deltas {
		ch <- d
	}
	close(ch)
	return ch, nil
}

func TestSend_ToolCallLoop_WriteBeforeDispatchAndSequential(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	sink := &captureSink{}
	reg := tools.NewRegistry(time.Second)
	p1 := probe.New("p1", `{"type":"object"}`)
	p1.Out = "result-of-p1"
	p2 := probe.New("p2", `{"type":"object"}`)
	p2.Out = "result-of-p2"
	_ = reg.Register(p1)
	_ = reg.Register(p2)

	prov := &scriptedProvider{iterations: [][]provider.Delta{
		// Iteration 1: assistant text + two tool calls.
		{
			{Text: "Let me check."},
			{ToolCall: &provider.ToolCall{ID: "c1", Name: "p1", Input: json.RawMessage(`{}`)}},
			{ToolCall: &provider.ToolCall{ID: "c2", Name: "p2", Input: json.RawMessage(`{}`)}},
			{Done: true, StopReason: "tool_use",
				Usage: &provider.Usage{InputTokens: 10, OutputTokens: 4}},
		},
		// Iteration 2: final assistant text.
		{
			{Text: "Final answer: 42."},
			{Done: true, StopReason: "end_turn",
				Usage: &provider.Usage{InputTokens: 30, OutputTokens: 6}},
		},
	}}

	res, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID, UserText: "q",
		Model: "x", Provider: prov, Registry: reg,
		Resolver: emptyResolver{}, RetrievalMode: RetrievalAutoGroundedDefault,
		Sink: sink,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalIterations != 2 || res.TotalToolCalls != 2 {
		t.Fatalf("counters: %+v", res)
	}
	events, _ := st.GetConversationDisplayEvents(conv.ID)
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	want := []string{
		store.EventKindUserMessage,
		store.EventKindAssistantText,     // "Let me check."
		store.EventKindAssistantToolCall, // c1
		store.EventKindToolResult,        // result-of-p1
		store.EventKindAssistantToolCall, // c2
		store.EventKindToolResult,        // result-of-p2
		store.EventKindAssistantText,     // "Final answer: 42."
	}
	for i, w := range want {
		if i >= len(kinds) || kinds[i] != w {
			t.Fatalf("event[%d] want %s got %v (full: %v)", i, w, kinds[i:], kinds)
		}
	}
	if p1.CallCount() != 1 || p2.CallCount() != 1 {
		t.Fatalf("tool call counts: p1=%d p2=%d", p1.CallCount(), p2.CallCount())
	}
}

func TestSend_MaxIterations_MarksErrored(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	reg := tools.NewRegistry(time.Second)
	p := probe.New("p", `{"type":"object"}`)
	p.Out = "x"
	_ = reg.Register(p)
	// Build a provider that emits exactly one tool call per iteration, forever.
	iter := []provider.Delta{
		{ToolCall: &provider.ToolCall{ID: "c", Name: "p", Input: json.RawMessage(`{}`)}},
		{Done: true, StopReason: "tool_use"},
	}
	prov := &scriptedProvider{}
	for i := 0; i < MaxIterationsDefault+2; i++ {
		prov.iterations = append(prov.iterations, iter)
	}
	res, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID, UserText: "q",
		Model: "x", Provider: prov, Registry: reg,
		Resolver: emptyResolver{}, RetrievalMode: RetrievalAutoGroundedDefault,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "max_iterations") {
		t.Fatalf("expected max_iterations error; got %v", err)
	}
	if res.TerminalReason != "max_iterations" {
		t.Fatalf("terminal_reason want max_iterations; got %q", res.TerminalReason)
	}
	run, _ := st.GetRun(res.RunID)
	if run.Status != "errored" || run.ActiveForReplay {
		t.Fatalf("max-iter run should be errored+inactive: %+v", run)
	}
}

func TestSend_StreamErr_WithoutCancel_MarksErrored(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	reg := tools.NewRegistry(time.Second)
	prov := &scriptedProvider{iterations: [][]provider.Delta{
		{
			{Text: "partial"},
			{Err: errors.New("upstream rate limit"), Done: true},
		},
	}}
	sink := &captureSink{}
	res, _ := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID, UserText: "q",
		Model: "x", Provider: prov, Registry: reg,
		Resolver: emptyResolver{}, RetrievalMode: RetrievalAutoGroundedDefault,
		Sink: sink,
	}, nil)
	if res.TerminalReason != "provider_error" {
		t.Fatalf("want provider_error; got %q", res.TerminalReason)
	}
	events, _ := st.GetConversationDisplayEvents(conv.ID)
	foundPartial := false
	for _, e := range events {
		if e.Kind == store.EventKindAssistantText && e.Text == "partial" {
			foundPartial = true
		}
	}
	if !foundPartial {
		t.Fatal("partial text must be persisted even when stream errors")
	}
	var sawErrored bool
	for _, e := range sink.events {
		if e.Kind == SinkRunErrored {
			sawErrored = true
		}
	}
	if !sawErrored {
		t.Fatal("sink should have received run_errored")
	}
}

func TestSend_StreamErr_AfterCancel_MarksCancelled(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	reg := tools.NewRegistry(time.Second)
	// Pre-cancelled context: the loop should see ctx.Err() != nil and the
	// stream's Err is plumbed as cancellation, not provider error.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prov := &scriptedProvider{iterations: [][]provider.Delta{
		{
			{Text: "interrupted"},
			{Err: context.Canceled, Done: true},
		},
	}}
	sink := &captureSink{}
	res, _ := svc.Send(ctx, SendParams{
		ConversationID: conv.ID, UserText: "q",
		Model: "x", Provider: prov, Registry: reg,
		Resolver: emptyResolver{}, RetrievalMode: RetrievalAutoGroundedDefault,
		Sink: sink,
	}, nil)
	if res.TerminalReason != "user_cancelled" {
		t.Fatalf("want user_cancelled; got %q", res.TerminalReason)
	}
	events, _ := st.GetConversationDisplayEvents(conv.ID)
	foundPartial := false
	for _, e := range events {
		if e.Kind == store.EventKindAssistantText && e.Text == "interrupted" {
			foundPartial = true
		}
	}
	if !foundPartial {
		t.Fatal("partial text must be persisted on cancellation too")
	}
	var sawCancelled bool
	for _, e := range sink.events {
		if e.Kind == SinkRunCancelled {
			sawCancelled = true
		}
	}
	if !sawCancelled {
		t.Fatal("sink should have received run_cancelled")
	}
}
