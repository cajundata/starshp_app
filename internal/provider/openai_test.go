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
