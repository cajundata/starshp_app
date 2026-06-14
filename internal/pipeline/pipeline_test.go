package pipeline

import (
	"errors"
	"testing"
)

func TestValidateTransition(t *testing.T) {
	cases := []struct {
		name           string
		from, to, reas string
		wantErr        bool
		wantCode       string
	}{
		{"raw to triaged", "raw", "triaged", "", false, ""},
		{"in_review to go", "in_review", "go", "", false, ""},
		{"kill needs reason ok", "in_review", "killed", "no demand", false, ""},
		{"kill missing reason", "in_review", "killed", "", true, "reason_required"},
		{"park missing reason", "validating", "parked", "", true, "reason_required"},
		{"illegal raw to go", "raw", "go", "", true, "invalid_transition"},
		{"killed is terminal", "killed", "triaged", "", true, "invalid_transition"},
		{"no-op rejected", "raw", "raw", "", true, "invalid_transition"},
		{"unknown target", "raw", "bogus", "", true, "invalid_transition"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateTransition(c.from, c.to, c.reas)
			if c.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v got err=%v", c.wantErr, err)
			}
			if c.wantErr {
				var te *TransitionError
				if !errors.As(err, &te) {
					t.Fatalf("want *TransitionError, got %T", err)
				}
				if te.Code != c.wantCode {
					t.Fatalf("code want %q got %q", c.wantCode, te.Code)
				}
			}
		})
	}
}
