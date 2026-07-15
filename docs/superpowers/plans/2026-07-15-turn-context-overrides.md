# Turn Context Overrides Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the operator a per-turn tri-state control (`auto` / `always` / `never`) over what each send replays to the model: pin a turn so it always survives persona boundaries, or exclude a turn so it stops being re-sent — without ever changing the displayed thread.

**Architecture:** A new `turn_context_overrides` table holds only the exceptions (`auto` = row absence). The `never` filter applies **only** on the `GetProviderReplayEvents` path, threaded through the shared store helpers as a provider-path-only parameter (the display path passes `nil`). `ConversationEvent` gains a `ContextOverride` ride-along field (same pattern as `PersonaID`/`Model`), which `chat.canonicalEvents` reads to fold an `always`-pinned foreign turn into an attributed user-role block at any distance. Two new Wails bound methods (`SetTurnContextOverride`, `GetTurnContextOverrides`) drive a hover control anchored on each turn's user bubble.

**Tech Stack:** Go 1.x (stdlib + modernc.org/sqlite + `github.com/google/uuid`, all already in go.mod), TypeScript + Vite frontend, Wails v2 bindings (**regenerated** — two new bound methods).

**Spec:** `docs/superpowers/specs/2026-07-14-turn-context-overrides-design.md`

## Global Constraints

- `internal/rag/{chunker,embedding,ragindex}/` are verbatim copies of acctutor — **never modify them**; verify formatting with targeted `gofmt -l` on touched dirs only, never repo-wide.
- `internal/provider` is **not modified**. Spec 2's mention parsing and routing are **not modified**.
- **The regression guard comes first:** a conversation with zero override rows must produce a **byte-identical provider payload** to Spec 2's output. Everything else is secondary to this (Task 1).
- **Payload only:** display events never consult overrides; the thread always shows the full history. The `never` filter must arrive at the shared store helpers (`turnSelection`, `eventsForRunsPlusUserMessages`) as a provider-path-only parameter — never unconditionally inside them.
- `auto` is stored by **deleting** the row; row absence *is* the default state.
- The table SQL is verbatim from the spec: `turn_id` is the PRIMARY KEY (turn IDs are globally unique — a turn's ID is its `user_message` event's ID); `conversation_id` carries `REFERENCES conversations(id) ON DELETE CASCADE`; `state` is CHECKed to `('always','never')`.
- **A turn currently being run is exempt** from its own `never`: a rerun of a `never` turn still includes that turn's own user message as its prompt. The override governs the turn *as history*, never the turn being answered.
- Foreign tool blocks are dropped from every attributed block, pinned or not — Spec 2's dangling-ID reasoning is unchanged.
- Every error crossing `appapi` is a typed `provider.AppError`; invalid state or unknown turn → `Code: "config"`, nothing persisted.
- Wails bindings **are regenerated this time** (`wails generate module` — the CLI is at `~/go/bin/wails`). If generation flips `frontend/wailsjs/go/*` to mode 755, `chmod 644` them before staging.
- The working tree currently carries uncommitted `frontend/dist/` and `frontend/wailsjs/` drift from earlier work — reconcile it (commit separately or stash) **before** Task 1 so every Spec 3 commit stays atomic.
- Branch: work on the repo mainline once `fix/accounting-remnants` has merged (solo repo; each task commits atomically).

## File Structure

| File | Role |
|---|---|
| `internal/chat/context_override_test.go` (create) | Task 1: byte-identical zero-override guard (`spec2Canonical` reference); Task 4: override assembly tests |
| `internal/store/schema.go` (modify) | `turn_context_overrides` table (idempotent `CREATE TABLE IF NOT EXISTS`, runs on every `Open`) |
| `internal/store/overrides.go` (create) | Override state constants, `ErrUnknownTurn`, `SetTurnContextOverride`, `GetTurnContextOverrides`, `neverTurnsForReplay` |
| `internal/store/overrides_test.go` (create) | CRUD/upsert, cascade, reopen-idempotence, replay-filter tests |
| `internal/store/events.go` (modify) | `ConversationEvent.ContextOverride` ride-along field |
| `internal/store/replay.go` (modify) | `exclude` parameter through both shared helpers; override LEFT JOIN |
| `internal/chat/chat.go` (modify) | `canonicalEvents` honors `ContextOverride` (in-place pin fold, defensive never-skip) |
| `internal/appapi/api.go` (modify) | Bound methods `SetTurnContextOverride` / `GetTurnContextOverrides` |
| `internal/appapi/override_test.go` (create) | Typed-error and round-trip tests |
| `frontend/wailsjs/go/appapi/API.js`, `API.d.ts` (regenerate) | Bindings for the two new methods |
| `frontend/src/main.ts` (modify) | Hover cycle control, override map load on open, pin glyph, dimming |
| `frontend/src/style.css` (modify) | `.ctx-btn`, `.ctx-excluded`, `.pin-glyph` rules |
| `docs/SMOKE.md` (modify) | Manual steps 60–64 |

---

### Task 1: The regression guard — zero overrides is byte-identical to Spec 2

The spec's "first test to write." Nothing in the codebase changes in this task; the guard pins Spec 2's exact payload behavior so Tasks 2–4 cannot silently drift it. Unlike a feature test, this is **expected to pass immediately** — its job is to stay green for the rest of the plan (and forever).

**Files:**
- Test: `internal/chat/context_override_test.go` (create)

**Interfaces:**
- Consumes: existing package-`chat` test helpers — `openStore(t)` (`chat_test.go:42`), `completedTurn` / `currentTurn` / `marshalEvents` / `stubNamer` (`canonical_events_test.go`); `canonicalEvents(rows, currentTurnID, currentPersonaID, namer)` (`chat.go:465` — signature unchanged by this feature).
- Produces: `spec2Canonical` and `spec2Predecessor` — the frozen Spec 2 reference implementations Task 4's work is measured against. Task 4 appends its tests to this same file.

- [ ] **Step 1: Write the guard**

Create `internal/chat/context_override_test.go`:

```go
package chat

import (
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

// spec2Canonical is Spec 2's persona-aware assembly, inlined verbatim as the
// byte-identical reference for Spec 3 (turn context overrides). A conversation
// with zero override rows must replay exactly as Spec 2 shipped it — row
// absence IS auto, so this guard is structural, not logical. If
// canonicalEvents with no overrides diverges from this, an existing
// conversation replays differently: the one failure this feature must never
// cause. Do not update this copy when chat.go changes — that is the point.
func spec2Canonical(rows []store.ConversationEvent, currentTurnID, currentPersonaID string, namer PersonaNamer) []provider.Event {
	predecessor := spec2Predecessor(rows, currentTurnID)
	out := make([]provider.Event, 0, len(rows))
	var batonTexts []string
	var batonPersona, batonModel string
	flushBaton := func() {
		if len(batonTexts) == 0 {
			return
		}
		name := batonPersona
		if namer != nil {
			if n, ok := namer.Name(batonPersona); ok {
				name = n
			}
		}
		out = append(out, provider.Event{
			Kind: store.EventKindUserMessage,
			Text: "From " + name + " (" + batonModel + "):\n" + strings.Join(batonTexts, "\n\n"),
		})
		batonTexts = nil
	}
	for _, r := range rows {
		if r.TurnID == currentTurnID {
			flushBaton()
		}
		foreign := r.PersonaID != "" && r.PersonaID != currentPersonaID
		switch {
		case r.Kind == store.EventKindUserMessage || !foreign:
			out = append(out, provider.Event{
				Kind: r.Kind, Text: r.Text,
				ToolCallID: r.ToolCallID, ToolName: r.ToolName,
				ToolInput: r.ToolInput, IsError: r.IsError,
			})
		case r.TurnID == predecessor && r.Kind == store.EventKindAssistantText:
			if len(batonTexts) == 0 {
				batonPersona, batonModel = r.PersonaID, r.Model
			}
			batonTexts = append(batonTexts, r.Text)
		}
	}
	flushBaton()
	return out
}

func spec2Predecessor(rows []store.ConversationEvent, currentTurnID string) string {
	prev := ""
	for _, r := range rows {
		if r.Kind != store.EventKindUserMessage {
			continue
		}
		if r.TurnID == currentTurnID {
			return prev
		}
		prev = r.TurnID
	}
	return ""
}

// TestZeroOverridesIsByteIdenticalToSpec2 re-runs Spec 2's payload fixtures —
// single-persona multi-turn with tools, the Scout → Skeptic → Scout thread,
// and legacy no-persona runs — through the full store → chat pipeline with
// zero override rows, and requires byte-identical provider payloads against
// the frozen Spec 2 reference. The spec's first test; everything else in
// Spec 3 is secondary to keeping this green.
func TestZeroOverridesIsByteIdenticalToSpec2(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T, st *store.Store, convID string) (turnID, runID, personaID string)
	}{
		{"single-persona multi-turn with tools", func(t *testing.T, st *store.Store, convID string) (string, string, string) {
			completedTurn(t, st, convID, "q1", "scout", "m1", true, "a1")
			completedTurn(t, st, convID, "q2", "scout", "m1", false, "a2")
			turnID, runID := currentTurn(t, st, convID, "q3", "scout", "m1")
			return turnID, runID, "scout"
		}},
		{"scout-skeptic-scout thread", func(t *testing.T, st *store.Store, convID string) (string, string, string) {
			completedTurn(t, st, convID, "find the angles", "scout", "m-scout", true, "scout answer")
			completedTurn(t, st, convID, "@skeptic poke holes", "skeptic", "m-skeptic", true, "skeptic critique")
			turnID, runID := currentTurn(t, st, convID, "respond to that", "scout", "m-scout")
			return turnID, runID, "scout"
		}},
		{"legacy no-persona runs", func(t *testing.T, st *store.Store, convID string) (string, string, string) {
			completedTurn(t, st, convID, "q1", "", "m1", true, "a1")
			completedTurn(t, st, convID, "q2", "", "m1", false, "a2")
			turnID, runID := currentTurn(t, st, convID, "q3", "scout", "m2")
			return turnID, runID, "scout"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t)
			conv, err := st.CreateConversation("guard")
			if err != nil {
				t.Fatal(err)
			}
			turnID, runID, personaID := tc.build(t, st, conv.ID)
			rows, err := st.GetProviderReplayEvents(conv.ID, runID)
			if err != nil {
				t.Fatal(err)
			}
			namer := stubNamer{"skeptic": "Skeptic", "scout": "Scout"}
			got := marshalEvents(t, canonicalEvents(rows, turnID, personaID, namer))
			want := marshalEvents(t, spec2Canonical(rows, turnID, personaID, namer))
			if got != want {
				t.Errorf("zero-override payload diverged from Spec 2:\n got %s\nwant %s", got, want)
			}
		})
	}
}
```

- [ ] **Step 2: Run it — must PASS immediately**

Run: `go test ./internal/chat/ -run TestZeroOverridesIsByteIdenticalToSpec2 -v`
Expected: PASS (all three subtests). This guard is green from day one by construction (`canonicalEvents` currently *is* `spec2Canonical`); it exists to stay green through Tasks 2–4, which touch both sides of the pipeline it exercises.

- [ ] **Step 3: Run the whole chat package to confirm no interference**

Run: `go test ./internal/chat/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
gofmt -l internal/chat  # must print nothing
git add internal/chat/context_override_test.go
git commit -m "test(chat): Spec 3 regression guard — zero overrides replay byte-identical to Spec 2"
```

---

### Task 2: Store — the `turn_context_overrides` table and its CRUD

**Files:**
- Modify: `internal/store/schema.go` (append table after the `runs_one_active_per_turn` index, line 70–71)
- Create: `internal/store/overrides.go`
- Test: `internal/store/overrides_test.go` (create)

**Interfaces:**
- Consumes: `openTestStore(t)` (`events_test.go:13`); `Open`/`schemaSQL` idempotence (`store.go:13` — schema runs on every open, FKs are ON in the DSN); `AppendUserMessage` / `CreateRun` / `AppendAssistantText` / `CompleteRun` / `DeleteConversation`.
- Produces (used by Tasks 3–5):
  - `const OverrideAuto = "auto"`, `OverrideAlways = "always"`, `OverrideNever = "never"`
  - `var ErrUnknownTurn error`
  - `(*Store).SetTurnContextOverride(convID, turnID, state string) error`
  - `(*Store).GetTurnContextOverrides(convID string) (map[string]string, error)`
  - Test helper `completedStoreTurn(t, st, convID, runID, userText, answer string) string`

- [ ] **Step 1: Write the failing tests**

Create `internal/store/overrides_test.go`:

```go
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

	if err := st.SetTurnContextOverride(c1.ID, "no-such-turn", OverrideAlways); !errors.Is(err, ErrUnknownTurn) {
		t.Errorf("bogus turn: err = %v, want ErrUnknownTurn", err)
	}
	if err := st.SetTurnContextOverride(c2.ID, turn1, OverrideAlways); !errors.Is(err, ErrUnknownTurn) {
		t.Errorf("cross-conversation turn: err = %v, want ErrUnknownTurn", err)
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/`
Expected: FAIL to compile — `st.SetTurnContextOverride undefined`, `OverrideAlways undefined`.

- [ ] **Step 3: Add the table to the schema**

In `internal/store/schema.go`, directly after the `runs_one_active_per_turn` index (line 70–71), insert:

```sql
CREATE TABLE IF NOT EXISTS turn_context_overrides (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    turn_id         TEXT NOT NULL PRIMARY KEY,
    state           TEXT NOT NULL CHECK (state IN ('always','never'))
);
```

(Verbatim from the spec. No `migrate.go` change: `schemaSQL` already runs idempotently on every `Open`, and `foreign_keys(on)` is already in the DSN, so the cascade is live.)

- [ ] **Step 4: Implement the CRUD**

Create `internal/store/overrides.go`:

