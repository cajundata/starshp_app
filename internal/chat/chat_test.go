package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
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
	// The usage event must carry modelID so the frontend footer can resolve the
	// model's max_context for the ctx N / M denominator.
	var usageModelID string
	for _, e := range sink.events {
		if e.Kind == SinkUsage {
			usageModelID, _ = e.Payload["modelID"].(string)
		}
	}
	if usageModelID != "gpt-x" {
		t.Errorf("usage event modelID = %q, want gpt-x", usageModelID)
	}
	_ = json.Valid // import guard for json package; used in later tests
}

// scriptedProvider emits a canned sequence of Delta arrays — one per
// iteration. The Nth call to Stream emits the Nth element.
type scriptedProvider struct {
	iterations [][]provider.Delta
	callCount  int
	reqs       []provider.ChatRequest // captured per Stream call for assertions
}

func (s *scriptedProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.Delta, error) {
	s.reqs = append(s.reqs, req)
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

// When the iteration budget is exhausted, the loop must not discard the work.
// It gives the model one final turn with tools withheld so it synthesizes an
// answer from the gathered tool results, and the run completes successfully with
// terminal_reason=max_iterations.
func TestSend_MaxIterations_FinalizesWithAnswer(t *testing.T) {
	t.Setenv("STARSHP_MAX_TOOL_ITERATIONS", "2")
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	reg := tools.NewRegistry(time.Second)
	p := probe.New("p", `{"type":"object"}`)
	p.Out = "x"
	_ = reg.Register(p)
	prov := &scriptedProvider{iterations: [][]provider.Delta{
		{{ToolCall: &provider.ToolCall{ID: "c1", Name: "p", Input: json.RawMessage(`{}`)}}, {Done: true, StopReason: "tool_use"}},
		{{ToolCall: &provider.ToolCall{ID: "c2", Name: "p", Input: json.RawMessage(`{}`)}}, {Done: true, StopReason: "tool_use"}},
		// Forced finalization turn (tools withheld): the model answers.
		{{Text: "Final answer: 42"}, {Done: true, StopReason: "end_turn", Usage: &provider.Usage{InputTokens: 5, OutputTokens: 3}}},
	}}
	res, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID, UserText: "q",
		Model: "x", Provider: prov, Registry: reg,
		Resolver: emptyResolver{}, RetrievalMode: RetrievalAutoGroundedDefault,
	}, nil)
	if err != nil {
		t.Fatalf("finalization should not error: %v", err)
	}
	if res.TerminalReason != "max_iterations" {
		t.Fatalf("terminal_reason want max_iterations; got %q", res.TerminalReason)
	}
	run, _ := st.GetRun(res.RunID)
	if run.Status != "completed" || !run.ActiveForReplay {
		t.Fatalf("finalized run should be completed+active: %+v", run)
	}
	events, _ := st.GetConversationDisplayEvents(conv.ID)
	var final string
	for _, e := range events {
		if e.Kind == store.EventKindAssistantText {
			final = e.Text
		}
	}
	if final != "Final answer: 42" {
		t.Fatalf("forced final answer not persisted; got %q", final)
	}
	// 2 tool rounds + 1 finalization; the finalization call must withhold tools.
	if len(prov.reqs) != 3 {
		t.Fatalf("expected 3 provider calls, got %d", len(prov.reqs))
	}
	if len(prov.reqs[2].Tools) != 0 {
		t.Fatalf("finalization call must withhold tools, got %d", len(prov.reqs[2].Tools))
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

// usagePayload returns the payload of the (last) SinkUsage event, or nil.
func usagePayload(sink *captureSink) map[string]any {
	var p map[string]any
	for _, e := range sink.events {
		if e.Kind == SinkUsage {
			p = e.Payload
		}
	}
	return p
}

func TestSend_ToolLoop_OccupancyVsCumulative(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	sink := &captureSink{}
	reg := tools.NewRegistry(time.Second)
	p1 := probe.New("p1", `{"type":"object"}`)
	p1.Out = "r1"
	_ = reg.Register(p1)

	prov := &scriptedProvider{iterations: [][]provider.Delta{
		{
			{ToolCall: &provider.ToolCall{ID: "c1", Name: "p1", Input: json.RawMessage(`{}`)}},
			{Done: true, StopReason: "tool_use",
				Usage: &provider.Usage{InputTokens: 50000, OutputTokens: 0}},
		},
		{
			{Text: "answer"},
			{Done: true, StopReason: "end_turn",
				Usage: &provider.Usage{InputTokens: 200000, OutputTokens: 2000}},
		},
	}}

	_, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID, UserText: "q",
		Model: "x", Provider: prov, Registry: reg,
		Resolver: emptyResolver{}, RetrievalMode: RetrievalAutoGroundedDefault,
		Sink: sink,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pl := usagePayload(sink)
	if pl == nil {
		t.Fatal("no usage event emitted")
	}
	// Cumulative (summed across iterations).
	if pl["input"] != 250000 || pl["output"] != 2000 {
		t.Errorf("cumulative: input=%v output=%v want 250000/2000", pl["input"], pl["output"])
	}
	// Final call only (occupancy basis).
	if pl["lastInput"] != 200000 || pl["lastOutput"] != 2000 {
		t.Errorf("final call: lastInput=%v lastOutput=%v want 200000/2000", pl["lastInput"], pl["lastOutput"])
	}
}

func TestSend_SingleCall_LastEqualsCumulative(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	sink := &captureSink{}
	reg := tools.NewRegistry(time.Second)

	_, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID, UserText: "hi",
		Model: "x", Provider: oneShotProvider{text: "hello"}, Registry: reg,
		Resolver: emptyResolver{}, RetrievalMode: RetrievalAutoGroundedDefault,
		Sink: sink,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pl := usagePayload(sink)
	// oneShotProvider reports Usage{Input:10, Output:5}.
	if pl["lastInput"] != 10 || pl["lastOutput"] != 5 {
		t.Errorf("lastInput=%v lastOutput=%v want 10/5", pl["lastInput"], pl["lastOutput"])
	}
	if pl["lastInput"] != pl["input"] || pl["lastOutput"] != pl["output"] {
		t.Errorf("single call: last should equal cumulative; got last=%v/%v cum=%v/%v",
			pl["lastInput"], pl["lastOutput"], pl["input"], pl["output"])
	}
}

func TestSend_FinalCallNoUsage_RetainsLastReported(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	sink := &captureSink{}
	reg := tools.NewRegistry(time.Second)
	p1 := probe.New("p1", `{"type":"object"}`)
	p1.Out = "r1"
	_ = reg.Register(p1)

	prov := &scriptedProvider{iterations: [][]provider.Delta{
		{
			{ToolCall: &provider.ToolCall{ID: "c1", Name: "p1", Input: json.RawMessage(`{}`)}},
			{Done: true, StopReason: "tool_use",
				Usage: &provider.Usage{InputTokens: 50000, OutputTokens: 1000}},
		},
		{
			{Text: "answer"},
			{Done: true, StopReason: "end_turn"}, // no Usage on the terminal call
		},
	}}

	_, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID, UserText: "q",
		Model: "x", Provider: prov, Registry: reg,
		Resolver: emptyResolver{}, RetrievalMode: RetrievalAutoGroundedDefault,
		Sink: sink,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pl := usagePayload(sink)
	// Final call reported no usage; lastCall retains the last call that did.
	if pl["lastInput"] != 50000 || pl["lastOutput"] != 1000 {
		t.Errorf("lastInput=%v lastOutput=%v want 50000/1000 (retained)", pl["lastInput"], pl["lastOutput"])
	}
}

// fakeImages is an in-memory ImageStore matching imagestore semantics.
type fakeImages struct{ files map[string][]byte }

func newFakeImages() *fakeImages { return &fakeImages{files: map[string][]byte{}} }

func (f *fakeImages) Put(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	f.files[h] = data
	return h, nil
}

func (f *fakeImages) Read(hash string) ([]byte, error) {
	b, ok := f.files[hash]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return b, nil
}

func TestSend_ImageDeltas_PersistInterleavedAndEmitSink(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	sink := &captureSink{}
	images := newFakeImages()

	prov := &scriptedProvider{iterations: [][]provider.Delta{{
		{Text: "Two options:"},
		{Image: &provider.ImageBlob{MIME: "image/png", Data: []byte("png-1")}},
		{Text: "and a variant:"},
		{Image: &provider.ImageBlob{MIME: "image/png", Data: []byte("png-2")}},
		{Done: true, StopReason: "end_turn", Usage: &provider.Usage{InputTokens: 5, OutputTokens: 9}},
	}}}

	res, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID,
		UserText:       "draw a cat",
		Model:          "gemini-3-pro-image",
		Provider:       prov,
		Registry:       tools.NewRegistry(time.Second),
		Resolver:       emptyResolver{},
		RetrievalMode:  RetrievalAutoGroundedDefault,
		Sink:           sink,
		Images:         images,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.TerminalReason != "end_turn" {
		t.Fatalf("terminal = %q", res.TerminalReason)
	}

	evs, _ := st.GetConversationDisplayEvents(conv.ID)
	kinds := make([]string, len(evs))
	for i, e := range evs {
		kinds[i] = e.Kind
	}
	want := []string{
		store.EventKindUserMessage,
		store.EventKindAssistantText,  // "Two options:"
		store.EventKindAssistantImage, // png-1
		store.EventKindAssistantText,  // "and a variant:"
		store.EventKindAssistantImage, // png-2
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("event kinds = %v, want %v", kinds, want)
	}
	if evs[2].ImageHash == "" || evs[4].ImageHash == "" || evs[2].ImageHash == evs[4].ImageHash {
		t.Fatalf("image hashes wrong: %q, %q", evs[2].ImageHash, evs[4].ImageHash)
	}
	if _, err := images.Read(evs[2].ImageHash); err != nil {
		t.Fatalf("first image not in store: %v", err)
	}

	var imageSinks []SinkEvent
	for _, e := range sink.events {
		if e.Kind == SinkImage {
			imageSinks = append(imageSinks, e)
		}
	}
	if len(imageSinks) != 2 {
		t.Fatalf("got %d image sink events, want 2", len(imageSinks))
	}
	if h, _ := imageSinks[0].Payload["hash"].(string); h != evs[2].ImageHash {
		t.Fatalf("sink hash = %q, want %q", h, evs[2].ImageHash)
	}
}

func TestSend_ImageDeltaWithoutStore_ErrorsRun(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	sink := &captureSink{}
	prov := &scriptedProvider{iterations: [][]provider.Delta{{
		{Image: &provider.ImageBlob{MIME: "image/png", Data: []byte("png")}},
		{Done: true, StopReason: "end_turn"},
	}}}
	res, _ := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID, UserText: "draw", Model: "m",
		Provider: prov, Registry: tools.NewRegistry(time.Second),
		Resolver: emptyResolver{}, RetrievalMode: RetrievalAutoGroundedDefault,
		Sink: sink, // Images deliberately nil
	}, nil)
	if res.TerminalReason != "provider_error" {
		t.Fatalf("terminal = %q, want provider_error", res.TerminalReason)
	}
}
