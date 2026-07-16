package provider

import (
	"encoding/json"
	"testing"

	"google.golang.org/genai"
)

func TestGeminiContentsFromEvents(t *testing.T) {
	events := []Event{
		{Kind: "user_message", Text: "add 2+2"},
		{Kind: "assistant_text", Text: "Let me compute."},
		{Kind: "assistant_tool_call", ToolCallID: "c1", ToolName: "safe_math", ToolInput: json.RawMessage(`{"expr":"2+2"}`)},
		{Kind: "tool_result", ToolCallID: "c1", ToolName: "safe_math", Text: "4"},
		{Kind: "assistant_text", Text: "It is 4."},
	}
	got := geminiContentsFromEvents(events)

	// user / model / user(functionResponse) / model — consecutive same-role
	// parts merge into one Content.
	if len(got) != 4 {
		t.Fatalf("len(contents) = %d, want 4", len(got))
	}
	if got[0].Role != genai.RoleUser || got[0].Parts[0].Text != "add 2+2" {
		t.Fatalf("contents[0] = %+v, want user text", got[0])
	}
	if got[1].Role != genai.RoleModel || len(got[1].Parts) != 2 {
		t.Fatalf("contents[1] = %+v, want model with text + functionCall parts", got[1])
	}
	fc := got[1].Parts[1].FunctionCall
	if fc == nil || fc.Name != "safe_math" || fc.Args["expr"] != "2+2" {
		t.Fatalf("functionCall = %+v, want safe_math{expr:2+2}", fc)
	}
	fr := got[2].Parts[0].FunctionResponse
	if got[2].Role != genai.RoleUser || fr == nil || fr.Name != "safe_math" || fr.Response["output"] != "4" {
		t.Fatalf("contents[2] = %+v, want user functionResponse output=4", got[2])
	}
	if got[3].Role != genai.RoleModel || got[3].Parts[0].Text != "It is 4." {
		t.Fatalf("contents[3] = %+v, want model text", got[3])
	}
}

func TestGeminiContentsFromEventsErrorResult(t *testing.T) {
	events := []Event{
		{Kind: "assistant_tool_call", ToolCallID: "c1", ToolName: "safe_math", ToolInput: json.RawMessage(`{}`)},
		{Kind: "tool_result", ToolCallID: "c1", ToolName: "safe_math", Text: "divide by zero", IsError: true},
	}
	got := geminiContentsFromEvents(events)
	if len(got) != 2 {
		t.Fatalf("len(contents) = %d, want 2", len(got))
	}
	fr := got[1].Parts[0].FunctionResponse
	if fr == nil || fr.Response["error"] != "divide by zero" {
		t.Fatalf("error result = %+v, want Response[error]", fr)
	}
}

func TestBuildGeminiTools(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"expr":{"type":"string"}},"required":["expr"]}`)
	tools := buildGeminiTools([]ToolDef{{Name: "safe_math", Description: "evaluate", InputSchema: schema}})
	if len(tools) != 1 || len(tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools = %+v, want one Tool with one declaration", tools)
	}
	d := tools[0].FunctionDeclarations[0]
	if d.Name != "safe_math" || d.Description != "evaluate" || d.ParametersJsonSchema == nil {
		t.Fatalf("declaration = %+v", d)
	}
	if buildGeminiTools(nil) != nil {
		t.Fatal("buildGeminiTools(nil) should be nil")
	}
}
