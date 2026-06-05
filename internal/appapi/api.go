// Package appapi exposes the Wails-bound API. It is the error-normalization
// boundary: every returned error is a provider.AppError.
package appapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cajundata/starshp_app/internal/assignment"
	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/library"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/rag"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/cajundata/starshp_app/internal/textbooks"
	"github.com/cajundata/starshp_app/internal/tools"
	"github.com/cajundata/starshp_app/internal/tools/safemath"
	"github.com/cajundata/starshp_app/internal/tools/searchtextbook"
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
	toolReg        *tools.Registry
	mu             sync.Mutex
	cancelInFlight context.CancelFunc

	assignmentFactory assignment.ProviderFactory     // overridable in tests
	emit              func(name string, payload any) // wruntime.EventsEmit wrapper; overridable in tests
	asgCancel         context.CancelFunc
	rerunning         bool // guards against concurrent single-item reruns (a.mu)
}

func NewAPI(cfg config.Config, st *store.Store, reg provider.Registry, ragAdpt *rag.Adapter) *API {
	a := &API{cfg: cfg, st: st, reg: reg, ragAdpt: ragAdpt,
		lib: library.New(cfg.LibraryDir), chatSvc: chat.New(st)}
	// In-process tool registry for the agentic loop. safe_math has no
	// dependencies; search_textbook is registered only when RAG is available.
	a.toolReg = tools.NewRegistry(30 * time.Second)
	if ragAdpt != nil {
		_ = a.toolReg.Register(searchtextbook.New(
			ragRetrieverShim{a: a},
			chatStoreResolver{st: st},
			4000,
		))
	}
	_ = a.toolReg.Register(safemath.New())
	a.assignmentFactory = func(modelID string) (provider.ChatProvider, string, error) {
		p, err := provider.New(a.reg, modelID, a.cfg.OpenAIAPIKey, a.cfg.AnthropicAPIKey)
		if err != nil {
			return nil, "", err
		}
		return p, providerNameFromModelID(a.reg, modelID), nil
	}
	a.emit = func(name string, payload any) { wruntime.EventsEmit(a.ctx, name, payload) }
	return a
}

// sinkEventName maps a chat SinkEventKind to its Wails event name. Pure so the
// taxonomy mapping is unit-testable without a Wails runtime. Returns "" for the
// legacy token kind, which wailsSink handles specially.
func sinkEventName(k chat.SinkEventKind) string {
	switch k {
	case chat.SinkRunStarted:
		return "chat:run_started"
	case chat.SinkGroundingReady:
		return "chat:grounding_ready"
	case chat.SinkToken:
		return "chat:token_v2"
	case chat.SinkToolCall:
		return "chat:tool_call"
	case chat.SinkToolResult:
		return "chat:tool_result"
	case chat.SinkRunCompleted:
		return "chat:run_completed"
	case chat.SinkRunErrored:
		return "chat:run_errored"
	case chat.SinkRunCancelled:
		return "chat:run_cancelled"
	case chat.SinkUsage:
		return "chat:usage"
	}
	return ""
}

// wailsSink maps chat lifecycle events onto the full chat:* Wails event
// taxonomy. Each payload carries convID/runID/turnID plus the event-specific
// fields. chat:token keeps its legacy single-string shape for the existing
// frontend token handler; chat:token_v2 carries the correlated payload.
type wailsSink struct{ a *API }

func (w wailsSink) Emit(e chat.SinkEvent) {
	if e.Kind == chat.SinkToken {
		if tok, ok := e.Payload["text"].(string); ok {
			wruntime.EventsEmit(w.a.ctx, "chat:token", tok)
		}
	}
	name := sinkEventName(e.Kind)
	if name == "" {
		return
	}
	payload := map[string]any{"convID": e.ConvID, "runID": e.RunID, "turnID": e.TurnID}
	for k, v := range e.Payload {
		payload[k] = v
	}
	wruntime.EventsEmit(w.a.ctx, name, payload)
}

