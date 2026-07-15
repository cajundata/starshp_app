package appapi

import (
	"testing"

	"github.com/cajundata/starshp_app/internal/provider"
)

// overrideAPI returns an API with one conversation holding one persisted turn.
func overrideAPI(t *testing.T) (*API, string, string) {
	t.Helper()
	a := newPersonaAPI(t, map[string]string{
		"scout.md": "---\nname: Scout\nmodel: gpt-5\n---\nYou are Scout.\n",
	})
	c, err := a.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	u, err := a.st.AppendUserMessage(c.ID, "q1")
	if err != nil {
		t.Fatal(err)
	}
	return a, c.ID, u.TurnID
}

// An invalid state is a typed config error and persists nothing.
func TestSetTurnContextOverrideRejectsInvalidState(t *testing.T) {
	a, convID, turnID := overrideAPI(t)
	err := a.SetTurnContextOverride(convID, turnID, "sometimes")
	ae, ok := err.(provider.AppError)
	if !ok {
		t.Fatalf("error is %T, want provider.AppError", err)
	}
	if ae.Code != "config" {
		t.Errorf("Code = %q, want config", ae.Code)
	}
	m, err := a.GetTurnContextOverrides(convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Errorf("overrides persisted after a rejected state: %v", m)
	}
}

// An unknown turn is a typed config error and persists nothing.
func TestSetTurnContextOverrideRejectsUnknownTurn(t *testing.T) {
	a, convID, _ := overrideAPI(t)
	err := a.SetTurnContextOverride(convID, "no-such-turn", "always")
	ae, ok := err.(provider.AppError)
	if !ok {
		t.Fatalf("error is %T, want provider.AppError", err)
	}
	if ae.Code != "config" {
		t.Errorf("Code = %q, want config", ae.Code)
	}
}

// The override map round-trips for UI seeding on conversation open, and auto
// removes a turn from it.
func TestTurnContextOverridesRoundTripOnOpen(t *testing.T) {
	a, convID, turn1 := overrideAPI(t)
	u2, err := a.st.AppendUserMessage(convID, "q2")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetTurnContextOverride(convID, turn1, "always"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetTurnContextOverride(convID, u2.TurnID, "never"); err != nil {
		t.Fatal(err)
	}
	m, err := a.GetTurnContextOverrides(convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m[turn1] != "always" || m[u2.TurnID] != "never" {
		t.Errorf("override map = %v", m)
	}
	if err := a.SetTurnContextOverride(convID, turn1, "auto"); err != nil {
		t.Fatal(err)
	}
	m, err = a.GetTurnContextOverrides(convID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m[turn1]; ok || len(m) != 1 {
		t.Errorf("after auto, override map = %v", m)
	}
}
