// Package appapi exposes the Wails-bound API. It is the error-normalization
// boundary: every returned error is a provider.AppError.
package appapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/library"
	"github.com/cajundata/starshp_app/internal/mention"
	"github.com/cajundata/starshp_app/internal/persona"
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
	personas       persona.Registry
	ragAdpt        *rag.Adapter
	lib            *library.Library
	chatSvc        *chat.Service
	toolReg        *tools.Registry
	mu             sync.Mutex
	cancelInFlight context.CancelFunc

	emit func(name string, payload any) // wruntime.EventsEmit wrapper; overridable in tests
}

// allToolNames is every tool the app can register. Persona `tools:` lists are
// validated against this, not against the live registry: search_textbook is
// only registered when RAG is available, and a RAG outage must not silently
// disable every persona that names it. TestAllToolNamesMatchesTheRegisterableTools
// keeps this in step with the tools actually constructed in NewAPI.
var allToolNames = []string{"safe_math", "search_textbook"}

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
	a.emit = func(name string, payload any) { wruntime.EventsEmit(a.ctx, name, payload) }

	// Seed a starter persona only when the folder is absent, then load. Loading
	// never writes and never fails: a bad persona file is disabled and reported
	// through StartupIssues.
	if err := persona.Seed(cfg.PersonaDir, defaultModelID(reg)); err != nil {
		slog.Warn("persona: seed failed", "dir", cfg.PersonaDir, "err", err)
	}
	a.personas = persona.LoadRegistry(cfg.PersonaDir, modelIDs(reg), allToolNames)
	return a
}

// defaultModelID is the model a seeded persona points at: the first entry in
// models.yaml. Empty when no models are configured, which makes Seed a no-op.
func defaultModelID(reg provider.Registry) string {
	if len(reg.Models) > 0 {
		return reg.Models[0].ID
	}
	return ""
}

