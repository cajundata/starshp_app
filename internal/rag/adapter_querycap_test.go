package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/rag/chunker"
	"github.com/cajundata/starshp_app/internal/textbooks"
)

// tokenLimitedEmbeddingServer mimics OpenAI's embeddings endpoint, rejecting any
// single input over 8192 tokens with a 400 — the exact failure we are fixing.
func tokenLimitedEmbeddingServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		for i, in := range req.Input {
			n, err := chunker.CountTokens(in)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if n > 8192 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{
					"message": "Invalid 'input[" + string(rune('0'+i)) + "]': maximum input length is 8192 tokens.",
					"type":    "invalid_request_error",
				})
				return
			}
		}
		var data []map[string]any
		for i := range req.Input {
			data = append(data, map[string]any{"embedding": []float64{float64(i + 1), 0.5, 0.25}, "index": i, "object": "embedding"})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": data, "model": "x", "object": "list"})
	}))
}

func TestRetrieve_OversizedQueryIsTruncatedNotRejected(t *testing.T) {
	srv := tokenLimitedEmbeddingServer(t)
	defer srv.Close()

	root := t.TempDir()
	bookDir := filepath.Join(root, "ia")
	os.MkdirAll(bookDir, 0o755)
	os.WriteFile(filepath.Join(bookDir, "chapter-01.md"),
		[]byte("# Chapter 1\n## Revenue\nRevenue recognized when earned.\n"), 0o600)

	a, err := NewAdapter(Options{RAGDBPath: filepath.Join(root, "rag.db"),
		EmbeddingModel: "text-embedding-3-small", OpenAIKey: "k", OpenAIBaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	defer a.Close()
	book := textbooks.Book{Name: "ia", Chapters: []textbooks.Chapter{
		{Num: 1, Path: filepath.Join(bookDir, "chapter-01.md")},
	}}
	if _, err := a.IndexBook(context.Background(), book, nil); err != nil {
		t.Fatalf("IndexBook: %v", err)
	}

	// A pasted homework JSON far exceeding 8192 tokens.
	hugeQuery := strings.Repeat(`{"q":"what is revenue recognition under ASC 606"} `, 6000)
	n, err := chunker.CountTokens(hugeQuery)
	if err != nil {
		t.Fatalf("CountTokens: %v", err)
	}
	if n <= 8192 {
		t.Fatalf("fixture must exceed 8192 tokens, got %d", n)
	}

	res, err := a.Retrieve(context.Background(), hugeQuery, []ScopeFilter{{Book: "ia", Chapters: []int{1}}}, 5, 100000)
	if err != nil {
		t.Fatalf("Retrieve with oversized query should succeed after truncation, got: %v", err)
	}
	if res.Context == "" {
		t.Fatal("expected non-empty context from truncated query")
	}
}
