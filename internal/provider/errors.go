package provider

import (
	"fmt"
	"strings"
)

type AppError struct {
	Code        string `json:"code"`
	UserMessage string `json:"userMessage"`
	Retryable   bool   `json:"retryable"`
}

func (e AppError) Error() string { return e.Code + ": " + e.UserMessage }

func NormalizeError(err error) AppError {
	if err == nil {
		return AppError{}
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "401") || strings.Contains(s, "unauthorized") || strings.Contains(s, "invalid api key"):
		return AppError{"auth", "Invalid or missing API key. Check your .env file.", false}
	case strings.Contains(s, "429") || strings.Contains(s, "rate limit"):
		return AppError{"rate_limit", "Rate limited by the provider. Wait a moment and retry.", true}
	case strings.Contains(s, "context length") || strings.Contains(s, "maximum context"):
		return AppError{"context_length", "Too much context. Trim the attached textbook scope and retry.", false}
	case strings.Contains(s, "connection refused") ||
		strings.Contains(s, "dial tcp") ||
		strings.Contains(s, "no such host") ||
		strings.Contains(s, "timeout") ||
		strings.Contains(s, "deadline exceeded"):
		return AppError{"network", "Network error reaching the provider. Check your connection.", true}
	default:
		return AppError{"unknown", "Unexpected error: " + err.Error(), false}
	}
}

// MaybeRemapLocal upgrades a generic network AppError into a more specific
// local_unreachable error when the failing model uses the openai_compat
// provider. The base URL is interpolated into the user message so the user
// knows which endpoint Starshp was calling. Returns the input unchanged
// otherwise.
func MaybeRemapLocal(e AppError, m ModelInfo) AppError {
	if m.Provider != "openai_compat" || e.Code != "network" {
		return e
	}
	return AppError{
		Code: "local_unreachable",
		UserMessage: fmt.Sprintf(
			"Local model server unreachable at %s. Is Ollama running? (Run `ollama serve` or start the Ollama app.)",
			m.BaseURL,
		),
		Retryable: true,
	}
}
