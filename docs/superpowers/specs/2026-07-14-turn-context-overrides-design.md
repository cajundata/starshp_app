# Turn Context Overrides — Design

**Date:** 2026-07-14
**Status:** Approved for planning
**Depends on:** [Multi-Persona Threads](2026-07-13-multi-persona-threads-design.md)
(Spec 2) — this feature layers on Spec 2's persona-aware context assembly and
must not begin implementation until Spec 2 has shipped.

## Context

Starshp can *measure* context but cannot *steer* it. The occupancy footer
(2026-06-23 spec) reports how full the window is, yet every send replays the
entire history unconditionally — the operator has no lever to pull when the
number climbs. Meanwhile Spec 2 introduces the first **implicit** dropping:
a foreign persona's turn that is not the immediate predecessor is omitted from
the payload entirely, with no way to say "no — Skeptic still needs Scout's
turn-1 analysis five turns later."

This spec gives the operator a manual, per-turn control over both directions:
pin a turn so it always survives, or exclude a turn so it stops being re-sent.
It is the same stance Spec 2 took on routing — **the operator decides, nothing
is silent** — applied to what the speaker sees instead of who speaks.

The decisions were settled during brainstorming and are not re-opened here:

- **Tri-state per turn.** `auto` / `always` / `never`. One state covers both
  pruning bloat and pinning across persona boundaries; two separate features
  would each be half of it.
