# Context occupancy footer — Design

**Date:** 2026-06-23
**Status:** Approved (design)

## Problem

The conversation-footer HUD (`frontend/src/main.ts`, `updateFooter`) shows
`ctx N / max_context` where `N` is `totalUsage.InputTokens` — the sum of input
tokens across **every** provider call in a turn (`internal/chat/chat.go:246`,
`355`). For a single-call (no-tool) turn that sum equals the one call's input,
so it reads as the context size. For a **tool-using** turn the agentic loop
calls the provider once per iteration with the *full* replay history each time
(`chat.go:208-307`), so the running sum counts the re-sent context multiple
times.

The result: against a `/ max_context` denominator that is meant to answer "how
full is the window?", the numerator over-counts on tool turns and overstates
how close the conversation is to the limit. The cumulative figure is a useful
throughput/cost proxy, but it is the wrong number for a window-occupancy gauge,
and the two are conflated into one display value.

## Goals

- Surface **current window occupancy** as the primary `/ max_context` gauge:
  the final provider call's input tokens plus the output it produced.
- Keep the **this-turn cumulative** input/output/cache visible as a distinct,
  clearly-labelled secondary figure, so the two concepts are not conflated.
- Capture the final-call usage without a schema change — ride the existing
  `chat:usage` event and footer-render path.

## Non-goals

- **Persistence.** The footer stays in-memory/ephemeral, fed only by the live
  `chat:usage` event, exactly as today. It still blanks on conversation reopen
  / app restart until the next send. No new columns, no DB migration, no
  reseed-from-store. (The pre-existing blank-on-reopen behaviour is out of
  scope.)
- **Per-iteration history.** Only the final call's usage is tracked, not every
  call's. No per-call breakdown UI.
- **Cost display.** Tokens only, no dollars — unchanged from the original
  context-tracking design.
- **appapi changes.** `wailsSink.Emit` already forwards every payload key
  (`internal/appapi/api.go:116-118`), so new keys flow through untouched.

## Design

### Definitions

- **Current window occupancy** = `lastCall.InputTokens + lastCall.OutputTokens`,
  where `lastCall` is the usage reported by the **terminal** provider call of
  the run. This approximates where the next turn starts (the final answer is now
  part of the window).
- **This-turn cumulative** = `totalUsage.InputTokens` / `.OutputTokens` /
  `.CachedInputTokens` — the existing summed-across-iterations figures.

### Data flow

Unchanged in shape. The loop already accumulates `totalUsage`; we add one local
that tracks the final call and emit two extra keys on the existing event.

```
chat.Send loop (per iteration)
   d.Usage != nil ──▶ totalUsage += d.Usage      (cumulative, existing)
                  └──▶ lastCall   = *d.Usage      (overwrite; last write wins)
   │
   ▼  run completes
completeRunSuccess(... totalUsage, lastCall ...)
   │
   ▼  SinkUsage payload gains lastInput / lastOutput
wailsSink.Emit ──forwards all keys──▶ "chat:usage" event
   │
   ▼
frontend updateFooter renders occupancy + this-turn
```

### `internal/chat/chat.go`

- Declare `var lastCall provider.Usage` next to `totalUsage` in `runLoop`
  (the function holding the loop at line 191). `finalizeWithoutTools` makes a
  single provider call and currently receives `totalUsage` as a parameter; it
  declares its **own** local `lastCall` for that one call.
- In the main stream loop (~`245`) and in `finalizeWithoutTools`'s stream loop
  (~`354`), inside the existing `if d.Usage != nil` block: keep the three
  `totalUsage += ...` lines, and add `lastCall = *d.Usage`. The terminal call's
  usage is the last one written — for `runLoop` that is the final iteration; for
  `finalizeWithoutTools` it is its one call.
- `completeRunSuccess` gains a `lastCall provider.Usage` parameter and adds two
  keys to the existing `SinkUsage` payload map:

  ```go
  emit(p.Sink, SinkUsage, p.ConversationID, runID, turnID,
      map[string]any{
          "input":      totalUsage.InputTokens,
          "output":     totalUsage.OutputTokens,
          "cached":     totalUsage.CachedInputTokens,
          "lastInput":  lastCall.InputTokens,
          "lastOutput": lastCall.OutputTokens,
          "modelID":    p.Model,
      })
  ```

