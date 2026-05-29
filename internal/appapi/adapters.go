package appapi

import (
	"context"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/rag"
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

// ragRetrieverShim adapts the app's rag.Adapter to searchtextbook.Retriever
// (the model-called escalation tool), distinct from the pre-turn ragRetriever.
type ragRetrieverShim struct{ a *API }

func (r ragRetrieverShim) Retrieve(ctx context.Context, query string, filters []rag.ScopeFilter, topK, budgetTokens int) (rag.RetrieveResult, error) {
	return r.a.ragAdpt.Retrieve(ctx, query, filters, topK, budgetTokens)
}
