package tools

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"
)

type fakeTool struct{ name string }

func (f fakeTool) Name() string                 { return f.name }
func (f fakeTool) Description() string          { return "fake" }
func (f fakeTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f fakeTool) Timeout() time.Duration       { return 0 }
func (f fakeTool) Execute(ctx context.Context, ec ExecContext, in json.RawMessage) (ExecResult, error) {
	return ExecResult{Output: "ok"}, nil
}

func names(r *Registry) []string {
	var out []string
	for _, d := range r.Catalog() {
		out = append(out, d.Name)
	}
	sort.Strings(out)
	return out
}

func TestSubsetKeepsOnlyNamedTools(t *testing.T) {
	r := NewRegistry(time.Second)
	if err := r.Register(fakeTool{name: "safe_math"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(fakeTool{name: "search_textbook"}); err != nil {
		t.Fatal(err)
	}

	got := names(r.Subset([]string{"safe_math"}))
	if len(got) != 1 || got[0] != "safe_math" {
		t.Errorf("Subset([safe_math]) = %v", got)
	}
}

// An empty whitelist means "no restriction" — a persona that omits `tools:`
// gets every tool.
func TestSubsetEmptyMeansEverything(t *testing.T) {
	r := NewRegistry(time.Second)
	_ = r.Register(fakeTool{name: "safe_math"})
	_ = r.Register(fakeTool{name: "search_textbook"})

	got := names(r.Subset(nil))
	if len(got) != 2 {
		t.Errorf("Subset(nil) = %v, want both tools", got)
	}
}

// search_textbook is only registered when RAG is available. A persona naming it
// on a RAG-less run must still work — with that tool simply absent.
func TestSubsetIgnoresUnregisteredNames(t *testing.T) {
	r := NewRegistry(time.Second)
	_ = r.Register(fakeTool{name: "safe_math"})

	got := names(r.Subset([]string{"safe_math", "search_textbook"}))
	if len(got) != 1 || got[0] != "safe_math" {
		t.Errorf("Subset = %v, want just safe_math", got)
	}
}
