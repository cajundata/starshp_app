package provider

import (
	"context"
	"fmt"
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
