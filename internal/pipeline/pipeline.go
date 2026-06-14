// Package pipeline holds pure domain logic for the idea pipeline: which status
// transitions are legal, and how to shape the launch-time reviews-due sweep.
// It has no storage or I/O dependencies so it is trivially testable.
package pipeline

import (
	"fmt"

	"github.com/cajundata/starshp_app/internal/store"
)

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

// ValidateTransition reports whether moving from -> to is legal and, when
// moving to killed or parked, that a non-empty reason was supplied.
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

// DueReviewView is a due kill criterion enriched with how overdue it is, for
// display in the Reviews Due panel. No JSON tags: like the store structs, the
// Wails binding generates PascalCase TS fields (IdeaTitle, DaysOverdue, …),
// which is the convention the frontend already follows.
type DueReviewView struct {
	CriterionID string
	IdeaID      string
	IdeaTitle   string
	IdeaStatus  string
	Metric      string
	Threshold   string
	ReviewDate  int64
	OnMiss      string
	DaysOverdue int
}

// ShapeDueReviews computes days-overdue (0 when due today) for each row,
// relative to asOf. Both are UnixMilli.
func ShapeDueReviews(rows []store.DueReview, asOf int64) []DueReviewView {
	const day = int64(86_400_000)
	out := make([]DueReviewView, 0, len(rows))
	for _, r := range rows {
		overdue := 0
		if asOf > r.ReviewDate {
			overdue = int((asOf - r.ReviewDate) / day)
		}
		out = append(out, DueReviewView{
			CriterionID: r.CriterionID, IdeaID: r.IdeaID, IdeaTitle: r.IdeaTitle,
			IdeaStatus: r.IdeaStatus, Metric: r.Metric, Threshold: r.Threshold,
			ReviewDate: r.ReviewDate, OnMiss: r.OnMiss, DaysOverdue: overdue,
		})
	}
	return out
}
