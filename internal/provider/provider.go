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

type Delta struct {
	Text string
	Done bool
	Err  error
}

type ChatProvider interface {
	Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error)
}
