package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"google.golang.org/genai"
)

type geminiProvider struct {
	apiKey  string
	baseURL string
}

// NewGemini builds a Gemini provider. baseURL may be empty for the default
// endpoint (tests pass an httptest URL).
func NewGemini(apiKey, baseURL string) ChatProvider {
	return &geminiProvider{apiKey: apiKey, baseURL: baseURL}
}

func (p *geminiProvider) Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error) {
	cc := &genai.ClientConfig{APIKey: p.apiKey, Backend: genai.BackendGeminiAPI}
	if p.baseURL != "" {
		cc.HTTPOptions.BaseURL = p.baseURL
	}
	client, err := genai.NewClient(ctx, cc)
	if err != nil {
		return nil, err
	}

	var contents []*genai.Content
	if len(req.Events) > 0 {
		contents = geminiContentsFromEvents(req.Events)
	} else {
		contents = make([]*genai.Content, 0, len(req.Messages))
		for _, m := range req.Messages {
			role := genai.RoleUser
			if m.Role == "assistant" {
				role = genai.RoleModel
			}
			contents = append(contents, genai.NewContentFromText(m.Content, genai.Role(role)))
		}
	}

	cfg := &genai.GenerateContentConfig{}
	sys := req.System
	if sys == "" {
		sys = req.CachedPrefix
	}
	if req.Grounding != "" {
		if sys != "" {
			sys += "\n\n"
		}
		sys += req.Grounding
	}
	if sys != "" {
		cfg.SystemInstruction = genai.NewContentFromText(sys, genai.RoleUser)
	}
	if tools := buildGeminiTools(req.Tools); len(tools) > 0 {
		cfg.Tools = tools
	}

	out := make(chan Delta)
	go func() {
		defer close(out)
		var (
			usage       Usage
			haveUsage   bool
			stopReason  string
			sawToolCall bool
		)
		for resp, serr := range client.Models.GenerateContentStream(ctx, req.Model, contents, cfg) {
			if serr != nil {
				out <- Delta{Done: true, Err: serr}
				return
			}
			if u := resp.UsageMetadata; u != nil {
				usage.InputTokens = int(u.PromptTokenCount)
				usage.OutputTokens = int(u.CandidatesTokenCount)
				usage.CachedInputTokens = int(u.CachedContentTokenCount)
				haveUsage = true
			}
			if len(resp.Candidates) == 0 {
				continue
			}
			cand := resp.Candidates[0]
			if cand.Content != nil {
				for _, part := range cand.Content.Parts {
					switch {
					case part.FunctionCall != nil:
						fc := part.FunctionCall
						input := json.RawMessage("{}")
						if len(fc.Args) > 0 {
							b, merr := json.Marshal(fc.Args)
							if merr != nil {
								out <- Delta{Done: true, Err: fmt.Errorf("gemini: functionCall args for %s: %w", fc.Name, merr)}
								return
							}
							input = b
						}
						id := fc.ID
						if id == "" {
							id = geminiCallID()
						}
						sawToolCall = true
						select {
						case out <- Delta{ToolCall: &ToolCall{ID: id, Name: fc.Name, Input: input}}:
						case <-ctx.Done():
							return
						}
					case part.Text != "" && !part.Thought:
						select {
						case out <- Delta{Text: part.Text}:
						case <-ctx.Done():
							return
						}
					}
				}
			}
			if cand.FinishReason != "" {
				switch cand.FinishReason {
				case genai.FinishReasonStop:
					stopReason = "end_turn"
				case genai.FinishReasonMaxTokens:
					stopReason = "max_tokens"
				default:
					stopReason = "error"
				}
			}
		}
		if sawToolCall {
			stopReason = "tool_use"
		}
		final := Delta{Done: true, StopReason: stopReason}
		if haveUsage {
			u := usage
			final.Usage = &u
		}
		out <- final
	}()
	return out, nil
}

// geminiContentsFromEvents assembles Gemini contents from the canonical
// Event timeline. Gemini matches function responses by name (not call ID),
// so ToolCallID is dropped on the wire — the store keeps it authoritative.
// Consecutive same-role events merge into one Content with multiple parts.
func geminiContentsFromEvents(events []Event) []*genai.Content {
	var out []*genai.Content
	resultByID := map[string]bool{}
	for _, e := range events {
		if e.Kind == "tool_result" {
			resultByID[e.ToolCallID] = true
		}
	}
	appendPart := func(role string, part *genai.Part) {
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Parts = append(out[n-1].Parts, part)
			return
		}
		out = append(out, &genai.Content{Role: role, Parts: []*genai.Part{part}})
	}
	for _, e := range events {
		switch e.Kind {
		case "user_message":
			appendPart(genai.RoleUser, genai.NewPartFromText(e.Text))
		case "assistant_text":
			appendPart(genai.RoleModel, genai.NewPartFromText(e.Text))
		case "assistant_tool_call":
			// Drop a tool_call that has no result anywhere — emitting it would
			// leave a trailing functionCall with no functionResponse.
			if !resultByID[e.ToolCallID] {
				continue
			}
			var args map[string]any
			if len(e.ToolInput) > 0 {
				_ = json.Unmarshal(e.ToolInput, &args)
			}
			appendPart(genai.RoleModel, &genai.Part{
				FunctionCall: &genai.FunctionCall{Name: e.ToolName, Args: args},
			})
		case "tool_result":
			resp := map[string]any{"output": e.Text}
			if e.IsError {
				resp = map[string]any{"error": e.Text}
			}
			appendPart(genai.RoleUser, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{Name: e.ToolName, Response: resp},
			})
		}
	}
	return out
}

// buildGeminiTools converts the tool catalog to functionDeclarations,
// passing our JSON Schema through the SDK's raw-schema field.
func buildGeminiTools(tools []ToolDef) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		var schema any
		if len(t.InputSchema) > 0 {
			_ = json.Unmarshal(t.InputSchema, &schema)
		}
		decls = append(decls, &genai.FunctionDeclaration{
			Name:                 t.Name,
			Description:          t.Description,
			ParametersJsonSchema: schema,
		})
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}

// geminiCallID synthesizes a unique tool-call ID. Gemini matches function
// responses by name and usually omits IDs, but the shared event log needs
// IDs unique across the whole conversation (the Anthropic replay path
// dedupes results by ID), so a fixed counter would collide across turns.
func geminiCallID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "gemcall_" + hex.EncodeToString(b[:])
}