func modelIDs(reg provider.Registry) []string {
	out := make([]string, 0, len(reg.Models))
	for _, m := range reg.Models {
		out = append(out, m.ID)
	}
	return out
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
			w.a.emit("chat:token", tok)
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
	w.a.emit(name, payload)
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

func (a *API) StartupIssues() []string {
	issues := ValidateStartup(a.cfg, a.reg)
	for _, is := range a.personas.Issues {
		issues = append(issues, "persona "+is.File+": "+is.Reason)
	}
	return issues
}

// Personas returns the loaded assistants for the picker. The system prompt is
// not included (Persona.Prompt is json:"-") — the frontend renders names,
// colors, and model chips, and has no use for it.
func (a *API) Personas() []persona.Persona { return a.personas.Personas }

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

// SendMessage runs the agentic loop for one user turn as the named persona.
// The persona supplies the model, the system prompt, and the tool subset.
// Assistant output is surfaced through the chat:* event taxonomy (the bubble
// renders from events), so this returns only a normalized error.
func (a *API) SendMessage(convID, userText, personaID string) error {
	p, rerr := a.routePersona(userText, personaID)
	if rerr != nil {
		return rerr
	}
	prov, err := provider.New(a.reg, p.Model, provider.Keys{
		OpenAI:    a.cfg.OpenAIAPIKey,
		Anthropic: a.cfg.AnthropicAPIKey,
		Gemini:    a.cfg.GeminiAPIKey,
	})
	if err != nil {
		return provider.NormalizeError(err)
	}

	// Auto-title from the first user message (best-effort; reads the canonical
	// event log now that messages is retired).
	existing, _ := a.st.GetConversationDisplayEvents(convID)
	if len(existing) == 0 {
		_ = a.st.SetConversationTitle(convID, titleFromText(userText))
	}

	systemPrompt, skipped, err := a.assembleSystemPrompt(convID, p)
	if err != nil {
		return provider.NormalizeError(err)
	}
	if len(skipped) > 0 {
		// A missing snippet is not fatal — skip it, surface a soft notice.
		a.emit("library:notice",
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
		Model:          p.Model,
		PersonaID:      p.ID,
		Namer:          a.personas,
		Provider:       prov,
		ProviderName:   providerNameFromModelID(a.reg, p.Model),
		Registry:       a.toolReg.Subset(p.Tools),
		Resolver:       chatStoreResolver{st: a.st},
		Retriever:      retr,
		RetrievalMode:  a.retrievalMode(convID),
		Sink:           wailsSink{a: a},
		// Upgrade a local (openai_compat) network failure to local_unreachable in
		// the run-errored event, where agentic errors are surfaced (the agentic
		// Send returns nil and reports errors via the sink, not the return value).
		RemapErr: a.localRemapErr(p.Model),
	}, nil)
	return err
}

// noPersonaMessage explains why a persona could not be resolved. When the
// registry loaded nothing valid, the useful thing to say is *which files failed
// and why* — not "unknown assistant", which describes a choice the operator was
// never offered.
func (a *API) noPersonaMessage(personaID string) string {
	if len(a.personas.Personas) == 0 {
		msg := "No assistants are available."
		if len(a.personas.Issues) > 0 {
			var parts []string
			for _, is := range a.personas.Issues {
				parts = append(parts, is.File+" ("+is.Reason+")")
			}
			msg += " These persona files failed to load: " + strings.Join(parts, "; ") + "."
		} else {
			msg += " Add a persona to your personas folder."
		}
		return msg
	}
	return "Unknown assistant \"" + personaID + "\". Check your personas folder."
}

// routePersona resolves who answers this message. A leading @mention routes
// exactly one turn and never touches pinned_persona; otherwise the picker's
// persona applies. There is no fallback to a default persona: a silent
// substitution would attribute output to an assistant the operator did not
// pick, which is the exact failure per-persona attribution exists to
// prevent. An unresolvable mention lists the real persona IDs — an
// edit-distance guess is a magic number that is wrong at the boundary.
func (a *API) routePersona(userText, pickerID string) (persona.Persona, error) {
	id := pickerID
	mentioned, hasMention := mention.Parse(userText)
	if hasMention {
		id = mentioned
	}
	if p, ok := a.personas.ByID(id); ok {
		return p, nil
	}
	if hasMention && len(a.personas.Personas) > 0 {
		ids := make([]string, len(a.personas.Personas))
		for i, p := range a.personas.Personas {
			ids[i] = p.ID
		}
		return persona.Persona{}, provider.AppError{
			Code:        "config",
			UserMessage: "No assistant named \"" + mentioned + "\". Available: " + strings.Join(ids, ", ") + ".",
			Retryable:   false,
		}
	}
	return persona.Persona{}, provider.AppError{
		Code:        "config",
		UserMessage: a.noPersonaMessage(id),
		Retryable:   false,
	}
}

// SetConversationPersona pins the persona the operator last used here. The
// persona's model is written alongside it, so pinned_model stays meaningful.
func (a *API) SetConversationPersona(convID, personaID string) error {
	p, ok := a.personas.ByID(personaID)
	if !ok {
		return provider.AppError{
			Code:        "config",
			UserMessage: "Unknown assistant \"" + personaID + "\".",
			Retryable:   false,
		}
	}
	if err := a.st.SetConversationPinned(convID, p.Model, p.ID); err != nil {
		return provider.NormalizeError(err)
	}
	return nil
}

// localRemapErr returns a chat.Send RemapErr closure that upgrades a provider
// network AppError into local_unreachable when the run's model is a local
// openai_compat entry. Shared by the chat, solve, and rerun paths.
func (a *API) localRemapErr(model string) func(provider.AppError) provider.AppError {
	return func(ae provider.AppError) provider.AppError {
		if m, found := a.reg.ByID(model); found {
			return provider.MaybeRemapLocal(ae, m)
		}
		return ae
	}
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
			a.emit("rag:index", map[string]any{"book": name, "done": done, "total": total})
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
	PersonaID     string          `json:"personaId,omitempty"`
	ModelID       string          `json:"modelId,omitempty"`
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
			ID: r.ID, TurnID: r.TurnID, RunID: r.RunID,
			PersonaID: r.PersonaID, ModelID: r.Model,
			Kind: r.Kind,
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

// SetTurnContextOverride records the operator's per-turn payload override:
// auto (row absence), always (pin), never (exclude). Payload-only — the
// displayed thread never consults it. Unknown turn or invalid state is a
// config error; nothing is persisted.
func (a *API) SetTurnContextOverride(convID, turnID, state string) error {
	switch state {
	case store.OverrideAuto, store.OverrideAlways, store.OverrideNever:
	default:
		return provider.AppError{
			Code:        "config",
			UserMessage: "Invalid context override \"" + state + "\". Use auto, always, or never.",
			Retryable:   false,
		}
	}
	if err := a.st.SetTurnContextOverride(convID, turnID, state); err != nil {
		if errors.Is(err, store.ErrUnknownTurn) {
			return provider.AppError{
				Code:        "config",
				UserMessage: "That turn no longer exists in this conversation. Reopen it and try again.",
				Retryable:   false,
			}
		}
		return provider.NormalizeError(err)
	}
	return nil
}

// GetTurnContextOverrides returns the turn → state map for UI seeding on
// conversation open, alongside the existing event load. Turns in auto are
// absent from the map.
func (a *API) GetTurnContextOverrides(convID string) (map[string]string, error) {
	m, err := a.st.GetTurnContextOverrides(convID)
	if err != nil {
		return nil, provider.NormalizeError(err)
	}
	return m, nil
}
