# Multi-Persona Threads — Design

**Date:** 2026-07-13
**Status:** Approved for planning
**Spec:** 2 of 2 (see [Persona Foundation](2026-07-13-persona-foundation-design.md))

## Context

Spec 1 gave Starshp a team of named personas, one pinned per conversation, with
every assistant bubble color-coded by who produced it. Spec 2 lets the operator
direct **several personas within a single conversation** by addressing a turn to
one of them: `@skeptic poke holes in that`.

Spec 1 deliberately shipped first so personas could be lived with before this
design risk was taken. The risk is not the routing — that is a string match. It
is **what the next persona sees**.

### The problem this spec exists to solve

`GetProviderReplayEvents` feeds each prior turn's `assistant_text` to the
provider as an **assistant-role** message. That is correct when one persona owns
the conversation: the model is reading its own prior words.

The moment Skeptic follows Scout, Scout's answer arrives in Skeptic's context
sitting in the assistant slot — that is, labeled as *Skeptic's own prior words*.
Skeptic would find its own name on text it never wrote and critique itself.
Nothing errors. It just quietly behaves wrong.

Every decision below follows from fixing that.

The decisions were settled during brainstorming and are not re-opened here:

- **Manual routing only.** The operator says who speaks. No orchestrator LLM, no
  automatic delegation, no personas talking to each other unprompted.
- **A handoff is relabeled and attributed.** Another persona's output reaches you
  as a user-role block, `From Scout (claude-opus-4-7):`, never in the assistant
  slot.
- **A persona keeps its own voice.** Its own prior turns stay assistant-role,
  with their tool blocks. Strict last-output-only would make a recalled persona
  amnesiac about its own reasoning.
- **Mentions are leading-only and one-shot.** A mention routes one turn and does
  not change the conversation's pinned persona.
- **An unresolvable mention is a hard error.** Consistent with Spec 1: an
  assistant is never silently substituted for the one you asked for.

## Architecture

**No schema change.** Spec 1 already records `runs.persona_id` per run and
already joins it onto every replayed event. A mid-thread persona switch is
therefore *already* stored correctly, and bubbles already color correctly. This
spec adds no tables, no columns, and no migration.

The feature lives in exactly two places:

```
frontend (@ autocomplete in the composer)
        │  App.SendMessage(convID, text, personaID)
        ▼
internal/appapi ── mention.Parse(text) ──► internal/mention   (new, pure)
        │          resolve → persona.Registry
        │          (pinned_persona untouched on a mention)
        ▼
internal/chat  ── canonicalEvents() becomes persona-aware
        │          folds foreign output into attributed user-role blocks
        ▼
internal/store (unchanged — already returns PersonaID + Model per event)
```

`internal/store` is not modified. `internal/provider` is not modified.

## Mention Parsing

New package `internal/mention`. One pure function:

```go
// Parse returns the persona ID a message is addressed to. ok is false when the
// message carries no leading mention.
func Parse(text string) (personaID string, ok bool)
```

A mention counts only when, after trimming leading whitespace, the message
**starts** with `@`, followed by one or more of `[a-zA-Z0-9-]`, followed by
whitespace or end-of-string. The captured name is lowercased to match a persona
ID (persona IDs are already `[a-z0-9-]`).

Everything else is literal text — including a mid-sentence `@`, an email address,
a `@decorator` in pasted code, and **a second `@name` in the body**. The rule
fits in one sentence, and pasted code can never silently reroute a turn.

| Input | Result |
|---|---|
| `@skeptic poke holes` | `("skeptic", true)` |
| `@Skeptic poke holes` | `("skeptic", true)` — case-insensitive |
| `@scout` | `("scout", true)` — mention alone is legal |
| `  @scout\nreview this` | `("scout", true)` — leading whitespace trimmed |
| `@scout @skeptic both?` | `("scout", true)` — the second is literal text |
| `ask @skeptic about it` | `("", false)` — not leading |
| `email me @ 5pm` | `("", false)` — `@` not followed by a name |
| `@property\ndef foo():` | `("property", true)` → **resolves to nothing → hard error** |

The last row is the intended cost of the rule: pasting a Python decorator as the
very first thing in a message produces an error naming the available personas,
not a silently misrouted turn. That is the right side to fail on.

## Context Assembly

`canonicalEvents` (`internal/chat/chat.go:447`) is the seam. It already receives
`PersonaID` and `Model` on every event — Spec 1's `LEFT JOIN runs` put them there
— and today discards both.

Building the provider payload for persona **P**, walking each prior turn's active
run **R**:

| R's persona | What P receives |
|---|---|
| **Same as P**, or **empty** (a pre-persona run) | R's events **verbatim**: `assistant_text` as assistant-role, plus its `assistant_tool_call` / `tool_result` pairs. Its own voice, with its own scratch work. |
| **Different**, and R is the **immediately preceding** turn | Only R's `assistant_text`, folded into a **user-role** block: `From Scout (claude-opus-4-7):\n<text>`. Tool blocks dropped. |
| **Different**, and R is **not** the immediate predecessor | Omitted entirely. |

The operator's `user_message` events are always included, in order. The in-flight
run's own events pass through verbatim — the agentic loop requires them.

### Why foreign tool blocks are dropped

This is forced, not chosen. A `tool_use` / `tool_result` pair carries
provider-specific IDs that would dangle in another persona's transcript, and the
receiving persona may not even have that tool in its registry — Spec 1's `tools:`
whitelist means Skeptic can be denied `search_textbook` entirely. A handoff is
therefore always **final text only**: Scout's answer, not Scout's working.

### The safety property

