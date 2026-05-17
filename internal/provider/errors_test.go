package provider

import (
	"errors"
	"testing"
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
