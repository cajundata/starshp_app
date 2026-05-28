package provider

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
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
		// Without this, OpenAI omits the usage block from streaming responses.
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true),
		},
	})
	out := make(chan Delta)
	go func() {
		defer close(out)
		defer stream.Close()
		var (
			usage   Usage
			haveAny bool
		)
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
			// Final chunk: choices is empty, usage is populated (when IncludeUsage was set).
			if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
				usage.InputTokens = int(chunk.Usage.PromptTokens)
				usage.OutputTokens = int(chunk.Usage.CompletionTokens)
				usage.CachedInputTokens = int(chunk.Usage.PromptTokensDetails.CachedTokens)
				haveAny = true
			}
		}
		final := Delta{Done: true}
		if haveAny {
			u := usage
			final.Usage = &u
		}
		if err := stream.Err(); err != nil {
			final.Err = err
			final.Usage = nil
		}
		out <- final
	}()
	return out, nil
}
