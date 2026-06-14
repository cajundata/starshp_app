// Package pipeline holds pure domain logic for the idea pipeline: which status
// transitions are legal, and how to shape the launch-time reviews-due sweep.
// It has no storage or I/O dependencies so it is trivially testable.
package pipeline

import "fmt"

// allowedTransitions maps a current status to the statuses it may move to.
// killed is terminal in this milestone (no outbound transitions).
var allowedTransitions = map[string][]string{
	"raw":        {"triaged", "killed", "parked"},
	"triaged":    {"in_review", "killed", "parked"},
	"in_review":  {"validating", "go", "parked", "killed"},
	"validating": {"go", "parked", "killed"},
	"go":         {"validating", "parked", "killed"},
	"parked":     {"raw", "triaged", "in_review", "killed"},
	"killed":     {},
}

// TransitionError is a validation failure with a stable code the API boundary
// maps to a user-facing message.
type TransitionError struct {
	Code    string // "invalid_transition" | "reason_required"
	Message string
}

func (e *TransitionError) Error() string { return e.Message }

// ValidateTransition reports whether moving from -> to is legal and, for
// terminal statuses (killed, parked), that a non-empty reason was supplied.
func ValidateTransition(from, to, reason string) error {
	allowed, ok := allowedTransitions[from]
	if !ok {
		return &TransitionError{Code: "invalid_transition",
			Message: fmt.Sprintf("Unknown current status %q.", from)}
	}
	legal := false
	for _, s := range allowed {
		if s == to {
			legal = true
			break
		}
	}
	if !legal {
		return &TransitionError{Code: "invalid_transition",
			Message: fmt.Sprintf("Cannot move an idea from %q to %q.", from, to)}
	}
	if (to == "killed" || to == "parked") && reason == "" {
		return &TransitionError{Code: "reason_required",
			Message: fmt.Sprintf("Moving to %q requires a reason.", to)}
	}
	return nil
}
