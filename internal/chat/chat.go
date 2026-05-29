// Package chat orchestrates the agentic loop: persist user_message ->
// pre-turn retrieve (if mode requires) -> create run -> loop(stream ->
// tool_use -> execute -> tool_result) -> transactional completion.
package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/cajundata/starshp_app/internal/tools"
	"github.com/google/uuid"
)

// MaxIterationsDefault caps the agentic loop. STARSHP_MAX_TOOL_ITERATIONS
// overrides it. Empirically sufficient for the multi-hop tax problems we
// care about; tune as Phase 2+ tools land.
const MaxIterationsDefault = 8

// Retriever is the pre-turn RAG seam. nil means no pre-turn retrieval.
type Retriever interface {
	Retrieve(ctx context.Context, query string) (block string, sourcesJSON string, sources []RetrievedSource, err error)
}

type RetrievedSource struct {
	Book    string `json:"book"`
	Chapter int    `json:"chapter"`
	ChunkID string `json:"chunkId"`
}

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

type SendParams struct {
	ConversationID string
	UserText       string
	SystemPrompt   string
	Model          string
	Provider       provider.ChatProvider
	ProviderName   string // "openai" | "anthropic" — recorded on runs
	Registry       *tools.Registry
	Resolver       ScopeResolver
	Retriever      Retriever // may be nil
	RetrievalMode  RetrievalMode
	Sink           EventSink
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
		providerName, p.Model, string(mode)); err != nil {
		return RunResult{}, fmt.Errorf("create run: %w", err)
	}
	emit(p.Sink, SinkRunStarted, p.ConversationID, runID, user.TurnID,
		map[string]any{"retrievalMode": string(mode),
			"grounding": map[string]any{"status": initialGroundingStatus(mode, p.Retriever)}})

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
		req := provider.ChatRequest{
			Model:     p.Model,
			System:    p.SystemPrompt,
			Grounding: grounding,
			Tools:     catalog,
			Events:    canonicalEvents(events),
		}
		ch, err := p.Provider.Stream(ctx, req)
		if err != nil {
			return s.errorOut(p, runID, turnID, "provider_error",
				"stream_error", err.Error()), provider.NormalizeError(err)
		}
		var (
			text       strings.Builder
			toolCalls  []*provider.ToolCall
			stopReason string
			streamErr  error
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
			if d.ToolCall != nil {
				toolCalls = append(toolCalls, d.ToolCall)
			}
			if d.Usage != nil {
				totalUsage.InputTokens += d.Usage.InputTokens
				totalUsage.OutputTokens += d.Usage.OutputTokens
				totalUsage.CachedInputTokens += d.Usage.CachedInputTokens
			}
			if d.Done && d.StopReason != "" {
				stopReason = d.StopReason
			}
		}
		if t := strings.TrimSpace(text.String()); t != "" {
			if _, err := s.st.AppendAssistantText(p.ConversationID, turnID, runID, t); err != nil {
				return s.errorOut(p, runID, turnID, "provider_error", "persist_assistant_text", err.Error()),
					err
			}
		}
		if streamErr != nil {
			return s.handleStreamErr(ctx, p, runID, turnID, streamErr), nil
		}
		if stopReason != "tool_use" {
			return s.completeRunSuccess(p, runID, turnID, stopReason, totalUsage,
				totalToolCalls, iter)
		}
		// Dispatch tool calls sequentially in emitted order.
		for _, tc := range toolCalls {
			if _, err := s.st.AppendAssistantToolCall(p.ConversationID, turnID, runID,
				tc.ID, tc.Name, tc.Input); err != nil {
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
	// Loop exhausted iterations.
	_ = s.st.MarkRunErrored(runID, "max_iterations", "max_iterations",
		fmt.Sprintf("hit %d iteration cap", maxIter))
	emit(p.Sink, SinkRunErrored, p.ConversationID, runID, turnID,
		map[string]any{"errorCode": "max_iterations",
			"errorMessage":   fmt.Sprintf("hit %d iteration cap", maxIter),
			"terminalReason": "max_iterations"})
	return RunResult{RunID: runID, TerminalReason: "max_iterations",
			TotalUsage: totalUsage, TotalToolCalls: totalToolCalls,
			TotalIterations: maxIter},
		fmt.Errorf("max_iterations: tool-use loop exceeded cap of %d", maxIter)
}

func (s *Service) completeRunSuccess(p SendParams, runID, turnID, stopReason string,
	totalUsage provider.Usage, totalToolCalls, iter int) (RunResult, error) {
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
			"output": totalUsage.OutputTokens,
			"cached": totalUsage.CachedInputTokens})
	return RunResult{RunID: runID, TerminalReason: stopReason,
		TotalUsage: totalUsage, TotalToolCalls: totalToolCalls,
		TotalIterations: iter}, nil
}

// handleStreamErr discriminates cancellation from provider error. Cancellation
// support lands in the next task; for now mid-stream errors surface as
// provider_error.
func (s *Service) handleStreamErr(ctx context.Context, p SendParams, runID, turnID string, sErr error) RunResult {
	_ = ctx
	return s.errorOut(p, runID, turnID, "provider_error", "stream_error", sErr.Error())
}

func (s *Service) errorOut(p SendParams, runID, turnID, reason, code, msg string) RunResult {
	_ = s.st.MarkRunErrored(runID, reason, code, msg)
	emit(p.Sink, SinkRunErrored, p.ConversationID, runID, turnID,
		map[string]any{"errorCode": code, "errorMessage": msg,
			"terminalReason": reason})
	return RunResult{RunID: runID, TerminalReason: reason}
}

func canonicalEvents(rows []store.ConversationEvent) []provider.Event {
	out := make([]provider.Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, provider.Event{
			Kind: r.Kind, Text: r.Text,
			ToolCallID: r.ToolCallID, ToolName: r.ToolName,
			ToolInput: r.ToolInput, IsError: r.IsError,
		})
	}
	return out
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
