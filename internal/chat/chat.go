// Package chat orchestrates the agentic loop: persist user_message ->
// pre-turn retrieve (if mode requires) -> create run -> loop(stream ->
// tool_use -> execute -> tool_result) -> transactional completion.
package chat

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/cajundata/starshp_app/internal/tools"
	"github.com/google/uuid"
)

// MaxIterationsDefault caps the number of tool-dispatch rounds in the agentic
// loop. STARSHP_MAX_TOOL_ITERATIONS overrides it. Runs have been observed making
// 9 distinct, productive tool calls, so the cap must comfortably exceed that.
// When the cap is reached the loop does not error — it forces one final
// tool-free answer (see finalizeWithoutTools).
const MaxIterationsDefault = 16

// Retriever is the pre-turn RAG seam. nil means no pre-turn retrieval.
type Retriever interface {
	Retrieve(ctx context.Context, query string) (block string, sourcesJSON string, sources []RetrievedSource, err error)
}

type RetrievedSource struct {
	Book    string `json:"book"`
	Chapter int    `json:"chapter"`
	ChunkID string `json:"chunkId"`
}

// ImageStore persists generated images by content hash; implemented by
// imagestore.Store. Nil is legal — an image delta then errors the run with a
// clear message instead of panicking.
type ImageStore interface {
	Put(data []byte) (string, error)
	Read(hash string) ([]byte, error)
}

// maxInlineImages caps how many of the persona's own prior images ride back
// into provider context as inline bytes (newest first). Each replayed image
// carries its ~1.4MB thought signature (~1.9MB as base64) on top of ~1MB of
// image data, and Gemini's inline request payload tops out around 20 MB —
// 4 keeps comfortable headroom. Older images degrade to a textual
// placeholder instead of hard-failing the call.
const maxInlineImages = 4

// SinkEventKind names the emitted lifecycle events.
type SinkEventKind string

const (
	SinkRunStarted     SinkEventKind = "run_started"
	SinkGroundingReady SinkEventKind = "grounding_ready"
	SinkToken          SinkEventKind = "token"
	SinkToolCall       SinkEventKind = "tool_call"
	SinkToolResult     SinkEventKind = "tool_result"
	SinkRunCompleted   SinkEventKind = "run_completed"
	SinkRunErrored     SinkEventKind = "run_errored"
	SinkRunCancelled   SinkEventKind = "run_cancelled"
	SinkUsage          SinkEventKind = "usage"
	SinkImage          SinkEventKind = "image"
)

type SinkEvent struct {
	Kind    SinkEventKind
	ConvID  string
	RunID   string
	TurnID  string
	Payload map[string]any
}

type EventSink interface {
	Emit(e SinkEvent)
}

// PersonaNamer resolves a persona ID to its display name, so a handoff can
// be attributed without chat importing the persona registry. Nil is legal:
// the literal persona ID is used instead.
type PersonaNamer interface {
	Name(personaID string) (string, bool)
}

type SendParams struct {
	ConversationID string
	UserText       string
	SystemPrompt   string
	Model          string
	PersonaID      string       // recorded on runs; "" for a run with no persona
	Namer          PersonaNamer // resolves persona IDs for handoff attribution; nil → literal IDs
	Provider       provider.ChatProvider
	ProviderName   string // "openai" | "anthropic" — recorded on runs
	// ReasoningEffort is forwarded to provider.ChatRequest verbatim (openai/
	// openai_compat only); "" means the provider default applies.
	ReasoningEffort string
	Registry        *tools.Registry
	Resolver        ScopeResolver
	Retriever       Retriever // may be nil
	RetrievalMode   RetrievalMode
	Sink            EventSink
	Images          ImageStore // image persistence for image-output models; may be nil
	// RemapErr, when set, post-processes a provider error's normalized AppError
	// before it is recorded/emitted (e.g. to upgrade a generic network failure of
	// a local openai_compat model into a friendlier local_unreachable message).
	// nil leaves the AppError unchanged.
	RemapErr func(provider.AppError) provider.AppError
}

