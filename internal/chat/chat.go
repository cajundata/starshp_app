// Package chat orchestrates retrieval + provider streaming + persistence.
package chat

import (
	"context"
	"strings"

	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

type Retriever interface {
	// Retrieve returns (contextBlock, sourcesJSON, error).
	Retrieve(ctx context.Context, query string) (string, string, error)
}

type Service struct{ st *store.Store }

func New(st *store.Store) *Service { return &Service{st: st} }

type SendParams struct {
	ConversationID string
	UserText       string
	SystemPrompt   string
	Model          string
	Provider       provider.ChatProvider
	Retriever      Retriever // may be nil (no textbook scope)
}

// Send persists the user message, retrieves context, streams the assistant
// response (token callback per chunk), persists the assistant message with
// any captured usage, and returns the full assistant text along with the
// terminal Usage (nil if the provider did not surface one).
func (s *Service) Send(ctx context.Context, p SendParams, onToken func(string)) (string, *provider.Usage, error) {
	if _, err := s.st.AddMessage(p.ConversationID, "user", p.UserText, "", "", "", nil); err != nil {
		return "", nil, err
	}

	var ragCtx, ragSrc string
	if p.Retriever != nil {
		c, src, err := p.Retriever.Retrieve(ctx, p.UserText)
		if err != nil {
			return "", nil, err // RAG failure is explicit, never silent (spec).
		}
		ragCtx, ragSrc = c, src
	}

	prefix := p.SystemPrompt
	if ragCtx != "" {
		if prefix != "" {
			prefix += "\n\n"
		}
		prefix += ragCtx
	}

	history, err := s.st.ListMessages(p.ConversationID)
	if err != nil {
		return "", nil, err
	}
	var msgs []provider.Message
	for _, m := range history {
		if m.Role == "user" || m.Role == "assistant" {
			msgs = append(msgs, provider.Message{Role: m.Role, Content: m.Content})
		}
	}

	ch, err := p.Provider.Stream(ctx, provider.ChatRequest{
		Model: p.Model, CachedPrefix: prefix, Messages: msgs,
	})
	if err != nil {
		return "", nil, provider.NormalizeError(err)
	}

	var sb strings.Builder
	var streamErr error
	var usage *provider.Usage
	for d := range ch {
		if d.Err != nil {
			streamErr = d.Err
			break
		}
		if d.Text != "" {
			sb.WriteString(d.Text)
			if onToken != nil {
				onToken(d.Text)
			}
		}
		if d.Usage != nil {
			usage = d.Usage
		}
	}

	content := sb.String()
	if streamErr != nil {
		content += "\n\n⚠ response interrupted"
	}
	if _, err := s.st.AddMessage(p.ConversationID, "assistant", content, p.Model, ragCtx, ragSrc, usage); err != nil {
		return content, usage, err
	}
	if streamErr != nil {
		return content, usage, provider.NormalizeError(streamErr)
	}
	return content, usage, nil
}
