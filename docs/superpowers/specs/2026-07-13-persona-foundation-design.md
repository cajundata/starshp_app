# Persona Foundation — Design

**Date:** 2026-07-13
**Status:** Approved for planning
**Spec:** 1 of 2 (see Roadmap)

## Context

Starshp's original purpose — accounting coursework — is finished. The operator
has completed all accounting courses he will ever complete. Starshp becomes a
**personal team of assistants**: named personas, each backed by a chosen LLM and
its own system prompt, which the operator directs to think through problems and
streamline brainstorming.

The headline requirement from the operator: **output must be visually
attributable** — it must be obvious at a glance which assistant, running which
model, produced any given block of text.

This spec covers the **foundation**: removing the accounting surface, a persona
registry, persona-tagged runs, and color-coded chat bubbles carrying a model
chip. One persona is pinned per conversation — the same shape as today's pinned
model, so this is a substitution rather than a new concept.

Multi-persona threads are deliberately Spec 2.

The decisions below were settled during brainstorming and are not re-opened
here:

- **Personas are the unit, orchestration is manual.** The operator explicitly
  chooses which persona speaks. No routing LLM, no automatic delegation.
- **File-backed personas.** Markdown + YAML frontmatter, one file per persona.
  Editable in any editor, diffable, trivially backed up. No in-app editor.
- **Color keys off the persona, not the model.** Two personas may share a model,
  so the persona owns the color and each bubble carries a muted model chip. The
  chip is what answers "which model produced this?"; the color answers "who?".
- **Persona = who, library = what.** The existing library is not absorbed or
  replaced. A persona's body is its identity; library items remain reference
  material attached to a conversation.
- **Persona replaces the model picker.** A persona's `model:` is authoritative.
  There is no separate model override.
- **Accounting is removed, not frozen.** The prior spec froze it; this spec
  deletes it. Git retains it.

## Roadmap (context for this spec's boundary)

- **Spec 1 (this doc):** accounting removal, persona registry, persona-tagged
  runs, color-coded bubbles with model chip. One persona per conversation.
- **Spec 2:** multi-persona threads. `@Persona` routing within a single
  conversation, baton-pass context assembly (a persona receives the operator's
  messages plus the immediately preceding persona's output, not the full shared
  thread), and the interaction between mid-thread persona switches and the
  `active_for_replay` run model.

Spec 2's context-assembly design is the real design risk in this pivot. Spec 1
exists so that personas can be used and lived with before that risk is taken.

## Architecture

A new `internal/persona` package owns the persona registry, mirroring
`internal/provider`'s registry pattern. `internal/appapi` gains a `Personas()`
binding and resolves persona → model + system prompt + tool subset before
calling `chat.Send`. `internal/store` gains two additive columns. The frontend's
model dropdown becomes a persona dropdown, and run bubbles gain an attribution
header.

```
frontend (persona picker, colored run bubbles w/ model chip)
        │  Wails-bound calls + chat:* events
        ▼
internal/appapi (Personas(); resolves persona → model, prompt, tools)
        │
        ├──► internal/persona   (registry: load, validate, color-assign)
        ├──► internal/provider  (unchanged: model registry, adapters)
        ├──► internal/library   (unchanged: reference items)
        └──► internal/chat      (unchanged loop; receives resolved inputs)
                    │
                    ▼
            internal/store (runs.persona_id, conversations.pinned_persona)
```

`internal/chat` and `internal/provider` are **not modified** beyond the
`chat:run_started` payload and the `persona_id` write on `CreateRun`. The
agentic loop does not learn what a persona is; it continues to receive a system
prompt, a model ID, and a tool registry, exactly as it does today.

## Persona File Format

One file per persona in `<app-dir>/personas/`. The filename stem is the persona
ID (`scout.md` → `scout`). IDs are lowercase, `[a-z0-9-]`.

```markdown
---
name: Scout
model: claude-opus-4-8
color: "#4fb3ff"        # optional
tools: [safemath]        # optional; omitted = all registered tools
library: [style-guide]   # optional; library items auto-attached for this persona
---
You are Scout. You find the opportunities others miss...
```

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | Display name in the picker and bubble header |
| `model` | yes | Must exist in the model registry (`models.yaml`) |
| `color` | no | Hex. Auto-assigned from a palette if omitted |
| `tools` | no | Whitelist of tool names. Omitted means all tools |
| `library` | no | Library item IDs auto-attached whenever this persona runs |

The body (everything after the frontmatter) is the persona's system prompt.

### Validation

`persona.LoadRegistry(dir)` runs at startup in `main.go`, after
`provider.LoadRegistry` (it depends on the model registry) and after the tool
registry is constructed.

