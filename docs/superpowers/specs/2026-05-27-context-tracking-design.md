# Context tracking — Design

**Date:** 2026-05-27
**Status:** Approved (design)

## Problem

Starshp sends multi-turn conversations to Claude and OpenAI models, but the
app has no visibility into how much of each model's context window the
conversation occupies. Both provider stream loops drop their SDK's
end-of-stream usage block on the floor:

- `internal/provider/anthropic.go:50` reads `ContentBlockDeltaEvent` for text
  and discards `MessageStartEvent` / `MessageDeltaEvent`, which carry input,
  output, and cached-input tokens.
- `internal/provider/openai.go:44` reads `chunk.Choices[0].Delta.Content` and
  never sets `StreamOptions.IncludeUsage`, so OpenAI omits the usage block
  from streaming responses.

`internal/store/schema.go` has no token columns on `messages`, and
`provider.Delta` (`internal/provider/provider.go:18`) has no usage field, so
even if the SDKs surfaced usage there's nowhere to put it. The user cannot
answer "how close is this conversation to the model's limit?" until a send
fails with the existing `context_length` error
(`internal/provider/errors.go:23`).

## Goals

- Capture exact input, output, and cached-input token counts from each
  provider's stream-end usage block, for every assistant message.
- Persist usage per assistant message in `app.db`.
- Display a live conversation-footer HUD: `ctx 12,430 / 200,000 · cache 8,200`.
- Make the per-model context-window limit (the denominator) configurable in
  `models.yaml` without recompile, so future models slot in by editing YAML.
- Degrade honestly when usage is partial: show last-known with a `~` marker.

## Non-goals

- **Pre-flight token counting.** No CountTokens API calls, no tiktoken-go
  dependency, no "will this fit?" indicator while typing. Post-flight only.
- **Hard pre-flight guard.** Existing `context_length` error path is the only
  guard. Sends that exceed the model window still fail loudly through that
  path, unchanged.
- **Cost display.** No dollar amounts. No pricing fields in `models.yaml`.
  Pricing drifts silently and stale numbers mislead.
- **Per-message annotations.** Only the conversation footer surfaces usage.
  Per-bubble tags were considered and rejected as visual noise.
- **Backend changes to RAG.** `CONTEXT_TOKEN_BUDGET` is unrelated and stays as
  the RAG injection budget.

## Design

### Architecture & data flow

```
SendMessage (appapi)
   │
   ▼
chat.Send  ──persists user msg──▶  store
   │
   ▼
provider.Stream  ──emits text Deltas──▶  UI (chat:token events)
   │                ──emits final Delta{Done:true, Usage:&{...}}──┐
   ▼                                                              │
chat.Send  ◀──────────────────────────────────────────────────────┘
   │
   ├──persists assistant msg with usage cols──▶ store
   │
   ▼
appapi emits "chat:usage" event {convID, input, output, cached}
   │
   ▼
frontend updates conversation footer HUD
```

The flow rides on the existing send pipeline. No new background workers, no
new processes. Three changed seams: `provider/` (the `Delta` shape and the
two SDK call sites), `chat/` (capture the final usage), `appapi/` + frontend
(emit + render the HUD).

### `internal/provider/provider.go`

`Delta` gains a nullable `Usage` field. `ChatProvider` and `ChatRequest` are
unchanged.

```go
type Usage struct {
    InputTokens       int
    OutputTokens      int
    CachedInputTokens int  // subset of InputTokens served from prompt cache
}

type Delta struct {
    Text  string
    Done  bool
    Err   error
    Usage *Usage  // non-nil only on the terminal Done frame, when SDK surfaced usage
}
```

`Usage` is optional on purpose: a cancelled or errored stream, or a future
provider that does not report usage, simply leaves it nil. Cached tokens
semantics are unified — both providers report "of the input tokens we
charged you, this many were cache hits."

### `internal/provider/anthropic.go`

Extend the existing `for stream.Next()` loop with two additional event
branches:

- `MessageStartEvent` → record `event.Message.Usage.InputTokens` and
  `event.Message.Usage.CacheReadInputTokens` into a local `Usage` accumulator.
- `MessageDeltaEvent` → record `event.Usage.OutputTokens` (the running total;
  the final one is authoritative).

When the loop exits cleanly, attach the accumulated `*Usage` to the terminal
`Delta{Done: true}` frame. If `stream.Err()` is non-nil, emit `Delta{Err,
Done: true}` without usage — same as today.

### `internal/provider/openai.go`

Two changes:

- Pass `StreamOptions: openai.ChatCompletionStreamOptionsParam{IncludeUsage:
  param.NewOpt(true)}` on the `ChatCompletionNewParams`. Without this, OpenAI
  omits the usage block from streaming responses entirely.
- The final streamed chunk carries `chunk.Usage` (non-nil only on the last
  chunk). Capture `PromptTokens`, `CompletionTokens`, and
  `PromptTokensDetails.CachedTokens`, then attach `*Usage` to the terminal
  `Delta{Done: true}` frame.

