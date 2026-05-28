// Package provider defines the generic streaming chat abstraction and its
// OpenAI and Anthropic implementations.
package provider

import "context"

type Message struct {
	Role    string // "user" | "assistant"
	Content string
}

type ChatRequest struct {
	Model        string
	CachedPrefix string // system prompt + textbook context (cacheable)
	Messages     []Message
}

// Usage carries token counts surfaced by a provider at end-of-stream.
// CachedInputTokens is the subset of InputTokens served from prompt cache.
type Usage struct {
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
}

type Delta struct {
	Text  string
	Done  bool
	Err   error
	Usage *Usage // non-nil only on the terminal Done frame, when the SDK surfaced usage
}

type ChatProvider interface {
	Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error)
}