A persona is **rejected** — reported and disabled, not fatal — if its `model` is
absent from the model registry, a name in `tools` is not a registered tool,
`color` fails to parse as hex, `name` is empty, or its ID collides with another
file. A typo in one persona file must not lock the operator out of the app.
Rejections surface to the frontend so a persona that silently vanished from the
picker is explainable.

If `<app-dir>/personas/` does not exist at startup, it is created and seeded
with exactly one persona, `assistant.md` — name `Assistant`, `model` set to the
first entry in `models.yaml`, no `tools` or `library` restriction, and a
general-purpose system prompt. This preserves today's plain-chat behavior as the
out-of-the-box default and guarantees the app is never in a zero-persona state
where no message can be sent. Seeding happens only when the directory is absent;
an existing directory is never written to.

### Color assignment

`color` is optional. When omitted, a color is assigned deterministically by
hashing the persona ID into a curated palette. The palette is fixed in Go, and
each entry is verified for contrast against the `#1d1d20` assistant-bubble
background and the `#2b2b30` border, so an auto-assigned color is always legible
in the dark theme. Adding a persona requires no color thinking; specifying one
is always available.

## Prompt Assembly

`assembleSystemPrompt` (`internal/appapi/library.go:118`) is extended, not
replaced. Composition order:

1. The persona's markdown body (identity, role, style).
2. Library items named in the persona's `library:` frontmatter.
3. Library items attached to the conversation by the operator.

Items appearing in both 2 and 3 are included once. Order is stable — a persona's
own reference material precedes conversation-scoped material, so a conversation
attachment reads as an addition to the persona's standing context rather than an
interruption of it.

## Tools

A persona's `tools:` list filters the tool registry passed into `chat.Send`.
This is the same mechanism the (now-deleted) assignment orchestrator used to
build a per-item registry, so the plumbing is already proven. Omitting `tools:`
passes the full registry.

## Data Model

Two additive columns. No lossy migration.

```sql
ALTER TABLE runs ADD COLUMN persona_id TEXT;              -- nullable
ALTER TABLE conversations ADD COLUMN pinned_persona TEXT; -- nullable
```

`runs.provider` and `runs.model` already exist and continue to be written —
the persona supplies the model, and the model is still recorded on the run.
`conversations.pinned_model` is retained and still written, so historical rows
keep their meaning.

**Pre-persona runs** have `persona_id = NULL`. They render as a neutral gray
bubble carrying only the model chip. This is honest about what is known rather
than fabricating a persona for output that never had one.

### Removals

```sql
DROP TABLE assignment_items;
DROP TABLE assignments;
-- conversations.assignment_id dropped
```

## Event Flow

`chat:run_started` (`internal/chat/chat.go:118`) gains `personaID`, `modelID`,
and `provider` in its payload. The bubble is therefore colored the instant it is
created, with no uncolored flash and no post-hoc recolor.

`store.GetConversationDisplayEvents` (`internal/store/replay.go:66`) and its
query `eventsForRunsPlusUserMessages` (`replay.go:116`) join `runs` on
`conversation_events.run_id`. `appapi.EventDTO` (`internal/appapi/api.go:538`)
gains `personaID` and `modelID`.

**The DTO carries IDs only — not names or colors.** The frontend resolves
display name and color from the persona registry it has already fetched via
`Personas()`. Editing a persona's color in its markdown file therefore recolors
all of that persona's history on next launch, with no data migration. It also
keeps the DTO small and keeps a single source of truth for presentation.

Live output and replayed history now derive attribution from the same two
fields, so a bubble cannot look one way live and another way after reload.

## Frontend

### Picker

`#modelSel` in `frontend/index.html` becomes `#personaSel`, populated from
`API.Personas()` in `loadMeta` (`frontend/src/main.ts:327`).

`App.SendMessage(convID, text, personaID)` — the third argument changes meaning
from model ID to persona ID. The backend resolves it. Reopening a conversation
restores `pinned_persona` exactly as it restores `pinned_model` today
(`main.ts:313`).

### Bubble

The run bubble gains an attribution header:

```
┌─────────────────────────────────────────┐
│ ● Scout            [opus-4.8]           │   dot + name in persona color;
├─────────────────────────────────────────┤   model chip muted, monospace
│ There are three angles here...          │
└─────────────────────────────────────────┘
 ▏← 3px left stripe, persona color
```

`ensureRunBubble` (`main.ts:95`) sets a `data-persona` attribute and an inline
`--persona-color` custom property on the bubble element. `style.css` consumes
that custom property for the stripe, the dot, and the name. No CSS-in-JS, no
generated per-persona stylesheet — one rule set, parameterized per bubble.

