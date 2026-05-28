# Tool calling — Design

**Date:** 2026-05-28
**Status:** Approved (design)

## Problem

Starshp today is a single-shot chat pipeline: `chat.Service.Send` persists
the user message, runs a single pre-turn RAG retrieval, calls
`provider.ChatProvider.Stream` once, and persists the assistant text. The
model has no way to ask for more context mid-turn, no way to run
deterministic arithmetic against its own claims, and no way to verify a
source before answering. For coursework-grade accounting and tax answers
this ceiling is the limiting factor on accuracy.

The product use case driving this change is concrete: when Starshp answers
an accounting or tax question, the model should be able to decide — mid-turn
— "I need the textbook rule, the current-year constant, and a deterministic
calculation check before I answer," then act on that decision and ground its
final answer in the result.

The existing `messages` table assumes one row per role-turn and assumes one
assistant reply per user message. Both assumptions break the moment a turn
contains tool calls and tool results. The current `Delta` shape carries text
+ optional usage and nothing else, so it cannot surface a tool-use stop
reason or a tool call to the loop. The current single-channel send path has
nowhere to put an agentic iteration boundary.

This phase delivers the foundation: the agentic loop, the provider
extensions for both OpenAI and Anthropic, the canonical event-log
persistence model, the in-process tool registry, two anchor tools that
exercise the loop end-to-end, observability/provenance from day one, and a
lightweight eval harness.

## Goals

- Replace the single-shot send with a run-oriented agentic loop:
  stream → tool_use → execute → tool_result → stream → repeat, with a
  max-iterations cap and full cancellation propagation.
- Persist every step as a discrete event in a canonical
  `conversation_events` log, with explicit `turn_id` and `run_id` so retries,
  regenerations, and cancellations are durably modelled.
- Extend `provider.ChatProvider` so OpenAI and Anthropic adapters both
  support tool definitions, tool calls, and tool results — with adapters
  owning all per-provider wire translation.
- Ship two model-callable tools that prove the loop:
  - `search_textbook` — model-driven escalation over the existing RAG
    adapter, returning source-grounded chunks with stable source IDs.
  - `safe_math` — deterministic decimal arithmetic, with a small custom
    parser, no I/O, hard execution timeout.
- Keep pre-turn RAG as the default grounding layer, distinct from
  model-called `search_textbook`. Surface the grounding metadata to the
  model so it knows what context it already has.
- Surface tool activity to the UI as inline collapsible blocks on the
  assistant bubble (search/calculation/result/error) via new Wails runtime
  events, with the existing Stop button cancelling the whole loop.
- Build observability and provenance in from day one: per-tool latency,
  iteration counts, persisted full capped tool results, stable source IDs,
  structured logs at every loop boundary.
- Ship a lightweight eval harness alongside the loop — loop-level + tool-
  level Go tests plus a small set of quality fixtures — so we can detect
  regressions before Phase 2.

## Non-goals

- **Phase 2+ domain tools.** `assignment_parser`, `question_type_router`,
  `answer_formatter`, `verifier`, `irs_tax_constants`, `tax_calculator`,
  `tax_research_tool`, `source_authority_resolver`, `table_extractor`,
  `concept_answer_verifier`, `calculation_ledger_builder`. These need their
  own design conversations once the loop is running.
- **Cross-cutting systems.** `mistake_memory` (a persistent learning store)
  and the full `eval_harness` platform (LLM-as-judge, scoring rubrics,
  dashboards). The Phase 1 eval harness is intentionally Go-tests-only.
- **MCP or user-installable tools.** Tool registration is in-process at
  startup. Pluggable / external tools are Phase D.
- **User-facing retrieval-mode toggle.** The retrieval-mode enum exists
  internally with a developer override (`STARSHP_SKIP_AUTO_GROUNDING`), but
  no UI control. Most users will not know which mode they need; we will
  surface this only after the agentic mode earns its keep.
- **Parallel tool execution within a single iteration.** A provider response
  may emit N tool calls; Phase 1 executes them sequentially, in emitted
  order. Parallel dispatch is a Phase 2 problem.
- **Loop-driven tool retries.** If a tool call fails, the failure becomes a
  `tool_result` with `is_error=true` and the model decides whether to retry
  by calling again. The loop never retries on its own.
- **Streaming partial tool-call JSON to the UI.** The loop waits for the
  complete `Input` JSON to be assembled before emitting `chat:tool_call`.
  Partial-JSON UI rendering is a future optimization.
- **Cross-turn tool-result token budgeting.** Each tool caps its own output
  at the tool boundary (`search_textbook` ~4,000 chars, `safe_math` numeric
  only). Aggregate budget enforcement across a turn is a Phase 2 problem.
- **Symbolic math, variables, units, currency formatting** in `safe_math`.
  Arithmetic only. Formatting belongs to a future `answer_formatter`.
- **A `pi` or constants table in `safe_math`.** Out of scope for accounting/
  tax use cases and invites scope creep.
- **Down-migrations.** The migration from `messages` → `conversation_events`
  + `runs` is forward-only. This is a desktop app; rollback story is
  reinstall.

## Design

### Architecture & data flow

```
SendMessage (appapi)
   │
   ▼
chat.Send  ── write user_message event (assigns turn_id)
   │       ── resolve retrieval_mode
   │       ── create run (status=in_progress, active_for_replay=false)
   │       ── emit chat:run_started {grounding.status: "pending"}
   │
   ├── pre-turn retrieve (if mode requires) ──▶ rag.Adapter
   │       on success: update runs.grounding_meta
   │                   emit chat:grounding_ready
   │       on failure: mark run errored, emit chat:run_errored, abort
   │
   ▼
loop iteration = 1..MAX_ITERATIONS
   │
   ├── store.GetProviderReplayEvents(convID, runID)
   │       canonical event timeline for this turn's replay
   │
   ├── provider.Stream(ChatRequest{System, Grounding, Tools, Events})
   │       │
   │       ├─ Delta{Text}            ──▶ emit chat:token
   │       ├─ Delta{ToolCall}        (buffered; surfaced after Done)
   │       └─ Delta{Done, StopReason, Usage}
   │
   ├── if accumulated text: persist assistant_text event
   │
   ├── if StopReason == "tool_use":
   │     for each ToolCall in emitted order:
   │       persist assistant_tool_call event   ← write-before-dispatch
   │       emit chat:tool_call
   │       output, isErr, latency = registry.Execute(ctx_tool, name, input)
   │       persist tool_result event (with metadata, latency, is_error)
   │       emit chat:tool_result
   │     continue loop
   │
   └── else: break loop with StopReason
       │
       ▼
   transactional completion:
       turn-scoped lock
       demote prior active run for this turn (if any)
       UPDATE this run SET status=completed, active_for_replay=true
       commit
       emit chat:run_completed
       emit chat:usage
```

