package assignment

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/eval/fakeprovider"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/cajundata/starshp_app/internal/tools"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "asg.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// scriptedFactory returns a provider that, for every question, emits a single
// submit_answer tool call then ends — simulating a solver.
func scriptedFactory(answer string) ProviderFactory {
	return func(string) (provider.ChatProvider, string, error) {
		return &fakeprovider.Scripted{Iterations: [][]provider.Delta{
			{
				{ToolCall: &provider.ToolCall{ID: "c1", Name: "submit_answer",
					Input: json.RawMessage(answer)}},
				{Done: true, StopReason: "tool_use"},
			},
			{{Text: "done"}, {Done: true, StopReason: "end_turn"}},
		}}, "openai", nil
	}
}

func newTestOrchestrator(t *testing.T, st *store.Store, pf ProviderFactory) *Orchestrator {
	t.Helper()
	return New(st, chat.New(st), pf, Options{
		Model:       "m",
		Concurrency: 1,
		Grounding:   NoGrounding{},
		Emit:        func(string, any) {},
	})
}

func tmpAssignmentDir(t *testing.T) string {
	t.Helper()
	src := testdataDir(t)
	dst := filepath.Join(t.TempDir(), "_json")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func TestOrchestrator_SolvesOneItem(t *testing.T) {
	st := openStore(t)
	pf := scriptedFactory(`{"confidence":"high","answerIndex":1}`)
	orc := newTestOrchestrator(t, st, pf)

	asgID, err := orc.Run(context.Background(), tmpAssignmentDir(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := st.ListAssignmentItems(asgID)
	var mc *store.AssignmentItem
	for i := range items {
		if items[i].SourcePath == "001.html" {
			mc = &items[i]
		}
	}
	if mc == nil {
		t.Fatal("001.html item not created")
	}
	if mc.Status != "answered" {
		t.Fatalf("MC item status want answered, got %q (err=%q)", mc.Status, mc.Error)
	}
	if mc.Confidence != "high" || mc.RunID == "" {
		t.Fatalf("item not populated: %+v", mc)
	}
	if mc.AnswerPath == "" {
		t.Fatal("answer file path not recorded")
	}
}

func TestOrchestrator_AllItemsSolvedConcurrently(t *testing.T) {
	st := openStore(t)
	pf := scriptedFactory(`{"confidence":"high","answerIndex":0}`)
	orc := New(st, chat.New(st), pf, Options{Model: "m", Concurrency: 4,
		Grounding: NoGrounding{}, Emit: func(string, any) {}})
	asgID, err := orc.Run(context.Background(), tmpAssignmentDir(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := st.ListAssignmentItems(asgID)
	if len(items) == 0 {
		t.Fatal("no items")
	}
	for _, it := range items {
		// The scripted MC-shaped answer fails the worksheet schema, so that
		// item may land answered-with-bogus or no_answer — but it must never be
		// left pending/solving.
		if it.Status == "pending" || it.Status == "solving" {
			t.Fatalf("item %s left unfinished: %s", it.SourcePath, it.Status)
		}
	}
}

func TestOrchestrator_CancelStopsBatch(t *testing.T) {
	st := openStore(t)
	pf := scriptedFactory(`{"confidence":"high","answerIndex":0}`)
	orc := New(st, chat.New(st), pf, Options{Model: "m", Concurrency: 1,
		Grounding: NoGrounding{}, Emit: func(string, any) {}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before running
	asgID, _ := orc.Run(ctx, tmpAssignmentDir(t), nil, nil)
	a, _ := st.GetAssignment(asgID)
	if a.Status != "cancelled" {
		t.Fatalf("assignment status want cancelled, got %q", a.Status)
	}
}

func TestOrchestrator_ResumeSkipsAnswered(t *testing.T) {
	st := openStore(t)
	dir := tmpAssignmentDir(t) // SAME dir for both runs (resume keys on dir+hash)
	orc := New(st, chat.New(st), scriptedFactory(`{"confidence":"high","answerIndex":0}`),
		Options{Model: "m", Concurrency: 2, Grounding: NoGrounding{}, Emit: func(string, any) {}})

	asgID, err := orc.Run(context.Background(), dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := st.ListAssignmentItems(asgID)
	runByPath := map[string]string{}
	for _, it := range first {
		runByPath[it.SourcePath] = it.RunID
	}

	asgID2, err := orc.Run(context.Background(), dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if asgID2 != asgID {
		t.Fatalf("resume should reuse the assignment id; got %s vs %s", asgID2, asgID)
	}
	second, _ := st.ListAssignmentItems(asgID)
	if len(second) != len(first) {
		t.Fatalf("resume created duplicate items: %d -> %d", len(first), len(second))
	}
	for _, it := range second {
		if it.Status == "answered" && it.RunID != runByPath[it.SourcePath] {
			t.Fatalf("answered item %s was re-run on resume (runID changed)", it.SourcePath)
		}
	}
}

func TestRerunItem_OverwritesInPlace(t *testing.T) {
	st := openStore(t)
	dir := tmpAssignmentDir(t)
	orc := newTestOrchestrator(t, st, scriptedFactory(`{"confidence":"low","answerIndex":0}`))

	asgID, err := orc.Run(context.Background(), dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	var seq int
	var oldRun, oldConv, itemID string
	found := false
	items, _ := st.ListAssignmentItems(asgID)
	for _, it := range items {
		if it.SourcePath == "001.html" {
			seq, oldRun, oldConv, itemID, found = it.Seq, it.RunID, it.ConversationID, it.ID, true
		}
	}
	if !found {
		t.Fatal("001.html item not created")
	}

	updated, err := orc.RerunItem(context.Background(), asgID, seq)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != itemID || updated.Seq != seq {
		t.Fatalf("item identity changed: %+v", updated)
	}
	if updated.Status != "answered" {
		t.Fatalf("want answered, got %q", updated.Status)
	}
	if updated.RunID == "" || updated.RunID == oldRun {
		t.Fatalf("expected fresh RunID, old=%q new=%q", oldRun, updated.RunID)
	}
	if updated.ConversationID == "" || updated.ConversationID == oldConv {
		t.Fatalf("expected fresh ConversationID, old=%q new=%q", oldConv, updated.ConversationID)
	}
	after, _ := st.ListAssignmentItems(asgID)
	if len(after) != len(items) {
		t.Fatalf("rerun changed item count: before=%d after=%d", len(items), len(after))
	}
}

func TestRerunItem_RejectsUnsupported(t *testing.T) {
	st := openStore(t)
	if err := st.CreateAssignment(store.Assignment{
		ID: "a1", SourceDir: "/nope", Title: "t", ManifestHash: "h",
		Model: "m", Status: "completed", TotalItems: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAssignmentItem(store.AssignmentItem{
		ID: "i1", AssignmentID: "a1", Seq: 0, SourcePath: "x.html",
		Type: string(TypeUnsupported), Status: "unsupported",
	}); err != nil {
		t.Fatal(err)
	}
	orc := newTestOrchestrator(t, st, scriptedFactory(`{}`))

	_, err := orc.RerunItem(context.Background(), "a1", 0)
	ae, ok := err.(provider.AppError)
	if !ok || ae.Code != "unsupported" {
		t.Fatalf("want unsupported AppError, got %v", err)
	}
}

func TestRerunItem_RejectsWhileBatchInProgress(t *testing.T) {
	st := openStore(t)
	if err := st.CreateAssignment(store.Assignment{
		ID: "a1", SourceDir: "/nope", Title: "t", ManifestHash: "h",
		Model: "m", Status: "in_progress", TotalItems: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAssignmentItem(store.AssignmentItem{
		ID: "i1", AssignmentID: "a1", Seq: 0, SourcePath: "001.html",
		Type: "multipleChoice", Status: "answered",
	}); err != nil {
		t.Fatal(err)
	}
	orc := newTestOrchestrator(t, st, scriptedFactory(`{}`))

	_, err := orc.RerunItem(context.Background(), "a1", 0)
	ae, ok := err.(provider.AppError)
	if !ok || ae.Code != "busy" {
		t.Fatalf("want busy AppError, got %v", err)
	}
}

// fakeTool is a minimal tools.Tool stand-in for asserting registry gating.
type fakeTool struct{ name string }

func (f fakeTool) Name() string                 { return f.name }
func (f fakeTool) Description() string          { return "fake" }
func (f fakeTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f fakeTool) Execute(context.Context, tools.ExecContext, json.RawMessage) (tools.ExecResult, error) {
	return tools.ExecResult{}, nil
}
func (f fakeTool) Timeout() time.Duration { return 0 }

func hasTool(cat []provider.ToolDef, name string) bool {
	for _, d := range cat {
		if d.Name == name {
			return true
		}
	}
	return false
}

func TestBuildRegistry_GatesSearchToolOnScope(t *testing.T) {
	st := openStore(t)
	orc := New(st, chat.New(st), scriptedFactory(`{}`), Options{
		Model: "m", Concurrency: 1, Grounding: NoGrounding{},
		Emit: func(string, any) {}, SearchTool: fakeTool{name: "search_textbook"},
	})
	q := Question{Type: TypeMultipleChoice, Title: "t", MultipleChoice: &MultipleChoiceBody{Stem: "s"}}

	if !hasTool(orc.buildRegistry(q, true).Catalog(), "search_textbook") {
		t.Error("search_textbook must be registered when scope is present")
	}
	if hasTool(orc.buildRegistry(q, false).Catalog(), "search_textbook") {
		t.Error("search_textbook must NOT be registered when scope is empty")
	}
}

func TestSolve_AttachesScopeToItemConversation(t *testing.T) {
	st := openStore(t)
	dir := tmpAssignmentDir(t)
	orc := newTestOrchestrator(t, st, scriptedFactory(`{"confidence":"low","answerIndex":0}`))

	asgID, err := orc.Run(context.Background(), dir, []store.TextbookScope{{Name: "blaw"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := st.ListAssignmentItems(asgID)
	var convID string
	for _, it := range items {
		if it.SourcePath == "001.html" {
			convID = it.ConversationID
		}
	}
	if convID == "" {
		t.Fatal("001.html item has no conversation")
	}
	tb, _ := st.GetConversationTextbooks(convID)
	if len(tb) != 1 || tb[0].Name != "blaw" {
		t.Fatalf("expected blaw attached to item conversation, got %+v", tb)
	}
}

func TestPrepare_NilScopeDoesNotClobber(t *testing.T) {
	st := openStore(t)
	dir := tmpAssignmentDir(t)
	orc := newTestOrchestrator(t, st, scriptedFactory(`{"confidence":"low","answerIndex":0}`))

	asgID, err := orc.Run(context.Background(), dir, []store.TextbookScope{{Name: "blaw"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Re-solving with a nil scope must NOT wipe the stored selection.
	if _, err := orc.Run(context.Background(), dir, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetAssignmentScope(asgID)
	if len(got) != 1 || got[0].Name != "blaw" {
		t.Fatalf("nil re-solve clobbered scope: %+v", got)
	}
}

func TestRerunItem_AttachesStoredScope(t *testing.T) {
	st := openStore(t)
	dir := tmpAssignmentDir(t)
	orc := newTestOrchestrator(t, st, scriptedFactory(`{"confidence":"low","answerIndex":0}`))

	asgID, err := orc.Run(context.Background(), dir, nil, nil) // solve with no scope
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAssignmentScope(asgID, []store.TextbookScope{{Name: "blaw"}}); err != nil {
		t.Fatal(err)
	}
	items, _ := st.ListAssignmentItems(asgID)
	var seq int
	for _, it := range items {
		if it.SourcePath == "001.html" {
			seq = it.Seq
		}
	}
	updated, err := orc.RerunItem(context.Background(), asgID, seq)
	if err != nil {
		t.Fatal(err)
	}
	tb, _ := st.GetConversationTextbooks(updated.ConversationID)
	if len(tb) != 1 || tb[0].Name != "blaw" {
		t.Fatalf("rerun should attach stored scope, got %+v", tb)
	}
}

func TestWithLibraryPreamble(t *testing.T) {
	if got := withLibraryPreamble("", "BASE"); got != "BASE" {
		t.Fatalf("empty preamble should pass through, got %q", got)
	}
	got := withLibraryPreamble("PRE", "BASE")
	if got != "PRE\n\nBASE" {
		t.Fatalf("got %q", got)
	}
	// the operative base prompt must remain last (recency)
	if !strings.HasSuffix(got, "BASE") {
		t.Fatalf("base must be last, got %q", got)
	}
}

func TestPrepare_PersistsAndNilGuardsLibraryItems(t *testing.T) {
	st := openStore(t)
	dir := tmpAssignmentDir(t)
	orc := newTestOrchestrator(t, st, scriptedFactory(`{"confidence":"low","answerIndex":0}`))

	asgID, err := orc.Run(context.Background(), dir, nil, []string{"tone.md"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetAssignmentLibraryItems(asgID); len(got) != 1 || got[0] != "tone.md" {
		t.Fatalf("solve did not persist library items: %+v", got)
	}
	// Re-solve with nil library items must NOT wipe the stored selection.
	if _, err := orc.Run(context.Background(), dir, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetAssignmentLibraryItems(asgID); len(got) != 1 || got[0] != "tone.md" {
		t.Fatalf("nil re-solve clobbered library items: %+v", got)
	}
}

func TestSolveItem_AppliesLibraryPreamble(t *testing.T) {
	st := openStore(t)
	dir := tmpAssignmentDir(t)
	var systems []string
	pf := func(string) (provider.ChatProvider, string, error) {
		return &fakeprovider.Scripted{
			Iterations: [][]provider.Delta{
				{
					{ToolCall: &provider.ToolCall{ID: "c1", Name: "submit_answer",
						Input: json.RawMessage(`{"confidence":"low","answerIndex":0}`)}},
					{Done: true, StopReason: "tool_use"},
				},
				{{Text: "done"}, {Done: true, StopReason: "end_turn"}},
			},
			Hook: func(_ int, req provider.ChatRequest) { systems = append(systems, req.System) },
		}, "openai", nil
	}
	orc := New(st, chat.New(st), pf, Options{
		Model: "m", Concurrency: 1, Grounding: NoGrounding{},
		Emit: func(string, any) {}, LibraryPreamble: "LIBRARY-PREAMBLE-XYZ",
	})

	if _, err := orc.Run(context.Background(), dir, nil, nil); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range systems {
		if strings.Contains(s, "LIBRARY-PREAMBLE-XYZ") {
			found = true
		}
	}
	if !found {
		t.Fatalf("library preamble not found in any of the %d system prompts sent", len(systems))
	}
}

// A user Stop cancels the in-flight run: the agentic Send returns nil and the
// run is marked cancelled (not errored), with no submit_answer recorded. The
// item must surface as "cancelled", not a misleading "no_answer".
func TestSolveItem_CancelledMidSolveMarksItemCancelled(t *testing.T) {
	st := openStore(t)
	dir := tmpAssignmentDir(t)
	pf := func(string) (provider.ChatProvider, string, error) {
		return &fakeprovider.Scripted{Iterations: [][]provider.Delta{
			{{Text: "thinking"}, {Err: context.Canceled, Done: true}},
		}}, "openai", nil
	}
	orc := newTestOrchestrator(t, st, pf)

	asgID, err := orc.Run(context.Background(), dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := st.ListAssignmentItems(asgID)
	found := false
	for _, it := range items {
		if it.SourcePath == "001.html" {
			found = true
			if it.Status != "cancelled" {
				t.Fatalf("cancelled mid-solve should mark item cancelled, got %q", it.Status)
			}
		}
	}
	if !found {
		t.Fatal("001.html item not created")
	}
}

// A provider stream error marks the run errored but the agentic Send returns
// nil (errors flow via the run record/event). The item must surface as
// "errored" with the message, not a silent "no_answer".
func TestSolveItem_ProviderErrorMarksItemErrored(t *testing.T) {
	st := openStore(t)
	dir := tmpAssignmentDir(t)
	pf := func(string) (provider.ChatProvider, string, error) {
		return &fakeprovider.Scripted{Iterations: [][]provider.Delta{
			{{Err: errors.New("dial tcp 127.0.0.1:1: connect: connection refused")}},
		}}, "openai", nil
	}
	orc := newTestOrchestrator(t, st, pf)

	asgID, err := orc.Run(context.Background(), dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := st.ListAssignmentItems(asgID)
	found := false
	for _, it := range items {
		if it.SourcePath == "001.html" {
			found = true
			if it.Status != "errored" {
				t.Fatalf("provider error should mark item errored, got %q", it.Status)
			}
			if it.Error == "" {
				t.Fatal("an errored item should carry an error message")
			}
		}
	}
	if !found {
		t.Fatal("001.html item not created")
	}
}
