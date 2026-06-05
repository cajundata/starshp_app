package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// helper: a registry containing a single openai_compat model pointing at the
// given URL, optionally requiring an env-var API key.
func compatReg(id, baseURL, apiKeyEnv string) Registry {
	return Registry{Models: []ModelInfo{{
		Display:   id,
		ID:        id,
		Provider:  "openai_compat",
		BaseURL:   baseURL,
		APIKeyEnv: apiKeyEnv,
	}}}
}

func TestFactoryOpenAICompatPointsAtBaseURL(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		flush := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"index\":0}]}\n\n")
		flush.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush.Flush()
	}))
	defer srv.Close()

	reg := compatReg("llama3.2", srv.URL, "")

	// Cloud keys are irrelevant for openai_compat; pass empty to prove they're ignored.
	p, err := New(reg, "llama3.2", "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "llama3.2", Messages: []Message{{Role: "user", Content: "hi"}},
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
	if sb.String() != "ok" {
		t.Fatalf("assembled = %q, want %q", sb.String(), "ok")
	}
	if gotAuth != "Bearer local" {
		t.Errorf("Authorization = %q, want \"Bearer local\" (dummy key)", gotAuth)
	}
}

func TestFactoryOpenAICompatHonorsAPIKeyEnv(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		flush := w.(http.Flusher)
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush.Flush()
	}))
	defer srv.Close()

	t.Setenv("LM_STUDIO_TOKEN", "lms-secret-42")
	reg := compatReg("qwen2.5", srv.URL, "LM_STUDIO_TOKEN")

	p, err := New(reg, "qwen2.5", "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "qwen2.5", Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}
	if gotAuth != "Bearer lms-secret-42" {
		t.Errorf("Authorization = %q, want \"Bearer lms-secret-42\"", gotAuth)
	}
}

func TestFactoryOpenAICompatFallsBackWhenEnvUnset(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		flush := w.(http.Flusher)
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush.Flush()
	}))
	defer srv.Close()

	// APIKeyEnv names an env var that is empty; resolveCompatKey treats
	// "" the same as "unset" and falls back to the dummy key.
	t.Setenv("SOME_UNSET_TOKEN_VAR", "")
	reg := compatReg("custom", srv.URL, "SOME_UNSET_TOKEN_VAR")

	p, err := New(reg, "custom", "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "custom", Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}
	if gotAuth != "Bearer local" {
		t.Errorf("Authorization = %q, want fallback \"Bearer local\"", gotAuth)
	}
}
