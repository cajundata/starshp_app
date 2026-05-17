// Package appapi exposes the Wails-bound API. It is the error-normalization
// boundary: every returned error is a provider.AppError.
package appapi

import (
	"context"
	"sync"

	"github.com/cajundata/discussion_engine/internal/chat"
	"github.com/cajundata/discussion_engine/internal/config"
	"github.com/cajundata/discussion_engine/internal/provider"
	"github.com/cajundata/discussion_engine/internal/rag"
	"github.com/cajundata/discussion_engine/internal/store"
	"github.com/cajundata/discussion_engine/internal/textbooks"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type API struct {
	ctx            context.Context
	cfg            config.Config
	st             *store.Store
	reg            provider.Registry
	ragAdpt        *rag.Adapter
	chatSvc        *chat.Service
	mu             sync.Mutex
	cancelInFlight context.CancelFunc
}

func NewAPI(cfg config.Config, st *store.Store, reg provider.Registry, ragAdpt *rag.Adapter) *API {
	return &API{cfg: cfg, st: st, reg: reg, ragAdpt: ragAdpt, chatSvc: chat.New(st)}
}

// Startup is called by Wails with the app context.
func (a *API) Startup(ctx context.Context) { a.ctx = ctx }

func (a *API) StartupIssues() []string { return ValidateStartup(a.cfg) }

func (a *API) ListConversations() ([]store.Conversation, error) { return a.st.ListConversations() }
func (a *API) CreateConversation(title string) (store.Conversation, error) {
	return a.st.CreateConversation(title)
}
func (a *API) DeleteConversation(id string) error              { return a.st.DeleteConversation(id) }
func (a *API) ListMessages(id string) ([]store.Message, error) { return a.st.ListMessages(id) }
func (a *API) ListPresets() ([]store.Preset, error)            { return a.st.ListPresets() }
func (a *API) CreatePreset(name, prompt string) (store.Preset, error) {
	return a.st.CreatePreset(name, prompt)
}
func (a *API) UpdatePreset(id, name, prompt string) error { return a.st.UpdatePreset(id, name, prompt) }
func (a *API) DeletePreset(id string) error               { return a.st.DeletePreset(id) }
func (a *API) Models() []provider.ModelInfo               { return a.reg.Models }
func (a *API) ListBooks() ([]textbooks.Book, error) {
	return textbooks.Scan(a.cfg.TextbooksConfig)
}
func (a *API) SetConversationScope(convID string, scopes []store.TextbookScope) error {
	return a.st.SetConversationTextbooks(convID, scopes)
}
func (a *API) GetConversationScope(convID string) ([]store.TextbookScope, error) {
	return a.st.GetConversationTextbooks(convID)
}
func (a *API) SetConversationMeta(convID, presetID, model string) error {
	return a.st.SetConversationMeta(convID, presetID, model)
}

// ragRetriever adapts rag.Adapter to chat.Retriever for one scoped request.
type ragRetriever struct {
	a      *API
	scopes []store.TextbookScope
}

func (r ragRetriever) Retrieve(ctx context.Context, q string) (string, string, error) {
	var filters []rag.ScopeFilter
	for _, s := range r.scopes {
		filters = append(filters, rag.ScopeFilter{Book: s.Name, Chapters: s.Chapters})
	}
	res, err := r.a.ragAdpt.Retrieve(ctx, q, filters, r.a.cfg.RAGTopK, r.a.cfg.ContextTokenBudget)
	if err != nil {
		return "", "", err
	}
	srcJSON, _ := jsonMarshal(res.Sources)
	return res.Context, srcJSON, nil
}

// SendMessage streams the assistant reply to the frontend via the
// "chat:token" event and returns the full text (or a normalized error).
func (a *API) SendMessage(convID, userText, systemPrompt, modelID string) (string, error) {
	prov, err := provider.New(a.reg, modelID, a.cfg.OpenAIAPIKey, a.cfg.AnthropicAPIKey)
	if err != nil {
		return "", provider.NormalizeError(err)
	}
	scopes, _ := a.st.GetConversationTextbooks(convID) // failure → no RAG scope, not fatal
	var retr chat.Retriever
	if len(scopes) > 0 && a.ragAdpt != nil {
		retr = ragRetriever{a: a, scopes: scopes}
	}

	// Derive a per-request cancellable context so CancelMessage can abort this stream.
	cctx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	a.cancelInFlight = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		a.cancelInFlight = nil
		a.mu.Unlock()
	}()

	return a.chatSvc.Send(cctx, chat.SendParams{
		ConversationID: convID, UserText: userText, SystemPrompt: systemPrompt,
		Model: modelID, Provider: prov, Retriever: retr,
	}, func(tok string) {
		wruntime.EventsEmit(a.ctx, "chat:token", tok) // use a.ctx: events always flow to UI
	})
}

// CancelMessage aborts the in-flight streaming response, if any.
func (a *API) CancelMessage() {
	a.mu.Lock()
	c := a.cancelInFlight
	a.mu.Unlock()
	if c != nil {
		c()
	}
}
