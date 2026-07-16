package provider

import (
	"context"
	"encoding/json"

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

// Stream is implemented in Task 4. This stub keeps the package compiling
// until then.
func (p *geminiProvider) Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error) {
	panic("not implemented")
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
