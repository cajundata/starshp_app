package provider

import "strings"

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
	case strings.Contains(s, "connection refused") || strings.Contains(s, "dial tcp") || strings.Contains(s, "timeout"):
		return AppError{"network", "Network error reaching the provider. Check your connection.", true}
	default:
		return AppError{"unknown", "Unexpected error: " + err.Error(), false}
	}
}