Both live bubbles and replayed bubbles are built by `ensureRunBubble`, so they
are styled by the same code path.

The model chip is deliberately quiet: muted gray, small, monospace. It answers
"which model?" when asked. It does not compete with the persona color.

**Unknown persona ID** (the operator deleted the markdown file) falls back to
the neutral gray bubble with the literal ID shown in place of a name. History
never breaks because a persona file was removed.

### Footer

The context-occupancy footer (`updateFooter`, `main.ts:23`) is unchanged in
behavior and additionally displays the active persona name.

## Accounting Removal

Deleted:

- `internal/assignment/` in its entirety, including the three hardcoded
  accounting prompts in `render.go:8,14,31` — the only domain-hardcoded prompts
  in the Go codebase.
- Assignment endpoints in `internal/appapi/` (`api.go`, `adapters.go`).
- The assignments view and detail pane in `frontend/src/main.ts`, including
  `openItemDetail` (`main.ts:1072-1161`).
- The `assignments` and `assignment_items` tables; the
  `conversations.assignment_id` column.
- Accounting fixtures in `internal/eval/testdata/fixtures/`.
- The stale "single coursework tax problem" comment at `internal/chat/chat.go:24`.

`main.ts:1072-1161` is a **second, duplicated copy of the entire run-bubble
building logic**, maintained in parallel with the chat view's copy. Removing the
assignments view means the bubble changes in this spec are written once rather
than twice, and no second copy can drift. This is the reason removal is
sequenced first rather than deferred.

Retained: `internal/rag`, `internal/textbooks`, the `search_textbook` tool,
`safemath`, `probe`, the business pipeline, and the context-occupancy footer.
This machinery is genre-neutral and will serve document-grounded personas.

## Error Handling

Consistent with the existing boundary: everything crossing `appapi` becomes a
typed `provider.AppError`.

| Condition | Behavior |
|---|---|
| Persona file fails validation | Disabled, not fatal. Reported to the frontend and excluded from the picker. |
| `personas/` directory missing | Created and seeded with `assistant.md`. |
| Directory exists but yields zero valid personas | Not re-seeded — an existing directory is never written to. The picker is empty and the send path returns `AppError{Code:"config"}` listing the validation failures, so the cause is visible rather than papered over by a surprise default persona. |
| `SendMessage` with an unknown persona ID | `AppError{Code:"config"}`. No silent fallback to a default persona — a silent substitution would produce output attributed to the wrong assistant, which is precisely the failure this feature exists to prevent. |
| Persona's model lacks an API key | Existing `AppError{Code:"auth"}` from `provider.New`, unchanged. |
| Bubble references an unknown persona ID | Neutral gray, literal ID as the name. Never an error. |

## Testing

**Go:**

- Persona registry: valid load; unknown model; unknown tool; unparseable color;
  empty name; duplicate ID; missing directory (seeds); directory with only
  invalid files.
- Color assignment: deterministic for a given ID; every palette entry meets the
  contrast threshold against the bubble background.
- Prompt assembly: composition order; deduplication between a persona's
  `library:` items and conversation-attached items.
- Store: `persona_id` round-trips on `runs`; the replay join returns persona and
  model on every assistant event; `persona_id = NULL` rows replay cleanly.
- Migration: against a database carrying the old `assignments` /
  `assignment_items` tables, `conversations.assignment_id`, and no `persona_id`.

**Manual (`docs/SMOKE.md`):** the frontend has no test harness. Added steps:

- Persona picker lists all valid personas; an invalid one is absent and its
  rejection is visible.
- Send with persona A, send with persona B in separate conversations — bubbles
  carry distinct colors, correct names, correct model chips.
- **Close and reopen a conversation: replayed bubbles come back the same colors
  they were live.** This is the highest-value check in the suite; live/replay
  divergence is the failure mode this design is built to prevent.
- Delete a persona's markdown file and reopen its conversation: history renders
  gray with the literal ID, no error.
- Edit a persona's color and relaunch: that persona's history recolors.
- A conversation created before this feature replays as neutral gray with a
  model chip.

## Out of Scope

- `@Persona` mentions and multi-persona threads (Spec 2).
- Baton-pass context assembly (Spec 2).
- An in-app persona editor. Personas are files.
- Per-conversation model override. The persona's `model:` is authoritative.
- Fanning one prompt out to several personas in parallel.
- Gemini or any new provider. The existing Anthropic / OpenAI / `openai_compat`
  set is unchanged.
