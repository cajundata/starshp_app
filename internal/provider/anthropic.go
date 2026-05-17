package provider

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type anthropicProvider struct {
	client anthropic.Client
}

// NewAnthropic builds an Anthropic provider. baseURL may be empty for the
// default endpoint (tests pass an httptest URL).
func NewAnthropic(apiKey, baseURL string) ChatProvider {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &anthropicProvider{client: anthropic.NewClient(opts...)}
}

func (p *anthropicProvider) Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error) {
	msgs := make([]anthropic.MessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		block := anthropic.NewTextBlock(m.Content)
		if m.Role == "assistant" {
			msgs = append(msgs, anthropic.NewAssistantMessage(block))
		} else {
			msgs = append(msgs, anthropic.NewUserMessage(block))
		}
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: 4096,
		Messages:  msgs,
	}
	if req.CachedPrefix != "" {
		params.System = []anthropic.TextBlockParam{{
			Text:         req.CachedPrefix,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}}
	}
	stream := p.client.Messages.NewStreaming(ctx, params)
	out := make(chan Delta)
	go func() {
		defer close(out)
		defer stream.Close() //nolint:errcheck
		for stream.Next() {
			event := stream.Current()
			if d, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
				if td, ok := d.Delta.AsAny().(anthropic.TextDelta); ok && td.Text != "" {
					select {
					case out <- Delta{Text: td.Text}:
					case <-ctx.Done():
						return
					}
				}
			}
		}
		if err := stream.Err(); err != nil {
			out <- Delta{Err: err, Done: true}
			return
		}
		out <- Delta{Done: true}
	}()
	return out, nil
}