type RunResult struct {
	RunID           string
	TerminalReason  string
	TotalUsage      provider.Usage
	TotalToolCalls  int
	TotalIterations int
}

type Service struct {
	st *store.Store
}

func New(st *store.Store) *Service { return &Service{st: st} }

func (s *Service) Send(ctx context.Context, p SendParams, onToken func(string)) (RunResult, error) {
	mode := ResolveRetrievalMode(p.RetrievalMode, os.Getenv)
	if mode == "" {
		mode = RetrievalAutoGroundedDefault
	}
	user, err := s.st.AppendUserMessage(p.ConversationID, p.UserText)
	if err != nil {
		return RunResult{}, fmt.Errorf("persist user_message: %w", err)
	}
	providerName := p.ProviderName
	if providerName == "" {
		providerName = "unknown"
	}
	runID := uuid.NewString()
	if err := s.st.CreateRun(p.ConversationID, user.TurnID, runID,
		providerName, p.Model, string(mode), p.PersonaID); err != nil {
		return RunResult{}, fmt.Errorf("create run: %w", err)
	}
	// Attribution rides on run_started so the bubble is colored the instant it
	// appears — no uncolored flash, no post-hoc recolor.
	emit(p.Sink, SinkRunStarted, p.ConversationID, runID, user.TurnID,
		map[string]any{
			"retrievalMode": string(mode),
			"personaID":     p.PersonaID,
			"modelID":       p.Model,
			"provider":      providerName,
			"grounding": map[string]any{
				"status": initialGroundingStatus(mode, p.Retriever),
			},
		})

	grounding, gErr := s.runPreTurnRetrieval(ctx, p, mode, runID, user.TurnID)
	if gErr != nil {
		_ = s.st.MarkRunErrored(runID, "grounding_error",
			"rag_unavailable", gErr.Error())
		emit(p.Sink, SinkRunErrored, p.ConversationID, runID, user.TurnID,
			map[string]any{"errorCode": "rag_unavailable",
				"errorMessage":   gErr.Error(),
				"terminalReason": "grounding_error"})
		return RunResult{RunID: runID, TerminalReason: "grounding_error"},
			provider.NormalizeError(gErr)
	}

	return s.runLoop(ctx, p, runID, user.TurnID, grounding)
}

func initialGroundingStatus(mode RetrievalMode, r Retriever) string {
	if mode.RequiresPreTurnRAG() && r != nil {
		return "pending"
	}
	return "not_required"
}

// runPreTurnRetrieval runs the pre-turn RAG call if the mode requires it,
// persists grounding metadata to the run, and emits grounding_ready.
func (s *Service) runPreTurnRetrieval(ctx context.Context, p SendParams, mode RetrievalMode, runID, turnID string) (string, error) {
	if !mode.RequiresPreTurnRAG() || p.Retriever == nil {
		return "", nil
	}
	block, _, sources, err := p.Retriever.Retrieve(ctx, p.UserText)
	if err != nil {
		return "", err
	}
	if block == "" {
		meta, _ := json.Marshal(map[string]any{
			"status": "not_available",
			"query":  p.UserText,
		})
		_ = s.st.SetRunGroundingMeta(runID, meta)
		emit(p.Sink, SinkGroundingReady, p.ConversationID, runID, turnID,
			map[string]any{"status": "not_available"})
		return "", nil
	}
	hash := sha256.Sum256([]byte(block))
	meta, _ := json.Marshal(map[string]any{
		"status":         "ready",
		"query":          p.UserText,
		"sources":        sources,
		"injected_chars": len(block),
		"context_hash":   hex.EncodeToString(hash[:]),
	})
	_ = s.st.SetRunGroundingMeta(runID, meta)
	emit(p.Sink, SinkGroundingReady, p.ConversationID, runID, turnID,
		map[string]any{"status": "ready",
			"sourceCount":   len(sources),
			"injectedChars": len(block),
			"contextHash":   hex.EncodeToString(hash[:])})
	return block, nil
}