```go
package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// Override states for a turn's contribution to the provider payload. auto is
// the absence of a row — it is "stored" by deleting — so only always and
// never persist (see turn_context_overrides in schema.go). Overrides shape
// what the model sees, never what the operator sees: the display path does
// not consult them.
const (
	OverrideAuto   = "auto"
	OverrideAlways = "always"
	OverrideNever  = "never"
)

// ErrUnknownTurn reports an override write against a turn that does not
// exist in the given conversation. appapi maps it to AppError{Code:"config"}.
var ErrUnknownTurn = errors.New("unknown turn")

// SetTurnContextOverride records the operator's per-turn context override.
// auto deletes the row; always/never upsert. The turn must be a user_message
// event of convID — turn IDs are user_message event IDs, so this also rejects
// a turn that belongs to another conversation.
func (s *Store) SetTurnContextOverride(convID, turnID, state string) error {
	switch state {
	case OverrideAuto:
		_, err := s.db.Exec(
			`DELETE FROM turn_context_overrides WHERE turn_id = ?`, turnID)
		return err
	case OverrideAlways, OverrideNever:
	default:
		return fmt.Errorf("invalid override state %q", state)
	}
	var id string
	err := s.db.QueryRow(
		`SELECT id FROM conversation_events
          WHERE id = ? AND conversation_id = ? AND kind = 'user_message'`,
		turnID, convID).Scan(&id)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: %s", ErrUnknownTurn, turnID)
	}
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO turn_context_overrides (conversation_id, turn_id, state)
         VALUES (?,?,?)
         ON CONFLICT(turn_id) DO UPDATE SET state = excluded.state`,
		convID, turnID, state)
	return err
}

