# Multi-Persona Threads Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the operator direct several personas within a single conversation via leading `@persona` mentions, with persona-aware context assembly so a persona never sees another persona's words labeled as its own.

**Architecture:** A new pure package `internal/mention` parses leading mentions. `internal/appapi.SendMessage` routes a mentioned turn to that persona for exactly one turn without touching `pinned_persona`. `internal/chat.canonicalEvents` becomes persona-aware: own-persona and pre-persona turns pass through verbatim (tool blocks included), the immediately preceding foreign turn's final text is folded into an attributed user-role block (`From Scout (model):`), and older foreign turns are omitted. The frontend gains an `@` autocomplete at composer position 0. **No schema change, no provider change, no new Wails bound methods.**

**Tech Stack:** Go 1.x (stdlib + `github.com/google/uuid`, both already in go.mod), TypeScript + Vite frontend, Wails bindings (unchanged).

**Spec:** `docs/superpowers/specs/2026-07-13-multi-persona-threads-design.md`

## Global Constraints

- `internal/rag/{chunker,embedding,ragindex}/` are verbatim copies of acctutor — **never modify them**; their gofmt drift is permanent (verify formatting with targeted `gofmt -l` on touched dirs only, never repo-wide).
- `internal/store` and `internal/provider` are **not modified** in this feature. No SQL migration, no new tables or columns.
- No new Wails bound methods; `SendMessage(convID, userText, personaID string)` keeps its exact signature, so `frontend/wailsjs/` must not be regenerated. If `wails dev` flips `frontend/wailsjs/go/*` to mode 755, run `chmod 644` on them before staging.
- The handoff block format is exactly `From <Name> (<model>):\n<text>` — `<Name>` from the persona registry, falling back to the literal persona ID; `<model>` from `runs.model` (already joined onto the event).
- Mentions are leading-only, one-shot, case-insensitive, `[a-zA-Z0-9-]+`, and are **never stripped** from the persisted message or the provider payload.
- Every error crossing `appapi` is a typed `provider.AppError`; an unresolvable mention is `Code: "config"` and must list the real persona IDs (no fuzzy matching, no silent substitution).
- A conversation where every run shares one persona (or has no persona) must produce a **byte-identical provider payload** to today's. This regression guard is written before any behavior change.
- Branch: work directly on `master` (solo repo; each task commits atomically).

## File Structure

| File | Role |
|---|---|
| `internal/mention/mention.go` (create) | Pure leading-mention parser, zero dependencies |
| `internal/mention/mention_test.go` (create) | Table tests, every row from the spec |
| `internal/chat/chat.go` (modify) | `PersonaNamer` interface, `SendParams.Namer`, persona-aware `canonicalEvents` + `predecessorTurnID` |
| `internal/chat/canonical_events_test.go` (create) | Byte-identical regression guards + persona-aware assembly tests |
| `internal/chat/attribution_leak_test.go` (modify) | Call-site update (Task 2) and comment reconciliation (Task 3) |
| `internal/persona/persona.go` (modify) | `Registry.Name` — satisfies `chat.PersonaNamer` |
| `internal/persona/persona_test.go` (modify) | Test for `Registry.Name` |
| `internal/appapi/api.go` (modify) | `routePersona` helper; `SendMessage` routes mention → persona; passes `Namer` |
| `internal/appapi/mention_routing_test.go` (create) | Routing tests: override, no-repin, unknown mention, nothing persisted |
| `frontend/index.html` (modify) | `#mentionPopup` element inside `#composer` |
| `frontend/src/style.css` (modify) | Popup styling (dark theme, matches `#1d1d20`/`#2b2b30` palette) |
| `frontend/src/main.ts` (modify) | Autocomplete logic, keydown routing, composer restore on config error |
| `docs/SMOKE.md` (modify) | Manual steps 55–58 |

---

### Task 1: `internal/mention` — leading-mention parser

**Files:**
- Create: `internal/mention/mention.go`
- Test: `internal/mention/mention_test.go`

**Interfaces:**
- Consumes: nothing (pure, stdlib only).
- Produces: `mention.Parse(text string) (personaID string, ok bool)` — Task 4's `routePersona` calls this.

- [ ] **Step 1: Write the failing test**

Create `internal/mention/mention_test.go`:

```go
package mention

import "testing"

// Every row of the table in the spec's Mention Parsing section, plus edge
// cases. A mention counts only when, after trimming leading whitespace, the
// message starts with @name followed by whitespace or end-of-string.
func TestParse(t *testing.T) {
	cases := []struct {
		in string
		id string
		ok bool
	}{
		{"@skeptic poke holes", "skeptic", true},
		{"@Skeptic poke holes", "skeptic", true}, // case-insensitive
		{"@scout", "scout", true},                // mention alone is legal
		{"  @scout\nreview this", "scout", true}, // leading whitespace trimmed
		{"@scout @skeptic both?", "scout", true}, // second mention is literal text
		{"ask @skeptic about it", "", false},     // not leading
		{"email me @ 5pm", "", false},            // @ not followed by a name
		{"@property\ndef foo():", "property", true}, // pasted decorator: parses, resolution errors later
		{"", "", false},
		{"@", "", false},
		{"@!bad", "", false},
		{"\t@scout hi", "scout", true},
	}
	for _, c := range cases {
		id, ok := Parse(c.in)
		if id != c.id || ok != c.ok {
			t.Errorf("Parse(%q) = (%q, %v), want (%q, %v)", c.in, id, ok, c.id, c.ok)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mention/`