func emit(s EventSink, k SinkEventKind, convID, runID, turnID string, payload map[string]any) {
	if s == nil {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	s.Emit(SinkEvent{Kind: k, ConvID: convID, RunID: runID, TurnID: turnID, Payload: payload})
}

func (s *Service) runLoop(ctx context.Context, p SendParams, runID, turnID, grounding string) (RunResult, error) {
	maxIter := MaxIterationsDefault
	if v := os.Getenv("STARSHP_MAX_TOOL_ITERATIONS"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			maxIter = n
		}
	}
	var (
		totalUsage     provider.Usage
		lastCall       provider.Usage
		totalToolCalls int
		catalog        []provider.ToolDef
	)
	if p.Registry != nil {
		catalog = p.Registry.Catalog()
	}

	for iter := 1; iter <= maxIter; iter++ {
		events, err := s.st.GetProviderReplayEvents(p.ConversationID, runID)
		if err != nil {
			return s.errorOut(p, runID, turnID, "provider_error",
				"store_error", err.Error()), provider.NormalizeError(err)
		}
		evs := canonicalEvents(events, turnID, p.PersonaID, p.Namer)
		inflateImages(evs, p.Images, maxInlineImages)
		req := provider.ChatRequest{
			Model:           p.Model,
			System:          p.SystemPrompt,
			Grounding:       grounding,
			Tools:           catalog,
			Events:          evs,
			ReasoningEffort: p.ReasoningEffort,
		}
		ch, err := p.Provider.Stream(ctx, req)
		if err != nil {
			return s.errorOut(p, runID, turnID, "provider_error",
				"stream_error", err.Error()), provider.NormalizeError(err)
		}
		var (
			text        strings.Builder
			toolCalls   []*provider.ToolCall
			stopReason  string
			streamErr   error
			persistErr  error
			persistCode string
		)
		// flushText persists the accumulated text segment, if any. Called when
		// an image lands (so the event log preserves text/image interleaving)
		// and once after the stream drains.
		flushText := func() {
			if persistErr != nil {
				return
			}
			t := strings.TrimSpace(text.String())
			text.Reset()
			if t == "" {
				return
			}
			if _, err := s.st.AppendAssistantText(p.ConversationID, turnID, runID, t); err != nil {
				persistErr, persistCode = err, "persist_assistant_text"
			}
		}
		for d := range ch {
			if d.Err != nil {
				streamErr = d.Err
				continue
			}
			if d.Text != "" {
				text.WriteString(d.Text)
				emit(p.Sink, SinkToken, p.ConversationID, runID, turnID,
					map[string]any{"text": d.Text})
			}
			if d.Image != nil && persistErr == nil {
				flushText()
				if persistErr == nil {
					hash, err := putImage(p.Images, d.Image.Data)
					if err != nil {
						persistErr, persistCode = err, "persist_image"
					} else if _, err := s.st.AppendAssistantImage(p.ConversationID, turnID, runID, hash,
						imageEventMetadata(d.Image)); err != nil {
						persistErr, persistCode = err, "persist_image"
					} else {
						emit(p.Sink, SinkImage, p.ConversationID, runID, turnID,
							map[string]any{"hash": hash})
					}
				}
			}
			if d.ToolCall != nil {
				toolCalls = append(toolCalls, d.ToolCall)
			}
			if d.Usage != nil {
				totalUsage.InputTokens += d.Usage.InputTokens
				totalUsage.OutputTokens += d.Usage.OutputTokens
				totalUsage.CachedInputTokens += d.Usage.CachedInputTokens
				lastCall = *d.Usage
			}
			if d.Done && d.StopReason != "" {
				stopReason = d.StopReason
			}
		}
		flushText()
		if persistErr != nil {
			return s.errorOut(p, runID, turnID, "provider_error", persistCode, persistErr.Error()),
				persistErr
		}
		if streamErr != nil {
			return s.handleStreamErr(ctx, p, runID, turnID, streamErr), nil
		}
		if stopReason != "tool_use" {
			return s.completeRunSuccess(p, runID, turnID, stopReason, totalUsage,
				lastCall, totalToolCalls, iter)
		}
		// Dispatch tool calls sequentially in emitted order.
		for _, tc := range toolCalls {
			if _, err := s.st.AppendAssistantToolCall(p.ConversationID, turnID, runID,
				tc.ID, tc.Name, tc.Input, tc.Metadata); err != nil {
				return s.errorOut(p, runID, turnID, "provider_error",
					"persist_tool_call", err.Error()), err
			}
			emit(p.Sink, SinkToolCall, p.ConversationID, runID, turnID,
				map[string]any{"toolCallId": tc.ID, "name": tc.Name,
					"input": json.RawMessage(tc.Input)})
			execCtx := tools.ExecContext{
				ConversationID: p.ConversationID,
				TurnID:         turnID,
				RunID:          runID,
				RetrievalMode:  string(p.RetrievalMode),
				TextbookScope:  bookNamesFromResolver(ctx, p),
			}
			result, isErr, latency, execErr := p.Registry.Execute(ctx, execCtx, tc.Name, tc.Input)
			if execErr != nil {
				// Underlying ctx cancellation surfaced through Execute.
				return s.handleStreamErr(ctx, p, runID, turnID, execErr), nil
			}
			ev, err := s.st.AppendToolResult(p.ConversationID, turnID, runID,
				tc.ID, tc.Name, result.Output, result.Metadata, isErr, latency.Milliseconds())
			if err != nil {
				return s.errorOut(p, runID, turnID, "provider_error",
					"persist_tool_result", err.Error()), err
			}
			totalToolCalls++
			errCode := ""
			if isErr {
				errCode = errorCodeFromMetadata(result.Metadata)
			}
			emit(p.Sink, SinkToolResult, p.ConversationID, runID, turnID,
				map[string]any{"toolCallId": tc.ID, "name": tc.Name,
					"isError":   isErr,
					"errorCode": errCode,
					"latencyMs": ev.ToolLatencyMs,
					"summary":   summarize(result.Output, 200)})
		}
	}
	// Iteration budget exhausted. Rather than discard the work, give the model
	// one final turn with tools withheld so it must synthesize an answer from
	// the tool results already gathered, then complete the run normally.
	return s.finalizeWithoutTools(ctx, p, runID, turnID, grounding, totalUsage, totalToolCalls, maxIter)
}