- **Whole-turn granularity.** A turn is the operator's message plus the
  exchange it produced (the active run's events). No per-message split — a
  dangling question invites the model to re-answer it. No sub-message
  selection — slicing text would make the provider payload diverge from the
  persisted event log, the exact divergence Spec 2 rejected for
  mention-stripping.
- **Spec 3, after Spec 2.** Same seam (`canonicalEvents`), touched twice on
  purpose. Each spec keeps its own byte-identical regression guard instead of
  one enlarged blast radius.
- **A separate override table, not a column on events.** `conversation_events`
  stays an append-only log of what happened; mutable operator intent lives in
  its own table, the way mutable run state already lives on `runs`.
- **Payload-only effect.** The displayed thread never changes. Overrides shape
  what the model sees, never what the operator sees.

## Semantics

Each turn carries one of three states, keyed by `turn_id`. Turn IDs are
globally unique — a turn's ID *is* its `user_message` event's ID, the primary
key of `conversation_events` — so `turn_id` alone identifies the turn;
`conversation_id` rides along for per-conversation lookup and cascade deletion,
not for uniqueness.

| State | Meaning |
|---|---|
| `auto` (default; no row stored) | Exactly Spec 2's rules: own-persona turns verbatim with tool blocks, immediate foreign predecessor folded into an attributed user-role block, older foreign turns omitted. |
| `always` | Guaranteed into every future payload. For the current persona's own turns this is a no-op today (they are already always included) — a forward guarantee. For a **foreign** persona's turn it overrides Spec 2's omission rule: the turn arrives as the attributed `From Scout (model):` user-role block even when it is not the immediate predecessor. Tool blocks are still dropped — Spec 2's dangling-ID reasoning is unchanged by pinning. |
| `never` | The turn contributes nothing to the payload: not the run's events **and not the operator's message either**. A dangling question invites a re-answer, so the whole exchange goes. |

Four rules govern the edges:

1. **Payload only.** Display events never consult overrides; the thread always
   shows the full history.
2. **A turn currently being run is exempt.** A rerun of a `never` turn still
   includes that turn's own user message as its prompt. The override governs
   the turn *as history* for later turns, never the turn being answered.
3. **Excluding the handoff baton is legal.** Marking the immediately-preceding
   foreign turn `never` means the next persona gets no baton — identical to
   Spec 2's errored-predecessor case. Not an error.
4. **The regression guard.** A conversation with zero override rows produces a
   **byte-identical payload** to Spec 2's output. Row absence *is* `auto`, so
   this is structural, not logical. First test to write.

## Architecture

`internal/provider` is not modified. Spec 2's mention parsing and routing are
not modified.

```
frontend (per-turn hover control)
        │  App.SetTurnContextOverride(convID, turnID, state)   (new)
        ▼
internal/appapi ── validates state ∈ {auto, always, never}
        ▼
internal/store  ── turn_context_overrides (new table)
        │           'auto' → DELETE the row; absence is the default
        │           replay LEFT JOINs it, as runs.persona_id already is
        ▼
internal/chat   ── canonicalEvents honors ContextOverride per event
```

### Storage

```sql
CREATE TABLE IF NOT EXISTS turn_context_overrides (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    turn_id         TEXT NOT NULL PRIMARY KEY,
    state           TEXT NOT NULL CHECK (state IN ('always','never'))
);
```

Created idempotently in the existing migration pattern. The table holds only
exceptions and stays tiny. Conversation deletion removes its override rows
alongside its events via the `ON DELETE CASCADE` — the same convention every
other conversation-scoped table in `schema.go` follows; no hand-written
cleanup code.

### `internal/store`

- `SetTurnContextOverride(convID, turnID, state)` — upsert; `auto` deletes the
  row.
- `GetTurnContextOverrides(convID) (map[string]string, error)` — turn → state,
  for UI seeding on conversation open.
- **Replay path:** the `never` filter applies **only on the
  `GetProviderReplayEvents` path**. `turnSelection` and
  `eventsForRunsPlusUserMessages` are shared helpers — they also serve
  `GetConversationDisplayEvents` — so the filter must arrive as a
  provider-path-only parameter (or live in a provider-path wrapper), never
  unconditionally inside the shared helpers, which would hide the turn from
  the displayed thread and violate rule 1. On the provider path,
  `turnSelection` skips turns marked `never` and
  `eventsForRunsPlusUserMessages` drops those turns' `user_message` events
  (today user messages are included unconditionally — this is the one behavior
  change in the store). The current run's turn is exempt per rule 2.
- `ConversationEvent` gains a `ContextOverride string` field, joined on — the
  same ride-along pattern as `PersonaID` and `Model`.
- `GetConversationDisplayEvents` does not consult overrides (rule 1).

### `internal/chat`

`canonicalEvents` — by then Spec 2's persona-aware version — reads
`ContextOverride` per event. Its decision table gains a leading column:

| Override | Effect |
|---|---|
| `never` | Skip. (Already filtered by the store; skipped here too, defensively.) |
| `always`, foreign persona, any distance | Fold `assistant_text` into the attributed user-role block — Spec 2's immediate-predecessor treatment, extended to any position. Tool blocks dropped. No double inclusion when the pinned turn *is* the immediate predecessor. |
| `always`, own persona or pre-persona | Verbatim, as `auto` — the pin is a forward guarantee, not a format change. |
| `auto` / empty | Spec 2's rules, unchanged. |

`chat` still does not know what a persona is. `ContextOverride` is a string on
the event; the Spec 1 boundary holds.

Overrides are read from the store at each provider call. A toggle never
retroactively affects streamed output; within a multi-iteration tool run it
may reach the run's later iterations, and it always applies from the next
send. The current turn is exempt either way (rule 2).

### `internal/appapi`

- New bound method `SetTurnContextOverride(convID, turnID, state)`. Unknown
  turn or invalid state → `AppError{Code:"config"}`, consistent with the
  typed-error boundary.
- New bound method `GetTurnContextOverrides(convID)` returning the override
  map. The frontend calls it on conversation open, alongside the existing
  event load — no change to the shape of the existing load path.

## Frontend

The control follows the existing hover-affordance pattern (as the copy button
does): hovering a turn reveals a small button that cycles
**auto → always → never → auto**. One control per exchange, anchored on the
turn's user-message bubble — the turn's stable anchor (the assistant side may
be an error or still streaming).

State is visible without hovering:

- **Pinned** — a small pin glyph beside the existing model chip.
- **Excluded** — both bubbles render dimmed (reduced opacity): at a glance the
  operator sees what the model will not see, while the text stays readable.
- **auto** — exactly today's rendering. A conversation with no overrides is
  visually unchanged.

On conversation open the frontend fetches the override map and applies
glyphs/dimming. The occupancy footer needs no changes — the send after an
exclusion simply reports lower occupancy, closing the loop the footer opened:
it can now measure the effect of a lever instead of only reporting drift.

## Error Handling

| Condition | Behavior |
|---|---|
| `SetTurnContextOverride` with unknown turn or invalid state | `AppError{Code:"config"}`. Nothing persisted. |
| Every turn marked `never` | Payload is the new user message alone. Legal. |
| `always` on a turn whose run errored (no `assistant_text`) | Nothing to pin; the operator message is included as always. No-op, not an error. |
| `always` on a foreign turn whose persona file was deleted | Attribution falls back to the literal persona ID — Spec 2's rule, unchanged. |
| Turn has multiple runs (rerun feature) | Orthogonal: `active_for_replay` picks **which run represents the turn**; the override decides **whether/how the turn appears**. They compose with no special case. |
| Toggle while a run is in flight | Never retroactive. May reach later tool iterations of the in-flight run; always applies from the next send. |

## Testing

**The regression guard, written first.** Spec 2's payload fixtures —
single-persona multi-turn with tools, the Scout → Skeptic → Scout thread, and
legacy no-persona runs — re-run with zero override rows must produce
**byte-identical provider payloads**. Everything else is secondary to this.

**Store** — upsert and round-trip; `auto` deletes the row; a `never` turn's
`user_message` and run events are absent from replay while display events are
untouched; a rerun of a `never` turn still includes its own user message;
conversation delete cleans override rows; migration is idempotent.

**Chat** — table-driven over a multi-persona thread: `always` on a
non-adjacent foreign turn produces the attributed block with tool blocks
absent; `always` on the immediate predecessor does not duplicate it; `never`
on the immediate predecessor means no baton; own-persona `always` stays
verbatim; a foreign persona's tool events never appear in any payload, pinned
or not.

**appapi** — invalid state rejected; unknown turn rejected; override map
round-trips on conversation open.

**Manual (`docs/SMOKE.md`)** — the hover control cycles through three states;
dimming and the pin glyph render; occupancy visibly drops on the send after
excluding a heavy turn; overrides survive an app restart; the displayed thread
never changes as overrides toggle.

## Out of Scope

- Sub-message granularity (text selections, paragraphs).
- Per-message (half-turn) control.
- Automatic eviction, summarization, or any token-budget-driven dropping.
- Bulk operations ("exclude everything before turn N").
- Any change to Spec 2's persona rules, the mention grammar, or the schema
  beyond the one new table.