- Both call sites of `completeRunSuccess` (the in-loop success return at ~`264`
  and the `finalizeWithoutTools` return at ~`369`) pass their `lastCall`.
  `RunResult` is unchanged; `RunTotals` / `CompleteRun` are unchanged (the
  persisted run still records only the cumulative totals — occupancy is
  ephemeral by decision).

### `internal/appapi`

No changes. `wailsSink.Emit` copies every `e.Payload` key into the emitted
event (`api.go:116-118`).

### Frontend — `frontend/src/main.ts`

- Extend the `Usage` type: add `lastInput: number; lastOutput: number`.
- The `chat:usage` handler already spreads `...p` into the map entry, so the new
  fields are stored automatically; no handler change beyond the type.
- Rewrite `updateFooter()`'s render line to the verbose two-group format:

  ```
  context <occupancy>[ / <max>] · this turn <input>→<output> · cache <cached>
  ```

  where `occupancy = u.lastInput + u.lastOutput`.

  - The `~` stale marker prefixes the **occupancy** number:
    `context ~202,000 / 1,000,000 · this turn …`.
  - When `max_context` is 0, drop the ` / <max>` segment (existing rule).
  - Fallback: if `lastInput`/`lastOutput` are absent or `NaN` (e.g. an
    in-flight event from before this change), occupancy falls back to
    `u.input` so the strip never renders `NaN`.
- This replaces the interim `· out <output>` segment added earlier; output now
  lives in the `this turn <input>→<output>` group.

### Error handling & edge cases

| Case | Behavior |
| --- | --- |
| Single-call (no-tool) turn | `lastCall` == the only call; occupancy ≈ the `this turn` input+output. The two segments nearly coincide — correct. |
| Final provider call omits usage (some `openai_compat` servers) | `lastCall` retains the most recent call that *did* report usage; if none ever did, occupancy is 0 and the existing `~`/blank degradation applies — consistent with the current footer. |
| `max_context` missing in `models.yaml` | Occupancy shown without a denominator. No error. |
| Occupancy exceeds `max_context` | Rendered as-is (e.g. `1,040,000 / 1,000,000`). The provider accepted the calls; the limit value was wrong, not the run. |
| Stream cancelled / errored mid-flight | No `chat:usage` emitted; footer marks its entry stale (`~`) as today. |

### RAG boundary

Unchanged. No files under `internal/rag/{embedding,chunker,ragindex}/` are
touched.

## Testing

### Chat orchestration tests (`internal/chat`)

- **Tool turn, divergent occupancy:** fake `ChatProvider` drives two iterations
  — call 1 `Usage{Input:50000, Output:0}` + a tool call, call 2
  `Usage{Input:200000, Output:2000}` ending the turn. Assert the captured
  `SinkUsage` payload carries cumulative `input=250000`, `output=2000`, and
  `lastInput=200000`, `lastOutput=2000`.
- **Single-call turn:** one call `Usage{Input:120, Output:45}` ending with
  `end_turn`. Assert `lastInput=120`, `lastOutput=45`, and they equal the
  cumulative `input`/`output`.
- **Final call without usage:** call 1 reports usage, the terminal call reports
  none. Assert `lastInput`/`lastOutput` reflect the last call that *did* report
  (documented degradation), and no panic.

### Frontend — manual smoke (`docs/SMOKE.md`)

- Add a step under "Context tracking footer": on a tool-using turn (textbook
  attached, a question that triggers a search), the `context` occupancy is
  visibly smaller than the `this turn` input. On a no-tool turn the two nearly
  match. Confirm the `~` marker still prefixes the occupancy number after Stop,
  and the denominator drops when `max_context` is unset.

## Decisions

- **Occupancy = final call input + its output.** A forward-looking "where the
  next turn starts" gauge, over the alternative of final input only.
- **Ephemeral, no schema change.** Occupancy rides the live `chat:usage` event;
  the persisted run still records cumulative totals only.
- **Raw last-call numbers on the event, occupancy composed in the frontend.**
  The event stays self-describing (`lastInput` + `lastOutput`); the UI owns the
  `+` and the layout. Preferred over emitting a single pre-summed `contextSize`.
- **Verbose two-group footer.**
  `context <occ> / <max> · this turn <in>→<out> · cache <cached>` — the clearest
  separation of window-occupancy from this-turn throughput.
- **Cumulative retained, not replaced.** The this-turn figure stays visible (the
  user's goal was to *distinguish* the two, not drop one).