### `internal/store/`

**`schema.go`** — three nullable columns on the `messages` table:

```sql
input_tokens INTEGER,
output_tokens INTEGER,
cached_input_tokens INTEGER
```

**`migrate.go`** — three additional `ALTER TABLE messages ADD COLUMN ...`
calls, each guarded by the existing `columnExists` helper. Idempotent on
fresh DBs, additive on existing ones, no down-migration needed (columns are
nullable, so a rollback to the prior binary still reads/writes cleanly).

**`Message` struct** — three new fields, `*int` so JSON omits them when
absent:

```go
InputTokens       *int `json:"inputTokens,omitempty"`
OutputTokens      *int `json:"outputTokens,omitempty"`
CachedInputTokens *int `json:"cachedInputTokens,omitempty"`
```

**`AddMessage`** — signature gains a `*provider.Usage` parameter. User
messages and migration callers pass `nil`. Internally, `nil` writes `NULL`
into all three columns.

**`ListMessages`** — `SELECT` adds the three columns and scans into the new
`*int` fields via `sql.NullInt64`.

### `internal/chat/chat.go`

Inside the existing `for d := range ch` loop, when `d.Usage != nil`, copy it
to a local `var usage *provider.Usage`. Pass `usage` through the existing
`s.st.AddMessage(...)` call at persist time.

`Send` returns the captured `*provider.Usage` so the appapi caller does not
need to re-query the store. The new return signature:

```go
func (s *Service) Send(ctx context.Context, p SendParams, onToken func(string)) (string, *provider.Usage, error)
```

Existing callers (only `appapi.SendMessage`) update accordingly. A
mid-stream error still returns whatever usage was captured (typically nil)
alongside the partial text and the normalized error.

### `internal/provider/registry.go`

`ModelInfo` gains an optional `MaxContext int` field:

```go
type ModelInfo struct {
    Display    string `yaml:"display" json:"display"`
    ID         string `yaml:"id" json:"id"`
    Provider   string `yaml:"provider" json:"provider"`
    MaxContext int    `yaml:"max_context,omitempty" json:"maxContext,omitempty"`
}
```

Omitted in YAML → Go zero value `0` → frontend renders the footer without a
denominator.

**`models.example.yaml`** updates each entry with a representative
`max_context`. **`models.yaml`** itself is per-user; the user adds the field
when they want a denominator.

### `internal/appapi/api.go`

`chatSvc.Send` now returns `(text, *provider.Usage, error)`. When `usage` is
non-nil, emit a `chat:usage` Wails event:

```go
wruntime.EventsEmit(a.ctx, "chat:usage", map[string]any{
    "convID":  convID,
    "input":   usage.InputTokens,
    "output":  usage.OutputTokens,
    "cached":  usage.CachedInputTokens,
    "modelID": modelID,
})
```

If `usage` is nil (cancel, SDK gap, mid-stream error), no event is emitted —
the frontend marks its in-memory entry stale on send-completion (see
frontend section) and renders the `~` marker.

### Frontend — `index.html`, `main.ts`, `style.css`

**`index.html`** — add a footer element between `#thread` and the input row:

```html
<div id="ctxFooter" class="ctx-footer"></div>
```

**`main.ts`** — three additions:

1. A `latestUsage` map keyed by `convID`, holding the most recently observed
   `{input, output, cached, modelID, stale: bool}`.
2. `updateFooter()` renders the footer for `activeConv`:
   - No data → empty footer.
   - Fresh data → `ctx 12,430 / 200,000 · cache 8,200`.
   - Stale data (post-cancel or no-usage send since last fresh) →
     `ctx ~12,430 / 200,000 · cache 8,200`.
   - Missing `max_context` → drop the denominator: `ctx 12,430 · cache 8,200`.
3. Subscribe to `chat:usage`. On receive, if `payload.convID === activeConv`,
   store fresh usage and re-render. Always update the map so switching back
   to that conversation seeds correctly.

**`openConversation`** — after loading messages, read the latest assistant
message's `inputTokens`/`outputTokens`/`cachedInputTokens`; if present, seed
`latestUsage[id]` as fresh. If absent and a prior in-session value exists,
mark stale.

**Post-send (in `send()`)** — when a send completes without a `chat:usage`
event (cancelled, errored, or SDK omitted usage), mark the conversation's
entry stale and re-render. The cleanest hook is to set a "pending" flag at
send-start; if no `chat:usage` arrives by the time `App.SendMessage`
resolves/rejects, flip `stale = true`.

**`style.css`** — `.ctx-footer` is a single-line strip, dim text color,
small font, no border, `padding: 4px 12px`. Hidden when empty.

### Error handling & edge cases

