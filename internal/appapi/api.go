// Package appapi exposes the Wails-bound API. It is the error-normalization
// boundary: every returned error is a provider.AppError.
package appapi

import (
	"context"
	"strings"
	"sync"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/library"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/rag"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/cajundata/starshp_app/internal/textbooks"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type API struct {
	ctx            context.Context
	cfg            config.Config
	st             *store.Store
	reg            provider.Registry
	ragAdpt        *rag.Adapter
	lib            *library.Library
	chatSvc        *chat.Service
	mu             sync.Mutex
	cancelInFlight context.CancelFunc
}

func NewAPI(cfg config.Config, st *store.Store, reg provider.Registry, ragAdpt *rag.Adapter) *API {
	return &API{cfg: cfg, st: st, reg: reg, ragAdpt: ragAdpt,
		lib: library.New(cfg.LibraryDir), chatSvc: chat.New(st)}
}

// Startup is called by Wails with the app context.
func (a *API) Startup(ctx context.Context) { a.ctx = ctx }

func (a *API) StartupIssues() []string { return ValidateStartup(a.cfg, a.reg) }

func (a *API) ListConversations() ([]store.Conversation, error) { return a.st.ListConversations() }
func (a *API) CreateConversation(title string) (store.Conversation, error) {
	return a.st.CreateConversation(title)
}
func (a *API) DeleteConversation(id string) error              { return a.st.DeleteConversation(id) }
func (a *API) ListMessages(id string) ([]store.Message, error) { return a.st.ListMessages(id) }
func (a *API) Models() []provider.ModelInfo                    { return a.reg.Models }
func (a *API) ListBooks() ([]textbooks.Book, error) {
	return textbooks.Scan(a.cfg.TextbooksConfig)
}
func (a *API) SetConversationScope(convID string, scopes []store.TextbookScope) error {
	return a.st.SetConversationTextbooks(convID, scopes)
}
func (a *API) GetConversationScope(convID string) ([]store.TextbookScope, error) {
	return a.st.GetConversationTextbooks(convID)
}
func (a *API) SetConversationMeta(convID, model string) error {
	return a.st.SetConversationMeta(convID, model)
}

// titleFromText derives a conversation title from the first user message.
// It collapses newlines, trims whitespace, and truncates to 60 runes.
func titleFromText(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " "))
	r := []rune(s)
	const max = 60
	if len(r) > max {
		return strings.TrimSpace(string(r[:max])) + "…"
	}
	if s == "" {
		return "New conversation"
	}
	return s
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

// buildChatUsageEvent assembles the payload sent on the "chat:usage" Wails
// event. Returns nil when usage is nil — callers should skip emitting in
// that case.
func buildChatUsageEvent(convID, modelID string, usage *provider.Usage) map[string]any {
	if usage == nil {
		return nil
	}
	return map[string]any{
		"convID":  convID,
		"input":   usage.InputTokens,
		"output":  usage.OutputTokens,
		"cached":  usage.CachedInputTokens,
		"modelID": modelID,
	}
}

// SendMessage streams the assistant reply to the frontend via the
// "chat:token" event and returns the full text (or a normalized error). The
// system prompt is assembled from the conversation's active library items.
func (a *API) SendMessage(convID, userText, modelID string) (string, error) {
	prov, err := provider.New(a.reg, modelID, a.cfg.OpenAIAPIKey, a.cfg.AnthropicAPIKey)
	if err != nil {
		return "", provider.NormalizeError(err)
	}
	// Auto-title: set title from first user message (best-effort, must not block send).
	existing, _ := a.st.ListMessages(convID)
	if len(existing) == 0 {
		_ = a.st.SetConversationTitle(convID, titleFromText(userText))
	}

	systemPrompt, skipped, err := a.assembleSystemPrompt(convID)
	if err != nil {
		return "", provider.NormalizeError(err)
	}
	if len(skipped) > 0 {
		// A missing snippet is not fatal — skip it, surface a soft notice.
		wruntime.EventsEmit(a.ctx, "library:notice",
			"Skipped missing library items: "+strings.Join(skipped, ", "))
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

	text, usage, err := a.chatSvc.Send(cctx, chat.SendParams{
		ConversationID: convID, UserText: userText, SystemPrompt: systemPrompt,
		Model: modelID, Provider: prov, Retriever: retr,
	}, func(tok string) {
		wruntime.EventsEmit(a.ctx, "chat:token", tok) // use a.ctx: events always flow to UI
	})
	if payload := buildChatUsageEvent(convID, modelID, usage); payload != nil {
		wruntime.EventsEmit(a.ctx, "chat:usage", payload)
	}
	if err != nil {
		if ae, ok := err.(provider.AppError); ok {
			if m, found := a.reg.ByID(modelID); found {
				err = provider.MaybeRemapLocal(ae, m)
			}
		}
	}
	return text, err
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

// booksToIndex returns configured book names that are in the requested set,
// preserving configured order.
func booksToIndex(configured, requested []string) []string {
	want := map[string]bool{}
	for _, r := range requested {
		want[r] = true
	}
	var out []string
	for _, c := range configured {
		if want[c] {
			out = append(out, c)
		}
	}
	return out
}

// EnsureIndexed indexes (idempotently) every attached book for a conversation,
// emitting "rag:index" progress events. Safe to call before each send.
func (a *API) EnsureIndexed(convID string) error {
	scopes, err := a.st.GetConversationTextbooks(convID)
	if err != nil {
		return provider.NormalizeError(err)
	}
	if len(scopes) == 0 {
		return nil
	}
	if a.ragAdpt == nil {
		return provider.AppError{Code: "rag_unavailable", UserMessage: "Textbook indexing is unavailable (RAG not initialized — check OPENAI_API_KEY).", Retryable: false}
	}
	books, err := textbooks.Scan(a.cfg.TextbooksConfig)
	if err != nil {
		return provider.NormalizeError(err)
	}
	var configured, requested []string
	byName := map[string]textbooks.Book{}
	for _, b := range books {
		configured = append(configured, b.Name)
		byName[b.Name] = b
	}
	for _, s := range scopes {
		requested = append(requested, s.Name)
	}
	for _, name := range booksToIndex(configured, requested) {
		b := byName[name]
		// Scan flags an unreadable chapter_dir on the book itself. Indexing
		// would otherwise silently no-op and the user would later get empty
		// retrieval with no explanation.
		if b.Error != "" {
			return provider.AppError{
				Code:        "textbook_unavailable",
				UserMessage: "Textbook " + name + " is unavailable: " + b.Error,
				Retryable:   false,
			}
		}
		_, err := a.ragAdpt.IndexBook(a.ctx, b, func(done, total int) {
			wruntime.EventsEmit(a.ctx, "rag:index", map[string]any{"book": name, "done": done, "total": total})
		})
		if err != nil {
			return provider.NormalizeError(err)
		}
	}
	return nil
}
