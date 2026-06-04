package appapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/eval/fakeprovider"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

// copyFixtures copies the mod04 _json fixtures into a temp _json dir and returns
// that _json directory (the companion dir Load/SolveAssignment expect).
func copyFixtures(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "assignment", "testdata", "mod04", "_json")
	dst := filepath.Join(t.TempDir(), "_json")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.json", "001.json", "004.json"} {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func TestSolveAssignment_RunsAndLists(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	reg := provider.Registry{Models: []provider.ModelInfo{{Display: "X", ID: "m1", Provider: "openai"}}}
	a := NewAPI(config.Config{}, st, reg, nil)
	a.Startup(context.Background())
	a.emit = func(string, any) {} // avoid live Wails runtime in tests
	a.assignmentFactory = func(string) (provider.ChatProvider, string, error) {
		return &fakeprovider.Scripted{Iterations: [][]provider.Delta{
			{
				{ToolCall: &provider.ToolCall{ID: "c1", Name: "submit_answer",
					Input: json.RawMessage(`{"confidence":"high","answerIndex":1}`)}},
				{Done: true, StopReason: "tool_use"},
			},
			{{Text: "done"}, {Done: true, StopReason: "end_turn"}},
		}}, "openai", nil
	}
	dir := copyFixtures(t)
	id, err := a.SolveAssignment(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Poll for completion (scripted provider is fast).
	deadline := time.Now().Add(5 * time.Second)
	for {
		asg, err := a.GetAssignment(id)
		if err != nil {
			t.Fatal(err)
		}
		if asg.Status == "completed" || asg.Status == "cancelled" || asg.Status == "errored" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("assignment did not finish; status=%s", asg.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	items, err := a.ListAssignmentItems(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("no items returned")
	}
}

// When ragAdpt is nil but the conversation has a textbook scope, SendMessage
// must not panic. It will fail earlier at provider.New (no API key / unknown
// model) and return a normalized error — the point is: no nil-pointer crash.
func TestSendMessageNilRagAdapterNoPanic(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	conv, _ := st.CreateConversation("t")
	if err := st.SetConversationTextbooks(conv.ID, []store.TextbookScope{{Name: "ia", Chapters: []int{1}}}); err != nil {
		t.Fatalf("SetConversationTextbooks: %v", err)
	}

	reg := provider.Registry{Models: []provider.ModelInfo{{Display: "X", ID: "m1", Provider: "openai"}}}
	api := NewAPI(config.Config{}, st, reg, nil) // ragAdpt == nil on purpose

	// Must return an error (no OpenAI key), and crucially must NOT panic.
	err = api.SendMessage(conv.ID, "hello", "m1")
	if err == nil {
		t.Fatal("expected an error (no API key), got nil")
	}
	// Sanity: it's a provider AppError, not a panic/raw.
	if !strings.Contains(strings.ToLower(err.Error()), "key") {
		t.Logf("note: error was %q (acceptable as long as no panic)", err.Error())
	}
}

func TestTitleFromText(t *testing.T) {
	if got := titleFromText("  Draft a post on ASC 606  "); got != "Draft a post on ASC 606" {
		t.Fatalf("got %q", got)
	}
	if got := titleFromText(""); got != "New conversation" {
		t.Fatalf("empty: got %q", got)
	}
	long := ""
	for i := 0; i < 100; i++ {
		long += "x"
	}
	got := titleFromText(long)
	if []rune(got)[len([]rune(got))-1] != '…' || len([]rune(got)) != 61 {
		t.Fatalf("truncation wrong: len=%d got=%q", len([]rune(got)), got)
	}
	if got := titleFromText("line1\nline2"); got != "line1 line2" {
		t.Fatalf("newline: got %q", got)
	}
}

// TestCancelMessageNoInFlightIsNoop ensures that calling CancelMessage when no
// stream is in flight does not panic (guards the nil cancelInFlight path).
func TestCancelMessageNoInFlightIsNoop(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	reg := provider.Registry{Models: []provider.ModelInfo{{Display: "X", ID: "m1", Provider: "openai"}}}
	api := NewAPI(config.Config{}, st, reg, nil)
	// Must not panic.
	api.CancelMessage()
}

func TestBuildChatUsageEvent(t *testing.T) {
	got := buildChatUsageEvent("conv-1", "claude-opus-4-7", &provider.Usage{
		InputTokens: 120, OutputTokens: 45, CachedInputTokens: 80,
	})
	if got == nil {
		t.Fatal("got nil, want populated map")
	}
	if got["convID"] != "conv-1" || got["modelID"] != "claude-opus-4-7" {
		t.Fatalf("convID/modelID wrong: %+v", got)
	}
	if got["input"] != 120 || got["output"] != 45 || got["cached"] != 80 {
		t.Fatalf("token fields wrong: %+v", got)
	}
}

func TestBuildChatUsageEventNilUsage(t *testing.T) {
	if got := buildChatUsageEvent("c", "m", nil); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}
