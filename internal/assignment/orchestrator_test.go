package assignment

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/eval/fakeprovider"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
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

	asgID, err := orc.Run(context.Background(), tmpAssignmentDir(t))
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
	asgID, err := orc.Run(context.Background(), tmpAssignmentDir(t))
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
	asgID, _ := orc.Run(ctx, tmpAssignmentDir(t))
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

	asgID, err := orc.Run(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := st.ListAssignmentItems(asgID)
	runByPath := map[string]string{}
	for _, it := range first {
		runByPath[it.SourcePath] = it.RunID
	}

	asgID2, err := orc.Run(context.Background(), dir)
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

	asgID, err := orc.Run(context.Background(), dir)
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
