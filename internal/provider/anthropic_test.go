package provider

import (
	"context"
	"fmt"
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