// retrievalMode reads the conversation's stored retrieval policy, falling back
// to the default when the row is missing or unreadable.
func (a *API) retrievalMode(convID string) chat.RetrievalMode {
	m, err := a.st.GetRetrievalMode(convID)
	if err != nil || m == "" {
		return chat.RetrievalAutoGroundedDefault
	}
	return chat.RetrievalMode(m)
}

// providerNameFromModelID resolves "openai" | "anthropic" for runs.provider.
func providerNameFromModelID(reg provider.Registry, modelID string) string {
	for _, m := range reg.Models {
		if m.ID == modelID {
			return m.Provider
		}
	}
	return ""
}

// Startup is called by Wails with the app context.
func (a *API) Startup(ctx context.Context) { a.ctx = ctx }

func (a *API) StartupIssues() []string { return ValidateStartup(a.cfg) }

func (a *API) ListConversations() ([]store.Conversation, error) { return a.st.ListConversations() }
func (a *API) CreateConversation(title string) (store.Conversation, error) {
	return a.st.CreateConversation(title)
}
func (a *API) DeleteConversation(id string) error { return a.st.DeleteConversation(id) }

// ListMessages is deprecated: history now flows through
// GetConversationDisplayEvents. Returns an empty slice so older frontend builds
// degrade gracefully rather than mixing schemas during rollout.
func (a *API) ListMessages(_ string) ([]store.Message, error) { return nil, nil }
func (a *API) Models() []provider.ModelInfo                   { return a.reg.Models }
func (a *API) ListBooks() ([]textbooks.Book, error) {
	return textbooks.Scan(a.cfg.TextbooksConfig)
}
func (a *API) SetConversationScope(convID string, scopes []store.TextbookScope) error {
	return a.st.SetConversationTextbooks(convID, scopes)
}
func (a *API) GetConversationScope(convID string) ([]store.TextbookScope, error) {
	return a.st.GetConversationTextbooks(convID)
}
func (a *API) SetAssignmentScope(asgID string, scopes []store.TextbookScope) error {
	return a.st.SetAssignmentScope(asgID, scopes)
}
func (a *API) GetAssignmentScope(asgID string) ([]store.TextbookScope, error) {
	return a.st.GetAssignmentScope(asgID)
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

func (r ragRetriever) Retrieve(ctx context.Context, q string) (string, string, []chat.RetrievedSource, error) {
	var filters []rag.ScopeFilter
	for _, s := range r.scopes {
		filters = append(filters, rag.ScopeFilter{Book: s.Name, Chapters: s.Chapters})
	}
	res, err := r.a.ragAdpt.Retrieve(ctx, q, filters, r.a.cfg.RAGTopK, r.a.cfg.ContextTokenBudget)
	if err != nil {
		return "", "", nil, err
	}
	srcJSON, _ := jsonMarshal(res.Sources)
	sources := make([]chat.RetrievedSource, 0, len(res.Sources))
	for _, s := range res.Sources {
		sources = append(sources, chat.RetrievedSource{Book: s.Book, Chapter: s.Chapter, ChunkID: s.ChunkID})
	}
	return res.Context, srcJSON, sources, nil
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

// SendMessage runs the agentic loop for one user turn. Assistant output is
// surfaced to the frontend through the chat:* Wails event taxonomy (the bubble
// renders from events), so the method returns only a normalized error. The
// system prompt is assembled from the conversation's active library items.
func (a *API) SendMessage(convID, userText, modelID string) error {
	prov, err := provider.New(a.reg, modelID, a.cfg.OpenAIAPIKey, a.cfg.AnthropicAPIKey)
	if err != nil {
		return provider.NormalizeError(err)
	}
	// Auto-title from the first user message (best-effort; reads the canonical
	// event log now that messages is retired).
	existing, _ := a.st.GetConversationDisplayEvents(convID)
	if len(existing) == 0 {
		_ = a.st.SetConversationTitle(convID, titleFromText(userText))
	}

	systemPrompt, skipped, err := a.assembleSystemPrompt(convID)
	if err != nil {
		return provider.NormalizeError(err)
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

	_, err = a.chatSvc.Send(cctx, chat.SendParams{
		ConversationID: convID,
		UserText:       userText,
		SystemPrompt:   systemPrompt,
		Model:          modelID,
		Provider:       prov,
		ProviderName:   providerNameFromModelID(a.reg, modelID),
		Registry:       a.toolReg,
		Resolver:       chatStoreResolver{st: a.st},
		Retriever:      retr,
		RetrievalMode:  a.retrievalMode(convID),
		Sink:           wailsSink{a: a},
	}, nil)
	return err
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

// SolveAssignment loads a companion _json directory and solves every question
// concurrently in the background. Returns the assignment id immediately.
func (a *API) SolveAssignment(dir string, scopes []store.TextbookScope, libraryItems []string) (string, error) {
	model := a.defaultModelID()
	if model == "" {
		return "", provider.AppError{Code: "config", UserMessage: "No model configured.", Retryable: false}
	}
	var search tools.Tool
	if a.ragAdpt != nil {
		search = searchtextbook.New(ragRetrieverShim{a: a}, chatStoreResolver{st: a.st}, 4000)
	}
	libPreamble, _, _ := a.assembleLibraryPreamble(libraryItems)
	opts := assignment.Options{
		Model:           model,
		Concurrency:     assignmentConcurrency(),
		Grounding:       assignment.NoGrounding{}, // v1: textbooks via the search_textbook tool, no pre-turn grounding
		SafeMath:        safemath.New(),
		SearchTool:      search,
		Resolver:        chatStoreResolver{st: a.st},
		LibraryPreamble: libPreamble,
		Emit:            a.emit,
	}
	orc := assignment.New(a.st, a.chatSvc, a.assignmentFactory, opts)

	cctx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	a.asgCancel = cancel
	a.mu.Unlock()

	id, err := orc.Start(cctx, dir, scopes, libraryItems, cancel)
	if err != nil {
		cancel()
		return "", provider.NormalizeError(err)
	}
	return id, nil
}

// RerunAssignmentItem re-solves a single item in place and returns the new
// conversation id. Idle-only: errors if another rerun is already in flight or a
// batch is running. The prior answer (and its _answers/NNN.json file) is
// overwritten. Runs synchronously: it returns when the item has been re-solved.
func (a *API) RerunAssignmentItem(asgID string, seq int) (string, error) {
	a.mu.Lock()
	if a.rerunning {
		a.mu.Unlock()
		return "", provider.AppError{Code: "busy", UserMessage: "Another rerun is already running.", Retryable: false}
	}
	a.rerunning = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.rerunning = false
		a.mu.Unlock()
	}()

	model := a.defaultModelID()
	if model == "" {
		return "", provider.AppError{Code: "config", UserMessage: "No model configured.", Retryable: false}
	}
	var search tools.Tool
	if a.ragAdpt != nil {
		search = searchtextbook.New(ragRetrieverShim{a: a}, chatStoreResolver{st: a.st}, 4000)
	}
	libItems, lerr := a.st.GetAssignmentLibraryItems(asgID)
	if lerr != nil {
		slog.Warn("assignment: rerun read library items failed; solving without library preamble", "assignmentId", asgID, "err", lerr)
	}
	libPreamble, _, _ := a.assembleLibraryPreamble(libItems)
	opts := assignment.Options{
		Model:           model,
		Concurrency:     1,
		Grounding:       assignment.NoGrounding{},
		SafeMath:        safemath.New(),
		SearchTool:      search,
		Resolver:        chatStoreResolver{st: a.st},
		LibraryPreamble: libPreamble,
		Emit:            func(_ string, _ any) {}, // decoupled from batch progress events
	}
	orc := assignment.New(a.st, a.chatSvc, a.assignmentFactory, opts)

	// Synchronous, no per-call cancel in v1: uses the app-lifetime context.
	// (Optional rerun cancel is a documented follow-up.)
	updated, err := orc.RerunItem(a.ctx, asgID, seq)
	if err != nil {
		if ae, ok := err.(provider.AppError); ok {
			return "", ae // preserve typed code; NormalizeError would mask it as "unknown"
		}
		return "", provider.NormalizeError(err)
	}
	return updated.ConversationID, nil
}

// CancelAssignment aborts the in-flight assignment batch, if any.
func (a *API) CancelAssignment(_ string) {
	a.mu.Lock()
	c := a.asgCancel
	a.mu.Unlock()
	if c != nil {
		c()
	}
}

func (a *API) ListAssignments() ([]store.Assignment, error)      { return a.st.ListAssignments() }
func (a *API) GetAssignment(id string) (store.Assignment, error) { return a.st.GetAssignment(id) }
func (a *API) ListAssignmentItems(id string) ([]store.AssignmentItem, error) {
	return a.st.ListAssignmentItems(id)
}

func (a *API) defaultModelID() string {
	if len(a.reg.Models) > 0 {
		return a.reg.Models[0].ID
	}
	return ""
}

func assignmentConcurrency() int {
	if v := os.Getenv("STARSHP_ASSIGNMENT_CONCURRENCY"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return 4
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
	return a.ensureBooksIndexed(scopes)
}

// EnsureIndexedScope indexes the given textbook scope directly (no conversation).
// Used by the assignment flow, which has no single conversation. Idempotent.
func (a *API) EnsureIndexedScope(scopes []store.TextbookScope) error {
	return a.ensureBooksIndexed(scopes)
}

// ensureBooksIndexed indexes (idempotently) every requested book, emitting
// "rag:index" progress events. Empty scope is a no-op.
func (a *API) ensureBooksIndexed(scopes []store.TextbookScope) error {
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

// EventDTO is the JSON shape the frontend uses to render the event-log-based
// assistant bubble (text + inline tool blocks).
type EventDTO struct {
	ID            string          `json:"id"`
	TurnID        string          `json:"turnId"`
	RunID         string          `json:"runId,omitempty"`
	Kind          string          `json:"kind"`
	Text          string          `json:"text,omitempty"`
	ToolCallID    string          `json:"toolCallId,omitempty"`
	ToolName      string          `json:"toolName,omitempty"`
	ToolInput     json.RawMessage `json:"toolInput,omitempty"`
	ToolMetadata  json.RawMessage `json:"toolMetadata,omitempty"`
	ToolLatencyMs int64           `json:"toolLatencyMs,omitempty"`
	IsError       bool            `json:"isError,omitempty"`
}

// GetConversationDisplayEvents returns the user-visible event timeline: the
// active completed run per turn, or the latest terminal run when none is
// active (so cancelled/errored partial output the user saw is preserved).
func (a *API) GetConversationDisplayEvents(convID string) ([]EventDTO, error) {
	rows, err := a.st.GetConversationDisplayEvents(convID)
	if err != nil {
		return nil, provider.NormalizeError(err)
	}
	out := make([]EventDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, EventDTO{
			ID: r.ID, TurnID: r.TurnID, RunID: r.RunID, Kind: r.Kind,
			Text: r.Text, ToolCallID: r.ToolCallID, ToolName: r.ToolName,
			ToolInput: r.ToolInput, ToolMetadata: r.ToolMetadata,
			ToolLatencyMs: r.ToolLatencyMs, IsError: r.IsError,
		})
	}
	return out, nil
}

// GetRetrievalMode returns the per-conversation retrieval policy.
func (a *API) GetRetrievalMode(convID string) (string, error) {
	m, err := a.st.GetRetrievalMode(convID)
	if err != nil {
		return "", provider.NormalizeError(err)
	}
	return m, nil
}

// SetRetrievalMode updates the per-conversation retrieval policy.
func (a *API) SetRetrievalMode(convID, mode string) error {
	return a.st.SetRetrievalMode(convID, mode)
}
