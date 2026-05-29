package searchtextbook

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/rag"
	"github.com/cajundata/starshp_app/internal/tools"
)

// fakeRetriever lets tests inject deterministic Retrieve results.
type fakeRetriever struct {
	last struct {
		filters []rag.ScopeFilter
		topK    int
	}
	result rag.RetrieveResult
	err    error
}

func (f *fakeRetriever) Retrieve(_ context.Context, _ string, filters []rag.ScopeFilter, topK, _ int) (rag.RetrieveResult, error) {
	f.last.filters = filters
	f.last.topK = topK
	return f.result, f.err
}

type fakeResolver struct{ entries []chat.TextbookEntry }

func (f fakeResolver) Resolve(_ context.Context, _ string) ([]chat.TextbookEntry, error) {
	return f.entries, nil
}

func TestSearchTextbook_Metadata(t *testing.T) {
	tool := New(&fakeRetriever{}, fakeResolver{}, 4000)
	if tool.Name() != "search_textbook" {
		t.Fatalf("name: %s", tool.Name())
	}
	if !json.Valid(tool.InputSchema()) {
		t.Fatal("schema must be valid JSON")
	}
}

func TestSearchTextbook_NoTextbooksAttached(t *testing.T) {
	tool := New(&fakeRetriever{}, fakeResolver{}, 4000)
	_, err := tool.Execute(context.Background(),
		tools.ExecContext{ConversationID: "c1", TextbookScope: nil},
		json.RawMessage(`{"query":"realization principle"}`))
	if err == nil || !strings.Contains(err.Error(), "no_textbooks_attached") {
		t.Fatalf("expected no_textbooks_attached; got %v", err)
	}
}

func TestSearchTextbook_InvalidBook(t *testing.T) {
	res := &fakeRetriever{}
	tool := New(res, fakeResolver{
		entries: []chat.TextbookEntry{{Book: "intermediate-accounting"}},
	}, 4000)
	_, err := tool.Execute(context.Background(),
		tools.ExecContext{ConversationID: "c1", TextbookScope: []string{"intermediate-accounting"}},
		json.RawMessage(`{"query":"q","book":"some-other-book"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid_book") {
		t.Fatalf("expected invalid_book; got %v", err)
	}
}

func TestSearchTextbook_FormatsSourcesWithStableIDs(t *testing.T) {
	res := &fakeRetriever{result: rag.RetrieveResult{
		Context: "## intermediate-accounting — Chapter 4\nrealization happens when...\n\n",
		Sources: []rag.Source{
			{Book: "intermediate-accounting", Chapter: 4, ChunkID: "ia-c4-001"},
		},
	}}
	tool := New(res, fakeResolver{
		entries: []chat.TextbookEntry{{Book: "intermediate-accounting"}},
	}, 4000)
	out, err := tool.Execute(context.Background(),
		tools.ExecContext{ConversationID: "c1", TextbookScope: []string{"intermediate-accounting"}},
		json.RawMessage(`{"query":"realization principle"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Output, "[source_id: chunk_") {
		t.Fatalf("output should embed stable source_id; got:\n%s", out.Output)
	}
	if !strings.Contains(out.Output, "## Source 1") {
		t.Fatalf("output should use ## Source N headers; got:\n%s", out.Output)
	}
	var meta struct {
		Sources []struct {
			ID, Book, Chapter, ChunkHash string
		} `json:"sources"`
		TopKReturned int  `json:"top_k_returned"`
		Truncated    bool `json:"truncated"`
	}
	_ = json.Unmarshal(out.Metadata, &meta)
	if len(meta.Sources) != 1 || meta.Sources[0].ID == "" {
		t.Fatalf("metadata sources missing stable id: %+v", meta)
	}
}

func TestSearchTextbook_BookArgumentNarrowsFilter(t *testing.T) {
	res := &fakeRetriever{result: rag.RetrieveResult{
		Context: "", Sources: nil,
	}}
	tool := New(res, fakeResolver{
		entries: []chat.TextbookEntry{
			{Book: "intermediate-accounting"},
			{Book: "tax-accounting"},
		},
	}, 4000)
	_, _ = tool.Execute(context.Background(),
		tools.ExecContext{ConversationID: "c1",
			TextbookScope: []string{"intermediate-accounting", "tax-accounting"}},
		json.RawMessage(`{"query":"q","book":"tax-accounting","chapter":4}`))
	if len(res.last.filters) != 1 || res.last.filters[0].Book != "tax-accounting" ||
		len(res.last.filters[0].Chapters) != 1 || res.last.filters[0].Chapters[0] != 4 {
		t.Fatalf("filter mismatch: %+v", res.last.filters)
	}
}

func TestSearchTextbook_TruncationMarkerWhenCapped(t *testing.T) {
	longChunk := strings.Repeat("x", 5000)
	res := &fakeRetriever{result: rag.RetrieveResult{
		Context: "## A — Chapter 1\n" + longChunk + "\n\n",
		Sources: []rag.Source{{Book: "A", Chapter: 1, ChunkID: "A-c1-001"}},
	}}
	tool := New(res, fakeResolver{
		entries: []chat.TextbookEntry{{Book: "A"}},
	}, 4000)
	out, err := tool.Execute(context.Background(),
		tools.ExecContext{ConversationID: "c1", TextbookScope: []string{"A"}},
		json.RawMessage(`{"query":"q"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Output, "(truncated") {
		t.Fatalf("expected truncation marker in capped output")
	}
}
