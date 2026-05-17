package appapi

import "testing"

// indexBookNames is the pure helper EnsureIndexed delegates to: given the
// configured books and requested scope names, returns the books to index.
func TestBooksToIndex(t *testing.T) {
	all := []string{"ia", "blaw", "audit"}
	got := booksToIndex(all, []string{"blaw", "ia", "missing"})
	if len(got) != 2 || got[0] != "ia" || got[1] != "blaw" {
		t.Fatalf("booksToIndex = %v", got)
	}
}