**In a thread where every run shares one persona, row 1 of the table catches all
of them, and the payload is byte-identical to today's.**

The same holds for runs recorded before personas existed (`persona_id` is NULL):
they are treated as the current persona's own voice rather than being
retroactively relabeled `From (unknown)`.

So this spec **cannot change how any existing conversation replays**. That is the
first test to write, and it is the one that matters most.

### The naming seam

`chat` must not learn what a persona is — Spec 1 established that boundary and it
still holds. It needs display names only for the `From Scout` line, so
`chat.SendParams` gains one small interface:

```go
// PersonaNamer resolves a persona ID to its display name, so a handoff can be
// attributed without chat importing the persona registry.
type PersonaNamer interface {
    Name(personaID string) (string, bool)
}
```

`appapi` passes the registry in. The model ID for the attribution line comes from
`runs.model`, which is already on the event. An unresolvable persona ID (its file
was deleted) falls back to the literal ID — consistent with how Spec 1's bubbles
render a deleted persona.

## Routing

`appapi.SendMessage(convID, userText, personaID)` keeps its signature. It parses
before it resolves:

- **Leading mention present** → that persona runs this turn. `pinned_persona` is
  **not** written. The picker keeps showing the conversation's default, and the
  next unmentioned message goes back to it.
- **No mention** → the picker's persona runs and is pinned, exactly as Spec 1
  does today.

### The mention is not stripped

The message is persisted raw and sent raw. The addressee seeing `@skeptic poke
holes in that` costs nothing — its system prompt already tells it who it is — and
it means a persona reading further back can see which turns were addressed to
whom.

Stripping would force the event log and the provider payload to hold two
different versions of what the operator typed. That divergence is not worth
buying. If it reads badly in practice, stripping is a one-line change confined to
`canonicalEvents`.

## Error Handling

Consistent with the existing boundary: everything crossing `appapi` becomes a
typed `provider.AppError`.

| Condition | Behavior |
|---|---|
| Leading mention resolves to no persona | `AppError{Code:"config"}` — `No assistant named "skpetic". Available: assistant, scout, skeptic.` Nothing is persisted; the operator's text stays in the composer. The message **lists the real names** rather than fuzzy-matching: an edit-distance threshold is a magic number that is wrong at the boundary, and this is the same no-silent-substitution rule Spec 1 enforces. |
| Mention resolves; that persona's model has no API key | Existing `AppError{Code:"auth"}` from `provider.New`. Unchanged. |
| The preceding turn errored or was cancelled with no `assistant_text` | No baton to pass. The next persona receives the operator's messages and its own history. Not an error. |
| A mentioned persona has never spoken in this thread | Normal. It sees the operator's messages plus the last output. No special case. |
| The preceding persona's file was deleted | The attribution line falls back to the literal persona ID. Not an error. |

## Frontend

Typing `@` as the **first character** of the composer opens an autocomplete list
of personas: filter as you type, Enter or Tab inserts `@id `. Because mentions are
leading-only, the popup cannot fire while pasting code — which is the entire
reason for that rule.

Nothing else changes. The persona picker is untouched, and the assistant bubble
already announces who answered, in their color, so a successful route is visible
without any new UI.

## Testing

**The regression guard, written first.** A single-persona, multi-turn,
tool-using conversation must produce a **byte-identical provider payload** before
and after this change. Same for a conversation of legacy runs with no persona.
This is the test that protects every existing conversation, and everything else
is secondary to it.

**Context assembly** — table-driven over a Scout → Skeptic → Scout thread. At
turn 3, Scout must see:

- its own turn-1 events as assistant-role, **including** its tool blocks;
- Skeptic's turn-2 `assistant_text` folded into a user-role `From Skeptic (…)`
  block, **excluding** Skeptic's tool blocks;
- and a foreign persona's `assistant_tool_call` / `tool_result` events must never
  appear in any payload, from any angle.

Plus: a persona three turns back that is neither the current persona nor the
immediate predecessor is omitted entirely.

**Mention parsing** — every row of the table in the Mention Parsing section, as
pure unit tests, plus: unknown mention returns an error listing the available
names.

**The existing attribution-leak test must be reconciled, not deleted.**
`internal/chat/attribution_leak_test.go` (added by Spec 1) asserts that
`PersonaID` and `Model` never reach `provider.Event`. Spec 2 **deliberately**
writes a persona's name and model into the *text* of a handoff block, which looks
like the thing that test forbids. Both are correct, and the distinction is
load-bearing:

- Persona metadata must never appear as **structured fields** on
  `provider.Event` — that guard stays, unchanged. `provider.Event` still carries
  no persona field.
- Persona metadata **may** appear inside `Text`, but **only** in a deliberately
  constructed `From <Name> (<model>):` handoff block.

The test is single-persona, so it stays green as written. Tighten its comment to
say *why* it stays green and what it is actually guarding, so the next reader does
not "fix" it by loosening the structural assertion when they see attributed text
flowing through.

**appapi** — a mention overrides the picker for one turn; `pinned_persona` is
unchanged after a mentioned turn; an unmentioned turn still pins.

**Manual (`docs/SMOKE.md`)** — the frontend has no test harness. Added steps: the
`@` autocomplete appears only at position 0; a mentioned turn is answered by the
mentioned persona (its color, its name, its model chip) while the picker still
shows the default; the next unmentioned turn goes back to the default; a typo'd
mention errors without sending.

## Out of Scope

- Fanning one message out to several personas at once.
- An orchestrator LLM choosing who speaks.
- Personas addressing each other without the operator in the loop.
- Mentions anywhere but the start of a message.
- Any change to the persona file format, the model registry, or the schema.