// finalizeWithoutTools runs one last provider turn with the tool catalog
// withheld and a directive to answer now, so a run that hit the iteration cap
// still produces a final answer from the gathered context instead of erroring.
// The run completes with terminal_reason=max_iterations for observability.
func (s *Service) finalizeWithoutTools(ctx context.Context, p SendParams, runID, turnID, grounding string,
	totalUsage provider.Usage, totalToolCalls, maxIter int) (RunResult, error) {
	events, err := s.st.GetProviderReplayEvents(p.ConversationID, runID)
	if err != nil {
		return s.errorOut(p, runID, turnID, "provider_error", "store_error", err.Error()),
			provider.NormalizeError(err)
	}
	system := strings.TrimSpace(p.SystemPrompt + "\n\n" +
		"You have reached the tool-use limit for this turn. Do not request any more tools. " +
		"Give your best, complete final answer now using the information already gathered.")
	evs := canonicalEvents(events, turnID, p.PersonaID, p.Namer)
	inflateImages(evs, p.Images, maxInlineImages)
	req := provider.ChatRequest{
		Model:           p.Model,
		System:          system,
		Grounding:       grounding,
		Tools:           nil, // withheld: force a tool-free answer
		Events:          evs,
		ReasoningEffort: p.ReasoningEffort,
	}
	ch, err := p.Provider.Stream(ctx, req)
	if err != nil {
		return s.errorOut(p, runID, turnID, "provider_error", "stream_error", err.Error()),
			provider.NormalizeError(err)
	}
	var (
		text      strings.Builder
		lastCall  provider.Usage
		streamErr error
	)
	for d := range ch {
		if d.Err != nil {
			streamErr = d.Err
			continue
		}
		if d.Text != "" {
			text.WriteString(d.Text)
			emit(p.Sink, SinkToken, p.ConversationID, runID, turnID,
				map[string]any{"text": d.Text})
		}
		if d.Usage != nil {
			totalUsage.InputTokens += d.Usage.InputTokens
			totalUsage.OutputTokens += d.Usage.OutputTokens
			totalUsage.CachedInputTokens += d.Usage.CachedInputTokens
			lastCall = *d.Usage
		}
		// Any tool call in this turn is ignored — tools were withheld.
		// Image deltas are likewise unreachable here: image mode omits tools, so a run can
		// never reach the iteration cap with an image-capable model.
	}
	if t := strings.TrimSpace(text.String()); t != "" {
		if _, err := s.st.AppendAssistantText(p.ConversationID, turnID, runID, t); err != nil {
			return s.errorOut(p, runID, turnID, "provider_error", "persist_assistant_text", err.Error()), err
		}
	}
	if streamErr != nil {
		return s.handleStreamErr(ctx, p, runID, turnID, streamErr), nil
	}
	return s.completeRunSuccess(p, runID, turnID, "max_iterations", totalUsage, lastCall, totalToolCalls, maxIter+1)
}

