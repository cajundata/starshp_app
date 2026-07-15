package store

import (
	"errors"
	"path/filepath"
	"testing"
)

// completedStoreTurn persists one full turn: user message + one completed run
// with a single assistant_text. Store-package twin of chat's completedTurn.
func completedStoreTurn(t *testing.T, st *Store, convID, runID, userText, answer string) string {
	t.Helper()
	u, err := st.AppendUserMessage(convID, userText)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRun(convID, u.TurnID, runID, "openai", "m1", "auto_grounded_default", "scout"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendAssistantText(convID, u.TurnID, runID, answer); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteRun(runID, RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}
	return u.TurnID
}

func TestSetTurnContextOverrideUpsertsAndRoundTrips(t *testing.T) {
	st := openTestStore(t)
	c, _ := st.CreateConversation("t")
	turn := completedStoreTurn(t, st, c.ID, "run-1", "q1", "a1")

	if err := st.SetTurnContextOverride(c.ID, turn, OverrideAlways); err != nil {
		t.Fatal(err)
	}
	m, err := st.GetTurnContextOverrides(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m[turn] != OverrideAlways || len(m) != 1 {
		t.Errorf("map = %v, want {%s: always}", m, turn)
	}
	// Upsert: the same turn flips to never in place.
	if err := st.SetTurnContextOverride(c.ID, turn, OverrideNever); err != nil {
		t.Fatal(err)
	}
	m, _ = st.GetTurnContextOverrides(c.ID)
	if m[turn] != OverrideNever || len(m) != 1 {
		t.Errorf("after flip, map = %v, want {%s: never}", m, turn)
	}
}

// auto is stored by deleting the row: absence IS the default. auto on a turn
// that has no row is a no-op, not an error.
func TestAutoDeletesTheRow(t *testing.T) {
	st := openTestStore(t)
	c, _ := st.CreateConversation("t")
	turn := completedStoreTurn(t, st, c.ID, "run-1", "q1", "a1")

	if err := st.SetTurnContextOverride(c.ID, turn, OverrideAlways); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTurnContextOverride(c.ID, turn, OverrideAuto); err != nil {
		t.Fatal(err)
	}
	m, err := st.GetTurnContextOverrides(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Errorf("map = %v, want empty (auto row deleted)", m)
	}
	if err := st.SetTurnContextOverride(c.ID, turn, OverrideAuto); err != nil {
		t.Errorf("auto with no existing row errored: %v", err)
	}
}

// An unknown turn — or a real turn from a different conversation — is
// rejected with ErrUnknownTurn so appapi can map it to a config error. A
// stale UI must not write rows that never match an event.
func TestSetOverrideRejectsUnknownTurn(t *testing.T) {
	st := openTestStore(t)
	c1, _ := st.CreateConversation("one")
	c2, _ := st.CreateConversation("two")
	turn1 := completedStoreTurn(t, st, c1.ID, "run-1", "q1", "a1")

	for _, state := range []string{OverrideAuto, OverrideAlways, OverrideNever} {
		if err := st.SetTurnContextOverride(c1.ID, "no-such-turn", state); !errors.Is(err, ErrUnknownTurn) {
			t.Errorf("state %q, bogus turn: err = %v, want ErrUnknownTurn", state, err)
		}
		if err := st.SetTurnContextOverride(c2.ID, turn1, state); !errors.Is(err, ErrUnknownTurn) {
			t.Errorf("state %q, cross-conversation turn: err = %v, want ErrUnknownTurn", state, err)
		}
	}
	m, _ := st.GetTurnContextOverrides(c1.ID)
	if len(m) != 0 {
		t.Errorf("rows persisted after rejected writes: %v", m)
	}
}

func TestSetOverrideRejectsInvalidState(t *testing.T) {
	st := openTestStore(t)
	c, _ := st.CreateConversation("t")
	turn := completedStoreTurn(t, st, c.ID, "run-1", "q1", "a1")

	err := st.SetTurnContextOverride(c.ID, turn, "sometimes")
	if err == nil {
		t.Fatal("invalid state accepted")
	}
	if errors.Is(err, ErrUnknownTurn) {
		t.Error("invalid state misreported as unknown turn")
	}
}

// Conversation deletion removes override rows via ON DELETE CASCADE — the
// same convention every other conversation-scoped table follows; no
// hand-written cleanup code. Counted directly in the table: the map getter
// cannot distinguish "cascaded" from "conversation gone".
func TestConversationDeleteCascadesOverrideRows(t *testing.T) {
	st := openTestStore(t)
	c, _ := st.CreateConversation("t")
	turn := completedStoreTurn(t, st, c.ID, "run-1", "q1", "a1")
	if err := st.SetTurnContextOverride(c.ID, turn, OverrideNever); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteConversation(c.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM turn_context_overrides`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("override rows after conversation delete = %d, want 0", n)
	}
}

// schemaSQL runs on every Open; the table creation must be idempotent and
// the data must survive a reopen (overrides survive an app restart).
func TestOverridesSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := st.CreateConversation("t")
	turn := completedStoreTurn(t, st, c.ID, "run-1", "q1", "a1")
	if err := st.SetTurnContextOverride(c.ID, turn, OverrideAlways); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := Open(path) // schema + migrate run a second time
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	m, err := st2.GetTurnContextOverrides(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m[turn] != OverrideAlways {
		t.Errorf("after reopen, map = %v, want {%s: always}", m, turn)
	}
}

// The never filter applies only on the provider replay path: the turn's
// user_message and its run events vanish from the payload while the
// displayed thread keeps the full history (rule 1: payload only).
func TestNeverTurnAbsentFromProviderReplayButPresentInDisplay(t *testing.T) {
	st := openTestStore(t)
	c, _ := st.CreateConversation("t")
	completedStoreTurn(t, st, c.ID, "run-1", "q1", "a1")
	turn2 := completedStoreTurn(t, st, c.ID, "run-2", "q2-heavy", "a2-heavy")
	completedStoreTurn(t, st, c.ID, "run-3", "q3", "a3")
	if err := st.SetTurnContextOverride(c.ID, turn2, OverrideNever); err != nil {
		t.Fatal(err)
	}

	replay, err := st.GetProviderReplayEvents(c.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range replay {
		if e.TurnID == turn2 {
			t.Errorf("never turn leaked into provider replay: kind=%s text=%q", e.Kind, e.Text)
		}
	}
	var texts []string
	for _, e := range replay {
		texts = append(texts, e.Text)
	}
	// Everything else survives, in order.
	wantPresent := []string{"q1", "a1", "q3", "a3"}
	for _, w := range wantPresent {
		found := false
		for _, txt := range texts {
			if txt == w {
				found = true
			}
		}
		if !found {
			t.Errorf("replay lost %q; got %v", w, texts)
		}
	}

	display, err := st.GetConversationDisplayEvents(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawUser, sawAssistant bool
	for _, e := range display {
		if e.TurnID != turn2 {
			continue
		}
		if e.Kind == EventKindUserMessage {
			sawUser = true
		}
		if e.Kind == EventKindAssistantText {
			sawAssistant = true
		}
	}
	if !sawUser || !sawAssistant {
		t.Errorf("display lost the never turn (user=%v assistant=%v) — overrides must be payload-only", sawUser, sawAssistant)
	}
}

// Rule 2: a turn currently being run is exempt from its own never. A rerun
// of a never turn still includes that turn's own user message as its prompt —
// the override governs the turn as history, never the turn being answered.
func TestRerunOfANeverTurnStillSeesItsOwnUserMessage(t *testing.T) {
	st := openTestStore(t)
	c, _ := st.CreateConversation("t")
	completedStoreTurn(t, st, c.ID, "run-1", "q1", "a1")
	turn2 := completedStoreTurn(t, st, c.ID, "run-2", "q2", "a2")
	if err := st.SetTurnContextOverride(c.ID, turn2, OverrideNever); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRun(c.ID, turn2, "rerun-1", "openai", "m1", "auto_grounded_default", "scout"); err != nil {
		t.Fatal(err)
	}

	replay, err := st.GetProviderReplayEvents(c.ID, "rerun-1")
	if err != nil {
		t.Fatal(err)
	}
	sawOwnPrompt := false
	for _, e := range replay {
		if e.TurnID == turn2 && e.Kind == EventKindUserMessage && e.Text == "q2" {
			sawOwnPrompt = true
		}
	}
	if !sawOwnPrompt {
		t.Errorf("rerun of a never turn lost its own prompt; replay = %+v", replay)
	}
}

// Spec error table: every turn marked never → the payload is the new user
// message alone. Legal, not an error.
func TestEveryTurnNeverLeavesOnlyTheCurrentUserMessage(t *testing.T) {
	st := openTestStore(t)
	c, _ := st.CreateConversation("t")
	t1 := completedStoreTurn(t, st, c.ID, "run-1", "q1", "a1")
	t2 := completedStoreTurn(t, st, c.ID, "run-2", "q2", "a2")
	for _, turn := range []string{t1, t2} {
		if err := st.SetTurnContextOverride(c.ID, turn, OverrideNever); err != nil {
			t.Fatal(err)
		}
	}
	u, err := st.AppendUserMessage(c.ID, "q3")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRun(c.ID, u.TurnID, "run-3", "openai", "m1", "auto_grounded_default", "scout"); err != nil {
		t.Fatal(err)
	}

	replay, err := st.GetProviderReplayEvents(c.ID, "run-3")
	if err != nil {
		t.Fatal(err)
	}
	if len(replay) != 1 || replay[0].Kind != EventKindUserMessage || replay[0].Text != "q3" {
		t.Errorf("replay = %+v, want exactly the new user message", replay)
	}
}

// ContextOverride rides along on events the way PersonaID and Model do:
// "always" and "never" from the joined row, "" when no row exists.
func TestContextOverrideRidesAlongOnEvents(t *testing.T) {
	st := openTestStore(t)
	c, _ := st.CreateConversation("t")
	t1 := completedStoreTurn(t, st, c.ID, "run-1", "q1", "a1")
	completedStoreTurn(t, st, c.ID, "run-2", "q2", "a2")
	if err := st.SetTurnContextOverride(c.ID, t1, OverrideAlways); err != nil {
		t.Fatal(err)
	}

	replay, err := st.GetProviderReplayEvents(c.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range replay {
		if e.TurnID == t1 && e.ContextOverride != OverrideAlways {
			t.Errorf("turn1 %s event ContextOverride = %q, want always", e.Kind, e.ContextOverride)
		}
		if e.TurnID != t1 && e.ContextOverride != "" {
			t.Errorf("unmarked turn event ContextOverride = %q, want empty", e.ContextOverride)
		}
	}
}