Expected: FAIL — `no required module provides package` / undefined `Parse` (package does not exist yet).

- [ ] **Step 3: Write the implementation**

Create `internal/mention/mention.go`:

```go
// Package mention parses the leading @persona mention that routes a single
// turn to a named assistant. The rule fits in one sentence: a mention counts
// only when, after trimming leading whitespace, the message starts with @,
// followed by one or more of [a-zA-Z0-9-], followed by whitespace or
// end-of-string. Everything else — a mid-sentence @, an email address, a
// @decorator in pasted code, a second @name in the body — is literal text.
package mention

import (
	"regexp"
	"strings"
)

var re = regexp.MustCompile(`^@([a-zA-Z0-9-]+)($|\s)`)

// Parse returns the persona ID a message is addressed to. ok is false when
// the message carries no leading mention. The captured name is lowercased to
// match persona IDs, which are already [a-z0-9-].
func Parse(text string) (personaID string, ok bool) {
	m := re.FindStringSubmatch(strings.TrimLeft(text, " \t\r\n"))
	if m == nil {
		return "", false
	}
	return strings.ToLower(m[1]), true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mention/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/mention  # must print nothing
git add internal/mention
git commit -m "feat(mention): leading-only @persona parser"
```

---

### Task 2: `canonicalEvents` seam change + byte-identical regression guards

The signature of `canonicalEvents` changes now, with behavior deliberately unchanged, and the byte-identical guards land in the same commit. This is the spec's "first test to write": it protects every existing conversation before Task 3 touches assembly logic.

**Files:**
- Modify: `internal/chat/chat.go` (SendParams at :67-85, call sites at :230 and :345, `canonicalEvents` at :447)
- Modify: `internal/chat/attribution_leak_test.go:63` (call-site only; comment reconciliation is Task 3)
- Test: `internal/chat/canonical_events_test.go` (create)

**Interfaces:**
- Consumes: `store.ConversationEvent` (has `TurnID`, `PersonaID`, `Model` fields already); existing test helpers in `chat_test.go` (`openStore(t)`).
- Produces:
  - `type PersonaNamer interface { Name(personaID string) (string, bool) }` in package `chat` — Task 4's `persona.Registry` satisfies it.
  - `SendParams.Namer PersonaNamer` field — Task 4 sets it from appapi.
  - `canonicalEvents(rows []store.ConversationEvent, currentTurnID, currentPersonaID string, namer PersonaNamer) []provider.Event` — Task 3 fills in the persona-aware behavior.
  - Test helpers `completedTurn`, `currentTurn`, `legacyCanonical`, `marshalEvents` in `canonical_events_test.go` — Task 3's tests reuse them.

- [ ] **Step 1: Write the failing tests (new signature + byte-identical guards)**

Create `internal/chat/canonical_events_test.go`:

```go
package chat

import (
	"encoding/json"
	"testing"

	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/google/uuid"
)

// completedTurn persists one full turn: a user message, then a completed run
// for personaID/model with an optional tool round and one assistant_text per
// entry in texts. Returns the turn ID.
func completedTurn(t *testing.T, st *store.Store, convID, userText, personaID, model string, withTool bool, texts ...string) string {
	t.Helper()
	u, err := st.AppendUserMessage(convID, userText)
	if err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	if err := st.CreateRun(convID, u.TurnID, runID, "openai", model, "auto_grounded_default", personaID); err != nil {
		t.Fatal(err)
	}
	if withTool {
		callID := "call-" + runID[:8]
		if _, err := st.AppendAssistantToolCall(convID, u.TurnID, runID, callID,
			"safemath", json.RawMessage(`{"expression":"2+2"}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := st.AppendToolResult(convID, u.TurnID, runID, callID,
			"safemath", "4", nil, false, 3); err != nil {
			t.Fatal(err)
		}
	}
	for _, txt := range texts {
		if _, err := st.AppendAssistantText(convID, u.TurnID, runID, txt); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CompleteRun(runID, store.RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}
	return u.TurnID
}

// currentTurn persists the in-flight turn: a user message plus an in_progress
// run, exactly the state runLoop sees on its first provider call.
func currentTurn(t *testing.T, st *store.Store, convID, userText, personaID, model string) (turnID, runID string) {
	t.Helper()
	u, err := st.AppendUserMessage(convID, userText)
	if err != nil {
		t.Fatal(err)
	}
	runID = uuid.NewString()
	if err := st.CreateRun(convID, u.TurnID, runID, "openai", model, "auto_grounded_default", personaID); err != nil {
		t.Fatal(err)
	}
	return u.TurnID, runID
}

// legacyCanonical is the pre-Spec-2 mapping, inlined verbatim as the
// byte-identical reference: every selected row passes through the six-field
// whitelist. If canonicalEvents diverges from this on a single-persona or
// no-persona thread, an existing conversation replays differently — the one
// failure this feature must never cause.
func legacyCanonical(rows []store.ConversationEvent) []provider.Event {
	out := make([]provider.Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, provider.Event{
			Kind: r.Kind, Text: r.Text,
			ToolCallID: r.ToolCallID, ToolName: r.ToolName,
			ToolInput: r.ToolInput, IsError: r.IsError,
		})
	}
	return out
}

func marshalEvents(t *testing.T, evs []provider.Event) string {
	t.Helper()
	b, err := json.Marshal(evs)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCanonicalEvents_SinglePersonaThreadIsByteIdenticalToLegacy(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("guard")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "q1", "scout", "m1", true, "a1")
	completedTurn(t, st, conv.ID, "q2", "scout", "m1", false, "a2")
	turnID, runID := currentTurn(t, st, conv.ID, "q3", "scout", "m1")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := marshalEvents(t, canonicalEvents(rows, turnID, "scout", nil))
	want := marshalEvents(t, legacyCanonical(rows))
	if got != want {
		t.Errorf("single-persona payload diverged from legacy:\n got %s\nwant %s", got, want)
	}
}

