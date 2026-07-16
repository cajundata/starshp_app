package provider

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/genai"
)

func TestNormalizeError(t *testing.T) {
	cases := []struct {
		in   error
		code string
	}{
		{errors.New("401 Unauthorized: invalid api key"), "auth"},
		{errors.New("429 Too Many Requests rate limit"), "rate_limit"},
		{errors.New("400 context length exceeded maximum"), "context_length"},
		{errors.New("dial tcp: connection refused"), "network"},
		{errors.New("weird"), "unknown"},
	}
	for _, c := range cases {
		got := NormalizeError(c.in)
		if got.Code != c.code {
			t.Errorf("NormalizeError(%q).Code = %q, want %q", c.in, got.Code, c.code)
		}
		if got.UserMessage == "" {
			t.Errorf("empty UserMessage for %q", c.in)
		}
	}
}

func TestMaybeRemapLocalRemapsNetworkForCompatModel(t *testing.T) {
	m := ModelInfo{ID: "llama3.2", Provider: "openai_compat", BaseURL: "http://localhost:11434/v1"}
	in := AppError{Code: "network", UserMessage: "Network error reaching the provider. Check your connection.", Retryable: true}
	out := MaybeRemapLocal(in, m)
	if out.Code != "local_unreachable" {
		t.Errorf("Code = %q, want local_unreachable", out.Code)
	}
	if !strings.Contains(out.UserMessage, "http://localhost:11434/v1") {
		t.Errorf("UserMessage %q does not interpolate the base URL", out.UserMessage)
	}
	if !strings.Contains(out.UserMessage, "Ollama") {
		t.Errorf("UserMessage %q does not mention Ollama in the suggestion", out.UserMessage)
	}
	if !out.Retryable {
		t.Errorf("Retryable should remain true after remap")
	}
}

func TestMaybeRemapLocalIgnoresCloudModels(t *testing.T) {
	for _, p := range []string{"openai", "anthropic"} {
		m := ModelInfo{ID: "x", Provider: p}
		in := AppError{Code: "network", UserMessage: "Network error reaching the provider.", Retryable: true}
		out := MaybeRemapLocal(in, m)
		if out.Code != "network" {
			t.Errorf("provider=%s: Code = %q, want network (unchanged)", p, out.Code)
		}
	}
}

func TestMaybeRemapLocalIgnoresNonNetworkCodes(t *testing.T) {
	m := ModelInfo{ID: "llama3.2", Provider: "openai_compat", BaseURL: "http://localhost:11434/v1"}
	for _, code := range []string{"auth", "rate_limit", "context_length", "unknown"} {
		in := AppError{Code: code, UserMessage: "x"}
		out := MaybeRemapLocal(in, m)
		if out.Code != code {
			t.Errorf("Code=%q: got %q, want unchanged", code, out.Code)
		}
	}
}

func TestNormalizeErrorClassifiesConnectionVariants(t *testing.T) {
	cases := []string{
		"dial tcp 127.0.0.1:11434: connect: connection refused",
		"Get \"http://localhost:11434/v1/chat/completions\": dial tcp: lookup wat.invalid: no such host",
		"Post \"http://localhost:11434/v1/chat/completions\": context deadline exceeded",
	}
	for _, msg := range cases {
		got := NormalizeError(errors.New(msg))
		if got.Code != "network" {
			t.Errorf("NormalizeError(%q).Code = %q, want network", msg, got.Code)
		}
	}
}

func TestNormalizeErrorGeminiAPIErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"api key invalid 400", genai.APIError{Code: 400, Message: "API key not valid. Please pass a valid API key.", Status: "INVALID_ARGUMENT"}, "auth"},
		{"permission denied 403", genai.APIError{Code: 403, Message: "permission denied", Status: "PERMISSION_DENIED"}, "auth"},
		{"unauthenticated 401", genai.APIError{Code: 401, Message: "unauthenticated", Status: "UNAUTHENTICATED"}, "auth"},
		{"resource exhausted 429", genai.APIError{Code: 429, Message: "quota exceeded", Status: "RESOURCE_EXHAUSTED"}, "rate_limit"},
		{"token overflow 400", genai.APIError{Code: 400, Message: "The input token count (2000001) exceeds the maximum number of tokens allowed (2000000).", Status: "INVALID_ARGUMENT"}, "context_length"},
		{"wrapped api error", fmt.Errorf("send: %w", genai.APIError{Code: 429, Message: "slow down", Status: "RESOURCE_EXHAUSTED"}), "rate_limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeError(tc.err); got.Code != tc.code {
				t.Fatalf("NormalizeError(%v).Code = %q, want %q", tc.err, got.Code, tc.code)
			}
		})
	}
}