func (s *Service) completeRunSuccess(p SendParams, runID, turnID, stopReason string,
	totalUsage, lastCall provider.Usage, totalToolCalls, iter int) (RunResult, error) {
	if stopReason == "" {
		stopReason = "end_turn"
	}
	err := s.st.CompleteRun(runID, store.RunTotals{
		InputTokens:       int64(totalUsage.InputTokens),
		OutputTokens:      int64(totalUsage.OutputTokens),
		CachedInputTokens: int64(totalUsage.CachedInputTokens),
		ToolCalls:         int64(totalToolCalls),
		Iterations:        int64(iter),
	}, stopReason)
	if err != nil {
		// Concurrent cancel/error already landed — surface and skip events.
		return RunResult{RunID: runID, TerminalReason: stopReason}, err
	}
	emit(p.Sink, SinkRunCompleted, p.ConversationID, runID, turnID,
		map[string]any{"terminalReason": stopReason,
			"totalToolCalls":  totalToolCalls,
			"totalIterations": iter})
	emit(p.Sink, SinkUsage, p.ConversationID, runID, turnID,
		map[string]any{"input": totalUsage.InputTokens,
			"output":     totalUsage.OutputTokens,
			"cached":     totalUsage.CachedInputTokens,
			"lastInput":  lastCall.InputTokens,
			"lastOutput": lastCall.OutputTokens,
			"modelID":    p.Model}) // frontend footer resolves max_context by modelID
	return RunResult{RunID: runID, TerminalReason: stopReason,
		TotalUsage: totalUsage, TotalToolCalls: totalToolCalls,
		TotalIterations: iter}, nil
}

// handleStreamErr discriminates cancellation from a provider-side error.
// Either path runs after any accumulated partial assistant text has already
// been persisted by the caller, so audit/display see what the model emitted.
func (s *Service) handleStreamErr(ctx context.Context, p SendParams, runID, turnID string, sErr error) RunResult {
	if ctx.Err() != nil || errors.Is(sErr, context.Canceled) {
		_ = s.st.MarkRunCancelled(runID, "user_cancelled")
		emit(p.Sink, SinkRunCancelled, p.ConversationID, runID, turnID,
			map[string]any{"terminalReason": "user_cancelled"})
		return RunResult{RunID: runID, TerminalReason: "user_cancelled"}
	}
	ae := provider.NormalizeError(sErr)
	if p.RemapErr != nil {
		ae = p.RemapErr(ae)
	}
	_ = s.st.MarkRunErrored(runID, "provider_error", ae.Code, ae.UserMessage)
	emit(p.Sink, SinkRunErrored, p.ConversationID, runID, turnID,
		map[string]any{"errorCode": ae.Code, "errorMessage": ae.UserMessage,
			"terminalReason": "provider_error"})
	return RunResult{RunID: runID, TerminalReason: "provider_error"}
}