// GetTurnContextOverrides returns turn → state for every override row in the
// conversation, for UI seeding on conversation open. Turns in auto have no
// row and are absent from the map.
func (s *Store) GetTurnContextOverrides(convID string) (map[string]string, error) {
	rows, err := s.db.Query(
		`SELECT turn_id, state FROM turn_context_overrides
          WHERE conversation_id = ?`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var turn, state string
		if err := rows.Scan(&turn, &state); err != nil {
			return nil, err
		}
		out[turn] = state
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/store/`
Expected: PASS — all new tests plus the full pre-existing store suite.

- [ ] **Step 6: Commit**

```bash
gofmt -l internal/store  # must print nothing
git add internal/store/schema.go internal/store/overrides.go internal/store/overrides_test.go
git commit -m "feat(store): turn_context_overrides table with tri-state upsert"
```

---

### Task 3: Store — provider-path `never` filter + `ContextOverride` ride-along

The one behavior change in the store: on the provider replay path, a `never` turn's run events **and its `user_message`** are dropped (a dangling question invites a re-answer, so the whole exchange goes). The display path is untouched — the filter arrives as a parameter both shared helpers receive, `nil` on the display path.

**Files:**
- Modify: `internal/store/events.go` (add field to `ConversationEvent`, line 20–38)
- Modify: `internal/store/replay.go` (all four functions)
- Modify: `internal/store/overrides.go` (add `neverTurnsForReplay`)
- Test: `internal/store/overrides_test.go` (append)

**Interfaces:**
- Consumes: Task 2's table, constants, and `completedStoreTurn` helper.
- Produces (used by Task 4):
  - `ConversationEvent.ContextOverride string` — `""` for auto (no row), else `"always"`/`"never"`, joined onto **both** display and provider events (the ride-along pattern of `PersonaID`/`Model`; display code simply never reads it).
  - `GetProviderReplayEvents` / `GetConversationDisplayEvents` keep their exact public signatures.
  - Private: `turnSelection(convID, sqlOrderedRuns, currentRunID string, exclude map[string]struct{})`, `eventsForRunsPlusUserMessages(convID string, runIDs []string, currentRunID string, exclude map[string]struct{})`, `(*Store).neverTurnsForReplay(convID, currentRunID string) (map[string]struct{}, error)`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/overrides_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/`
Expected: FAIL to compile — `e.ContextOverride undefined`. (After Step 3's field lands, the behavior tests fail on assertions instead.)

- [ ] **Step 3: Add the ride-along field**

In `internal/store/events.go`, add one field to `ConversationEvent` after `Model` (line 26):

```go
	ContextOverride string      `json:"contextOverride,omitempty"`
```

(Run `gofmt` — it re-aligns the struct tags.)

- [ ] **Step 4: Add the replay-path exclusion set**

Append to `internal/store/overrides.go`:

```go
// neverTurnsForReplay returns the turns excluded from the provider payload:
// every turn marked never, except the current run's own turn — an override
// governs the turn as history for later turns, never the turn being answered
// (rule 2: a rerun of a never turn still gets its own user message as its
// prompt). Only GetProviderReplayEvents calls this; the display path never
// consults overrides (rule 1).
func (s *Store) neverTurnsForReplay(convID, currentRunID string) (map[string]struct{}, error) {
	rows, err := s.db.Query(
		`SELECT turn_id FROM turn_context_overrides
          WHERE conversation_id = ? AND state = 'never'`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	exclude := map[string]struct{}{}
	for rows.Next() {
		var turn string
		if err := rows.Scan(&turn); err != nil {
			return nil, err
		}
		exclude[turn] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(exclude) == 0 || currentRunID == "" {
		return exclude, nil
	}
	var current string
	err = s.db.QueryRow(`SELECT turn_id FROM runs WHERE id = ?`, currentRunID).Scan(&current)
	if err == sql.ErrNoRows {
		return exclude, nil
	}
	if err != nil {
		return nil, err
	}
	delete(exclude, current)
	return exclude, nil
}
```

- [ ] **Step 5: Thread the filter through `replay.go`**

In `internal/store/replay.go`:

**5a.** `turnSelection` (line 9) gains the `exclude` parameter and skips excluded turns:

```go
// turnSelection picks one run per turn. exclude lists turns dropped from the
// provider payload (state='never'); it is nil on the display path — the
// filter is a provider-path-only parameter, never a shared-helper default.
func (s *Store) turnSelection(convID, sqlOrderedRuns string, currentRunID string, exclude map[string]struct{}) ([]string, error) {
```

and inside the `for _, turn := range turns` loop, first thing:

```go
	for _, turn := range turns {
		if _, skip := exclude[turn]; skip {
			continue
		}
```

(The rest of the loop body is unchanged.)

**5b.** `GetProviderReplayEvents` (line 55) computes the exclusion set and passes it down:

```go
func (s *Store) GetProviderReplayEvents(convID, currentRunID string) ([]ConversationEvent, error) {
	exclude, err := s.neverTurnsForReplay(convID, currentRunID)
	if err != nil {
		return nil, fmt.Errorf("override selection: %w", err)
	}
	runs, err := s.turnSelection(convID,
		`SELECT id FROM runs
          WHERE turn_id = ? AND active_for_replay = 1 AND status = 'completed'
          LIMIT 1`, currentRunID, exclude)
	if err != nil {
		return nil, fmt.Errorf("provider replay selection: %w", err)
	}
	return s.eventsForRunsPlusUserMessages(convID, runs, currentRunID, exclude)
}
```

**5c.** `GetConversationDisplayEvents` (line 66) passes `nil` at both call sites. The `turnSelection` call becomes (the SQL is unchanged; only the trailing arguments change):

```go
	runs, err := s.turnSelection(convID,
		`SELECT id FROM runs
          WHERE turn_id = ?
          ORDER BY active_for_replay DESC,
                   CASE status
                       WHEN 'completed' THEN 0
                       WHEN 'cancelled' THEN 1
                       WHEN 'errored'   THEN 2
                       WHEN 'in_progress' THEN 3
                   END,
                   COALESCE(ended_at, 0) DESC,
                   started_at DESC
          LIMIT 1`, "", nil)
```

and the events call becomes:

```go
	events, err := s.eventsForRunsPlusUserMessages(convID, runs, "", nil)
```

**5d.** `eventsForRunsPlusUserMessages` (line 118): new parameter, override join, scan, and the `user_message` drop — the full function body:

```go
func (s *Store) eventsForRunsPlusUserMessages(convID string, runIDs []string, currentRunID string, exclude map[string]struct{}) ([]ConversationEvent, error) {
	runSet := map[string]struct{}{}
	for _, id := range runIDs {
		runSet[id] = struct{}{}
	}
	if currentRunID != "" {
		runSet[currentRunID] = struct{}{}
	}
	// turn_context_overrides has turn_id as its PRIMARY KEY, so the LEFT JOIN
	// contributes at most one row per event — no fan-out.
	rows, err := s.db.Query(
		`SELECT e.id, e.conversation_id, e.turn_id, COALESCE(e.run_id,''),
                e.sequence_index, e.kind, COALESCE(e.text,''),
                COALESCE(e.tool_call_id,''), COALESCE(e.tool_name,''),
                COALESCE(e.tool_input,''), COALESCE(e.tool_metadata,''),
                COALESCE(e.tool_result_hash,''),
                COALESCE(e.tool_latency_ms,0), e.is_error, e.created_at,
                COALESCE(r.persona_id,''), COALESCE(r.model,''),
                COALESCE(o.state,'')
           FROM conversation_events e
           LEFT JOIN runs r ON r.id = e.run_id
           LEFT JOIN turn_context_overrides o ON o.turn_id = e.turn_id
          WHERE e.conversation_id = ?
          ORDER BY e.sequence_index`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConversationEvent
	for rows.Next() {
		var ev ConversationEvent
		var input, meta string
		var isErrInt int
		if err := rows.Scan(
			&ev.ID, &ev.ConversationID, &ev.TurnID, &ev.RunID,
			&ev.SequenceIndex, &ev.Kind, &ev.Text,
			&ev.ToolCallID, &ev.ToolName, &input, &meta,
			&ev.ToolResultHash, &ev.ToolLatencyMs, &isErrInt, &ev.CreatedAt,
			&ev.PersonaID, &ev.Model, &ev.ContextOverride,
		); err != nil {
			return nil, err
		}
		if input != "" {
			ev.ToolInput = json.RawMessage(input)
		}
		if meta != "" {
			ev.ToolMetadata = json.RawMessage(meta)
		}
		ev.IsError = isErrInt != 0
		if ev.Kind == EventKindUserMessage {
			// A never turn's user_message goes too — a dangling question
			// invites the model to re-answer it. exclude is nil on the
			// display path, so this drop is provider-path-only.
			if _, skip := exclude[ev.TurnID]; skip {
				continue
			}
			out = append(out, ev)
			continue
		}
		if _, ok := runSet[ev.RunID]; ok {
			out = append(out, ev)
		}
	}
	return out, rows.Err()
}
```

- [ ] **Step 6: Run tests to verify they pass — store, chat guard, and the full suite**

Run: `go test ./internal/store/ ./internal/chat/`
Expected: PASS — the new filter tests, the entire pre-existing store suite (display-path tests prove nothing changed there), and Task 1's byte-identical guard (zero rows → empty exclusion set → identical selection and payload).

- [ ] **Step 7: Commit**

```bash
gofmt -l internal/store  # must print nothing
git add internal/store
git commit -m "feat(store): provider-path never filter and ContextOverride ride-along"
```

---

### Task 4: Chat — `canonicalEvents` honors `ContextOverride`

The decision table gains its leading column: `never` → skip (defensively; the store already filtered); `always` on a foreign turn → fold into the attributed block at any distance, in place; `always` on an own-persona or pre-persona turn → verbatim (a forward guarantee, not a format change); `auto`/empty → Spec 2's rules unchanged. `chat` still does not know what a persona is — `ContextOverride` is a string on the event; the Spec 1 boundary holds.

**Files:**
- Modify: `internal/chat/chat.go` (`canonicalEvents` body, line 465–511)
- Test: `internal/chat/context_override_test.go` (append)

**Interfaces:**
- Consumes: `ConversationEvent.ContextOverride` and `store.OverrideAlways`/`store.OverrideNever` from Tasks 2–3; Task 1's guard and helpers; `completedTurn`/`currentTurn`/`stubNamer`/`countFromBlocks`/`marshalEvents` from `canonical_events_test.go`.
- Produces: the final assembly behavior. `canonicalEvents`' signature is unchanged — no call-site edits anywhere.

- [ ] **Step 1: Write the failing tests**

Append to `internal/chat/context_override_test.go` (add `"reflect"` to its imports):

```go
// always on a non-adjacent foreign turn produces the attributed block, in
// place, with tool blocks absent — Spec 2's immediate-predecessor treatment
// extended to any position. Multiple assistant_texts join into ONE block.
func TestAlwaysPinsANonAdjacentForeignTurn(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	pinned := completedTurn(t, st, conv.ID, "@skeptic scan this", "skeptic", "m-skeptic", true, "part one", "part two")
	completedTurn(t, st, conv.ID, "thanks", "scout", "m-scout", false, "scout ack")
	turnID, runID := currentTurn(t, st, conv.ID, "now use the findings", "scout", "m-scout")
	if err := st.SetTurnContextOverride(conv.ID, pinned, store.OverrideAlways); err != nil {
		t.Fatal(err)
	}

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "scout", stubNamer{"skeptic": "Skeptic"})

	kinds := make([]string, len(got))
	for i, e := range got {
		kinds[i] = e.Kind
	}
	want := []string{
		"user_message", // @skeptic scan this
		"user_message", // pinned Skeptic block, folded in place
		"user_message", // thanks
		"assistant_text", // scout ack (own voice, verbatim)
		"user_message", // now use the findings
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	if got[1].Text != "From Skeptic (m-skeptic):\npart one\n\npart two" {
		t.Errorf("pinned block = %q", got[1].Text)
	}
	// A foreign persona's tool events never appear in any payload, pinned or
	// not — Spec 2's dangling-ID reasoning is unchanged by pinning.
	for _, e := range got {
		if e.Kind == "assistant_tool_call" || e.Kind == "tool_result" {
			t.Errorf("foreign tool event leaked into a pinned payload: %+v", e)
		}
	}
	if n := countFromBlocks(got); n != 1 {
		t.Errorf("From-blocks = %d, want exactly 1", n)
	}
}

// always on the immediate predecessor must not duplicate it: the baton path
// already folds that turn, and the pin is satisfied by the same block.
func TestAlwaysOnTheImmediatePredecessorDoesNotDuplicate(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "q1", "scout", "m-scout", false, "scout one")
	pinned := completedTurn(t, st, conv.ID, "@skeptic poke", "skeptic", "m-skeptic", false, "skeptic critique")
	turnID, runID := currentTurn(t, st, conv.ID, "respond", "scout", "m-scout")
	if err := st.SetTurnContextOverride(conv.ID, pinned, store.OverrideAlways); err != nil {
		t.Fatal(err)
	}

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "scout", stubNamer{"skeptic": "Skeptic"})
	if n := countFromBlocks(got); n != 1 {
		t.Fatalf("From-blocks = %d, want exactly 1 (no double inclusion)", n)
	}
	found := false
	for _, e := range got {
		if e.Text == "From Skeptic (m-skeptic):\nskeptic critique" {
			found = true
		}
	}
	if !found {
		t.Errorf("predecessor baton missing from %+v", got)
	}
}

// never on the immediate predecessor means the next persona gets no baton —
// identical to Spec 2's errored-predecessor case. Not an error. The whole
// exchange goes: the excluded turn's operator message vanishes too.
func TestNeverOnTheImmediatePredecessorMeansNoBaton(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "q1", "scout", "m-scout", false, "scout one")
	excluded := completedTurn(t, st, conv.ID, "@skeptic poke", "skeptic", "m-skeptic", false, "skeptic critique")
	turnID, runID := currentTurn(t, st, conv.ID, "q3", "scout", "m-scout")
	if err := st.SetTurnContextOverride(conv.ID, excluded, store.OverrideNever); err != nil {
		t.Fatal(err)
	}

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "scout", stubNamer{"skeptic": "Skeptic"})
	if n := countFromBlocks(got); n != 0 {
		t.Errorf("From-blocks = %d, want 0 (no baton to pass)", n)
	}
	for _, e := range got {
		if e.Text == "@skeptic poke" || e.Text == "skeptic critique" {
			t.Errorf("excluded turn leaked into payload: %q", e.Text)
		}
	}
}

// always on the current persona's own turn is a forward guarantee, not a
// format change: the payload is byte-identical to the same thread with no
// override. (Fixtures use no tools so tool-call IDs — derived from random
// run IDs — cannot differ between the two conversations.)
func TestAlwaysOnAnOwnPersonaTurnStaysVerbatim(t *testing.T) {
	st := openStore(t)

	build := func(name string) (convID, firstTurn, currentTurnID, runID string) {
		conv, err := st.CreateConversation(name)
		if err != nil {
			t.Fatal(err)
		}
		firstTurn = completedTurn(t, st, conv.ID, "q1", "scout", "m-scout", false, "a1")
		completedTurn(t, st, conv.ID, "q2", "scout", "m-scout", false, "a2")
		currentTurnID, runID = currentTurn(t, st, conv.ID, "q3", "scout", "m-scout")
		return conv.ID, firstTurn, currentTurnID, runID
	}

	pinConv, pinTurn, pinCurrent, pinRun := build("pinned")
	if err := st.SetTurnContextOverride(pinConv, pinTurn, store.OverrideAlways); err != nil {
		t.Fatal(err)
	}
	plainConv, _, plainCurrent, plainRun := build("plain")

	pinRows, err := st.GetProviderReplayEvents(pinConv, pinRun)
	if err != nil {
		t.Fatal(err)
	}
	plainRows, err := st.GetProviderReplayEvents(plainConv, plainRun)
	if err != nil {
		t.Fatal(err)
	}
	got := marshalEvents(t, canonicalEvents(pinRows, pinCurrent, "scout", nil))
	want := marshalEvents(t, canonicalEvents(plainRows, plainCurrent, "scout", nil))
	if got != want {
		t.Errorf("own-persona pin changed the payload:\n got %s\nwant %s", got, want)
	}
}

// Defensive never-skip: the store already filters never turns, but if a row
// slips through (constructed here by hand), canonicalEvents drops it too —
// except the current turn, which an override never governs (rule 2).
func TestChatSkipsNeverRowsDefensivelyButNeverTheCurrentTurn(t *testing.T) {
	rows := []store.ConversationEvent{
		{TurnID: "t1", Kind: store.EventKindUserMessage, Text: "q1", ContextOverride: store.OverrideNever},
		{TurnID: "t1", RunID: "r1", PersonaID: "scout", Model: "m", Kind: store.EventKindAssistantText, Text: "a1", ContextOverride: store.OverrideNever},
		{TurnID: "t2", Kind: store.EventKindUserMessage, Text: "q2", ContextOverride: store.OverrideNever},
	}
	// t2 is the current turn: its never row must NOT hide its own prompt.
	got := canonicalEvents(rows, "t2", "scout", nil)
	if len(got) != 1 || got[0].Text != "q2" {
		t.Errorf("payload = %+v, want exactly the current turn's user message", got)
	}
}
```

- [ ] **Step 2: Run tests to verify the new ones fail**

Run: `go test ./internal/chat/`
Expected: FAIL — `TestAlwaysPinsANonAdjacentForeignTurn` (skeptic's turn is omitted, no From-block), `TestChatSkipsNeverRowsDefensivelyButNeverTheCurrentTurn` (q1/a1 pass through). `TestAlwaysOnTheImmediatePredecessorDoesNotDuplicate`, `TestNeverOnTheImmediatePredecessorMeansNoBaton`, and `TestAlwaysOnAnOwnPersonaTurnStaysVerbatim` may already pass (store filtering and own-voice verbatim are live) — that is fine; they lock the behavior. Task 1's guard and every Spec 2 test still PASS.

- [ ] **Step 3: Implement the override-aware assembly**

In `internal/chat/chat.go`, replace `canonicalEvents` (line 465–511) with (`predecessorTurnID` is unchanged):

```go
// canonicalEvents builds the provider payload for the persona speaking now
// (currentPersonaID, answering currentTurnID). Own-persona and pre-persona
// rows pass through the six-field whitelist verbatim — a persona keeps its
// own voice, tool blocks included. The immediately preceding foreign turn's
// final text folds into one attributed user-role block; a foreign turn the
// operator pinned `always` gets the same treatment at any distance, folded
// in place. Tool blocks of foreign turns are always dropped — their
// provider-specific IDs would dangle in another persona's transcript and the
// receiving persona may not even have the tool in its registry. Other
// foreign turns are omitted entirely. A turn marked `never` contributes
// nothing — the store already filters it from replay; it is skipped here
// defensively too, except the current turn, which an override never governs
// (a rerun of a never turn still gets its own prompt). The operator's other
// user_message rows are always included, in order. rows arrive ordered by
// sequence_index.
func canonicalEvents(rows []store.ConversationEvent, currentTurnID, currentPersonaID string, namer PersonaNamer) []provider.Event {
	predecessor := predecessorTurnID(rows, currentTurnID)
	out := make([]provider.Event, 0, len(rows))
	attributed := func(personaID, model string, texts []string) provider.Event {
		name := personaID
		if namer != nil {
			if n, ok := namer.Name(personaID); ok {
				name = n
			}
		}
		return provider.Event{
			Kind: store.EventKindUserMessage,
			Text: "From " + name + " (" + model + "):\n" + strings.Join(texts, "\n\n"),
		}
	}
	var batonTexts []string
	var batonPersona, batonModel string
	flushBaton := func() {
		if len(batonTexts) == 0 {
			return
		}
		out = append(out, attributed(batonPersona, batonModel, batonTexts))
		batonTexts = nil
	}
	// A pinned (`always`) foreign turn folds exactly like the baton, but in
	// place: accumulated while its rows pass, flushed when they end. The
	// predecessor is handled by the baton case alone (matched first below),
	// so a pin on the predecessor cannot double-include it.
	var pinTexts []string
	var pinTurn, pinPersona, pinModel string
	flushPin := func() {
		if len(pinTexts) == 0 {
			return
		}
		out = append(out, attributed(pinPersona, pinModel, pinTexts))
		pinTexts = nil
	}
	for _, r := range rows {
		if r.TurnID != pinTurn {
			flushPin()
		}
		// The baton lands immediately before the current turn's rows, i.e.
		// right after the predecessor turn it summarizes.
		if r.TurnID == currentTurnID {
			flushBaton()
		}
		if r.ContextOverride == store.OverrideNever && r.TurnID != currentTurnID {
			continue
		}
		foreign := r.PersonaID != "" && r.PersonaID != currentPersonaID
		switch {
		case r.Kind == store.EventKindUserMessage || !foreign:
			out = append(out, provider.Event{
				Kind: r.Kind, Text: r.Text,
				ToolCallID: r.ToolCallID, ToolName: r.ToolName,
				ToolInput: r.ToolInput, IsError: r.IsError,
			})
		case r.TurnID == predecessor && r.Kind == store.EventKindAssistantText:
			if len(batonTexts) == 0 {
				batonPersona, batonModel = r.PersonaID, r.Model
			}
			batonTexts = append(batonTexts, r.Text)
		case r.ContextOverride == store.OverrideAlways && r.Kind == store.EventKindAssistantText:
			if len(pinTexts) == 0 {
				pinTurn, pinPersona, pinModel = r.TurnID, r.PersonaID, r.Model
			}
			pinTexts = append(pinTexts, r.Text)
		}
		// Any other foreign row (older unpinned turn, or any foreign tool
		// block) is omitted.
	}
	flushPin()
	flushBaton()
	return out
}
```

Note the zero-override reading of this function: `pinTexts` never accumulates and both `flushPin` calls are no-ops, so the control flow reduces exactly to Spec 2's — that is what keeps Task 1's guard green structurally, not accidentally.

- [ ] **Step 4: Run tests to verify everything passes**

Run: `go test ./internal/chat/`
Expected: PASS — all new tests, Task 1's byte-identical guard, both Spec 2 guards, the attribution-leak test, and every pre-existing chat test.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/chat  # must print nothing
git add internal/chat
git commit -m "feat(chat): canonicalEvents honors per-turn context overrides"
```

---

### Task 5: appapi — bound methods with typed errors

**Files:**
- Modify: `internal/appapi/api.go` (imports at line 5–26; new methods after `SetRetrievalMode`, line 524–526)
- Test: `internal/appapi/override_test.go` (create)

**Interfaces:**
- Consumes: `store.OverrideAuto/Always/Never`, `store.ErrUnknownTurn`, the store CRUD from Task 2; `newPersonaAPI(t, files)` (`persona_test.go:53`); `provider.AppError` / `provider.NormalizeError`.
- Produces (bound to the frontend in Task 6):
  - `(*API).SetTurnContextOverride(convID, turnID, state string) error`
  - `(*API).GetTurnContextOverrides(convID string) (map[string]string, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/appapi/override_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/appapi/`
Expected: FAIL to compile — `a.SetTurnContextOverride undefined`.

- [ ] **Step 3: Implement the bound methods**

In `internal/appapi/api.go`:

**3a.** Add `"errors"` to the stdlib import group (line 5–11).

**3b.** After `SetRetrievalMode` (line 524–526), add:

```go
// SetTurnContextOverride records the operator's per-turn payload override:
// auto (row absence), always (pin), never (exclude). Payload-only — the
// displayed thread never consults it. Unknown turn or invalid state is a
// config error; nothing is persisted.
func (a *API) SetTurnContextOverride(convID, turnID, state string) error {
	switch state {
	case store.OverrideAuto, store.OverrideAlways, store.OverrideNever:
	default:
		return provider.AppError{
			Code:        "config",
			UserMessage: "Invalid context override \"" + state + "\". Use auto, always, or never.",
			Retryable:   false,
		}
	}
	if err := a.st.SetTurnContextOverride(convID, turnID, state); err != nil {
		if errors.Is(err, store.ErrUnknownTurn) {
			return provider.AppError{
				Code:        "config",
				UserMessage: "That turn no longer exists in this conversation. Reopen it and try again.",
				Retryable:   false,
			}
		}
		return provider.NormalizeError(err)
	}
	return nil
}

// GetTurnContextOverrides returns the turn → state map for UI seeding on
// conversation open, alongside the existing event load. Turns in auto are
// absent from the map.
func (a *API) GetTurnContextOverrides(convID string) (map[string]string, error) {
	m, err := a.st.GetTurnContextOverrides(convID)
	if err != nil {
		return nil, provider.NormalizeError(err)
	}
	return m, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/appapi/`
Expected: PASS — the three new tests plus the full pre-existing appapi suite.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/appapi  # must print nothing
git add internal/appapi
git commit -m "feat(appapi): turn context override bound methods"
```

---

### Task 6: Frontend — hover cycle control, pin glyph, dimming

The frontend has no test harness; verification is `npm run build` (tsc + vite) plus Task 7's SMOKE steps. The control follows the copy button's hover-affordance pattern (`.msg-actions` reveals on `.msg:hover`, `style.css:44-45`), anchored on the turn's **user bubble** — the turn's stable anchor.

**Files:**
- Regenerate: `frontend/wailsjs/go/appapi/API.js`, `frontend/wailsjs/go/appapi/API.d.ts` (via `wails generate module`)
- Modify: `frontend/src/main.ts`
- Modify: `frontend/src/style.css` (append)

**Interfaces:**
- Consumes: `App.SetTurnContextOverride(convId, turnId, state): Promise<void>` and `App.GetTurnContextOverrides(convId): Promise<Record<string,string>>` (Task 5, via regenerated bindings); `EventDTO.turnId` (already on every display event, including the synthetic `run_error`); the `chat:run_started` payload's `turnID` field (every `chat:*` payload carries `convID`/`runID`/`turnID` — `api.go:137`); `ensureRunBubble` (`main.ts:113`), `addMsg` (`main.ts:64`), `send()` (`main.ts:398`).
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Regenerate the Wails bindings**

```bash
wails generate module
grep -n "SetTurnContextOverride\|GetTurnContextOverrides" frontend/wailsjs/go/appapi/API.d.ts
```

Expected: both functions appear, shaped like the existing entries (`API.d.ts:33,75`):

```ts
export function GetTurnContextOverrides(arg1:string):Promise<Record<string, string>>;
export function SetTurnContextOverride(arg1:string,arg2:string,arg3:string):Promise<void>;
```

If `wails generate module` fails, add exactly those two lines to `API.d.ts` (alphabetical position) and the matching pair to `API.js` (the `window['go']['appapi']['API'][...]` passthrough pattern, `API.js:49-51`) — this is precisely what the generator would emit, and the next `wails dev` reconciles it.

- [ ] **Step 2: Add the override state module to `main.ts`**

After the `COPY_ICON`/`CHECK_ICON` constants (line 78–79), add:

```ts
const PIN_ICON = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 17v5"/><path d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V6h1a2 2 0 0 0 0-4H8a2 2 0 0 0 0 4h1z"/></svg>`
const EYE_OFF_ICON = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9.88 9.88a3 3 0 1 0 4.24 4.24"/><path d="M10.73 5.08A10.43 10.43 0 0 1 12 5c7 0 10 7 10 7a13.16 13.16 0 0 1-1.67 2.68"/><path d="M6.61 6.61A13.526 13.526 0 0 0 2 12s3 7 10 7a9.74 9.74 0 0 0 5.39-1.61"/><line x1="2" y1="2" x2="22" y2="22"/></svg>`
const AUTO_ICON = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9" stroke-dasharray="4 3"/></svg>`

// ---- Turn context overrides -------------------------------------------------
// Tri-state per turn: auto (no entry) / always / never. Payload-only — the
// thread always shows the full history; the visuals mark what the model is
// guaranteed to see (pin glyph) or will not see (dimmed bubbles).
let overrides = new Map<string, string>()        // turnId → 'always' | 'never'
const turnEls = new Map<string, HTMLElement[]>() // turnId → [user bubble, run bubbles…]
let pendingUserEl: HTMLElement | null = null     // optimistic user bubble awaiting its turnId

const OVERRIDE_CYCLE: Record<string, string> = { auto: 'always', always: 'never', never: 'auto' }
const OVERRIDE_ICON: Record<string, string> = { auto: AUTO_ICON, always: PIN_ICON, never: EYE_OFF_ICON }
const OVERRIDE_TITLE: Record<string, string> = {
  auto: 'Context: auto — click to always include this turn',
  always: 'Context: always included — click to exclude',
  never: 'Context: excluded from the model — click for auto',
}

function overrideState(turnId: string): string {
  return overrides.get(turnId) || 'auto'
}

function registerTurnEl(turnId: string, el: HTMLElement) {
  if (!turnId) return
  const els = turnEls.get(turnId) || []
  if (!els.includes(el)) els.push(el)
  turnEls.set(turnId, els)
  applyOverrideVisuals(turnId)
}

// applyOverrideVisuals stamps the state onto every element of the turn:
// never dims both bubbles (text stays readable), always shows a pin glyph
// beside the model chip, auto is exactly today's rendering.
function applyOverrideVisuals(turnId: string) {
  const state = overrideState(turnId)
  for (const el of turnEls.get(turnId) || []) {
    el.classList.toggle('ctx-excluded', state === 'never')
    const btn = el.querySelector('.ctx-btn') as HTMLElement | null
    if (btn) {
      btn.innerHTML = OVERRIDE_ICON[state]
      btn.title = OVERRIDE_TITLE[state]
      btn.classList.toggle('active', state !== 'auto')
    }
    const attrib = el.querySelector('.msg-attrib')
    if (attrib) {
      let pin = attrib.querySelector('.pin-glyph') as HTMLElement | null
      if (state === 'always' && !pin) {
        pin = document.createElement('span')
        pin.className = 'pin-glyph'
        pin.innerHTML = PIN_ICON
        pin.title = 'Always included in context'
        attrib.appendChild(pin)
      } else if (state !== 'always' && pin) {
        pin.remove()
      }
    }
  }
}

// attachTurnControl adds the hover control that cycles a turn's context
// override auto → always → never → auto. Anchored on the user bubble — the
// turn's stable anchor (the assistant side may be an error or still
// streaming).
function attachTurnControl(userEl: HTMLElement, turnId: string) {
  if (!turnId || userEl.querySelector('.ctx-btn')) return
  const row = document.createElement('div')
  row.className = 'msg-actions'
  const btn = document.createElement('button')
  btn.className = 'ctx-btn'
  btn.onclick = async () => {
    const conv = activeConv
    if (!conv) return
    const next = OVERRIDE_CYCLE[overrideState(turnId)]
    try {
      await App.SetTurnContextOverride(conv, turnId, next)
    } catch (e: any) {
      alert(`Could not change the turn's context state: ${e?.userMessage || e}`)
      return
    }
    if (next === 'auto') overrides.delete(turnId)
    else overrides.set(turnId, next)
    applyOverrideVisuals(turnId)
  }
  row.appendChild(btn)
  userEl.appendChild(row)
  applyOverrideVisuals(turnId)
}
```

- [ ] **Step 3: Wire the history path (`openConversation`, line 331)**

Replace the top of `openConversation` so the override map loads alongside the event load, and stamp/register elements as they are built:

```ts
async function openConversation(id: string) {
  activeConv = id
  thread.innerHTML = ''
  runBubbles.clear()
  turnEls.clear()
  overrides = new Map(Object.entries((await App.GetTurnContextOverrides(id)) || {}))
```

In the event loop, replace the `user_message` branch:

```ts
    if (ev.kind === 'user_message') {
      const el = addMsg('user', ev.text || '')
      registerTurnEl(ev.turnId, el)
      attachTurnControl(el, ev.turnId)
      continue
    }
```

and directly after the existing `ensureRunBubble(ev.runId, ev.personaId || '', ev.modelId || '')` line, add:

```ts
    registerTurnEl(ev.turnId, runBubbles.get(ev.runId)!.el)
```

- [ ] **Step 4: Wire the live path**

In `send()` (line 398): after `const userEl = addMsg('user', text)`, add:

```ts
  pendingUserEl = userEl
```

In the same function's `catch` block, inside the existing `if (e?.code === 'config') {` branch (which removes `userEl`), add:

```ts
      pendingUserEl = null
```

and in the `finally` block, first line:

```ts
    pendingUserEl = null
```

Replace the `chat:run_started` handler (line 475–478) with:

```ts
EventsOn('chat:run_started', (p: any) => {
  if (p.convID !== activeConv) return
  const b = ensureRunBubble(p.runID, p.personaID || '', p.modelID || '')
  // Stamp the optimistic user bubble with its turn ID — the backend assigns
  // it, and run_started is the first event that carries it.
  if (p.turnID) {
    if (pendingUserEl) {
      registerTurnEl(p.turnID, pendingUserEl)
      attachTurnControl(pendingUserEl, p.turnID)
      pendingUserEl = null
    }
    registerTurnEl(p.turnID, b.el)
  }
})
```

(`ensureRunBubble` already returns the bubble; no signature change.)

- [ ] **Step 5: Style it**

Append to `frontend/src/style.css`:

```css
/* --- Turn context overrides ------------------------------------------- */
/* The cycle control shares the copy button's hover-reveal (.msg-actions). */
.ctx-btn { display: inline-flex; align-items: center; background: #202024; border: 1px solid #34343a; color: #a9a9ad; border-radius: 6px; padding: 3px 6px; cursor: pointer; }
.ctx-btn:hover { color: #e7e7e8; }
.ctx-btn.active { color: #d7a64a; border-color: #5a4a1f; }
.ctx-btn svg { display: block; }
.msg.user .msg-actions { justify-content: flex-end; }
/* Excluded turns dim but stay readable — at a glance the operator sees
   what the model will not see. The whole exchange dims: user bubble, text
   segments, and tool blocks alike. */
.msg.ctx-excluded .msg-text, .msg.ctx-excluded .tool-call, .msg.ctx-excluded .grounding-header { opacity: .45; }
/* Pinned: a small glyph beside the model chip on the turn's assistant row. */
.pin-glyph { display: inline-flex; align-items: center; color: #d7a64a; }
.pin-glyph svg { display: block; }
```

- [ ] **Step 6: Build to verify**

Run: `cd frontend && npm run build && cd ..`
Expected: `tsc` clean, vite build succeeds (regenerated `frontend/dist/` assets are expected output — this repo commits them).

- [ ] **Step 7: Commit**

```bash
ls -l frontend/wailsjs/go/appapi/  # confirm mode 644; if 755: chmod 644 frontend/wailsjs/go/appapi/* frontend/wailsjs/go/models.ts
git add frontend/src frontend/dist frontend/wailsjs
git commit -m "feat(frontend): per-turn context override control — pin glyph, dimmed exclusion"
```

---

### Task 7: SMOKE steps + full-suite verification

**Files:**
- Modify: `docs/SMOKE.md` (append after item 59, the end of the "Multi-persona threads" section)

**Interfaces:**
- Consumes: the shipped behavior of Tasks 1–6.
- Produces: the operator's manual verification checklist.

- [ ] **Step 1: Append the SMOKE section**

After item 59 in `docs/SMOKE.md`, add:

```markdown
## Turn context overrides

60. [ ] **The hover control cycles through three states.** Hover a turn's
        user bubble — a small control appears beside it. Clicking cycles
        auto → always → never → auto (dashed circle → pin → crossed eye),
        and the tooltip names the state each time. The control appears on
        every turn's user bubble, including reopened conversations.
61. [ ] **Dimming and the pin glyph render.** Set a turn to `always` — a
        gold pin glyph appears beside that turn's model chip. Set a turn to
        `never` — both its bubbles (user and assistant, tool blocks
        included) dim, with the text still readable. Return to auto — the
        conversation renders exactly as before.
62. [ ] **Occupancy visibly drops.** In a conversation with a heavy turn
        (e.g. a long tool-assisted answer), note the context footer's
        occupancy, mark that turn `never`, and send another message. The
        footer reports lower occupancy than the previous send — the lever
        the footer was waiting for.
63. [ ] **Overrides survive a restart.** Set one turn `always` and another
        `never`, quit the app (or stop `wails dev`), relaunch, and reopen
        the conversation. The pin glyph and dimming reappear on the same
        turns.
64. [ ] **The displayed thread never changes.** Toggle a turn through all
        three states — every bubble's full text, tool blocks, colors, and
        chips stay identical throughout; only the dimming/pin adornments
        change. Excluding the immediately-preceding foreign turn and sending
        again still works (the next persona simply gets no baton).
```

- [ ] **Step 2: Run the full verification suite**

```bash
go build ./... && go test ./...
gofmt -l internal/store internal/chat internal/appapi  # must print nothing
```

Expected: build clean, all packages PASS, no formatting drift in touched packages. (Do not run repo-wide `gofmt -l` — `internal/rag`'s drift is permanent and out of scope.)

- [ ] **Step 3: Commit**

```bash
git add docs/SMOKE.md
git commit -m "docs(smoke): turn context override steps"
```

---

## Spec Coverage Map (self-review)

| Spec section | Task |
|---|---|
| Regression guard: zero override rows → byte-identical to Spec 2, written first | 1 |
| Storage: exceptions-only table, `turn_id` PK, `ON DELETE CASCADE`, idempotent creation | 2 |
| `SetTurnContextOverride` upsert; `auto` deletes the row | 2 |
| `GetTurnContextOverrides` map for UI seeding | 2 (store) + 5 (bound) |
| `never` filter on the `GetProviderReplayEvents` path only; provider-path-only parameter | 3 |
| `never` drops the turn's `user_message` too (the one store behavior change) | 3 |
| Rule 1: display events never consult overrides | 3 (test) + Global Constraint |
| Rule 2: turn currently being run is exempt (rerun keeps its prompt) | 3 (store) + 4 (chat defensive) |
| Rule 3: excluding the handoff baton is legal — no baton, not an error | 4 |
| `ConversationEvent.ContextOverride` ride-along | 3 |
| Chat decision table: `never` skip / foreign `always` fold any distance / own-persona `always` verbatim / `auto` unchanged | 4 |
| No double inclusion when the pinned turn is the predecessor | 4 |
| Foreign tool blocks never appear, pinned or not | 4 |
| Every turn `never` → payload is the new user message alone | 3 |
| `always` on an errored turn → no-op, not an error (nothing to pin; operator message included) | 4 (structural: an errored turn contributes no `assistant_text`, so `pinTexts` stays empty — covered by the errored-predecessor behavior Spec 2 already tests) |
| Deleted persona file → attribution falls back to the literal ID | Unchanged Spec 2 path (`TestCanonicalEvents_NamerFallsBackToTheLiteralID` still green; the pin fold shares `attributed()`) |
| Rerun × override composition (`active_for_replay` picks the run; override decides the turn) | 3 (rerun test) |
| Toggle mid-run applies from the next send | Structural: the filter lives inside `GetProviderReplayEvents`, exactly where the spec's architecture places it, so no watch/subscription exists and a toggle never retroactively affects streamed output. (The run loop re-reads replay per tool iteration — the spec's own store-side placement implies this — so a mid-run toggle can reach a later iteration of the in-flight run; the current turn is exempt either way per rule 2.) |
| appapi: invalid state / unknown turn → `AppError{Code:"config"}`, nothing persisted | 5 |
| Frontend: hover cycle control on the user-bubble anchor | 6 |
| Frontend: pin glyph beside the model chip; dimmed exclusion; auto unchanged | 6 |
| Frontend: override map fetched on conversation open alongside the event load | 6 |
| Occupancy footer needs no changes | Global (no task touches it; SMOKE 62 observes the drop) |
| Manual SMOKE steps | 7 |
| Out of scope: sub-message granularity, half-turn control, auto-eviction, bulk ops, schema beyond one table | No task implements them |