| Case | Behavior |
| --- | --- |
| Provider didn't surface usage | `Delta.Usage == nil`; persist with `NULL` columns; footer shows `~` marker. |
| Stream cancelled mid-flight | Terminal `Done` frame never carries usage; same `NULL` + `~` path. |
| Stream errored mid-flight | `Delta{Err, Done: true}` without usage; same `NULL` + `~` path. Existing `NormalizeError` flow unchanged. |
| `max_context` missing in `models.yaml` | `MaxContext == 0`; footer omits denominator. No error. |
| Actual usage exceeds `max_context` | Render as-is: `ctx 215,000 / 200,000`. The provider already accepted the call; the limit was wrong, not the send. |
| Schema migration on old DB | `columnExists`-guarded `ALTER TABLE ADD COLUMN` runs once and becomes a no-op. Nullable columns keep prior-binary rollback safe. |
| Conversation switched mid-stream | Existing `streaming` gate blocks this in the UI. `chat:usage` payload carries `convID`; frontend ignores events for non-active conversations (defensive). |
| No usage row ever existed for this conversation | Footer empty (no `~`, nothing to be stale against) until first successful send populates it. |

### RAG boundary

Unchanged. No files under `internal/rag/{embedding,chunker,ragindex}/` are
touched. The architectural rule in `README.md` holds.

### Error-normalization boundary

Unchanged. No new error codes. Usage capture failures (which should not
exist — usage is read, not requested) would never reach `NormalizeError`
because nil-usage is a legitimate non-error state.

## Testing

### Provider tests

- `anthropic_test.go`: new fixture mocking `message_start` with
  `usage.input_tokens=120, cache_read_input_tokens=80` and a final
  `message_delta` with `usage.output_tokens=45`. Assert the terminal `Delta`
  carries `Usage = &{120, 45, 80}`.
- `openai_test.go`: new fixture asserting the request body includes
  `"stream_options":{"include_usage":true}`, mocking a final chunk with
  `usage:{prompt_tokens:120, completion_tokens:45,
  prompt_tokens_details:{cached_tokens:80}}`. Assert the same `Delta.Usage`
  shape.
- Per provider: an "omitted usage" fixture asserting the terminal `Delta`
  carries `Usage == nil` and no panic.

### Store tests

- `AddMessage` with a non-nil `*Usage` round-trips through `ListMessages`.
- `AddMessage` with `nil` usage leaves all three columns `NULL`; the JSON
  encoding omits the three fields.
- Migration test: open a DB with the pre-token schema, call `migrate`,
  assert the three columns exist via `PRAGMA table_info(messages)`.

### Chat orchestration tests

- Fake `ChatProvider` emits `Delta{Done: true, Usage: &{100, 50, 20}}`;
  assert the persisted assistant message carries those exact numbers.
- Fake provider streams text then `Done: true` without `Usage`; assert the
  persisted assistant message has nil usage and `chat.Send` returns no error.

### Registry tests

- `models.yaml` with `max_context: 200000` round-trips to
  `ModelInfo.MaxContext == 200000`.
- `models.yaml` without `max_context` yields `MaxContext == 0`.

### API tests

- After a successful `SendMessage`, a `chat:usage` event is emitted with the
  expected `convID`, token counts, and `modelID`. Tests already intercept
  Wails events via the bound runtime in `api_test.go`.
- After a cancelled `SendMessage`, no `chat:usage` event is emitted.

### Frontend — manual smoke

Add new steps to `docs/SMOKE.md`:

- Footer renders after first reply on a fresh conversation, with denominator
  when `max_context` is set.
- Footer updates across model switches mid-conversation (denominator
  changes; values keep accumulating because they are per-message).
- Footer shows `~` marker after a Stop-button cancellation.
- Footer shows no denominator when the active model omits `max_context`.
- Footer survives conversation switches — re-seeds from the last assistant
  message of the newly-opened conversation.

### Drift detection

No changes to copied `internal/rag/{embedding,chunker,ragindex}/` packages.
Their existing tests continue to run unmodified.

## Decisions

- **Post-flight only.** No CountTokens, no tiktoken-go, no pre-flight guard.
  The free-and-exact data from end-of-stream usage is the MVP.
- **Conversation footer only.** Single HUD strip below the message thread.
  No per-message annotations, no separate cost line.
- **Tokens only, no dollars.** Pricing drifts silently; tokens are the
  resource that matters for fitting context.
- **Three columns on `messages`, not a separate `message_usage` table.**
  Matches the existing `rag_context`/`rag_sources` pattern on the same row;
  one write, no joins, fits the existing `migrate.go` idempotent pattern.
- **`Usage` carried on the terminal `Delta`.** Stays in the existing channel
  pattern. No new method on `ChatProvider`, no new return value from
  `Stream()`.
- **`max_context` in `models.yaml`, optional.** Future models slot in by
  editing YAML. Missing → footer omits denominator.
- **Stale marker over hiding.** A cancelled send keeps the prior value with
  a `~` prefix rather than blanking the footer — less visual churn.
