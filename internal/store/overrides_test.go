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
