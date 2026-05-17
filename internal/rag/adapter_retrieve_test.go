package rag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajundata/discussion_engine/internal/textbooks"
)

func TestRetrieveScopedAndBudgeted(t *testing.T) {
	srv := fakeEmbeddingServer(t)
	defer srv.Close()
	root := t.TempDir()
	bookDir := filepath.Join(root, "ia")
	os.MkdirAll(bookDir, 0o755)
	os.WriteFile(filepath.Join(bookDir, "chapter-01.md"),
		[]byte("# Chapter 1\n## Revenue\nRevenue recognized when earned.\n"), 0o600)
	os.WriteFile(filepath.Join(bookDir, "chapter-02.md"),
		[]byte("# Chapter 2\n## Leases\nLease classification rules.\n"), 0o600)

	a, _ := NewAdapter(Options{RAGDBPath: filepath.Join(root, "rag.db"),
		EmbeddingModel: "m", OpenAIKey: "k", OpenAIBaseURL: srv.URL})
	defer a.Close()
	book := textbooks.Book{Name: "ia", Chapters: []textbooks.Chapter{
		{Num: 1, Path: filepath.Join(bookDir, "chapter-01.md")},
		{Num: 2, Path: filepath.Join(bookDir, "chapter-02.md")},
	}}
	a.IndexBook(context.Background(), book, nil)

	// Scope to book "ia", chapter 1 only.
	res, err := a.Retrieve(context.Background(), "revenue", []ScopeFilter{{Book: "ia", Chapters: []int{1}}}, 10, 100000)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if res.Context == "" {
		t.Fatal("expected non-empty context")
	}
	if strings.Contains(res.Context, "Lease classification") {
		t.Fatal("chapter 2 leaked past the chapter-1 scope filter")
	}
	if len(res.Sources) == 0 {
		t.Fatal("expected sources recorded")
	}

	// Tiny budget => context is trimmed (shorter than the unbudgeted version).
	small, _ := a.Retrieve(context.Background(), "revenue", []ScopeFilter{{Book: "ia"}}, 10, 5)
	if len(small.Context) >= len(res.Context) {
		t.Fatal("expected budgeted context to be shorter")
	}

	// No scope => RAG skipped, empty context, no error.
	none, err := a.Retrieve(context.Background(), "revenue", nil, 10, 1000)
	if err != nil || none.Context != "" {
		t.Fatalf("expected empty context with no scope, got %q err=%v", none.Context, err)
	}
}
