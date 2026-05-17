package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cajundata/discussion_engine/internal/textbooks"
)

// fakeEmbeddings returns a deterministic 3-dim vector per input.
func fakeEmbeddingServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var data []map[string]any
		for i := range req.Input {
			data = append(data, map[string]any{"embedding": []float64{float64(i + 1), 0.5, 0.25}, "index": i, "object": "embedding"})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": data, "model": "x", "object": "list"})
	}))
}

func TestIndexBook(t *testing.T) {
	srv := fakeEmbeddingServer(t)
	defer srv.Close()

	root := t.TempDir()
	bookDir := filepath.Join(root, "ia")
	os.MkdirAll(bookDir, 0o755)
	os.WriteFile(filepath.Join(bookDir, "chapter-01.md"),
		[]byte("# Chapter 1\n## Revenue\nRevenue is recognized when earned.\n"), 0o600)

	a, err := NewAdapter(Options{
		RAGDBPath:      filepath.Join(root, "rag.db"),
		EmbeddingModel: "text-embedding-3-small",
		OpenAIKey:      "test",
		OpenAIBaseURL:  srv.URL,
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	defer a.Close()

	book := textbooks.Book{Name: "ia", Chapters: []textbooks.Chapter{{Num: 1, Path: filepath.Join(bookDir, "chapter-01.md")}}}
	res, err := a.IndexBook(context.Background(), book, nil)
	if err != nil {
		t.Fatalf("IndexBook: %v", err)
	}
	if res.ChunksIndexed == 0 {
		t.Fatal("expected chunks indexed")
	}
	// Second call must be a no-op (already indexed, unchanged).
	res2, err := a.IndexBook(context.Background(), book, nil)
	if err != nil {
		t.Fatalf("IndexBook 2: %v", err)
	}
	if !res2.SkippedUpToDate {
		t.Fatal("expected second index to skip (up to date)")
	}
}
