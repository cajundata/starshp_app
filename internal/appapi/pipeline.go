package appapi

import (
	"errors"
	"time"

	"github.com/cajundata/starshp_app/internal/pipeline"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/google/uuid"
)

// CreateIdea creates a new idea in the 'raw' status and returns it.
func (a *API) CreateIdea(title, summary, pathway string, financialFlag bool) (store.Idea, error) {
	if title == "" {
		return store.Idea{}, provider.AppError{
			Code: "invalid_input", UserMessage: "An idea needs a title.", Retryable: false}
	}
	idea := store.Idea{
		ID: uuid.NewString(), Title: title, Summary: summary, Pathway: pathway,
		Status: "raw", FinancialFlag: financialFlag, Source: "manual",
	}
	if err := a.st.CreateIdea(idea); err != nil {
		return store.Idea{}, err
	}
	return a.st.GetIdea(idea.ID)
}

func (a *API) UpdateIdea(i store.Idea) error    { return a.st.UpdateIdea(i) }
func (a *API) ListIdeas() ([]store.Idea, error) { return a.st.ListIdeas() }
func (a *API) GetIdea(id string) (store.Idea, error) {
	return a.st.GetIdea(id)
}
func (a *API) DeleteIdea(id string) error { return a.st.DeleteIdea(id) }

// SetIdeaStatus validates the transition (legality + reason-required for
// terminal statuses) before persisting it.
func (a *API) SetIdeaStatus(id, toStatus, reason string) error {
	cur, err := a.st.GetIdea(id)
	if err != nil {
		return provider.AppError{Code: "not_found",
			UserMessage: "That idea no longer exists.", Retryable: false}
	}
	if verr := pipeline.ValidateTransition(cur.Status, toStatus, reason); verr != nil {
		var te *pipeline.TransitionError
		if errors.As(verr, &te) {
			return provider.AppError{Code: te.Code, UserMessage: te.Message, Retryable: false}
		}
		return verr
	}
	return a.st.SetIdeaStatus(id, toStatus, reason)
}

func (a *API) ListStatusHistory(ideaID string) ([]store.StatusChange, error) {
	return a.st.ListStatusHistory(ideaID)
}

// AddKillCriterion stores a new kill criterion (status 'pending') and returns it.
func (a *API) AddKillCriterion(ideaID, metric, threshold string, reviewDate int64, onMiss string) (store.KillCriterion, error) {
	if metric == "" || threshold == "" {
		return store.KillCriterion{}, provider.AppError{Code: "invalid_input",
			UserMessage: "A kill criterion needs a metric and a threshold.", Retryable: false}
	}
	switch onMiss {
	case "kill", "park", "halt":
	default:
		return store.KillCriterion{}, provider.AppError{Code: "invalid_input",
			UserMessage: "On-miss must be kill, park, or halt.", Retryable: false}
	}
	k := store.KillCriterion{
		ID: uuid.NewString(), IdeaID: ideaID, Metric: metric, Threshold: threshold,
		ReviewDate: reviewDate, OnMiss: onMiss, Status: "pending",
	}
	if err := a.st.AddKillCriterion(k); err != nil {
		return store.KillCriterion{}, err
	}
	return k, nil
}

func (a *API) UpdateKillCriterion(k store.KillCriterion) error { return a.st.UpdateKillCriterion(k) }
func (a *API) DeleteKillCriterion(id string) error             { return a.st.DeleteKillCriterion(id) }
func (a *API) ListKillCriteria(ideaID string) ([]store.KillCriterion, error) {
	return a.st.ListKillCriteria(ideaID)
}

// ListReviewsDue runs the on-launch sweep: pending kill criteria due at or
// before now, shaped with days-overdue for the Reviews Due panel.
func (a *API) ListReviewsDue() ([]pipeline.DueReviewView, error) {
	now := time.Now().UnixMilli()
	rows, err := a.st.ListDueKillCriteria(now)
	if err != nil {
		return nil, err
	}
	return pipeline.ShapeDueReviews(rows, now), nil
}