func (s *Service) errorOut(p SendParams, runID, turnID, reason, code, msg string) RunResult {
	_ = s.st.MarkRunErrored(runID, reason, code, msg)
	emit(p.Sink, SinkRunErrored, p.ConversationID, runID, turnID,
		map[string]any{"errorCode": code, "errorMessage": msg,
			"terminalReason": reason})
	return RunResult{RunID: runID, TerminalReason: reason}
}

// canonicalEvents builds the provider payload for the persona speaking now
// (currentPersonaID, answering currentTurnID). Own-persona and pre-persona
// rows pass through the six-field whitelist verbatim — a persona keeps its
// own voice, tool blocks included. The immediately preceding foreign turn's
// final text folds into one attributed user-role block; a foreign turn the
// operator pinned `always` gets the same treatment at any distance, folded
// in place. Tool blocks of foreign turns are always dropped — their
// provider-specific IDs would dangle in another persona's transcript and the
// receiving persona may not even have the tool in its registry. Other
// foreign turns are omitted entirely. A turn marked `never` contributes
// nothing — the store already filters it from replay; it is skipped here
// defensively too, except the current turn, which an override never governs
// (a rerun of a never turn still gets its own prompt). The operator's other
// user_message rows are always included, in order. rows arrive ordered by
// sequence_index.
func canonicalEvents(rows []store.ConversationEvent, currentTurnID, currentPersonaID string, namer PersonaNamer) []provider.Event {
	predecessor := predecessorTurnID(rows, currentTurnID)
	// turnPrompts lets a foreign image textualize as the prompt that produced
	// it: the image turn's own user_message.
	turnPrompts := map[string]string{}
	for _, r := range rows {
		if r.Kind == store.EventKindUserMessage {
			turnPrompts[r.TurnID] = r.Text
		}
	}
	out := make([]provider.Event, 0, len(rows))
	attributed := func(personaID, model string, texts []string) provider.Event {
		name := personaID
		if namer != nil {
			if n, ok := namer.Name(personaID); ok {
				name = n
			}
		}
		return provider.Event{
			Kind: store.EventKindUserMessage,
			Text: "From " + name + " (" + model + "):\n" + strings.Join(texts, "\n\n"),
		}
	}
	var batonTexts []string
	var batonPersona, batonModel string
	flushBaton := func() {
		if len(batonTexts) == 0 {
			return
		}
		out = append(out, attributed(batonPersona, batonModel, batonTexts))
		batonTexts = nil
	}
	// A pinned (`always`) foreign turn folds exactly like the baton, but in
	// place: accumulated while its rows pass, flushed when they end. The
	// predecessor is handled by the baton case alone (matched first below),
	// so a pin on the predecessor cannot double-include it.
	var pinTexts []string
	var pinTurn, pinPersona, pinModel string
	flushPin := func() {
		if len(pinTexts) == 0 {
			return
		}
		out = append(out, attributed(pinPersona, pinModel, pinTexts))
		pinTexts = nil
	}
	for _, r := range rows {
		if r.TurnID != pinTurn {
			flushPin()
		}
		// The baton lands immediately before the current turn's rows, i.e.
		// right after the predecessor turn it summarizes.
		if r.TurnID == currentTurnID {
			flushBaton()
		}
		if r.ContextOverride == store.OverrideNever && r.TurnID != currentTurnID {
			continue
		}
		foreign := r.PersonaID != "" && r.PersonaID != currentPersonaID
		switch {
		case r.Kind == store.EventKindUserMessage || !foreign:
			ev := provider.Event{
				Kind: r.Kind, Text: r.Text,
				ToolCallID: r.ToolCallID, ToolName: r.ToolName,
				ToolInput: r.ToolInput, IsError: r.IsError,
				ImageHash: r.ImageHash,
			}
			if r.Kind == store.EventKindAssistantToolCall || r.Kind == store.EventKindAssistantImage {
				ev.ToolMetadata = r.ToolMetadata
			}
			out = append(out, ev)
		case r.TurnID == predecessor &&
			(r.Kind == store.EventKindAssistantText || r.Kind == store.EventKindAssistantImage):
			if len(batonTexts) == 0 {
				batonPersona, batonModel = r.PersonaID, r.Model
			}
			batonTexts = append(batonTexts, batonLine(r, turnPrompts))
		case r.ContextOverride == store.OverrideAlways &&
			(r.Kind == store.EventKindAssistantText || r.Kind == store.EventKindAssistantImage):
			if len(pinTexts) == 0 {
				pinTurn, pinPersona, pinModel = r.TurnID, r.PersonaID, r.Model
			}
			pinTexts = append(pinTexts, batonLine(r, turnPrompts))
		}
		// Any other foreign row (older unpinned turn, or any foreign tool
		// block) is omitted.
	}
	// Pins flush before the trailing baton. For a rerun whose events were
	// appended out of sequence, a pinned block can land later than the turn's
	// original position — content, not position, is the guarantee there.
	flushPin()
	flushBaton()
	return out
}

