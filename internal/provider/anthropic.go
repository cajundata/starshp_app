package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

// partialToolUse accumulates a streaming tool_use block's input JSON across
// input_json_delta events, keyed by content-block index.
type partialToolUse struct {
	ID, Name  string
	InputJSON string
}

func (p *anthropicProvider) Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error) {
	var msgs []anthropic.MessageParam
	if len(req.Events) > 0 {
		msgs = anthropicMessagesFromEvents(req.Events)
	} else {
		msgs = make([]anthropic.MessageParam, 0, len(req.Messages))
		for _, m := range req.Messages {
			block := anthropic.NewTextBlock(m.Content)
			if m.Role == "assistant" {
				msgs = append(msgs, anthropic.NewAssistantMessage(block))
			} else {
				msgs = append(msgs, anthropic.NewUserMessage(block))
			}
		}
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: 4096,
		Messages:  msgs,
	}
	if sys := buildAnthropicSystemBlocks(req.System, req.CachedPrefix, req.Grounding); len(sys) > 0 {
		params.System = sys
	}
	if tools := buildAnthropicTools(req.Tools); len(tools) > 0 {
		params.Tools = tools
	}
	stream := p.client.Messages.NewStreaming(ctx, params)
	out := make(chan Delta)
	go func() {
		defer close(out)
		defer stream.Close() //nolint:errcheck
		var (
			usage      Usage
			haveAny    bool
			stopReason string
			toolBuf    = map[int64]*partialToolUse{}
		)
		for stream.Next() {
			event := stream.Current()
			switch e := event.AsAny().(type) {
			case anthropic.MessageStartEvent:
				usage.InputTokens = int(e.Message.Usage.InputTokens)
				usage.CachedInputTokens = int(e.Message.Usage.CacheReadInputTokens)
				haveAny = true
			case anthropic.MessageDeltaEvent:
				// OutputTokens is cumulative across deltas; input/cached fields may be
				// omitted by the SDK (decoded as zero) — keep the MessageStart value
				// in that case so we don't overwrite a real number with a zero.
				if e.Usage.InputTokens > 0 {
					usage.InputTokens = int(e.Usage.InputTokens)
				}
				if e.Usage.CacheReadInputTokens > 0 {
					usage.CachedInputTokens = int(e.Usage.CacheReadInputTokens)
				}
				usage.OutputTokens = int(e.Usage.OutputTokens)
				if e.Delta.StopReason != "" {
					stopReason = string(e.Delta.StopReason)
				}
				haveAny = true
			case anthropic.ContentBlockStartEvent:
				if e.ContentBlock.Type == "tool_use" {
					toolBuf[e.Index] = &partialToolUse{
						ID:   e.ContentBlock.ID,
						Name: e.ContentBlock.Name,
					}
				}
			case anthropic.ContentBlockDeltaEvent:
				switch d := e.Delta.AsAny().(type) {
				case anthropic.TextDelta:
					if d.Text != "" {
						select {
						case out <- Delta{Text: d.Text}:
						case <-ctx.Done():
							return
						}
					}
				case anthropic.InputJSONDelta:
					if buf, ok := toolBuf[e.Index]; ok {
						buf.InputJSON += d.PartialJSON
					}
				}
			case anthropic.ContentBlockStopEvent:
				if buf, ok := toolBuf[e.Index]; ok {
					raw := strings.TrimSpace(buf.InputJSON)
					if raw == "" {
						raw = "{}"
					}
					input := json.RawMessage(raw)
					if !json.Valid(input) {
						out <- Delta{Done: true, Err: fmt.Errorf("anthropic: tool_use input JSON invalid for call %s", buf.ID)}
						return
					}
					select {
					case out <- Delta{ToolCall: &ToolCall{ID: buf.ID, Name: buf.Name, Input: input}}:
					case <-ctx.Done():
						return
					}
					delete(toolBuf, e.Index)
				}
			}
		}
		final := Delta{Done: true, StopReason: stopReason}
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

// anthropicMessagesFromEvents assembles content-block messages from the
// canonical Event timeline. Consecutive assistant_text + assistant_tool_call
// events collapse into one assistant message; tool_result events collapse into
// one user message.
func anthropicMessagesFromEvents(events []Event) []anthropic.MessageParam {
	var out []anthropic.MessageParam
	var assistantBlocks []anthropic.ContentBlockParamUnion
	flushAssistant := func() {
		if len(assistantBlocks) > 0 {
			out = append(out, anthropic.NewAssistantMessage(assistantBlocks...))
			assistantBlocks = nil
		}
	}
	var pendingToolResults []anthropic.ContentBlockParamUnion
	flushToolResults := func() {
		if len(pendingToolResults) > 0 {
			out = append(out, anthropic.NewUserMessage(pendingToolResults...))
			pendingToolResults = nil
		}
	}
	for _, e := range events {
		switch e.Kind {
		case "user_message":
			flushAssistant()
			flushToolResults()
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(e.Text)))
		case "assistant_text":
			flushToolResults()
			assistantBlocks = append(assistantBlocks, anthropic.NewTextBlock(e.Text))
		case "assistant_tool_call":
			flushToolResults()
			input := json.RawMessage(e.ToolInput)
			if len(strings.TrimSpace(string(input))) == 0 {
				input = json.RawMessage("{}")
			}
			assistantBlocks = append(assistantBlocks,
				anthropic.NewToolUseBlock(e.ToolCallID, input, e.ToolName))
		case "tool_result":
			flushAssistant()
			pendingToolResults = append(pendingToolResults,
				anthropic.NewToolResultBlock(e.ToolCallID, e.Text, e.IsError))
		}
	}
	flushAssistant()
	flushToolResults()
	return out
}

func buildAnthropicSystemBlocks(system, cachedPrefix, grounding string) []anthropic.TextBlockParam {
	var blocks []anthropic.TextBlockParam
	sys := system
	if sys == "" {
		sys = cachedPrefix
	}
	if sys != "" {
		blocks = append(blocks, anthropic.TextBlockParam{
			Text:         sys,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		})
	}
	if grounding != "" {
		blocks = append(blocks, anthropic.TextBlockParam{
			Text:         grounding,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		})
	}
	return blocks
}

func buildAnthropicTools(tools []ToolDef) []anthropic.ToolUnionParam {
	if len(tools) == 0 {
		return nil
	}
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for i, t := range tools {
		var parsed struct {
			Properties any      `json:"properties"`
			Required   []string `json:"required"`
		}
		_ = json.Unmarshal(t.InputSchema, &parsed)
		td := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: parsed.Properties,
				Required:   parsed.Required,
			},
		}
		// cache_control on the LAST tool marks the end of the stable prefix.
		if i == len(tools)-1 {
			td.CacheControl = anthropic.NewCacheControlEphemeralParam()
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &td})
	}
	return out
}
