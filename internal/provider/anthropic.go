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
		var (
			usage   Usage
			haveAny bool
		)
		for stream.Next() {
			event := stream.Current()
			switch e := event.AsAny().(type) {
			case anthropic.MessageStartEvent:
				usage.InputTokens = int(e.Message.Usage.InputTokens)
				usage.CachedInputTokens = int(e.Message.Usage.CacheReadInputTokens)
				haveAny = true
			case anthropic.MessageDeltaEvent:
				// MessageDeltaUsage fields are cumulative; the final one is the truth.
				if e.Usage.InputTokens > 0 {
					usage.InputTokens = int(e.Usage.InputTokens)
				}
				if e.Usage.CacheReadInputTokens > 0 {
					usage.CachedInputTokens = int(e.Usage.CacheReadInputTokens)
				}
				usage.OutputTokens = int(e.Usage.OutputTokens)
				haveAny = true
			case anthropic.ContentBlockDeltaEvent:
				if td, ok := e.Delta.AsAny().(anthropic.TextDelta); ok && td.Text != "" {
					select {
					case out <- Delta{Text: td.Text}:
					case <-ctx.Done():
						return
					}
				}
			}
		}
		final := Delta{Done: true}
		if haveAny {
			u := usage
			final.Usage = &u
		}
		if err := stream.Err(); err != nil {
			final.Err = err
			final.Usage = nil // errors mean no clean usage
		}
		out <- final
	}()
	return out, nil
}
