// Package provider defines the generic streaming chat abstraction and its
// OpenAI and Anthropic implementations.
package provider

import (
	"context"
	"encoding/json"
)

// Message is the legacy single-text message shape kept for the transition
// window. Use Event for new code; adapters fall back to Messages when Events
// is empty.
type Message struct {
	Role    string // "user" | "assistant"
	Content string
}

// Event is the canonical conversation timeline element used by the agentic
// loop. Adapters translate a slice of Events into provider-specific wire
// format (role-based for OpenAI, content-block for Anthropic).
type Event struct {
	Kind       string          // user_message | assistant_text | assistant_tool_call | tool_result
	Text       string          // user_message, assistant_text, tool_result.output
	ToolCallID string          // assistant_tool_call, tool_result
	ToolName   string          // assistant_tool_call, tool_result
	ToolInput  json.RawMessage // assistant_tool_call: provider input JSON
	IsError    bool            // tool_result
}

// ToolDef is the provider-facing description of a registered tool.
type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage // JSON Schema
}

// ChatRequest carries one provider call.
//
// System + Grounding + Tools form the stable cacheable prefix when the
// provider supports prompt caching. Events is the canonical replay timeline
// returned by store.GetProviderReplayEvents.
//
// CachedPrefix and Messages are retained for the transition window — adapters
// use Events when len(Events) > 0, otherwise fall back to CachedPrefix +
// Messages so the legacy code path keeps working.
type ChatRequest struct {
	Model        string
	System       string    // bare system prompt (cacheable)
	Grounding    string    // pre-turn RAG block with metadata header (cacheable)
	Tools        []ToolDef // tool catalog (cacheable when stable)
	Events       []Event   // canonical timeline; preferred when non-empty
	CachedPrefix string    // LEGACY: system prompt + textbook context
	Messages     []Message // LEGACY: text-only message history
}

// Usage carries token counts surfaced by a provider at end-of-stream.
// CachedInputTokens is the subset of InputTokens served from prompt cache.
type Usage struct {
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
}

// ToolCall is emitted on a Delta once the provider's streaming tool-call
// input JSON is fully buffered. Schema validation happens in
// registry.Execute, not in the adapter.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// Delta is one frame of a streaming response.
//
// StopReason is populated only on the terminal Done frame:
//
//	end_turn | tool_use | max_tokens | error
type Delta struct {
	Text       string
	ToolCall   *ToolCall
	StopReason string
	Done       bool
	Err        error
	Usage      *Usage
}

type ChatProvider interface {
	Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error)
}
