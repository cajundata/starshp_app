package appapi

import (
	"context"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/store"
)

// chatStoreResolver adapts store.Store to chat.ScopeResolver, translating the
// conversation's attached textbooks into the chat-layer scope type the tool
// boundary consumes.
type chatStoreResolver struct{ st *store.Store }

func (r chatStoreResolver) Resolve(_ context.Context, convID string) ([]chat.TextbookEntry, error) {
	scopes, err := r.st.GetConversationTextbooks(convID)
	if err != nil {
		return nil, err
	}
	out := make([]chat.TextbookEntry, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, chat.TextbookEntry{Book: s.Name, Chapters: s.Chapters})
	}
	return out, nil
}
