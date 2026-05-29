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

func TestAnthropicStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		writeSSE := func(event, data string) {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			fl.Flush()
		}
		writeSSE("message_start", `{"type":"message_start","message":{"id":"m","role":"assistant","content":[],"model":"x","usage":{"input_tokens":1,"output_tokens":0}}}`)
		writeSSE("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		writeSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`)
		writeSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`)
		writeSSE("content_block_stop", `{"type":"content_block_stop","index":0}`)
		writeSSE("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`)
		writeSSE("message_stop", `{"type":"message_stop"}`)
	}))
	defer srv.Close()

	p := NewAnthropic("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "claude-opus-4-7", CachedPrefix: "You are helpful.",
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

func TestAnthropicStreamCapturesUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		writeSSE := func(event, data string) {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			fl.Flush()
		}
		writeSSE("message_start", `{"type":"message_start","message":{"id":"m","role":"assistant","content":[],"model":"x","usage":{"input_tokens":120,"output_tokens":0,"cache_read_input_tokens":80}}}`)
		writeSSE("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		writeSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`)
		writeSSE("content_block_stop", `{"type":"content_block_stop","index":0}`)
		writeSSE("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":120,"output_tokens":45,"cache_read_input_tokens":80}}`)
		writeSSE("message_stop", `{"type":"message_stop"}`)
	}))
	defer srv.Close()

	p := NewAnthropic("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "claude-opus-4-7", Messages: []Message{{Role: "user", Content: "hi"}},
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
	if final.Usage == nil {
		t.Fatal("terminal Delta.Usage = nil, want populated")
	}
	if final.Usage.InputTokens != 120 || final.Usage.OutputTokens != 45 || final.Usage.CachedInputTokens != 80 {
		t.Fatalf("Usage = %+v, want {120, 45, 80}", *final.Usage)
	}
}

func TestAnthropicStreamUsageAbsentNoCrash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		writeSSE := func(event, data string) {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			fl.Flush()
		}
		// No message_start, no message_delta — only text content blocks.
		writeSSE("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		writeSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`)
		writeSSE("content_block_stop", `{"type":"content_block_stop","index":0}`)
		writeSSE("message_stop", `{"type":"message_stop"}`)
	}))
	defer srv.Close()

	p := NewAnthropic("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "claude-opus-4-7", Messages: []Message{{Role: "user", Content: "hi"}},
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

// TestAnthropic_AssemblesContentBlocksFromEvents captures the outgoing request
// body (via the httptest handler) and asserts the canonical Events timeline is
// assembled into content-block messages, tool_result blocks, a system block,
// and a tools array.
func TestAnthropic_AssemblesContentBlocksFromEvents(t *testing.T) {
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		writeSSE := func(event, data string) {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			fl.Flush()
		}
		writeSSE("message_start", `{"type":"message_start","message":{"id":"m","role":"assistant","content":[],"model":"x","usage":{"input_tokens":1,"output_tokens":0}}}`)
		writeSSE("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		writeSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`)
		writeSSE("content_block_stop", `{"type":"content_block_stop","index":0}`)
		writeSSE("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`)
		writeSSE("message_stop", `{"type":"message_stop"}`)
	}))
	defer srv.Close()

	p := NewAnthropic("anth-key", srv.URL)
	req := ChatRequest{
		Model:     "claude-sonnet-4-6",
		System:    "You are an accounting tutor.",
		Grounding: "## Source 1 — intermediate-accounting · Chapter 4\nrealization...\n",
		Tools: []ToolDef{{
			Name: "search_textbook", Description: "...",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
		}},
		Events: []Event{
			{Kind: "user_message", Text: "explain realization principle"},
			{Kind: "assistant_text", Text: "Realization recognizes revenue when..."},
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
	if !strings.Contains(body, `"system"`) || !strings.Contains(body, "accounting tutor") {
		t.Fatalf("system block missing: %s", body)
	}
	if !strings.Contains(body, `"tool_use"`) || !strings.Contains(body, "call_1") {
		t.Fatalf("assistant tool_use block missing: %s", body)
	}
	if !strings.Contains(body, `"tool_result"`) || !strings.Contains(body, "## Source 1") {
		t.Fatalf("tool_result block missing: %s", body)
	}
	if !strings.Contains(body, `"tools"`) || !strings.Contains(body, "search_textbook") {
		t.Fatalf("tools array missing: %s", body)
	}
}

func TestAnthropic_StreamSurfacesToolUseAndStopReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		writeSSE := func(event, data string) {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			fl.Flush()
		}
		writeSSE("message_start", `{"type":"message_start","message":{"id":"m","role":"assistant","content":[],"model":"x","usage":{"input_tokens":10,"output_tokens":0}}}`)
		writeSSE("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"search_textbook","input":{}}}`)
		writeSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"realization"}}`)
		writeSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":" principle\"}"}}`)
		writeSSE("content_block_stop", `{"type":"content_block_stop","index":0}`)
		writeSSE("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`)
		writeSSE("message_stop", `{"type":"message_stop"}`)
	}))
	defer srv.Close()

	p := NewAnthropic("anth-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{Model: "claude-sonnet-4-6"})
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
	if len(calls) != 1 || calls[0].Name != "search_textbook" || calls[0].ID != "toolu_1" {
		t.Fatalf("tool call mismatch: %+v", calls)
	}
	var parsed map[string]any
	if err := json.Unmarshal(calls[0].Input, &parsed); err != nil {
		t.Fatalf("input not valid JSON after assembly: %s", calls[0].Input)
	}
	if parsed["query"] != "realization principle" {
		t.Fatalf("input assembly wrong: %v", parsed)
	}
	if stopReason != "tool_use" {
		t.Fatalf("stop reason want tool_use, got %q", stopReason)
	}
}