The send pipeline still bottoms out at one user call. What changes is the
shape of the middle: a single provider stream becomes a loop, persistence
moves from "one assistant message" to "an ordered event log per run," and
cancellation/lifecycle becomes a first-class concern on the new `runs`
table.

### Persistence model

#### `conversation_events` — canonical event log

Each row is one event in the conversation timeline. Valid `kind` values:

- `user_message`
- `assistant_text`
- `assistant_tool_call`
- `tool_result`

There is no `assistant_final` kind. Finality is computed at read time, not
stored.

```sql
CREATE TABLE conversation_events (
    id                  TEXT PRIMARY KEY,
    conversation_id     TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    turn_id             TEXT NOT NULL,
    run_id              TEXT,                          -- NULL for user_message
    sequence_index      INTEGER NOT NULL,              -- monotonic, conversation-scoped, gap-tolerant
    kind                TEXT NOT NULL CHECK (kind IN (
                            'user_message',
                            'assistant_text',
                            'assistant_tool_call',
                            'tool_result'
                        )),
    text                TEXT,                          -- user_message, assistant_text, tool_result.output
    tool_call_id        TEXT,                          -- assistant_tool_call: provider-assigned id
                                                       -- tool_result:        FK to the call's id
    tool_name           TEXT,                          -- assistant_tool_call, tool_result
    tool_input          TEXT,                          -- assistant_tool_call: raw JSON input
    tool_metadata       TEXT,                          -- tool_result: JSON metadata (sources, hashes, etc.)
    tool_result_hash    TEXT,                          -- tool_result: integrity hash of text
    tool_latency_ms     INTEGER,                       -- tool_result
    is_error            INTEGER NOT NULL DEFAULT 0,    -- tool_result: 0|1
    created_at          INTEGER NOT NULL               -- unix ms, for audit only — not replay ordering
);

CREATE INDEX conversation_events_conv_seq
    ON conversation_events(conversation_id, sequence_index);
CREATE INDEX conversation_events_turn
    ON conversation_events(turn_id);
CREATE INDEX conversation_events_run
    ON conversation_events(run_id);
```

Notes:

- `sequence_index` is the authoritative append/audit order. It is monotonic,
  conversation-scoped, gap-tolerant, assigned at write time, and never
  renumbered. Regenerated runs receive higher indexes than the original
  events; they do not interleave back into earlier positions.
- `created_at` is informational only; replay must not order by timestamp.
- `role` is not stored. Provider role is derived from `kind` inside the
  provider adapters. This prevents drift.
- `tool_input` is stored as the raw provider JSON to preserve fidelity.
- `tool_metadata` is `ExecResult.Metadata` (JSON), distinct from
  `text` which is `ExecResult.Output` (the bytes the model saw).

#### `runs` — lifecycle metadata

```sql
CREATE TABLE runs (
    id                          TEXT PRIMARY KEY,
    conversation_id             TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    turn_id                     TEXT NOT NULL,
    status                      TEXT NOT NULL CHECK (status IN (
                                    'in_progress',
                                    'completed',
                                    'superseded',
                                    'errored',
                                    'cancelled'
                                )),
    active_for_replay           INTEGER NOT NULL DEFAULT 0,    -- 0|1
    provider                    TEXT NOT NULL,                 -- "openai" | "anthropic"
    model                       TEXT NOT NULL,
    retrieval_mode              TEXT NOT NULL,                 -- snapshot of mode enum
    grounding_meta              TEXT,                          -- JSON; NULL when not applicable
    started_at                  INTEGER NOT NULL,
    ended_at                    INTEGER,
    terminal_reason             TEXT,                          -- end_turn | tool_use_exhausted |
                                                               -- max_iterations | user_cancelled |
                                                               -- provider_error | tool_error |
                                                               -- grounding_error
    error_code                  TEXT,
    error_message               TEXT,
    total_input_tokens          INTEGER NOT NULL DEFAULT 0,
    total_output_tokens         INTEGER NOT NULL DEFAULT 0,
    total_cached_input_tokens   INTEGER NOT NULL DEFAULT 0,
    total_tool_calls            INTEGER NOT NULL DEFAULT 0,
    total_iterations            INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX runs_conv_turn ON runs(conversation_id, turn_id);

-- Exactly one active run per turn:
CREATE UNIQUE INDEX runs_one_active_per_turn
    ON runs(turn_id)
    WHERE active_for_replay = 1;
```

`grounding_meta` JSON shape:

```json
{
  "status": "ready" | "not_required" | "not_available",
  "query": "...",
  "sources": [
    {"id": "...", "book": "...", "chapter": "...", "score": 0.82, "chunk_hash": "..."}
  ],
  "injected_chars": 3120,
  "injected_tokens_estimate": 780,
  "context_hash": "..."
}
```

#### Activation: transactional, race-safe

A new run is created with `status='in_progress'` and `active_for_replay=0`.
Activation happens only on successful completion, inside one transaction
that serializes on the turn:

```sql
BEGIN IMMEDIATE;

-- Serialize completions for this turn. SQLite has no row-level FOR UPDATE;
-- BEGIN IMMEDIATE acquires a reserved lock for the whole DB, which is
-- sufficient given the single-process desktop deployment.

UPDATE runs SET active_for_replay = 0
 WHERE turn_id = ? AND active_for_replay = 1;

UPDATE runs
   SET status='completed',
       active_for_replay=1,
       ended_at=?,
       terminal_reason=?,
       total_input_tokens=?,
       total_output_tokens=?,
       total_cached_input_tokens=?,
       total_tool_calls=?,
       total_iterations=?
 WHERE id=? AND status='in_progress';

COMMIT;
```

On errored or cancelled runs, the transaction only marks the run's terminal
state. It never touches `active_for_replay` on any other run. A failed
regeneration therefore cannot demote a prior good answer.

#### Replay timeline — store-owned

`store.GetProviderReplayEvents(conversationID, currentRunID)` is the
canonical replay query. Every provider call site uses it. It returns events
ordered as the provider should see them:

1. For each turn in `turn_id` order (assigned by user-message
   `sequence_index`):
2. Pick the run for that turn — `active_for_replay=1`, OR the
   `currentRunID` if that run targets this turn and is still in-progress.
3. Within that run, order events by `sequence_index`.

Implementation is one SQL query plus an in-memory turn-by-turn assembly. It
explicitly does not order all events globally by `sequence_index`, because
regenerated events for an earlier turn carry indexes appended after later
turns.

