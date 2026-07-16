package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
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

// partialOpenAIToolCall accumulates a streaming tool call across delta chunks,
// keyed by the tool_calls[index] position.
type partialOpenAIToolCall struct {
	ID        string
	Name      string
	Arguments string
}

func (p *openAIProvider) Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error) {
	var msgs []openai.ChatCompletionMessageParamUnion
	if len(req.Events) > 0 {
		msgs = openaiMessagesFromEvents(req.System, req.Grounding, req.Events)
	} else {
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
	}
	params := openai.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: msgs,
		// Without this, OpenAI omits the usage block from streaming responses.
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true),
		},
	}
	if tools := openaiToolsFromDefs(req.Tools); len(tools) > 0 {
		params.Tools = tools
	}
	if req.ReasoningEffort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(req.ReasoningEffort)
	}
	stream := p.client.Chat.Completions.NewStreaming(ctx, params)
	out := make(chan Delta)
	go func() {
		defer close(out)
		defer stream.Close()
		var (
			usage      Usage
			haveAny    bool
			stopReason string
			toolBuf    = map[int]*partialOpenAIToolCall{}
		)
		for stream.Next() {
			chunk := stream.Current()
			for _, choice := range chunk.Choices {
				if txt := choice.Delta.Content; txt != "" {
					select {
					case out <- Delta{Text: txt}:
					case <-ctx.Done():
						return
					}
				}
				for _, tc := range choice.Delta.ToolCalls {
					idx := int(tc.Index)
					buf, ok := toolBuf[idx]
					if !ok {
						buf = &partialOpenAIToolCall{}
						toolBuf[idx] = buf
					}
					if tc.ID != "" {
						buf.ID = tc.ID
					}
					if tc.Function.Name != "" {
						buf.Name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						buf.Arguments += tc.Function.Arguments
					}
				}
				if choice.FinishReason != "" {
					switch choice.FinishReason {
					case "tool_calls":
						stopReason = "tool_use"
					case "stop":
						stopReason = "end_turn"
					case "length":
						stopReason = "max_tokens"
					default:
						stopReason = string(choice.FinishReason)
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

		// Emit completed tool calls in index order.
		indices := make([]int, 0, len(toolBuf))
		for i := range toolBuf {
			indices = append(indices, i)
		}
		sort.Ints(indices)
		for _, i := range indices {
			buf := toolBuf[i]
			input := json.RawMessage(buf.Arguments)
			if len(buf.Arguments) == 0 {
				input = json.RawMessage("{}")
			}
			if !json.Valid(input) {
				out <- Delta{Done: true, Err: fmt.Errorf("openai: tool_calls[%d] arguments invalid JSON", i)}
				return
			}
			select {
			case out <- Delta{ToolCall: &ToolCall{ID: buf.ID, Name: buf.Name, Input: input}}:
			case <-ctx.Done():
				return
			}
		}

		final := Delta{Done: true, StopReason: stopReason}
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

// openaiMessagesFromEvents assembles role-based messages from the canonical
// Event timeline. System + grounding become one system message. Consecutive
// assistant_text + assistant_tool_call events collapse into a single assistant
// message carrying content + tool_calls; tool_result events become tool-role
// messages.
//
// OpenAI requires every assistant message carrying tool_calls to be immediately
// followed by a tool message for each tool_call_id. The event timeline cannot
// be trusted to already satisfy that: legacy rows migrated before sequence_index
// was globally monotonic can interleave a user_message between a tool_call and
// its result. So we index tool_results by id up front and emit each call's
// result right after its assistant message, regardless of stream position. A
// tool_call with no matching result is dropped (emitting it would guarantee a
// 400); a tool_result already emitted alongside its call is skipped when later
// encountered.
func openaiMessagesFromEvents(system, grounding string, events []Event) []openai.ChatCompletionMessageParamUnion {
	var msgs []openai.ChatCompletionMessageParamUnion
	sys := system
	if grounding != "" {
		if sys != "" {
			sys += "\n\n"
		}
		sys += grounding
	}
	if sys != "" {
		msgs = append(msgs, openai.SystemMessage(sys))
	}
	// Index tool_results by tool_call_id so a call can find its result even when
	// the result is not adjacent in the event stream.
	resultByID := map[string]Event{}
	for _, e := range events {
		if e.Kind == "tool_result" {
			if _, dup := resultByID[e.ToolCallID]; !dup {
				resultByID[e.ToolCallID] = e
			}
		}
	}
	emitted := map[string]bool{} // tool_call_ids whose result we've already placed
	i := 0
	for i < len(events) {
		e := events[i]
		switch e.Kind {
		case "user_message":
			msgs = append(msgs, openai.UserMessage(e.Text))
			i++
		case "assistant_text", "assistant_tool_call":
			var text string
			var calls []openai.ChatCompletionMessageToolCallUnionParam
			var callIDs []string
			for i < len(events) {
				ee := events[i]
				if ee.Kind == "assistant_text" {
					if text != "" {
						text += "\n"
					}
					text += ee.Text
					i++
					continue
				}
				if ee.Kind == "assistant_tool_call" {
					// Drop a tool_call that has no result anywhere — emitting it
					// would leave a tool_call_id without a response message.
					if _, ok := resultByID[ee.ToolCallID]; !ok {
						i++
						continue
					}
					args := string(ee.ToolInput)
					if args == "" {
						args = "{}"
					}
					calls = append(calls, openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID: ee.ToolCallID,
							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      ee.ToolName,
								Arguments: args,
							},
						},
					})
					callIDs = append(callIDs, ee.ToolCallID)
					i++
					continue
				}
				break
			}
			msg := openai.AssistantMessage(text)
			if len(calls) > 0 && msg.OfAssistant != nil {
				msg.OfAssistant.ToolCalls = calls
			}
			msgs = append(msgs, msg)
			// Immediately follow with each call's tool result, in call order.
			for _, id := range callIDs {
				res := resultByID[id]
				msgs = append(msgs, openai.ToolMessage(res.Text, id))
				emitted[id] = true
			}
		case "tool_result":
			// Skip results already placed next to their assistant message.
			if !emitted[e.ToolCallID] {
				msgs = append(msgs, openai.ToolMessage(e.Text, e.ToolCallID))
				emitted[e.ToolCallID] = true
			}
			i++
		default:
			i++
		}
	}
	return msgs
}

func openaiToolsFromDefs(defs []ToolDef) []openai.ChatCompletionToolUnionParam {
	if len(defs) == 0 {
		return nil
	}
	out := make([]openai.ChatCompletionToolUnionParam, 0, len(defs))
	for _, d := range defs {
		var parameters map[string]any
		_ = json.Unmarshal(d.InputSchema, &parameters)
		out = append(out, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        d.Name,
			Description: openai.String(d.Description),
			Parameters:  parameters,
		}))
	}
	return out
}
