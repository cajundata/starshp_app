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
)

func TestOpenAIStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flush := w.(http.Flusher)
		chunks := []string{"Hel", "lo"}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"index\":0}]}\n\n", c)
			flush.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush.Flush()
	}))
	defer srv.Close()

	p := NewOpenAI("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "gpt-5.4-2026-03-05", CachedPrefix: "You are helpful.",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var sb strings.Builder
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("delta err: %v", d.Err)
		}
		sb.WriteString(d.Text)
	}
	if sb.String() != "Hello" {
		t.Fatalf("assembled = %q, want %q", sb.String(), "Hello")
	}
}

func TestOpenAIStreamCapturesUsageAndRequestsIt(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture body so we can assert include_usage was set.
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = buf

		w.Header().Set("Content-Type", "text/event-stream")
		flush := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"},\"index\":0}]}\n\n")
		flush.Flush()
		// Final chunk: empty choices, usage populated.
		fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":120,\"completion_tokens\":45,\"total_tokens\":165,\"prompt_tokens_details\":{\"cached_tokens\":80}}}\n\n")
		flush.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush.Flush()
	}))
	defer srv.Close()

	p := NewOpenAI("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "gpt-5.4-2026-03-05", Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var final Delta
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("delta err: %v", d.Err)
		}
		if d.Done {
			final = d
		}
	}

	if !strings.Contains(string(gotBody), `"include_usage":true`) {
		t.Fatalf("request body did not request usage: %s", string(gotBody))
	}
	if final.Usage == nil {
		t.Fatal("terminal Delta.Usage = nil, want populated")
	}
	if final.Usage.InputTokens != 120 || final.Usage.OutputTokens != 45 || final.Usage.CachedInputTokens != 80 {
		t.Fatalf("Usage = %+v, want {120, 45, 80}", *final.Usage)
	}
}

func TestOpenAIStreamUsageAbsentNoCrash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flush := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"},\"index\":0}]}\n\n")
		flush.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush.Flush()
	}))
	defer srv.Close()

	p := NewOpenAI("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "gpt-5.4-2026-03-05", Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var final Delta
	for d := range ch {
		final = d
	}
	if final.Usage != nil {
		t.Fatalf("Usage = %+v, want nil when SDK does not emit usage", *final.Usage)
	}
}

func TestOpenAI_AssemblesMessagesFromEvents(t *testing.T) {
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		flush := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"index\":0}]}\n\n")
		flush.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush.Flush()
	}))
	defer srv.Close()

	p := NewOpenAI("openai-key", srv.URL)
	req := ChatRequest{
		Model:     "gpt-5.4-2026-03-05",
		System:    "You are an accounting tutor.",
		Grounding: "## Source 1 — intermediate-accounting · Chapter 4\n...\n",
		Tools: []ToolDef{{
			Name: "search_textbook", Description: "...",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
		}},
		Events: []Event{
			{Kind: "user_message", Text: "explain realization principle"},
			{Kind: "assistant_text", Text: "Realization recognizes..."},
			{Kind: "assistant_tool_call", ToolCallID: "call_1",
				ToolName:  "search_textbook",
				ToolInput: json.RawMessage(`{"query":"realization principle"}`)},
			{Kind: "tool_result", ToolCallID: "call_1", Text: "## Source 1..."},
			{Kind: "user_message", Text: "summarize"},
		},
	}
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch { //nolint:revive // drain
	}
	body := capturedBody
	if !strings.Contains(body, `"role":"system"`) {
		t.Fatalf("system message missing: %s", body)
	}
	if !strings.Contains(body, `"tool_calls"`) || !strings.Contains(body, `"call_1"`) {
		t.Fatalf("assistant tool_calls missing: %s", body)
	}
	if !strings.Contains(body, `"role":"tool"`) || !strings.Contains(body, "## Source 1") {
		t.Fatalf("tool role message missing: %s", body)
	}
	if !strings.Contains(body, `"tools"`) || !strings.Contains(body, "search_textbook") {
		t.Fatalf("tools array missing: %s", body)
	}
}

func TestOpenAI_StreamSurfacesToolCallsAndStopReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flush := w.(http.Flusher)
		writeChunk := func(data string) {
			fmt.Fprintf(w, "data: %s\n\n", data)
			flush.Flush()
		}
		writeChunk(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search_textbook","arguments":"{\"query\":\"realization"}}]}}]}`)
		writeChunk(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":" principle\"}"}}]}}]}`)
		writeChunk(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
		writeChunk(`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":0}}}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush.Flush()
	}))
	defer srv.Close()

	p := NewOpenAI("openai-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{Model: "gpt-5.4-2026-03-05"})
	if err != nil {
		t.Fatal(err)
	}
	var calls []*ToolCall
	var stopReason string
	for d := range ch {
		if d.ToolCall != nil {
			calls = append(calls, d.ToolCall)
		}
		if d.Done {
			stopReason = d.StopReason
		}
	}
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "search_textbook" {
		t.Fatalf("tool call mismatch: %+v", calls)
	}
	var parsed map[string]any
	if err := json.Unmarshal(calls[0].Input, &parsed); err != nil {
		t.Fatalf("input JSON invalid: %s", calls[0].Input)
	}
	if parsed["query"] != "realization principle" {
		t.Fatalf("input assembly wrong: %v", parsed)
	}
	if stopReason != "tool_use" {
		t.Fatalf("stop reason want tool_use (normalized from tool_calls), got %q", stopReason)
	}
}