#### Recovery of orphaned/in-progress runs

At read/load time, an `in_progress` run with a dangling
`assistant_tool_call` and no matching `tool_result` is treated as
non-replayable. Provider replay excludes it. A startup sweep reconciles its
stored `runs.status` to `errored` (with `terminal_reason='orphaned'`), but
the read path must not depend on the sweep having already run.

Cancelled runs persist whatever was emitted before cancellation: partial
assistant text events, emitted assistant_tool_call events, completed
tool_result events, and the cancellation metadata on the run row. Provider
replay excludes them. Eval/audit tooling can read them.

### Retrieval mode enum

Lives in `internal/chat`, single source of truth:

```go
type RetrievalMode string

const (
    RetrievalAutoGroundedDefault     RetrievalMode = "auto_grounded_default"
    RetrievalAgenticOnly             RetrievalMode = "agentic_only"
    RetrievalTextbookOnly            RetrievalMode = "textbook_only"
    RetrievalNoRetrieval             RetrievalMode = "no_retrieval"
    RetrievalExternalAuthorityAllowed RetrievalMode = "external_authority_allowed"
)
```

Stored per-conversation as a new `conversations.retrieval_mode TEXT NOT NULL
DEFAULT 'auto_grounded_default'` column. Snapshotted onto `runs.retrieval_mode`
at run creation so historical runs remain interpretable if the conversation
default changes.

Mode → pre-turn behavior table:

| Mode | Pre-turn RAG runs when textbooks attached |
| --- | --- |
| `auto_grounded_default` | Yes |
| `textbook_only` | Yes; tool catalog excludes tools other than `search_textbook` |
| `agentic_only` | No |
| `no_retrieval` | No |
| `external_authority_allowed` | Yes (Phase 1 behavior identical to `auto_grounded_default`; the mode is reserved for Phase 2+ external sources) |

Dev override: setting env var `STARSHP_SKIP_AUTO_GROUNDING=1` forces
`no_retrieval` regardless of the conversation setting. No UI toggle in
Phase 1.

### Provider abstraction

`internal/provider/provider.go`:

```go
type Event struct {
    Kind       string          // user_message | assistant_text | assistant_tool_call | tool_result
    Text       string          // user_message, assistant_text, tool_result.output
    ToolCallID string          // assistant_tool_call, tool_result
    ToolName   string          // assistant_tool_call, tool_result
    ToolInput  json.RawMessage // assistant_tool_call
    IsError    bool            // tool_result
}

type ToolDef struct {
    Name        string
    Description string
    InputSchema json.RawMessage // JSON Schema
}

type ChatRequest struct {
    Model     string
    System    string    // bare system prompt (cacheable)
    Grounding string    // pre-turn RAG block with metadata header (cacheable)
    Tools     []ToolDef // tool catalog (cacheable when stable)
    Events    []Event   // canonical turn timeline for replay
}

type ToolCall struct {
    ID    string          // provider-assigned id
    Name  string
    Input json.RawMessage // fully buffered raw provider input;
                          // schema validation happens in registry.Execute
}

type Delta struct {
    Text       string
    ToolCall   *ToolCall // populated once a tool call's input JSON is fully assembled
    StopReason string    // populated on Done: end_turn | tool_use | max_tokens | error
    Done       bool
    Err        error
    Usage      *Usage    // unchanged from context-tracking phase
}

type ChatProvider interface {
    Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error)
}
```

`ChatRequest.Events` is the canonical timeline; each adapter maps it onto
its own wire format. `Delta.ToolCall` is emitted only after the provider's
streaming input JSON is fully assembled — partial-JSON streaming to the UI
is out of scope.

The existing `Message` shape disappears once the migration completes; the
adapter assembles role-based or block-based messages from `Events`.

#### OpenAI adapter

- `assistant_text` events → `{role: "assistant", content: text}`.
- A consecutive sequence of `assistant_text` + `assistant_tool_call` events
  in the same run collapses into one `{role: "assistant", content,
  tool_calls: [...]}` message — OpenAI requires tool_calls live on the same
  assistant message as the text that precedes them.
- `tool_result` events → `{role: "tool", tool_call_id, content}`. Errors
  carry the error string in `content`.
- `Tools` becomes `tools: [{type: "function", function: {name, description,
  parameters: input_schema}}]`.
- Pass `StreamOptions.IncludeUsage = true` (already done for usage).
- Streaming tool calls arrive as `delta.tool_calls[index]` accumulators by
  index; assemble per-index until `finish_reason="tool_calls"`, then emit
  each as a complete `Delta.ToolCall`.

#### Anthropic adapter

- Assemble content-block messages from consecutive same-turn events.
  Assistant messages have `content: [{type: "text", text}, {type:
  "tool_use", id, name, input}, ...]`. Tool results come back on a user
  message: `content: [{type: "tool_result", tool_use_id, content,
  is_error}, ...]`.
- `Tools` becomes `tools: [{name, description, input_schema}]`.
- `cache_control: {type: "ephemeral"}` markers on the System block, the
  Grounding block, and the last tool definition — these are the stable
  prefix.
- Streaming tool use arrives as `content_block_start` (type=tool_use) →
  N × `content_block_delta` (type=input_json_delta) → `content_block_stop`.
  Buffer the JSON across deltas, parse on stop, emit `Delta.ToolCall`.
- `stop_reason="tool_use"` → `Delta.StopReason="tool_use"`;
  `stop_reason="end_turn"` → `"end_turn"`; etc.

### The agentic loop — `chat.Service`

```go
type SendParams struct {
    ConversationID string
    UserText       string
    SystemPrompt   string
    Model          string
    Provider       provider.ChatProvider
    Retriever      Retriever                // may be nil
    RetrievalMode  RetrievalMode            // resolved per conversation
    Registry       *tools.Registry
    EventSink      EventSink                // emits chat:* runtime events
}

type RunResult struct {
    RunID           string
    TerminalReason  string
    TotalUsage      provider.Usage
    TotalToolCalls  int
    TotalIterations int
}

func (s *Service) Send(ctx context.Context, p SendParams, onToken func(string)) (RunResult, error)
```

Loop body, in order:

