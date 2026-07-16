package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestGeminiContentsFromEventsDropsOrphanedToolCall(t *testing.T) {
	events := []Event{
		{Kind: "user_message", Text: "add 2+2"},
		{Kind: "assistant_tool_call", ToolCallID: "c1", ToolName: "safe_math", ToolInput: json.RawMessage(`{"expr":"2+2"}`)},
	}
	got := geminiContentsFromEvents(events)
	if len(got) != 1 || got[0].Role != genai.RoleUser {
		t.Fatalf("orphaned tool call not dropped: %+v", got)
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

// newGeminiFake serves canned SSE frames and captures the request body.
func newGeminiFake(t *testing.T, frames []string, gotBody *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, ":streamGenerateContent") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if k := r.Header.Get("x-goog-api-key"); k != "test-key" {
			t.Errorf("x-goog-api-key = %q, want test-key", k)
		}
		if gotBody != nil {
			b, _ := io.ReadAll(r.Body)
			*gotBody = b
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\r\n\r\n", f)
			fl.Flush()
		}
	}))
}

func TestGeminiStreamText(t *testing.T) {
	var body []byte
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Hel"}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":45,"cachedContentTokenCount":80}}`,
	}, &body)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model:  "gemini-3-pro",
		System: "You are helpful.",
		Events: []Event{{Kind: "user_message", Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var sb strings.Builder
	var final Delta
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("delta err: %v", d.Err)
		}
		sb.WriteString(d.Text)
		if d.Done {
			final = d
		}
	}
	if sb.String() != "Hello" {
		t.Fatalf("assembled = %q, want %q", sb.String(), "Hello")
	}
	if final.StopReason != "end_turn" {
		t.Fatalf("StopReason = %q, want end_turn", final.StopReason)
	}
	if final.Usage == nil || final.Usage.InputTokens != 120 || final.Usage.OutputTokens != 45 || final.Usage.CachedInputTokens != 80 {
		t.Fatalf("Usage = %+v, want {120 45 80}", final.Usage)
	}

	// The posted request must carry systemInstruction and the user content.
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	if _, ok := req["systemInstruction"]; !ok {
		t.Fatalf("request lacks systemInstruction: %s", body)
	}
}

func TestGeminiStreamGroundingConcatenatedIntoSystem(t *testing.T) {
	var body []byte
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`,
	}, &body)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model:     "gemini-3-pro",
		System:    "SYS.",
		Grounding: "GROUND.",
		Events:    []Event{{Kind: "user_message", Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}
	s := string(body)
	if !strings.Contains(s, "SYS.") || !strings.Contains(s, "GROUND.") {
		t.Fatalf("system+grounding not in request: %s", s)
	}
}

func TestGeminiStreamMaxTokensStopReason(t *testing.T) {
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"x"}]},"finishReason":"MAX_TOKENS"}]}`,
	}, nil)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model:  "gemini-3-pro",
		Events: []Event{{Kind: "user_message", Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var final Delta
	for d := range ch {
		if d.Done {
			final = d
		}
	}
	if final.StopReason != "max_tokens" {
		t.Fatalf("StopReason = %q, want max_tokens", final.StopReason)
	}
}

func TestGeminiStreamLegacyMessagesFallback(t *testing.T) {
	var body []byte
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`,
	}, &body)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model:        "gemini-3-pro",
		CachedPrefix: "You are helpful.",
		Messages:     []Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "yes?"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}
	s := string(body)
	if !strings.Contains(s, `"hi"`) || !strings.Contains(s, `"yes?"`) || !strings.Contains(s, "You are helpful.") {
		t.Fatalf("legacy fallback request missing content: %s", s)
	}
}