func TestCanonicalEvents_LegacyNoPersonaRunsAreByteIdenticalToLegacy(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("guard-legacy")
	if err != nil {
		t.Fatal(err)
	}
	// Runs recorded before personas existed: persona_id empty. They are the
	// current persona's own voice, never relabeled "From (unknown)".
	completedTurn(t, st, conv.ID, "q1", "", "m1", true, "a1")
	completedTurn(t, st, conv.ID, "q2", "", "m1", false, "a2")
	turnID, runID := currentTurn(t, st, conv.ID, "q3", "scout", "m2")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := marshalEvents(t, canonicalEvents(rows, turnID, "scout", nil))
	want := marshalEvents(t, legacyCanonical(rows))
	if got != want {
		t.Errorf("legacy no-persona payload diverged:\n got %s\nwant %s", got, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/chat/`
Expected: FAIL to compile — `too many arguments in call to canonicalEvents`.

- [ ] **Step 3: Change the seam, preserving behavior**

In `internal/chat/chat.go`:

**3a.** Add the interface directly above `type SendParams struct` (line 67):

```go
// PersonaNamer resolves a persona ID to its display name, so a handoff can
// be attributed without chat importing the persona registry. Nil is legal:
// the literal persona ID is used instead.
type PersonaNamer interface {
	Name(personaID string) (string, bool)
}
```

**3b.** Add one field to `SendParams`, after `PersonaID`:

```go
	Namer          PersonaNamer // resolves persona IDs for handoff attribution; nil → literal IDs
```

**3c.** Replace `canonicalEvents` (line 447) with the same behavior under the new signature:

```go
func canonicalEvents(rows []store.ConversationEvent, currentTurnID, currentPersonaID string, namer PersonaNamer) []provider.Event {
	out := make([]provider.Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, provider.Event{
			Kind: r.Kind, Text: r.Text,
			ToolCallID: r.ToolCallID, ToolName: r.ToolName,
			ToolInput: r.ToolInput, IsError: r.IsError,
		})
	}
	return out
}
```

**3d.** Update both call sites. In `runLoop` (line 230):

```go
			Events:    canonicalEvents(events, turnID, p.PersonaID, p.Namer),
```

In `finalizeWithoutTools` (line 345):

```go
		Events:    canonicalEvents(events, turnID, p.PersonaID, p.Namer),
```

**3e.** Update `internal/chat/attribution_leak_test.go:63`:

```go
	events := canonicalEvents(rows, rows[0].TurnID, sentinelPersona, nil)
```

(`rows[0].TurnID` is the single turn's ID — the current turn; the sentinel persona is the current persona, so every row remains "own voice" through Task 3's change as well.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/chat/`
Expected: PASS — both guards, plus every pre-existing chat test including `TestCanonicalEvents_NoPersonaOrModelLeak`.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/chat  # must print nothing
git add internal/chat
git commit -m "refactor(chat): thread turn, persona, and namer into canonicalEvents behind byte-identical guards"
```

---

### Task 3: Persona-aware context assembly

**Files:**
- Modify: `internal/chat/chat.go` (`canonicalEvents` body; add `predecessorTurnID`)
- Modify: `internal/chat/attribution_leak_test.go` (comment reconciliation, lines 13–20)
- Test: `internal/chat/canonical_events_test.go` (extend)

**Interfaces:**
- Consumes: Task 2's signature and test helpers (`completedTurn`, `currentTurn`, `openStore`); `store.EventKindUserMessage` / `store.EventKindAssistantText` constants.
- Produces: the final assembly behavior. `predecessorTurnID(rows []store.ConversationEvent, currentTurnID string) string` is package-private to `chat`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/chat/canonical_events_test.go` (add `"reflect"` and `"strings"` to its imports):

```go
type stubNamer map[string]string

func (m stubNamer) Name(id string) (string, bool) {
	n, ok := m[id]
	return n, ok
}

// countFromBlocks returns how many events carry a handoff attribution header.
func countFromBlocks(evs []provider.Event) int {
	n := 0
	for _, e := range evs {
		if strings.HasPrefix(e.Text, "From ") {
			n++
		}
	}
	return n
}

// The core Scout → Skeptic → Scout thread from the spec. At turn 3 Scout
// must see its own turn-1 events verbatim (tool blocks included), Skeptic's
// turn-2 text folded into an attributed user-role block (tool blocks
// dropped), and every operator message in order.
func TestCanonicalEvents_FoldsTheImmediateForeignPredecessor(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "find the angles", "scout", "m-scout", true, "scout answer")
	completedTurn(t, st, conv.ID, "@skeptic poke holes", "skeptic", "m-skeptic", true, "skeptic critique")
	turnID, runID := currentTurn(t, st, conv.ID, "respond to that", "scout", "m-scout")

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
		"user_message",        // find the angles
		"assistant_tool_call", // scout's own tool round survives
		"tool_result",
		"assistant_text", // scout answer
		"user_message",   // @skeptic poke holes (mention not stripped)
		"user_message",   // folded Skeptic baton
		"user_message",   // respond to that
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	if got[5].Text != "From Skeptic (m-skeptic):\nskeptic critique" {
		t.Errorf("baton = %q", got[5].Text)
	}
	if got[4].Text != "@skeptic poke holes" {
		t.Errorf("operator message altered: %q", got[4].Text)
	}
	// A foreign persona's tool events never appear in any payload: the only
	// tool pair present is scout's own from turn 1.
	toolEvents := 0
	for _, e := range got {
		if e.Kind == "assistant_tool_call" || e.Kind == "tool_result" {
			toolEvents++
		}
	}
	if toolEvents != 2 {
		t.Errorf("tool events = %d, want 2 (scout's own pair only)", toolEvents)
	}
}

// A foreign persona that is neither the current persona nor the immediate
// predecessor is omitted entirely — its operator message stays.
func TestCanonicalEvents_OmitsANonAdjacentForeignTurn(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "q1", "scout", "m-scout", false, "scout one")
	completedTurn(t, st, conv.ID, "@skeptic poke", "skeptic", "m-skeptic", false, "skeptic critique")
	completedTurn(t, st, conv.ID, "q3", "scout", "m-scout", false, "scout two")
	turnID, runID := currentTurn(t, st, conv.ID, "q4", "scout", "m-scout")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "scout", stubNamer{"skeptic": "Skeptic"})

	for _, e := range got {
		if strings.Contains(e.Text, "skeptic critique") {
			t.Errorf("non-adjacent foreign output leaked into payload: %q", e.Text)
		}
	}
	if n := countFromBlocks(got); n != 0 {
		t.Errorf("From-blocks = %d, want 0", n)
	}
	// The operator's messages all survive, including the mentioned one.
	var userTexts []string
	for _, e := range got {
		if e.Kind == "user_message" {
			userTexts = append(userTexts, e.Text)
		}
	}
	if !reflect.DeepEqual(userTexts, []string{"q1", "@skeptic poke", "q3", "q4"}) {
		t.Errorf("user messages = %v", userTexts)
	}
}

// An errored predecessor has no completed active run, so there is no baton —
// not an error. The next persona sees the operator's messages and its own
// history only.
func TestCanonicalEvents_ErroredPredecessorMeansNoBaton(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "q1", "scout", "m-scout", false, "scout one")
	u, err := st.AppendUserMessage(conv.ID, "@skeptic poke")
	if err != nil {
		t.Fatal(err)
	}
	erroredRun := uuid.NewString()
	if err := st.CreateRun(conv.ID, u.TurnID, erroredRun, "openai", "m-skeptic", "auto_grounded_default", "skeptic"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRunErrored(erroredRun, "provider_error", "auth", "no key"); err != nil {
		t.Fatal(err)
	}
	turnID, runID := currentTurn(t, st, conv.ID, "q3", "scout", "m-scout")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "scout", stubNamer{"skeptic": "Skeptic"})
	if n := countFromBlocks(got); n != 0 {
		t.Errorf("From-blocks = %d, want 0 (no baton to pass)", n)
	}
}

// A deleted persona file: the namer cannot resolve the ID, so the
// attribution line falls back to the literal ID — consistent with how the
// bubbles render a deleted persona.
func TestCanonicalEvents_NamerFallsBackToTheLiteralID(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "@skeptic poke", "skeptic", "m-skeptic", false, "critique")
	turnID, runID := currentTurn(t, st, conv.ID, "and?", "scout", "m-scout")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, namer := range []PersonaNamer{stubNamer{}, nil} {
		got := canonicalEvents(rows, turnID, "scout", namer)
		found := false
		for _, e := range got {
			if e.Text == "From skeptic (m-skeptic):\ncritique" {
				found = true
			}
		}
		if !found {
			t.Errorf("namer=%v: no literal-ID baton in %+v", namer, got)
		}
	}
}

// A foreign predecessor whose run produced several assistant_text events
// (multi-iteration tool runs do this) folds into ONE attributed block.
func TestCanonicalEvents_JoinsMultipleForeignTextsIntoOneBaton(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "@skeptic poke", "skeptic", "m-skeptic", true, "part one", "part two")
	turnID, runID := currentTurn(t, st, conv.ID, "and?", "scout", "m-scout")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "scout", stubNamer{"skeptic": "Skeptic"})
	if n := countFromBlocks(got); n != 1 {
		t.Fatalf("From-blocks = %d, want exactly 1", n)
	}
	want := "From Skeptic (m-skeptic):\npart one\n\npart two"
	found := false
	for _, e := range got {
		if e.Text == want {
			found = true
		}
	}
	if !found {
		t.Errorf("joined baton %q not found in %+v", want, got)
	}
}
```

- [ ] **Step 2: Run tests to verify the new ones fail**

Run: `go test ./internal/chat/`
Expected: FAIL — `TestCanonicalEvents_FoldsTheImmediateForeignPredecessor` (skeptic's tool events and raw `assistant_text` pass through; no `From` block), `TestCanonicalEvents_OmitsANonAdjacentForeignTurn`, `TestCanonicalEvents_NamerFallsBackToTheLiteralID`, `TestCanonicalEvents_JoinsMultipleForeignTextsIntoOneBaton` all fail. The two byte-identical guards and the leak test still PASS.

- [ ] **Step 3: Implement persona-aware assembly**

In `internal/chat/chat.go`, replace the Task 2 `canonicalEvents` body and add `predecessorTurnID` below it:

```go
// canonicalEvents builds the provider payload for the persona speaking now
// (currentPersonaID, answering currentTurnID). Own-persona and pre-persona
// rows pass through the six-field whitelist verbatim — a persona keeps its
// own voice, tool blocks included. The immediately preceding foreign turn's
// final text folds into one attributed user-role block; its tool blocks are
// dropped because their provider-specific IDs would dangle in another
// persona's transcript and the receiving persona may not even have the tool
// in its registry. Older foreign turns are omitted entirely. The operator's
// user_message rows are always included, in order. rows arrive ordered by
// sequence_index.
func canonicalEvents(rows []store.ConversationEvent, currentTurnID, currentPersonaID string, namer PersonaNamer) []provider.Event {
	predecessor := predecessorTurnID(rows, currentTurnID)
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
		// The baton lands immediately before the current turn's rows, i.e.
		// right after the predecessor turn it summarizes.
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
		// Any other foreign row (older turn, or a predecessor tool block) is
		// omitted.
	}
	flushBaton()
	return out
}

// predecessorTurnID returns the turn immediately before currentTurnID, in
// user-message order. User messages are appended chronologically and are
// unique per turn, so they define turn order even if a turn's run events
// were appended out of sequence (a rerun). "" means no predecessor.
func predecessorTurnID(rows []store.ConversationEvent, currentTurnID string) string {
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
```

- [ ] **Step 4: Run tests to verify everything passes**

Run: `go test ./internal/chat/`
Expected: PASS — all new tests, both byte-identical guards, the attribution-leak test, and every pre-existing chat test.

- [ ] **Step 5: Reconcile the attribution-leak test's comment**

The spec requires this test be reconciled, not deleted: it stays green (single-persona), and its comment must now say why. In `internal/chat/attribution_leak_test.go`, replace the comment block at lines 13–20 with:

```go
// TestCanonicalEvents_NoPersonaOrModelLeak guards the six-field whitelist in
// canonicalEvents (Kind, Text, ToolCallID, ToolName, ToolInput, IsError). That
// whitelist is the only thing keeping PersonaID and Model — which live right
// next to those fields on store.ConversationEvent, joined in from runs for
// display attribution — out of the payload sent to the LLM provider.
//
// Multi-persona threads (Spec 2) deliberately write a persona's display name
// and model into the *text* of a handoff block ("From Scout (model):").
// That is not the leak this test forbids, and the distinction is
// load-bearing:
//   - persona metadata must never appear as structured fields on
//     provider.Event — the structural guard below stays, unchanged;
//   - persona metadata may appear inside Text, but only in a deliberately
//     constructed handoff block, which this single-persona thread never
//     produces.
// If this test goes red, do not loosen the structural assertion — a widened
// whitelist or a new provider.Event field is leaking attribution.
```

- [ ] **Step 6: Run the full chat package again**

Run: `go test ./internal/chat/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
gofmt -l internal/chat  # must print nothing
git add internal/chat
git commit -m "feat(chat): persona-aware context assembly with attributed handoff baton"
```

---

### Task 4: Mention routing in `appapi` + `Registry.Name`

**Files:**
- Modify: `internal/persona/persona.go` (add `Name` method after `ByID`, line 52)
- Modify: `internal/persona/persona_test.go` (append one test)
- Modify: `internal/appapi/api.go` (`SendMessage` at :241-252, new `routePersona` after `noPersonaMessage` at :333, `Namer` in SendParams literal at :293-310)
- Test: `internal/appapi/mention_routing_test.go` (create)

**Interfaces:**
- Consumes: `mention.Parse(text) (string, bool)` from Task 1; `chat.PersonaNamer` and `SendParams.Namer` from Task 2; existing `newPersonaAPI(t, files)` helper in `persona_test.go` (appapi package).
- Produces: `(persona.Registry).Name(id string) (string, bool)`; `(*API).routePersona(userText, pickerID string) (persona.Persona, error)`. `SendMessage`'s bound signature is unchanged.

- [ ] **Step 1: Write the failing test for `Registry.Name`**

Append to `internal/persona/persona_test.go`:

```go
// Name satisfies chat.PersonaNamer: the handoff attribution line resolves a
// persona ID to its display name through this method.
func TestRegistryNameResolvesDisplayName(t *testing.T) {
	r := Registry{Personas: []Persona{{ID: "scout", Name: "Scout"}}}
	if n, ok := r.Name("scout"); !ok || n != "Scout" {
		t.Errorf("Name(scout) = (%q, %v), want (Scout, true)", n, ok)
	}
	if _, ok := r.Name("ghost"); ok {
		t.Error("Name(ghost) resolved; want ok=false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/persona/`
Expected: FAIL to compile — `r.Name undefined`.

- [ ] **Step 3: Implement `Registry.Name`**

In `internal/persona/persona.go`, directly after `ByID` (line 52):

```go
// Name resolves a persona ID to its display name. It satisfies the
// chat.PersonaNamer interface (declared there), so a handoff block can be
// attributed without chat importing this package.
func (r Registry) Name(id string) (string, bool) {
	p, ok := r.ByID(id)
	return p.Name, ok
}
```

Run: `go test ./internal/persona/`
Expected: PASS

- [ ] **Step 4: Write the failing routing tests**

Create `internal/appapi/mention_routing_test.go`:

```go
package appapi

import (
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/provider"
)

func twoPersonaAPI(t *testing.T) *API {
	t.Helper()
	return newPersonaAPI(t, map[string]string{
		"scout.md":   "---\nname: Scout\nmodel: gpt-5\n---\nYou are Scout.\n",
		"skeptic.md": "---\nname: Skeptic\nmodel: gpt-5\n---\nYou are Skeptic.\n",
	})
}

// A leading mention routes exactly one turn to the mentioned persona,
// overriding the picker.
func TestRoutePersonaMentionOverridesThePicker(t *testing.T) {
	a := twoPersonaAPI(t)
	p, err := a.routePersona("@skeptic poke holes", "scout")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "skeptic" {
		t.Errorf("routed to %q, want skeptic", p.ID)
	}
}

// Case-insensitive: @Skeptic routes to skeptic.
func TestRoutePersonaMentionIsCaseInsensitive(t *testing.T) {
	a := twoPersonaAPI(t)
	p, err := a.routePersona("@Skeptic poke holes", "scout")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "skeptic" {
		t.Errorf("routed to %q, want skeptic", p.ID)
	}
}

// No mention → the picker's persona, exactly as before.
func TestRoutePersonaNoMentionUsesThePicker(t *testing.T) {
	a := twoPersonaAPI(t)
	p, err := a.routePersona("ask @skeptic about it", "scout") // not leading
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "scout" {
		t.Errorf("routed to %q, want scout", p.ID)
	}
}

// An unresolvable mention is a hard error that lists the real persona IDs —
// never a silent substitution, never fuzzy matching.
func TestRoutePersonaUnknownMentionListsAvailablePersonas(t *testing.T) {
	a := twoPersonaAPI(t)
	_, err := a.routePersona("@skpetic poke holes", "scout")
	if err == nil {
		t.Fatal("unknown mention returned nil error")
	}
	ae, ok := err.(provider.AppError)
	if !ok {
		t.Fatalf("error is %T, want provider.AppError", err)
	}
	if ae.Code != "config" {
		t.Errorf("Code = %q, want config", ae.Code)
	}
	for _, want := range []string{`"skpetic"`, "scout", "skeptic"} {
		if !strings.Contains(ae.UserMessage, want) {
			t.Errorf("UserMessage = %q, want it to contain %s", ae.UserMessage, want)
		}
	}
}

// A mention when zero personas loaded falls back to the explain-the-failures
// message from Spec 1 rather than "Available: " with an empty list.
func TestRoutePersonaMentionWithNoValidPersonasExplainsItself(t *testing.T) {
	a := newPersonaAPI(t, map[string]string{
		"broken.md": "---\nname: Broken\nmodel: no-such-model\n---\nbody\n",
	})
	_, err := a.routePersona("@scout hi", "scout")
	ae, ok := err.(provider.AppError)
	if !ok {
		t.Fatalf("error is %T, want provider.AppError", err)
	}
	if !strings.Contains(ae.UserMessage, "broken.md") {
		t.Errorf("UserMessage = %q, want it to name the failing file", ae.UserMessage)
	}
}

// SendMessage never writes pinned_persona — pinning is the frontend's
// separate SetConversationPersona call, which a mentioned turn does not
// change. The pin must still read "scout" after a @skeptic send attempt.
func TestSendMessageWithMentionDoesNotRepin(t *testing.T) {
	a := twoPersonaAPI(t)
	c, err := a.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetConversationPersona(c.ID, "scout"); err != nil {
		t.Fatal(err)
	}
	// Errors at provider.New (no API key) — after routing, before any write.
	_ = a.SendMessage(c.ID, "@skeptic poke holes", "scout")
	convs, err := a.st.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	for _, cv := range convs {
		if cv.ID == c.ID && cv.PinnedPersona != "scout" {
			t.Errorf("PinnedPersona = %q, want scout (unchanged)", cv.PinnedPersona)
		}
	}
}

// An unresolvable mention persists nothing: no user message, no run. The
// operator's text stays in the composer, and a reload shows no new turn.
func TestSendMessageUnknownMentionPersistsNothing(t *testing.T) {
	a := twoPersonaAPI(t)
	c, err := a.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SendMessage(c.ID, "@skpetic poke holes", "scout"); err == nil {
		t.Fatal("SendMessage with unknown mention returned nil")
	}
	events, err := a.st.GetConversationDisplayEvents(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Errorf("events persisted after a rejected mention: %+v", events)
	}
}
```

- [ ] **Step 5: Run tests to verify they fail**

Run: `go test ./internal/appapi/`
Expected: FAIL to compile — `a.routePersona undefined`.

- [ ] **Step 6: Implement routing**

In `internal/appapi/api.go`:

**6a.** Add the import `"github.com/cajundata/starshp_app/internal/mention"` to the import block.

**6b.** Replace the persona-resolution block at the top of `SendMessage` (lines 241–252):

```go
func (a *API) SendMessage(convID, userText, personaID string) error {
	p, rerr := a.routePersona(userText, personaID)
	if rerr != nil {
		return rerr
	}
```

(The old `a.personas.ByID` + AppError block is deleted; its no-silent-substitution comment moves onto `routePersona`.)

**6c.** In the `chat.SendParams{...}` literal (line ~293), add after `PersonaID: p.ID,`:

```go
		Namer:          a.personas,
```

**6d.** Add `routePersona` directly after `noPersonaMessage` (line ~333):

```go
// routePersona resolves who answers this message. A leading @mention routes
// exactly one turn and never touches pinned_persona; otherwise the picker's
// persona applies. There is no fallback to a default persona: a silent
// substitution would attribute output to an assistant the operator did not
// pick, which is the exact failure per-persona attribution exists to
// prevent. An unresolvable mention lists the real persona IDs — an
// edit-distance guess is a magic number that is wrong at the boundary.
func (a *API) routePersona(userText, pickerID string) (persona.Persona, error) {
	id := pickerID
	mentioned, hasMention := mention.Parse(userText)
	if hasMention {
		id = mentioned
	}
	if p, ok := a.personas.ByID(id); ok {
		return p, nil
	}
	if hasMention && len(a.personas.Personas) > 0 {
		ids := make([]string, len(a.personas.Personas))
		for i, p := range a.personas.Personas {
			ids[i] = p.ID
		}
		return persona.Persona{}, provider.AppError{
			Code:        "config",
			UserMessage: "No assistant named \"" + mentioned + "\". Available: " + strings.Join(ids, ", ") + ".",
			Retryable:   false,
		}
	}
	return persona.Persona{}, provider.AppError{
		Code:        "config",
		UserMessage: a.noPersonaMessage(id),
		Retryable:   false,
	}
}
```

(`persona` and `strings` are already imported in api.go.)

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/appapi/ ./internal/persona/`
Expected: PASS — all new tests plus the pre-existing persona/appapi suites (`TestSendMessageRejectsAnUnknownPersona` and `TestSendMessageWithNoValidPersonasNamesTheValidationFailures` exercise the no-mention path through `routePersona` and must stay green).

- [ ] **Step 8: Commit**

```bash
gofmt -l internal/appapi internal/persona  # must print nothing
git add internal/appapi internal/persona
git commit -m "feat(appapi): route a leading @mention to its persona for one turn"
```

---

### Task 5: Frontend `@` autocomplete + composer restore on rejection

The frontend has no test harness; verification is `npm run build` (tsc + vite) plus the SMOKE steps added in Task 6.

**Files:**
- Modify: `frontend/index.html` (inside `#composer`, line 15)
- Modify: `frontend/src/style.css` (append popup rules)
- Modify: `frontend/src/main.ts` (`send()` at :397-459, keydown listener at :721-723, new autocomplete block)

**Interfaces:**
- Consumes: `cachedPersonas` (populated by `loadMeta`, main.ts:391 — objects with `id`, `name`, `color`), `input` (`HTMLTextAreaElement`, main.ts:57), `addMsg(role, text)` returning the bubble element, the existing `.hidden` CSS class.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Add the popup element**

In `frontend/index.html`, inside `<div id="composer">` (line 15), add the popup as the first child, before the textarea:

```html
      <div id="composer">
        <div id="mentionPopup" class="hidden"></div>
        <textarea id="input" placeholder="Message…" rows="3"></textarea>
```

- [ ] **Step 2: Style it**

Append to `frontend/src/style.css`:

```css
/* --- @mention autocomplete ------------------------------------------- */
#composer { position: relative; }
#mentionPopup {
  position: absolute;
  bottom: 100%;
  left: 12px;
  margin-bottom: 6px;
  min-width: 220px;
  background: #1d1d20;
  border: 1px solid #2b2b30;
  border-radius: 8px;
  padding: 4px;
  z-index: 30;
}
#mentionPopup .mention-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
}
#mentionPopup .mention-item.sel { background: #2b2b30; }
#mentionPopup .mention-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
}
#mentionPopup .mention-id {
  color: #8a8a92;
  font-family: ui-monospace, monospace;
  font-size: 11px;
  margin-left: auto;
}
```

If `#composer` already declares `position` in the existing rules, skip the duplicate line rather than overriding it.

- [ ] **Step 3: Add the autocomplete logic**

In `frontend/src/main.ts`, add near the other element lookups (after line 59):

```ts
const mentionPopup = $('mentionPopup') as HTMLDivElement
```

Add the autocomplete block just above the existing `input.addEventListener('keydown', ...)` (line 721):

```ts
// --- @mention autocomplete -------------------------------------------
// Mentions are leading-only, so the popup exists only while the composer
// holds nothing but a partial leading @name. Pasting code mid-message can
// never trigger it — that is the entire reason for the leading-only rule.
let mentionMatches: typeof cachedPersonas = []
let mentionSel = 0
let mentionDismissed = false

function mentionPrefix(): string | null {
  const m = /^\s*@([a-zA-Z0-9-]*)$/.exec(input.value)
  return m ? m[1].toLowerCase() : null
}

function hideMentionPopup() {
  mentionPopup.classList.add('hidden')
  mentionMatches = []
}

function updateMentionPopup() {
  const prefix = mentionPrefix()
  if (prefix === null) { mentionDismissed = false; hideMentionPopup(); return }
  if (mentionDismissed) { hideMentionPopup(); return }
  mentionMatches = cachedPersonas.filter(p => p.id.startsWith(prefix))
  if (!mentionMatches.length) { hideMentionPopup(); return }
  if (mentionSel >= mentionMatches.length) mentionSel = 0
  mentionPopup.innerHTML = mentionMatches.map((p, i) =>
    `<div class="mention-item${i === mentionSel ? ' sel' : ''}" data-id="${p.id}">` +
    `<span class="mention-dot" style="background:${p.color}"></span>` +
    `<span>${p.name}</span><span class="mention-id">@${p.id}</span></div>`
  ).join('')
  mentionPopup.classList.remove('hidden')
}

function insertMention(id: string) {
  input.value = '@' + id + ' '
  hideMentionPopup()
  input.focus()
  input.setSelectionRange(input.value.length, input.value.length)
}

input.addEventListener('input', () => { mentionSel = 0; updateMentionPopup() })
// mousedown, not click: it fires before the textarea loses focus.
mentionPopup.addEventListener('mousedown', (e) => {
  const item = (e.target as HTMLElement).closest('.mention-item') as HTMLElement | null
  if (item?.dataset.id) { e.preventDefault(); insertMention(item.dataset.id) }
})
```

Replace the existing keydown listener (lines 721–723) with:

```ts
input.addEventListener('keydown', (e) => {
  if (!mentionPopup.classList.contains('hidden')) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      mentionSel = (mentionSel + 1) % mentionMatches.length
      updateMentionPopup()
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      mentionSel = (mentionSel + mentionMatches.length - 1) % mentionMatches.length
      updateMentionPopup()
      return
    }
    if (e.key === 'Enter' || e.key === 'Tab') {
      e.preventDefault()
      insertMention(mentionMatches[mentionSel].id)
      return
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      mentionDismissed = true
      hideMentionPopup()
      return
    }
  }
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) send()
})
```

- [ ] **Step 4: Restore the composer when a send is rejected**

In `send()` (main.ts:397-459): capture the optimistic user bubble and restore state on a `config` rejection. Change line 420 from `addMsg('user', text)` to:

```ts
  const userEl = addMsg('user', text)
```

and extend the catch block (lines 435–439) to:

```ts
  } catch (e: any) {
    // A thrown error before any run started (e.g. bad model / missing key)
    // has no run bubble to attach to — surface it inline. Errors raised mid-run
    // are already rendered via chat:run_errored.
    addMsg('assistant', `[${e?.code || 'error'}] ${e?.userMessage || e}`)
    // A config rejection (typo'd mention, unknown assistant) persisted
    // nothing server-side — put the text back in the composer and drop the
    // optimistic user bubble so the view matches the store.
    if (e?.code === 'config') {
      userEl.remove()
      input.value = text
    }
  }
```

- [ ] **Step 5: Build to verify**

Run: `cd frontend && npm run build && cd ..`
Expected: `tsc` clean, vite build succeeds (regenerated `frontend/dist/` assets are expected output — this repo commits them).

- [ ] **Step 6: Commit**

```bash
ls -l frontend/wailsjs/go/appapi/  # confirm mode 644; if 755: chmod 644 frontend/wailsjs/go/appapi/* frontend/wailsjs/go/models.ts
git add frontend/index.html frontend/src frontend/dist
git commit -m "feat(frontend): leading-@ persona autocomplete; composer survives a rejected mention"
```

---

### Task 6: SMOKE steps + full-suite verification

**Files:**
- Modify: `docs/SMOKE.md` (append a new section after the "Business pipeline" section, which ends at item 54)

**Interfaces:**
- Consumes: the shipped behavior of Tasks 1–5.
- Produces: manual verification checklist for the operator.

- [ ] **Step 1: Append the SMOKE section**

After item 54 in `docs/SMOKE.md`, add:

```markdown
## Multi-persona threads

55. [ ] **@ autocomplete is leading-only.** In a conversation, type `@` as
        the first character of the composer — a popup lists the personas
        (dot in each persona's color, name, `@id`), filters as you type,
        arrows move the selection, and Enter/Tab inserts `@id `. Escape
        dismisses it. Type `@` anywhere after the first character (or paste
        code containing a `@decorator` mid-message) — no popup.
56. [ ] **A mention routes one turn.** With persona A pinned, send
        `@<persona-b-id> hello`. The reply bubble carries B's color, name,
        and model chip; the persona picker still shows A; the next
        unmentioned message is answered by A again.
57. [ ] **A typo'd mention fails without sending.** Send `@no-such-persona
        hi`. The error names the available persona IDs, the message text is
        back in the composer, and after closing and reopening the
        conversation no new turn exists.
58. [ ] **The baton passes attributed.** In one conversation: ask A a
        question, then `@<b-id> critique that`, then an unmentioned message
        asking A to respond to the critique. A's answer shows it can see B's
        output (it arrived as `From <B> (<model>):` context, so A responds
        to the critique's substance). Close and reopen the conversation —
        every bubble replays with the same colors and chips it had live.
```

- [ ] **Step 2: Run the full verification suite**

```bash
go build ./... && go test ./...
gofmt -l internal/mention internal/chat internal/appapi internal/persona  # must print nothing
```

Expected: build clean, all packages PASS, no formatting drift in touched packages. (Do not run repo-wide `gofmt -l` — `internal/rag`'s drift is permanent and out of scope.)

- [ ] **Step 3: Commit**

```bash
git add docs/SMOKE.md
git commit -m "docs(smoke): multi-persona thread steps — autocomplete, routing, baton"
```

---

## Spec Coverage Map (self-review)

| Spec section | Task |
|---|---|
| Mention parsing (grammar, every table row) | 1 |
| Byte-identical regression guard (single-persona + legacy no-persona) | 2 |
| Context assembly decision table (own/pre-persona verbatim, predecessor folded, older omitted) | 3 |
| Foreign tool blocks dropped; `From <Name> (<model>):` format; namer fallback to literal ID | 3 |
| Errored/cancelled predecessor → no baton | 3 |
| `PersonaNamer` seam (chat never imports persona) | 2 (interface) + 4 (impl) |
| Attribution-leak test reconciled, not deleted | 3 |
| Routing: mention overrides picker one-shot; `pinned_persona` untouched; unmentioned turn pins as today (frontend flow unchanged) | 4 |
| Unresolvable mention → `AppError{config}` listing real names; nothing persisted | 4 |
| Mention not stripped (persisted raw, sent raw) | 3 (test asserts `@skeptic poke holes` verbatim in payload) |
| Frontend `@` autocomplete at position 0 only | 5 |
| Operator's text stays in composer on rejection | 5 |
| Manual SMOKE steps | 6 |
| No schema change; store/provider untouched | Global constraint (no task touches them) |