1. Begin a write transaction. Insert one `user_message` event; the conversation's
   next `sequence_index` becomes the `turn_id` of this turn. (Use the
   event's `id` as `turn_id` for simplicity.) Commit.
2. Apply `STARSHP_SKIP_AUTO_GROUNDING` if set; otherwise use the provided
   `RetrievalMode`.
3. Insert a `runs` row: `status='in_progress'`, `active_for_replay=0`,
   `provider`, `model`, snapshotted `retrieval_mode`.
4. Emit `chat:run_started` with `grounding.status="pending"` (or
   `"not_required"` if mode skips RAG).
5. If mode requires pre-turn RAG and the conversation has textbooks
   attached:
   - Call `Retriever.Retrieve(ctx, userText)` → context block + sources.
   - On success: update `runs.grounding_meta`; emit `chat:grounding_ready`.
   - On failure: mark run `errored`, terminal_reason `grounding_error`,
     emit `chat:run_errored`, return.
6. Build the tool catalog from `Registry.Catalog()` (or a mode-restricted
   subset for `textbook_only`).
7. Loop `iteration := 1; iteration <= MAX_ITERATIONS; iteration++`:
   a. `events := store.GetProviderReplayEvents(convID, runID)`.
   b. Build `ChatRequest{System, Grounding: groundingBlock(meta), Tools,
      Events}`.
   c. `ch, err := provider.Stream(ctx, req)`. On error: mark run errored,
      emit `chat:run_errored`, return.
   d. Accumulate text and tool calls per delta. Emit `chat:token` per text
      delta. Accumulate the terminal `Usage` and `StopReason`.
   e. On `Done`:
      - If accumulated text is non-empty, insert one `assistant_text` event
        (regardless of `StopReason` — text emitted before a tool use must
        be persisted before the tool-call events).
      - If `StopReason == "tool_use"`:
        For each `ToolCall` in emitted order:
        - Insert `assistant_tool_call` event with `tool_call_id`,
          `tool_name`, `tool_input`. Commit. (Write-before-dispatch.)
        - Emit `chat:tool_call`.
        - `ctx_tool := context.WithTimeout(ctx, tool.Timeout())`. If the
          tool has no override, use the registry default (30s).
        - `result, isErr, latency := Registry.Execute(ctx_tool, name, input)`.
        - Insert `tool_result` event with `text=result.Output`,
          `tool_metadata=result.Metadata`, `tool_result_hash`,
          `tool_latency_ms`, `is_error=isErr`.
        - Emit `chat:tool_result`.
        Continue loop (next iteration).
      - Else (`end_turn`, `max_tokens`, `error`, …): break loop, record
        terminal_reason.
8. If the loop exited because `iteration > MAX_ITERATIONS`: mark run
   errored, `terminal_reason='max_iterations'`, emit `chat:run_errored`,
   return.
9. Otherwise: open a transactional completion (see "Activation" above).
   Demote prior active run for this turn, mark this run completed and
   active. Aggregate `total_*` fields from accumulated usage/tool-call
   counts.
10. Emit `chat:run_completed` and `chat:usage`. Return `RunResult`.

`MAX_ITERATIONS` default is 8, override via env `STARSHP_MAX_TOOL_ITERATIONS`.
Per-tool timeout default is 30s, override via the `Tool.Timeout()` method.

Cancellation: when `ctx` is cancelled mid-loop, the in-flight provider
stream and any in-flight `Tool.Execute` receive the cancellation through
their own `ctx`. The loop catches `ctx.Err()`, persists any partial
assistant text already accumulated, marks the run `cancelled` with
`terminal_reason='user_cancelled'`, emits `chat:run_cancelled`. The Stop
button in the UI calls the existing Wails cancel hook to signal this.

### Tool registry

`internal/tools/registry.go`:

```go
type ExecResult struct {
    Output   string          // exact content shown to the model
    Metadata json.RawMessage // structured provenance/diagnostics persisted on the tool_result event
}

type Tool interface {
    Name() string
    Description() string
    InputSchema() json.RawMessage // JSON Schema
    Execute(ctx context.Context, input json.RawMessage) (ExecResult, error)
    Timeout() time.Duration       // 0 → use registry default
}

type Registry struct {
    defaultTimeout time.Duration
    tools          map[string]Tool
    schemas        map[string]*gojsonschema.Schema
}

func (r *Registry) Register(t Tool) error
func (r *Registry) Catalog() []provider.ToolDef
func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage)
    (result ExecResult, isError bool, latency time.Duration, err error)
```

`Execute` semantics:

- Unknown name → returns `is_error=true` with normalized error
  `unknown_tool`. The loop persists this as a tool_result so the model can
  self-correct.
- Schema validation failure → returns `is_error=true` with normalized error
  `schema_validation_error` and the validator's message in `Output`.
- `ctx.Done()` → returns `is_error=true` with normalized error `timeout`
  (when triggered by the loop's `WithTimeout`) or surfaces cancellation up
  the call chain (when triggered by the outer user cancel).
- Tool-raised error → returns `is_error=true` with normalized error
  `execution_error`.

Normalized tool-error categories (Phase 1):

- `unknown_tool`
- `schema_validation_error`
- `timeout`
- `execution_error`

These are tool-result-level errors, distinct from the existing provider
`AppError` taxonomy. Provider errors (auth, rate limit, context length,
network, rag_unavailable, unknown) still flow through `NormalizeError` and
surface as `chat:run_errored`.

Tools are registered at app startup by `appapi`, after `rag.Adapter` is
constructed and config is loaded:

```go
registry := tools.NewRegistry(30 * time.Second)
_ = registry.Register(tools.NewSearchTextbook(ragAdapter))
_ = registry.Register(tools.NewSafeMath())
```

### Anchor tools

#### `search_textbook` — `internal/tools/searchtextbook/`

Argument schema:

```json
{
  "type": "object",
  "properties": {
    "query":   {"type": "string", "minLength": 1},
    "book":    {"type": "string"},
    "chapter": {"type": ["string", "integer"]},
    "top_k":   {"type": "integer", "minimum": 1, "maximum": 10, "default": 5}
  },
  "required": ["query"],
  "additionalProperties": false
}
```

Tool description (model-visible) emphasizes when to call vs. when the
pre-turn grounding already covers the question:

> Search the user's attached accounting textbooks for relevant passages.
> Call this when the pre-turn grounding context (already in your prompt) is
> insufficient — when you need a different chapter, a specific rule the
> grounding did not cover, a follow-up lookup for a multi-step problem, or
> a check to verify a claim before answering. Each result has a stable
> `source_id` you can cite back to the user.

Behavior:

1. Honors the conversation's attached textbook scope. If `book` is
   supplied, narrows further. If `book` is not in the attached scope,
   returns `is_error=true` with `invalid_book`.
2. Calls the existing `rag.Adapter` with the supplied query, scope, and
   `top_k` (default 5, max 10). If `chapter` is supplied, filters retrieved
   chunks to that chapter post-retrieval (the existing adapter's scope
   filter is coarser than chapter; chapter filtering is a thin layer).
3. Formats results with stable, model-visible source IDs:

   ```
   ## Source 1 [source_id: chunk_<hash16>] — <book> · Chapter <n>
   <chunk text>

   ## Source 2 [source_id: chunk_<hash16>] — <book> · Chapter <n>
   <chunk text>
   ```

4. Caps total output at 4,000 characters. Truncation marker
   `\n\n…(truncated; call again with a narrower query for more)\n` is
   appended if the cap is hit.

`ExecResult.Output` = the formatted string above. `ExecResult.Metadata`:

```json
{
  "sources": [
    {"id": "chunk_<hash16>", "book": "...", "chapter": "...", "score": 0.82, "chunk_hash": "..."}
  ],
  "result_hash": "<sha256-of-output>",
  "query_normalized": "...",
  "top_k_requested": 5,
  "top_k_returned": 4,
  "truncated": false
}
```

Stable `source_id` is `chunk_` + first 16 hex chars of the chunk's content
hash, sourced from the existing `rag.Adapter` chunk metadata. This is the
identifier Phase 2's verifier/citation tools will resolve.

Errors (all `is_error=true`):

- `rag_unavailable` — embedding/index lookup failure.
- `textbook_unavailable` — the requested book exists in `textbooks.yaml`
  but its directory is not readable.
- `no_textbooks_attached` — no scope set on the conversation.
- `invalid_book` — `book` argument not in the conversation's attached
  scope.

#### `safe_math` — `internal/tools/safemath/`

Argument schema:

```json
{
  "type": "object",
  "properties": {
    "expression": {"type": "string", "minLength": 1, "maxLength": 1000}
  },
  "required": ["expression"],
  "additionalProperties": false
}
```

Tool description:

> Deterministic decimal arithmetic. Use for any non-trivial calculation —
> tax computations, present value, percentages, subtotals — to verify your
> work. Supports `+ - * / ^`, parentheses, unary minus, percent suffix
> (`22%` = 0.22), and functions `min, max, abs, round, sqrt, floor, ceil`.
> Decimal-precise. Not for symbolic algebra, variables, or units.

Grammar (recursive-descent, hand-written):

```
expr      := term  (('+' | '-') term)*
term      := factor (('*' | '/') factor)*
factor    := power ('^' power)*
power     := unary
unary     := ('-' | '+') unary | postfix
postfix   := primary ('%')?
primary   := number | '(' expr ')' | funcCall
funcCall  := IDENT '(' (expr (',' expr)*)? ')'
number    := /[0-9]+(\.[0-9]+)?/
IDENT     := /[a-zA-Z_][a-zA-Z_0-9]*/
```

Implementation uses `github.com/shopspring/decimal` for all arithmetic. No
`math/big.Float`, no `float64` intermediate values.

Functions: `min(a, b, ...)`, `max(a, b, ...)`, `abs(x)`, `round(x)`,
`sqrt(x)`, `floor(x)`, `ceil(x)`. `round` uses banker's rounding to nearest
integer (matches `decimal.RoundBank`). `sqrt` uses
`decimal.Decimal.Sqrt(precision=16)`.

Percent suffix: `X%` = `X * 0.01`, applied after the postfix expression
evaluates. So `(5 + 5)%` = `0.10`, `22%` = `0.22`.

Limits:

- Max expression length: 1000 chars (enforced at schema level too).
- Max parse depth: 50.
- Hard execution timeout: 5s (overrides registry default via
  `Tool.Timeout()`).
- No I/O. No filesystem. No network. No reflection. No `eval`.
- No symbol table beyond function names. No variables. No constants
  (no `pi`, no `e`).

`ExecResult.Output`: `decimal.String()` of the result (avoids scientific
notation; preserves precision exactly).

`ExecResult.Metadata`:

```json
{
  "normalized_expression": "50000 * 0.22 + 1000",
  "result_decimal_string": "12000",
  "result_hash": "<sha256>"
}
```

Errors (all `is_error=true`):

- `parse_error` — invalid syntax. Output includes the location of the error.
- `divide_by_zero` — division by zero.
- `domain_error` — `sqrt` of negative, etc.
- `expression_too_long` — length cap exceeded (defense in depth; schema
  catches it first).
- `depth_exceeded` — parse depth cap exceeded.

### UI surfacing — Wails runtime events

Events emitted by `appapi.SendMessage`, all keyed on `convID`:

| Event | When | Payload |
| --- | --- | --- |
| `chat:run_started` | Run row inserted | `{convID, runID, turnID, retrievalMode, grounding: {status: "pending"|"not_required"}}` |
| `chat:grounding_ready` | Pre-turn RAG completes (only when status was pending) | `{convID, runID, grounding: {status, sourceCount, totalCharsInjected, contextHash}}` |
| `chat:token` | Per streamed text token (unchanged shape) | `{convID, runID, text}` |
| `chat:tool_call` | `assistant_tool_call` persisted, before dispatch | `{convID, runID, toolCallID, name, input}` |
| `chat:tool_result` | `tool_result` persisted | `{convID, runID, toolCallID, name, isError, latencyMs, summary}` (summary = first 200 chars of output) |
| `chat:run_completed` | Successful completion transaction committed | `{convID, runID, terminalReason, totalToolCalls, totalIterations}` |
| `chat:run_errored` | Any error path | `{convID, runID, errorCode, errorMessage, terminalReason}` |
| `chat:run_cancelled` | User cancel propagated | `{convID, runID, terminalReason}` |
| `chat:usage` | After completion (existing) | `{convID, runID, modelID, input, output, cached}` |

The existing `chat:token` event keeps its current shape so the existing
frontend token-streaming path keeps working through the migration.

Frontend rendering in `frontend/src/main.ts`:

- The assistant bubble for a run is a sequence of inline blocks rendered in
  event order: text segments, tool-call blocks, tool-result blocks.
- Tool-call block (collapsed default):
  `🔍 search_textbook("realization principle") · 5 sources · 240 ms`
  Click expands to show full arguments + the `summary` from
  `chat:tool_result`.
- Errored tool-result block renders in red with the error code.
- The grounding header (when `chat:grounding_ready` fires with
  `status="ready"`) shows as a small dim line above the bubble:
  `↳ grounded · 5 sources from intermediate-accounting`.
- `chat:run_cancelled` flips the bubble to a "cancelled" visual state but
  keeps everything rendered so far.

### Observability and provenance

Built into the loop, not a separate concern:

- Structured logs (Go `log/slog`) at every loop boundary:
  `run_started`, `grounding_ready` (or `grounding_failed`),
  `iteration_started`, `tool_dispatched`, `tool_completed`,
  `iteration_completed`, `run_terminal`. All include `conv_id`, `run_id`,
  `turn_id`, and the iteration counter where applicable.
- Per-tool: arguments, latency, success/failure, error code, output length.
- `runs.total_tool_calls`, `total_iterations`, `total_input_tokens`,
  `total_output_tokens`, `total_cached_input_tokens` aggregated at
  completion.
- Tool results persist their full capped output. Rebuilding from RAG state
  is not replay; it is re-derivation, and it breaks silently when indexing
  or chunking changes. The hash is an integrity check, not a substitute.
- `search_textbook` source IDs and chunk hashes flow from the tool's
  `Metadata` into the `tool_result.tool_metadata` column, ready for Phase 2
  citation/verifier work.

### Lightweight eval harness

Lives in `internal/eval/`. No new external dependencies beyond what
exists.

Three layers, all standard Go tests:

1. **Loop-level** (`internal/eval/loop_test.go`):
   A fake `provider.ChatProvider` (in `internal/eval/fakeprovider`) emits
   scripted `Delta` sequences. Real `tools.Registry` (with mock tools
   where needed). Tests assert on:
   - Event log shape per turn (kinds, ordering, write-before-dispatch).
   - `runs` lifecycle transitions, including the transactional activation.
   - One-active-run-per-turn invariant under regenerate.
   - Cancellation: emitted partial text persisted, run marked
     `cancelled`, prior active run not demoted.
   - Max-iterations exit path.
   - Orphan recovery: simulate crash between `assistant_tool_call` and
     `tool_result`, reopen, verify replay excludes that run.

2. **Tool-level** (`internal/tools/searchtextbook/*_test.go`,
   `internal/tools/safemath/*_test.go`):
   - `search_textbook` against a mock `rag.Adapter` exercising: top_k
     defaulting, max enforcement, book/chapter filtering, invalid_book
     error, no_textbooks_attached error, output cap + truncation marker,
     stable source_id format, metadata shape.
   - `safe_math` grammar coverage: numbers, decimals, percent suffix at
     literal and parenthesized positions, all operators including `^`,
     unary minus stacking, every function with valid and invalid arity,
     parse errors with location, divide_by_zero, domain_error (sqrt of
     negative), depth_exceeded, expression_too_long.

3. **Quality-level** (`internal/eval/quality_test.go`):
   A `testdata/eval/fixtures/` directory of 5–10 coursework prompts. Each
   fixture is one YAML file:
   ```yaml
   name: percent-of-subtotal
   prompt: |
     A purchase has line totals of $1,250 and $3,475. Sales tax is 8.25%.
     Show the tax amount and the total. Use a calculation tool to verify.
   expected_substrings:
     - "389.81"   # 0.0825 * (1250 + 3475)
     - "5114.81"  # subtotal + tax
   expected_min_tool_calls: 1
   expected_tools_called_at_least_once:
     - safe_math
   max_iterations: 5
   ```
   Fixtures use deterministic, self-contained arithmetic so expected
   answers can be verified by hand; they intentionally avoid tax-year-
   specific data until the Phase 3 tax tools land.
   Runs against the real provider only when API keys are present (skipped
   via `t.Skip` otherwise, matching existing integration test patterns).
   Reports pass/fail per fixture; the test fails if any required fixture
   regresses. Fixtures are intentionally small and stable; the full eval
   platform (LLM-as-judge, scoring, dashboards) is a separate later
   project.

### Migration: `messages` → `conversation_events` + `runs`

Implemented in `internal/store/migrate.go` as a versioned migration step
guarded by the existing `userVersion` check.

Pre-migration: `messages(id, conversation_id, role, content, model, rag_context, rag_sources, input_tokens, output_tokens, cached_input_tokens, created_at)`.

Post-migration: `conversation_events` + `runs` exist; `messages` is dropped.

Steps, all inside one transaction:

1. Create `conversation_events`, `runs`, and the partial unique index on
   `runs(turn_id) WHERE active_for_replay = 1`.
2. Add `retrieval_mode TEXT NOT NULL DEFAULT 'auto_grounded_default'` to
   `conversations`.
3. For each conversation, in `messages.created_at` order:
   - Walk messages pairwise. Each `user` row starts a new turn.
   - User row → insert `user_message` event. `turn_id` = the event's `id`.
     Assign next `sequence_index` (per conversation, monotonic from 0).
   - Assistant row that follows → synthesize one `runs` row:
     `status='completed'`, `active_for_replay=1`, `provider` inferred from
     the message's `model` via `registry.Lookup` (fallback `"openai"`),
     `model` = the message's `model`, `retrieval_mode='auto_grounded_default'`,
     `started_at = created_at`, `ended_at = created_at`,
     `terminal_reason='end_turn'`, totals from the existing token columns
     (or 0 if NULL). Insert one `assistant_text` event linked to that run.
   - Fold any non-empty `rag_context`/`rag_sources` into the run's
     `grounding_meta`:
     ```json
     {"status": "ready", "query": null, "sources": <parsed rag_sources>,
      "injected_chars": len(rag_context), "context_hash": null}
     ```
     If both fields are empty/null, set `grounding_meta` to
     `{"status": "not_available"}`.
   - A trailing `user` row with no following assistant (mid-send crash from
     a prior session) inserts the `user_message` event but no run. Replay
     of that conversation will simply have an unanswered last user message,
     which is correct.
4. Verify counts: every original `messages` row maps to exactly one
   `conversation_events` row (or, for grounding-bearing assistant rows,
   exactly one event + run grounding metadata).
5. Drop `messages`.

Forward-only. Failure mid-migration rolls back the transaction and reverts
to the pre-migration schema; the user can re-launch and try again.

### `internal/appapi` changes

- `SendMessage` orchestrates: resolve provider, retriever, retrieval mode,
  registry, then call `chat.Service.Send` with the new `SendParams`.
- Emits all `chat:*` events listed above. The existing `chat:token` and
  `chat:usage` emissions move under this orchestration.
- New API methods needed by the frontend:
  - `GetConversationEvents(convID) []EventDTO` — returns the canonical
    replay timeline (active runs per turn + events ordered by
    `sequence_index`) for rendering history on conversation open.
  - `GetRetrievalMode(convID) string` and `SetRetrievalMode(convID, mode)`
    — present but not bound to a UI control in Phase 1, used by tests and
    a dev menu.
- Error normalization unchanged at the appapi boundary. Tool-result errors
  are not appapi-level errors; they ride inside the event stream.

### RAG boundary

Unchanged. Files under `internal/rag/{embedding,chunker,ragindex}/` are not
touched. The new `search_textbook` tool calls `rag.Adapter` exclusively;
any new query method needed lives in a new file inside `internal/rag/`,
never a modification of an upstream file. The architectural rule in
`README.md` holds.

### Error-normalization boundary

Unchanged for provider/SDK errors — they still flow through
`provider.NormalizeError` and become `AppError`. Tool-result errors are a
new, distinct category: they live on the event log, the model sees them,
and they do not surface as `AppError` to the UI.

## Testing

### Provider adapter tests

- OpenAI: fixture stream containing assistant text + `delta.tool_calls`
  accumulators + `finish_reason="tool_calls"`. Assert one or more
  `Delta.ToolCall` emitted with fully assembled inputs. Separate fixture
  for `finish_reason="stop"`.
- OpenAI: request body assembled from `Events` containing
  `assistant_tool_call` + `tool_result` collapses correctly into the
  assistant-with-tool_calls message + tool-role message shape.
- OpenAI: tool definitions assembled into `tools: [{type: "function",
  function: {...}}]` with the schema preserved.
- Anthropic: fixture stream containing `content_block_start` (tool_use) +
  N `input_json_delta` + `content_block_stop` + `message_delta` with
  `stop_reason="tool_use"`. Assert assembled `Delta.ToolCall` and
  `StopReason`.
- Anthropic: request body assembled from `Events` containing assistant
  tool_use + user tool_result lands as content blocks with correct
  `tool_use_id` linkage.
- Anthropic: `cache_control` markers present on System + Grounding +
  last tool definition.

### Store tests

- `conversation_events` round-trip: insert each kind, query back, verify
  ordering by `sequence_index` is stable under interleaved inserts across
  conversations.
- `runs` partial unique index rejects a second `active_for_replay=1` row
  for the same `turn_id`.
- Transactional completion: start two `in_progress` runs for the same
  `turn_id`, complete one; assert the other's `active_for_replay` is 0
  before and remains 0 (only this transaction touches activation).
  Complete the second; assert the first is now demoted and the second
  active.
- Errored run: `complete-as-errored` does not touch any other run's
  `active_for_replay` (verify via a prior active completed run staying
  active).
- `GetProviderReplayEvents`: across a conversation with three turns where
  turn 2 was regenerated twice, the timeline returned contains only the
  active run for each turn, events ordered by `sequence_index` within the
  run.
- Orphan handling: insert an `in_progress` run with an
  `assistant_tool_call` but no matching `tool_result`. Open via the
  read path; assert the orphan is excluded from replay even if the
  startup sweep has not yet run. Run the sweep; assert
  `runs.status='errored'`, `terminal_reason='orphaned'`.

### Migration test

- Open a DB at the pre-migration schema with: one conversation with three
  user/assistant pairs, one of which has non-empty `rag_context`/
  `rag_sources` and token columns; one conversation with only a trailing
  unanswered user message. Run migration.
- Assert `conversation_events` row counts and ordering.
- Assert `runs` rows synthesized correctly with active_for_replay,
  terminal_reason, totals.
- Assert grounding_meta `status="ready"` where RAG was present,
  `"not_available"` otherwise.
- Assert the trailing user message survives as a `user_message` event with
  no run.
- Assert `messages` table no longer exists.

### Tool registry tests

- `Register` rejects duplicate names.
- `Register` rejects an invalid `InputSchema`.
- `Catalog` returns one `ToolDef` per registered tool with name,
  description, and schema preserved.
- `Execute` unknown name → `unknown_tool`.
- `Execute` schema-invalid input → `schema_validation_error` with the
  validator message in `Output`.
- `Execute` ctx timeout → `timeout`.
- `Execute` tool-raised error → `execution_error`.
- `Execute` happy path → `is_error=false`, latency populated.

### `search_textbook` tests

- Default top_k = 5; max top_k = 10 (11 rejected by schema).
- `book` argument narrows scope; not-in-scope `book` → `invalid_book`.
- `chapter` argument filters returned chunks to that chapter.
- Empty scope → `no_textbooks_attached`.
- Output formatted with `## Source N [source_id: chunk_<hash16>]` header
  on each result.
- 4,000-char cap honored; truncation marker appended on overrun.
- Metadata shape: sources[], result_hash, query_normalized,
  top_k_requested, top_k_returned, truncated.
- `rag.Adapter` failure surfaces as `rag_unavailable`.

### `safe_math` tests

- Numbers (int, decimal); operators (+ - * / ^); parentheses; unary minus
  stacking (`---5 = -5`).
- Percent suffix at literal and parenthesized positions.
- Functions: arity-valid happy path for each (min/max variadic, abs/round/
  sqrt/floor/ceil unary).
- Function arity errors.
- `parse_error` with location info.
- `divide_by_zero`.
- `domain_error` (sqrt(-1)).
- `depth_exceeded` (deeply nested parens).
- `expression_too_long` (defense in depth; schema catches first).
- Decimal precision: `0.1 + 0.2 = 0.3` exactly (not 0.30000…04).
- No symbol table: `pi` → `parse_error: unknown function 'pi'` (or
  `unknown identifier` per parser policy).

### Loop tests (`internal/eval/loop_test.go`)

- Single iteration, no tool calls: text streamed, `assistant_text`
  persisted, run completed and active.
- Single tool call: write-before-dispatch verified by injecting a hook
  that asserts the `assistant_tool_call` row exists before
  `Tool.Execute` is invoked.
- Multiple tool calls in one provider response: all persisted in emitted
  order, executed sequentially, all `tool_result` rows persisted before
  the next iteration starts.
- Mixed text + tool use in one response: `assistant_text` persisted
  before any `assistant_tool_call`.
- Max iterations: scripted provider always emits `tool_use`. After
  `MAX_ITERATIONS`, run marked errored with
  `terminal_reason='max_iterations'`.
- Cancellation mid-iteration: cancel the ctx mid-stream. Assert partial
  text persisted, run marked `cancelled`, prior active run for this turn
  (if any from a regenerate) untouched.
- Regenerate: complete a run for turn T; start a second run for turn T;
  on its completion, assert the first is demoted and the second active.
- Failed regenerate: complete a run for turn T; start a second run for
  turn T; cancel it. Assert the first remains active.
- Orphan: kill the loop between `assistant_tool_call` persist and
  `tool_result` persist (use a hook). Reopen; assert the run is
  non-replayable via `GetProviderReplayEvents` even before the sweep
  runs.

### Quality fixtures (`internal/eval/quality_test.go`)

Run only when `OPENAI_API_KEY` and `ANTHROPIC_API_KEY` are set (skipped
otherwise). 5–10 coursework fixtures covering:

- A tax calculation that should trigger `safe_math`.
- A definitional question that should be answered from pre-turn grounding
  without any tool calls.
- A multi-hop question that should trigger `search_textbook` at least
  once.
- An obviously-wrong arithmetic claim the user makes; the model should
  call `safe_math` to verify and correct.
- A question with no attached textbooks; `search_textbook` should return
  `no_textbooks_attached` and the model should respond appropriately.

Each fixture passes when expected substrings appear in the final
assistant text and the expected tool-use pattern is observed.

### Frontend manual smoke

Add new steps to `docs/SMOKE.md`:

- Tool-call inline blocks render in event order on the assistant bubble.
- Tool-call block is collapsed by default; click expands to show input
  and the summary from `chat:tool_result`.
- Errored `tool_result` renders in red with the error code.
- Grounding header (`↳ grounded · N sources from <book>`) appears above
  the bubble when `chat:grounding_ready` fires.
- Stop button mid-tool-call: bubble shows partial text + any completed
  tool-call blocks + a "cancelled" indicator.
- Conversation switch mid-loop: events for the inactive conversation are
  not rendered (defensive `convID` check on the frontend).
- Reopening a conversation rebuilds the bubble from
  `GetConversationEvents`, with the same inline-block rendering.
- Regenerate (when implemented in UI; backend ready in Phase 1):
  re-rendering the bubble shows the new active run's events; the prior
  run is not visible.

### Drift detection

No changes to copied `internal/rag/{embedding,chunker,ragindex}/` packages.
Their existing tests continue to run unmodified.

## Decisions

- **Canonical event log, not messages.** `conversation_events` replaces
  `messages` because the new semantics — multiple assistant-and-tool events
  per turn, multiple runs per turn — break the "row equals chat bubble"
  assumption baked into the old name. Renaming forces every call site to
  re-examine its assumptions.
- **`kind` is canonical; `role` is derived in adapters.** Storing both
  invites drift; deriving role from kind in adapter code is one extra hop
  and keeps the schema honest.
- **Explicit `turn_id` and `run_id`.** Implicit grouping ("rows since the
  last user row") breaks under regenerate and cancellation. Explicit IDs
  cost nothing and make eval/audit queries trivial.
- **`runs` table separate from `conversation_events`.** Events are the
  timeline; runs are the lifecycle. Mixing them in one table requires
  per-row nullable lifecycle columns, makes the partial unique index on
  active runs awkward, and complicates the orphan-recovery sweep.
- **`active_for_replay` is the authoritative replay flag.** "Latest
  completed run" is the default activation behavior but not the rule; the
  product or user could later mark a different completed run as active
  (e.g., "use the earlier answer"), and the replay path must honor that.
- **Run activation only on successful completion, transactional.** A
  freshly-created run cannot be active because it might fail or cancel; an
  errored/cancelled run cannot demote a prior good answer. The completion
  transaction serializes per turn so two near-simultaneous completions
  can't both end up active.
- **Run created before pre-turn retrieval.** Otherwise a failed retrieval
  leaves a user event with no run record, breaking the invariant that
  every user message has at least one run (even if errored).
- **Pre-turn RAG is grounding metadata, not a tool event.** Faking
  pre-turn RAG as a `search_textbook` call would conflate two product
  layers (default vs escalation). The model needs to know both that it
  already has grounding and that it can ask for more.
- **`sequence_index`, not `created_at`, orders events.** Timestamps are
  not monotonic under high-rate inserts and offer no advantage over an
  integer counter.
- **Replay timeline is store-owned.** Every provider call needs the same
  active-run-per-turn selection; making each call site reinvent it
  guarantees subtle divergence.
- **`assistant_text` persisted before `assistant_tool_call` in the same
  iteration.** Both Anthropic and OpenAI may emit text before a tool call;
  persisting in emitted order preserves the conversation faithfully.
- **Write-before-dispatch on `assistant_tool_call`.** Crash recovery
  needs to detect orphans, and that's only possible if the call is
  durable before the side-effecting execute starts.
- **Full capped tool results persisted, not metadata-only.** Replay is
  the durable record of what the model saw. Re-deriving from RAG state
  silently breaks when the index, chunking, or content changes.
- **Provider adapters assemble tool calls; registry validates schemas.**
  Adapters know provider wire format; the registry knows the registered
  tool's schema. Splitting responsibilities along this seam keeps each
  side small and testable.
- **`ExecResult{Output, Metadata}` shape.** Threading structured
  provenance through a string return value forces every tool to invent
  its own format; a typed second field keeps `Output` clean for the model
  and `Metadata` rich for persistence.
- **Sequential tool execution in Phase 1.** Parallel dispatch is a
  Phase 2 optimization. The schema and event-log model already allow N
  tool calls per provider response; only the execution policy is
  sequential.
- **No loop-driven retries on tool failure.** The model decides whether
  to retry, by calling again. Auto-retry hides errors from the model and
  introduces nondeterminism.
- **Iteration cap default 8.** Empirically sufficient for the multi-hop
  tax problems we care about; configurable via env for power users.
- **Per-tool timeout default 30s; `safe_math` overrides to 5s.** Generous
  enough for `search_textbook` over real RAG; tight for pure arithmetic.
- **Stable model-visible source IDs in `search_textbook` output.** Phase
  2's citation/verifier tools need a referent; embedding the ID in the
  formatted output lets the model cite it back to the user verbatim.
- **`safe_math` uses `shopspring/decimal`.** Tax math requires decimal
  precision. `float64` rounding errors compound across cents; a custom
  parser over decimal arithmetic is 150 lines and removes a class of bug.
- **`safe_math` excludes `pi` and constants.** Out of scope for
  accounting/tax. Including constants is a magnet for scope creep
  (variables, units, symbolic algebra) that belongs to a future tool.
- **Internal retrieval-mode enum with dev flag; no UI toggle.** Users
  don't know which mode they need until they see the agentic mode behave
  differently. A dev override (`STARSHP_SKIP_AUTO_GROUNDING`) gives us a
  way to test pure agentic behavior without shipping UI surface.
- **`chat:grounding_ready` distinct from `chat:run_started`.** Lets the
  UI show a spinner during retrieval and a fixed header once grounding
  is in. Cheap to emit, expensive to retrofit later.
- **Forward-only migration.** Desktop app; downgrade is a reinstall. A
  reversible migration costs significant complexity for no real-world
  benefit.
- **Lightweight eval harness now, full platform later.** Phase 1 ships
  enough automated coverage to prevent regressions in the loop and the
  two tools. The full evaluator (LLM-as-judge, rubrics, dashboards) is a
  separate later project, after the tool stack has more shape to
  evaluate.