// inflateImages loads the newest max assistant_image events' bytes so the
// provider replays them inline (iterative refinement edits the actual image).
// Walking newest-first, an unreadable file (deleted) is skipped without
// consuming a cap slot; anything not inflated keeps empty ImageData and the
// adapter renders "[earlier image omitted]" instead. Only the current
// persona's own images reach this point — canonicalEvents already textualized
// foreign ones.
func inflateImages(events []provider.Event, images ImageStore, max int) {
	if images == nil {
		return
	}
	inflated := 0
	for i := len(events) - 1; i >= 0 && inflated < max; i-- {
		if events[i].Kind != store.EventKindAssistantImage || events[i].ImageHash == "" {
			continue
		}
		data, err := images.Read(events[i].ImageHash)
		if err != nil {
			continue
		}
		events[i].ImageData = data
		inflated++
	}
}

// predecessorTurnID returns the turn immediately before currentTurnID, in
// user-message order. User messages are appended chronologically and are
// unique per turn, so they define turn order even if a turn's run events
// were appended out of sequence (a rerun). "" means no predecessor.
func predecessorTurnID(rows []store.ConversationEvent, currentTurnID string) string {
	prev := ""
	for _, r := range rows {
		if r.Kind != store.EventKindUserMessage {
			continue
		}
		if r.TurnID == currentTurnID {
			return prev
		}
		prev = r.TurnID
	}
	return ""
}

// batonLine renders one foreign event inside an attributed block: text rides
// verbatim; an image becomes a textual description carrying the prompt that
// produced it. Raw image bytes never cross a persona boundary.
func batonLine(r store.ConversationEvent, turnPrompts map[string]string) string {
	if r.Kind != store.EventKindAssistantImage {
		return r.Text
	}
	return `[image — generated from: "` + truncateRunes(turnPrompts[r.TurnID], 120) + `"]`
}

// truncateRunes shortens s to at most n runes with an ellipsis, never
// splitting a multibyte character (summarize is byte-based; prompts are
// operator text).
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func bookNamesFromResolver(ctx context.Context, p SendParams) []string {
	if p.Resolver == nil {
		return nil
	}
	entries, err := p.Resolver.Resolve(ctx, p.ConversationID)
	if err != nil {
		return nil
	}
	return BookNames(entries)
}

// putImage stores one generated image, guarding the nil-store case (an image
// model was invoked through a path that never wired an ImageStore).
func putImage(images ImageStore, data []byte) (string, error) {
	if images == nil {
		return "", errors.New("image store unavailable: cannot persist generated image")
	}
	return images.Put(data)
}

// imageEventMetadata packages the provider-opaque per-image payload persisted
// to tool_metadata: the real mime type and, when present, Gemini's thought
// signature (base64) — echoed verbatim on replay for multi-turn editing.
func imageEventMetadata(img *provider.ImageBlob) json.RawMessage {
	m := map[string]string{}
	if img.MIME != "" {
		m["mime"] = img.MIME
	}
	if len(img.ThoughtSignature) > 0 {
		m["thought_signature"] = base64.StdEncoding.EncodeToString(img.ThoughtSignature)
	}
	if len(m) == 0 {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}

func errorCodeFromMetadata(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m struct {
		Code string `json:"error_code"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.Code
}

func summarize(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
