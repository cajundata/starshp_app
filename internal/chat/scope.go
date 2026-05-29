package chat

import "context"

// TextbookEntry is one attached textbook for a conversation, optionally
// narrowed to specific chapters. Nil/empty Chapters means whole book.
type TextbookEntry struct {
	Book     string
	Chapters []int
}

// ScopeResolver returns the attached textbook scope for a conversation. It is
// the seam between tools and the store package — tools depend on this
// interface, not on store.Store directly.
type ScopeResolver interface {
	Resolve(ctx context.Context, conversationID string) ([]TextbookEntry, error)
}

// BookNames extracts just the book names from a scope, preserving order.
// Convenience for callers that only need the validation set.
func BookNames(entries []TextbookEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Book)
	}
	return out
}
