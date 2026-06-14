package appapi

import (
	"path/filepath"
	"testing"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

func newPipelineTestAPI(t *testing.T) *API {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewAPI(config.Config{}, st, provider.Registry{}, nil)
}

func TestCreateAndListIdeas(t *testing.T) {
	a := newPipelineTestAPI(t)
	idea, err := a.CreateIdea("Home automation", "aging-in-place", "side_business", true)
	if err != nil {
		t.Fatal(err)
	}
	if idea.ID == "" || idea.Status != "raw" {
		t.Fatalf("new idea wrong: %+v", idea)
	}
	list, err := a.ListIdeas()
	if err != nil || len(list) != 1 {
		t.Fatalf("list want 1, got %d err=%v", len(list), err)
	}
}

func TestSetIdeaStatusRejectsIllegalTransition(t *testing.T) {
	a := newPipelineTestAPI(t)
	idea, _ := a.CreateIdea("X", "", "small_project", false)
	err := a.SetIdeaStatus(idea.ID, "go", "") // raw -> go is illegal
	if err == nil {
		t.Fatal("expected error")
	}
	ae, ok := err.(provider.AppError)
	if !ok || ae.Code != "invalid_transition" {
		t.Fatalf("want invalid_transition AppError, got %#v", err)
	}
}

func TestSetIdeaStatusRequiresKillReason(t *testing.T) {
	a := newPipelineTestAPI(t)
	idea, _ := a.CreateIdea("X", "", "small_project", false)
	_ = a.SetIdeaStatus(idea.ID, "triaged", "")
	err := a.SetIdeaStatus(idea.ID, "killed", "")
	ae, ok := err.(provider.AppError)
	if !ok || ae.Code != "reason_required" {
		t.Fatalf("want reason_required AppError, got %#v", err)
	}
}

func TestReviewsDueSweep(t *testing.T) {
	a := newPipelineTestAPI(t)
	idea, _ := a.CreateIdea("Home automation", "", "side_business", false)
	if _, err := a.AddKillCriterion(idea.ID, "Paid installs", ">=2", 1000, "kill"); err != nil {
		t.Fatal(err)
	}
	due, err := a.ListReviewsDue()
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].Metric != "Paid installs" {
		t.Fatalf("due want 1 [Paid installs], got %+v", due)
	}
	if due[0].DaysOverdue <= 0 {
		t.Fatalf("expected positive days overdue, got %d", due[0].DaysOverdue)
	}
}
