package appapi

import (
	"testing"

	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

// booksToIndex is the pure helper EnsureIndexed delegates to: given the
// configured books and requested scope names, returns the books to index.
func TestBooksToIndex(t *testing.T) {
	all := []string{"ia", "blaw", "audit"}
	got := booksToIndex(all, []string{"blaw", "ia", "missing"})
	if len(got) != 2 || got[0] != "ia" || got[1] != "blaw" {
		t.Fatalf("booksToIndex = %v", got)
	}
}

func TestEnsureIndexedScope_EmptyIsNoop(t *testing.T) {
	a := &API{} // ragAdpt is nil; an empty scope must return before touching it
	if err := a.EnsureIndexedScope(nil); err != nil {
		t.Fatalf("empty scope should be a no-op, got %v", err)
	}
}

func TestEnsureIndexedScope_NoRAGErrors(t *testing.T) {
	a := &API{} // ragAdpt is nil
	err := a.EnsureIndexedScope([]store.TextbookScope{{Name: "blaw"}})
	ae, ok := err.(provider.AppError)
	if !ok || ae.Code != "rag_unavailable" {
		t.Fatalf("want rag_unavailable AppError, got %v", err)
	}
}