func TestGeminiStreamToolCall(t *testing.T) {
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"safe_math","args":{"expr":"2+2"}},"thoughtSignature":"c2lnLWJ5dGVzLTE="},{"functionCall":{"name":"safe_math","args":{"expr":"3+3"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`,
	}, nil)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model:  "gemini-3-pro",
		Tools:  []ToolDef{{Name: "safe_math", Description: "evaluate", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Events: []Event{{Kind: "user_message", Text: "add"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var calls []*ToolCall
	var final Delta
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("delta err: %v", d.Err)
		}
		if d.ToolCall != nil {
			calls = append(calls, d.ToolCall)
		}
		if d.Done {
			final = d
		}
	}
	if len(calls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(calls))
	}
	if calls[0].ID == "" || calls[1].ID == "" || calls[0].ID == calls[1].ID {
		t.Fatalf("IDs not unique/non-empty: %q, %q", calls[0].ID, calls[1].ID)
	}
	if calls[0].Name != "safe_math" || string(calls[0].Input) != `{"expr":"2+2"}` {
		t.Fatalf("call[0] = %+v", calls[0])
	}
	if final.StopReason != "tool_use" {
		t.Fatalf("StopReason = %q, want tool_use", final.StopReason)
	}
	var meta struct {
		ThoughtSignature string `json:"thought_signature"`
	}
	if calls[0].Metadata == nil {
		t.Fatal("calls[0].Metadata is nil, want thought_signature payload")
	}
	if err := json.Unmarshal(calls[0].Metadata, &meta); err != nil {
		t.Fatalf("calls[0].Metadata unmarshal: %v", err)
	}
	if meta.ThoughtSignature != "c2lnLWJ5dGVzLTE=" {
		t.Fatalf("calls[0] thought_signature = %q, want c2lnLWJ5dGVzLTE=", meta.ThoughtSignature)
	}
	if calls[1].Metadata != nil {
		t.Fatalf("calls[1].Metadata = %s, want nil (no signature on the frame)", calls[1].Metadata)
	}
}

func TestGeminiContentsThoughtSignatureRoundTrip(t *testing.T) {
	var body []byte
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"4"}]},"finishReason":"STOP"}]}`,
	}, &body)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "gemini-3-pro",
		Events: []Event{
			{Kind: "user_message", Text: "add 2+2"},
			{Kind: "assistant_tool_call", ToolCallID: "c1", ToolName: "safe_math",
				ToolInput:    json.RawMessage(`{"expr":"2+2"}`),
				ToolMetadata: json.RawMessage(`{"thought_signature":"c2lnLWJ5dGVzLTE="}`)},
			{Kind: "tool_result", ToolCallID: "c1", ToolName: "safe_math", Text: "4"},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}
	s := string(body)
	if !strings.Contains(s, `"thoughtSignature":"c2lnLWJ5dGVzLTE="`) {
		t.Fatalf("request lacks echoed thoughtSignature: %s", s)
	}
}

func TestGeminiContentsNoSignatureOmitsField(t *testing.T) {
	var body []byte
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"4"}]},"finishReason":"STOP"}]}`,
	}, &body)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "gemini-3-pro",
		Events: []Event{
			{Kind: "user_message", Text: "add 2+2"},
			{Kind: "assistant_tool_call", ToolCallID: "c1", ToolName: "safe_math",
				ToolInput: json.RawMessage(`{"expr":"2+2"}`)},
			{Kind: "tool_result", ToolCallID: "c1", ToolName: "safe_math", Text: "4"},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}
	s := string(body)
	if strings.Contains(s, "thoughtSignature") {
		t.Fatalf("request should omit thoughtSignature when none stored: %s", s)
	}
}

func TestGeminiStreamToolResultRoundTrip(t *testing.T) {
	// A replay containing a tool call + result must POST functionCall and
	// functionResponse parts back to the API.
	var body []byte
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"4"}]},"finishReason":"STOP"}]}`,
	}, &body)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "gemini-3-pro",
		Events: []Event{
			{Kind: "user_message", Text: "add 2+2"},
			{Kind: "assistant_tool_call", ToolCallID: "c1", ToolName: "safe_math", ToolInput: json.RawMessage(`{"expr":"2+2"}`)},
			{Kind: "tool_result", ToolCallID: "c1", ToolName: "safe_math", Text: "4"},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}
	s := string(body)
	if !strings.Contains(s, `"functionCall"`) || !strings.Contains(s, `"functionResponse"`) {
		t.Fatalf("request lacks functionCall/functionResponse: %s", s)
	}
}

func TestGeminiStreamCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\r\n\r\n", `{"candidates":[{"content":{"role":"model","parts":[{"text":"first"}]}}]}`)
		fl.Flush()
		select {
		case <-r.Context().Done(): // client hung up
		case <-release: // safety valve so the test can't wedge
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(ctx, ChatRequest{
		Model:  "gemini-3-pro",
		Events: []Event{{Kind: "user_message", Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	first := <-ch
	if first.Text != "first" {
		t.Fatalf("first delta = %+v, want text 'first'", first)
	}
	cancel()
	// The channel must terminate (either a Done frame with a ctx error, or
	// simply closing). Drain with a timeout guard.
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate after cancel")
	}
}
