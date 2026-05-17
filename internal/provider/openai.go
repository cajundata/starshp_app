package provider

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type openAIProvider struct {
	client openai.Client
}

// NewOpenAI builds an OpenAI provider. baseURL may be empty for the default
// endpoint (tests pass an httptest URL).
func NewOpenAI(apiKey, baseURL string) ChatProvider {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &openAIProvider{client: openai.NewClient(opts...)}
}

func (p *openAIProvider) Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error) {
	msgs := []openai.ChatCompletionMessageParamUnion{}
	if req.CachedPrefix != "" {
		msgs = append(msgs, openai.SystemMessage(req.CachedPrefix))
	}
	for _, m := range req.Messages {
		if m.Role == "assistant" {
			msgs = append(msgs, openai.AssistantMessage(m.Content))
		} else {
			msgs = append(msgs, openai.UserMessage(m.Content))
		}
	}
	stream := p.client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: msgs,
	})
	out := make(chan Delta)
	go func() {
		defer close(out)
		defer stream.Close()
		for stream.Next() {
			chunk := stream.Current()
			if len(chunk.Choices) > 0 {
				if txt := chunk.Choices[0].Delta.Content; txt != "" {
					select {
					case out <- Delta{Text: txt}:
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
