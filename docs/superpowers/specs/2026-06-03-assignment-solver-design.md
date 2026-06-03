# Assignment Solver — Design

**Date:** 2026-06-03
**Status:** Approved (design)

## Problem

Starshp today is conversation-centric: a user creates a conversation, attaches
textbooks, types one question, and the agentic loop (`chat.Service.Send`)
produces one answer. Working through a whole assignment means hand-driving the
loop once per question.

The user has a directory of accounting coursework exported by the **Starshp
companion** — each homework/quiz question converted into a discrete,
machine-readable JSON record (a `manifest.json` plus per-question `NNN.json`
files). The goal is to point Starshp at such a directory and have it **solve
every question concurrently**, producing worked answers, structured
machine-readable answer files, and per-question confidence/flags — reviewable
inside the app afterward.

This is a batch, fan-out workload layered on top of the single-shot agentic
loop. The loop, tool registry, RAG retrieval, provider abstraction, and
event/run persistence built in the tool-calling milestone are the substrate.
What is new is: parsing the companion JSON into work items, rendering them
(especially complex worksheets) into solvable prompts, orchestrating many
concurrent solver runs, capturing a *structured* answer per question, and
surfacing the batch for review.

## Goals

- **Solve, don't grade.** Produce worked answers for each question in a
  companion-exported directory. The companion JSON carries no answer key
  (`correctIndex: null`); Starshp derives the answers.
- **Parallel fan-out.** An orchestrator reads the manifest and dispatches
  bounded-concurrent solver runs — one `chat.Service.Send` per question —
  collecting results. The run is **autonomous**: start it, it solves the whole
  folder, a Stop cancels the batch, and the user reviews afterward.
- **Both question types, worksheets best-effort.** Solve `multipleChoice`
  fully; solve `worksheet` questions as far as confidence allows, filling the
  cells it is sure of and flagging the rest.
- **Structured answers via a `submit_answer` tool.** Each solver must call a
  `submit_answer` tool whose schema-validated input *is* the answer (a choice
  index, or a cell-id→value map), plus `confidence` and `flags`. The answer is
  recovered from the persisted tool-call event — the event log is the single
  source of truth.
- **Three output forms.** (1) An in-app review surface reusing the run/event
  taxonomy; (2) structured answer JSON written to disk mirroring the companion
  convention; (3) per-question `confidence` and a first-class `flags` channel,
  explicitly including a "this question appears to be missing information" flag.
- **Grounded in textbooks.** v1 grounds the solver in the existing textbook RAG
  via a pluggable `GroundingSource` interface. Arithmetic must be verified with
  `safe_math`.
- **Reuse, don't fork.** The agentic loop, tool registry, RAG adapter, provider
  factory, and `conversation_events`/`runs` persistence are reused. New concerns
  live in a new `internal/assignment` package and two new store tables.

## Non-goals

- **Grading / verification against an answer key.** Checking student or
  proposed answers against correct ones is a separate future mode. (The
  companion's *review* page captures, which contain correct answers, are the
  natural input for that mode — see Future work.)
- **Independent verifier agent.** v1 quality bar is solver self-check +
  mandatory `safe_math` arithmetic verification. A second verifier agent per
  question (debits=credits, rule-citation checks) is deferred.
- **HTML / PDF / OCR ingestion.** Starshp consumes the companion's JSON only.
  Converting source HTML to JSON is the companion's job, not Starshp's.
- **Lesson-page grounding in v1.** The sampled lesson captures currently emit
  `type: "unknown"` with only `rawClasses` (no clean text) and contain answer
  keys (`-correct`/`studentreview` markers). Indexing them now would add noise
  and leak answers. The `GroundingSource` interface is built so clean,
  answer-free lesson/content grounding can be added later.
- **Worksheet formula evaluation / auto-fill of computed cells.** `formula`
  cells are rendered as context and excluded from the answerable set; the live
  assignment form computes them. v1 does not evaluate spreadsheet formulas.
- **Round-trip auto-fill into the live assignment form.** v1 writes
  round-trip-ready answer JSON keyed by stable cell IDs, but does not drive the
  source web form. Auto-fill is a future companion/automation concern.
- **Detailed UI visual design.** The spec fixes the Assignments surface and its
  data; pixel layout is a follow-on UI pass.

## Design

### Architecture & data flow

A new package `internal/assignment` holds all batch concerns. Existing
packages (`chat`, `tools`, `store`, `rag`, `provider`) are reused; changes to
them are additive (new store methods, a nullable column, a new tool).

Components:

- **Manifest loader** — reads `manifest.json` + each `NNN.json` into typed Go
  structs: `Question{Path, Type, Title, Body}`, where `Body` is a
  `MultipleChoiceBody{Stem, Choices[]}` or a
  `WorksheetBody{Scenario, Required[], Tabs[]→Tables→Rows→Cells}`.
- **Prompt builder** — renders one `Question` into a type-specific
  `(systemPrompt, userText)`. For worksheets it flattens tabs/tables/cells into
  a textual layout that preserves each cell's stable `id`.
- **`submit_answer` tool** — registered per-question with a schema tailored to
  that question; its input is the structured answer. (See "The `submit_answer`
  tool".)
- **Orchestrator** — reads the manifest, ensures grounding is indexed, creates
  the `assignment` + `assignment_item` rows, fans out bounded-concurrent
  `chat.Service.Send` calls, extracts each submitted answer from the run's
  events, persists it, writes the answer JSON, and emits batch progress.

```
User picks a directory (the companion _json folder)
 └─ Orchestrator.Run(ctx, dir, opts)
     ├─ load manifest + question JSONs
     ├─ ensure grounding indexed (textbook RAG; pluggable GroundingSource)
     ├─ create `assignment` row + N `assignment_item` rows (status=pending)
     ├─ emit assignment:started {total}
     ├─ worker pool (bounded concurrency, cancellable):
     │    per question →
     │      reg := registry with search_textbook + safe_math + submit_answer(question)
     │      build SendParams (type prompt + "verify arithmetic via safe_math",
     │                        UserText = rendered question, Registry = reg,
     │                        grounding scope = attached textbooks,
     │                        per-item conversation_id)
     │      RunResult := chat.Service.Send(...)        ← reuses the agentic loop
     │      answer := store.GetSubmittedAnswer(RunResult.RunID)  ← from events
     │      validate + persist assignment_item (answer, confidence, flags, runID)
     │      write <dir>/_answers/NNN.json
     │      emit assignment:item_done {seq, status, confidence, flagCount}
     └─ emit assignment:completed {counts}
```

How the orchestrator recovers the answer: the `submit_answer` call is persisted
(write-before-dispatch) as an `assistant_tool_call` event whose `tool_input`
*is* the answer JSON. After `Send` returns, the orchestrator reads the run's
events to extract the latest `submit_answer` input. No shared mutable state
between tool and orchestrator; the tool's own output to the model is a
confirmation that ends the turn.

### Input model — the companion JSON contract

Starshp consumes the companion's `schemaVersion: 1` output. A directory (the
`_json` folder) contains:

- `manifest.json`: `{schemaVersion, generatedFrom, count, questions[]}` where
  each entry is `{path, courseCode, module, type, title, hasCapturedChoices,
  warnings}`.
- Per-question `NNN.json`: `{schemaVersion, source{path,…}, type, title,
  capture{…}, warnings[], tags[], body}`.

Two body shapes are supported in v1:

- **`multipleChoice`**: `body{stem, choices[{index,text}], correctIndex}`.
  `correctIndex` is null (no key).
- **`worksheet`**: `body{scenario, required[], tabs[{label, tables[{headers,
  rows[{label, cells[{id, cellType, ariaLabel, formula, value, options}]}]}]}]}`.
  `cellType ∈ {input, dropdown, readonly, formula}`.

Unknown `type` values (e.g. `unknown`) are loaded as items but marked
`unsupported` and flagged, never silently dropped.

### The `submit_answer` tool

Registered fresh per question (each `Send` gets its own `Registry`), with a
schema tailored to that question — turning the registry's existing JSON-schema
validation into a strong guardrail.

Multiple-choice input schema (illustrative):

```json
{
  "type": "object",
  "properties": {
    "confidence": {"enum": ["high","medium","low"]},
    "answerIndex": {"type": "integer", "minimum": 0, "maximum": <len(choices)-1>},
    "answerText": {"type": "string"},
    "flags": {"type": "array", "items": {"$ref": "#/$defs/flag"}},
    "notes": {"type": "string"}
  },
  "required": ["confidence", "answerIndex"],
  "additionalProperties": false
}
```

Worksheet input schema (illustrative):

```json
{
  "type": "object",
  "properties": {
    "confidence": {"enum": ["high","medium","low"]},
    "cells": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": {"enum": [<answerable cell ids>]},
          "value": {"type": "string"}
        },
        "required": ["id","value"],
        "additionalProperties": false
      }
    },
    "flags": {"type": "array", "items": {"$ref": "#/$defs/flag"}},
    "notes": {"type": "string"}
  },
  "required": ["confidence", "cells"],
  "additionalProperties": false
}
```

Flag vocabulary (`flag.code`, closed set): `missing_information`,
`uncaptured_dropdown_options`, `ambiguous_requirement`, `out_of_scope`,
`low_confidence`. Each flag carries free-text `detail` and an optional `cellId`.

Worksheet best-effort is represented concretely: cells the model cannot answer
are omitted from `cells` and explained via a flag.

Loop interaction: no loop changes. The prompt instructs the model to call
`submit_answer` once and then stop; the orchestrator extracts the answer from
the event log regardless of trailing text. If `submit_answer` is never called,
the item is marked `no_answer` and flagged (the run remains fully reviewable).
An optional single re-prompt retry is deferred.

### Worksheet rendering & the answerable-cell rule

The renderer is a deterministic pure function with golden-file tests.

- **Answerable-cell rule:** a cell is answerable **iff `cellType ∈ {input,
  dropdown}` AND `value == null`.** Cells with a non-null `value` are *given
  context* (e.g. `"WASHINGTON CITY"`, `"1"`, `"12(a)"`) and rendered as-is.
  `readonly` cells render as fixed labels. `formula` cells render as
  `(auto-computed: =SUM(B8:B13))` and are **excluded** from the answerable set
  so a future round-trip never overwrites a computed cell.
- The per-question `submit_answer` schema enumerates exactly the answerable
  cell IDs.
- Rendering: header block (title, scenario, `required[]` as a numbered list);
  each tab → a labeled section; each table → a legible grid; answerable cells
  shown as `⟦0_table0_cell_c2_r5⟧` tagged with their row label / `ariaLabel`
  for context. Dropdowns render their `options` when captured; when not
  (`dropdown-options-not-captured` warning), the model supplies its intended
  value and raises an `uncaptured_dropdown_options` flag.
- The system prompt requires deriving numeric values with `safe_math` before
  calling `submit_answer`.

### Grounding — pluggable `GroundingSource`

```go
// GroundingSource yields indexable content for a scope. v1 wires up only the
// textbook source (backed by the existing rag.Adapter).
type GroundingSource interface {
    // Documents returns indexable units for the given scope key.
    Documents(ctx context.Context, scope string) ([]GroundingDoc, error)
}

type GroundingDoc struct {
    ID    string
    Title string
    Text  string
}
```

v1 grounds solver runs in the existing textbook RAG (`search_textbook` +
pre-turn grounding) via the textbook source; an assignment may carry an
attached textbook scope or none. A lesson/content source slots in later, once
the companion emits clean, answer-free text. The orchestrator indexes any
needed source once at assignment start (with progress), keyed by content hash
so re-runs skip re-indexing.

### Persistence model

New tables, additive alongside `conversation_events` / `runs`:

```sql
CREATE TABLE assignments (
    id                  TEXT PRIMARY KEY,
    source_dir          TEXT NOT NULL,
    title               TEXT NOT NULL,
    manifest_hash       TEXT NOT NULL,
    model               TEXT NOT NULL,
    grounding_scope     TEXT,                       -- JSON: attached books / sources
    status              TEXT NOT NULL CHECK (status IN (
                            'in_progress','completed','cancelled','errored')),
    total_items         INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);

CREATE TABLE assignment_items (
    id                  TEXT PRIMARY KEY,
    assignment_id       TEXT NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    seq                 INTEGER NOT NULL,           -- manifest order
    source_path         TEXT NOT NULL,              -- "004.html"
    type                TEXT NOT NULL,              -- multipleChoice | worksheet | unsupported
    title               TEXT,
    run_id              TEXT,                       -- the solver run (review link)
    conversation_id     TEXT,                       -- per-item conversation
    status              TEXT NOT NULL CHECK (status IN (
                            'pending','solving','answered','no_answer',
                            'errored','cancelled','unsupported')),
    confidence          TEXT,                       -- high | medium | low | NULL
    answer_json         TEXT,                       -- submit_answer input, verbatim
    flags_json          TEXT,                       -- extracted flags array
    answer_path         TEXT,                       -- written JSON file
    error               TEXT,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);

CREATE INDEX assignment_items_assignment ON assignment_items(assignment_id, seq);
CREATE INDEX assignment_items_run ON assignment_items(run_id);
```

Plus a nullable `conversations.assignment_id` (forward-only `ALTER TABLE`) so
item-conversations are tagged and **filtered out of the normal conversation
sidebar**, surfaced only under the assignment view. The item links to its
`run_id`/`conversation_id` so the review surface reuses
`GetConversationDisplayEvents`.

New store methods (additive): `CreateAssignment`, `CreateAssignmentItem`,
`UpdateAssignmentItem`, `GetAssignment`, `ListAssignments`,
`ListAssignmentItems`, and `GetSubmittedAnswer(runID)` (returns the latest
`submit_answer` `assistant_tool_call` input for a run).

### Disk output artifact

`<dir>/_answers/NNN.json`, mirroring the companion's `_json` convention:

```json
{
  "schemaVersion": 1,
  "source": "004.html",
  "type": "worksheet",
  "title": "Exercise 7-4 (Algo) …",
  "confidence": "medium",
  "answer": { /* submit_answer input verbatim */ },
  "flags": [ { "code": "uncaptured_dropdown_options", "detail": "…", "cellId": "…" } ],
  "runId": "…",
  "solvedAt": 0
}
```

The sibling `_answers/` folder is the round-trip-ready output a future auto-fill
step would consume.

### Orchestration

- **appapi entry points:** `SolveAssignment(dir) (assignmentID string, err)`
  starts the batch in a goroutine and returns immediately;
  `CancelAssignment(id)` stops it. The batch runs under its own cancellable
  context, separate from the interactive `SendMessage`/`CancelMessage` path.
- **Concurrency:** a bounded worker pool (semaphore), default 4, overridable via
  `STARSHP_ASSIGNMENT_CONCURRENCY`. Workers are provider-API-bound; the cap
  mainly guards provider rate limits. `chat.Service.Send` is stateless per call,
  and each item is its own conversation, so per-conversation `sequence_index`
  and per-turn run activation never collide across items.
- **SQLite under concurrency:** the store must run in **WAL mode with a
  `busy_timeout`** so concurrent event/run inserts serialize cleanly instead of
  returning `SQLITE_BUSY`. Verifying/adding these pragmas is an explicit
  implementation task.
- **Batch events** (distinct from the per-token `chat:token` stream, which is
  not emitted for batch items): `assignment:started{assignmentId,total,title}`,
  `assignment:item_started{seq,title,type}`,
  `assignment:item_done{seq,status,confidence,flagCount}`,
  `assignment:completed{counts}`, `assignment:errored{errorCode,message}`.
  Per-item runs still persist their full `conversation_events` for later review.
- **Error isolation:** a per-item failure (provider error, missing
  `submit_answer`, validation failure) marks that item `errored`/`no_answer`
  and the batch continues. Only batch-fatal problems (manifest unreadable,
  output dir not writable, required grounding index failure) error the whole
  assignment.
- **Re-runs / resume:** `manifest_hash` identifies a known assignment; a re-run
  skips items already `answered` and retries `pending`/`errored` ones.
  Cancellation leaves answered items persisted, so resume continues naturally
  from store state.

### appapi surface

Additive methods, Wails-bound: `SolveAssignment(dir)`,
`CancelAssignment(id)`, `ListAssignments()`, `GetAssignment(id)`,
`ListAssignmentItems(assignmentId)`. The orchestrator is constructed at startup
alongside the existing registry/RAG wiring, sharing the same `store.Store`,
`provider` factory, and `tools.Registry` building blocks. The `submit_answer`
tool is registered per-item by the orchestrator, not globally.

### UI surface

A new **Assignments** view (vanilla TS, reusing the run renderer from the
tool-calling milestone):

- A "Solve a folder…" action (directory picker → `SolveAssignment`).
- An assignment list with a live progress bar driven by `assignment:*` events.
- Drill into an assignment → an item list: `seq`, `title`, `type`, status,
  **confidence badge, flag indicators**. Flagged and low-confidence items are
  highlighted for triage.
- Clicking an item opens its run via `GetConversationDisplayEvents` — the worked
  reasoning, tool calls, grounding, and the `submit_answer` payload rendered
  legibly (MC choice; worksheet cell table). Item-conversations are excluded
  from the normal sidebar via `conversations.assignment_id`.

Detailed visual layout is a follow-on UI pass.

## Testing

- **Loaders:** parse real fixtures (copy `001.json` MC + `004.json` worksheet
  into `testdata/`) into typed structs; handle null fields, warnings, and
  unknown types.
- **Worksheet renderer:** golden-file test on `004.json`; assert the answerable
  cell set excludes `formula`, `readonly`, and prefilled (`value != null`)
  cells, and includes exactly the blank `input`/`dropdown` cells.
- **`submit_answer` tool:** per-type schema validation (MC `answerIndex`
  bounds; worksheet `id` enumeration), flag-vocabulary enforcement, happy path,
  malformed-input rejection.
- **Answer extraction:** `GetSubmittedAnswer(runID)` returns the correct
  `submit_answer` input from a run's events.
- **Orchestrator:** drive it with `internal/eval/fakeprovider` scripted to emit
  a `submit_answer` tool call → assert items persisted, answer JSON written,
  statuses correct, bounded concurrency honored, **cancellation** (answered
  persisted, pending cancelled, prior good runs untouched), and **per-item
  error isolation** (one item errors, batch completes).
- **Concurrency stress:** run several items concurrently against one store to
  shake out SQLite contention with WAL + `busy_timeout`.
- **Quality fixtures (API-gated, same pattern as the tool-calling eval):** solve
  a couple of real questions end-to-end when API keys are present; assert
  structured answers and flags are produced. Skipped without keys.

## Future work

- **Grade / verify mode** using the companion's answer-bearing review-page
  captures as a key, distinct from solving.
- **Independent verifier agent** per question (debits=credits, rule-citation,
  recomputation) as a quality tier above self-check.
- **Lesson/content `GroundingSource`** once the companion emits clean,
  answer-free study text.
- **Round-trip auto-fill** of the live assignment form from `_answers/NNN.json`.
- **Worksheet formula evaluation** for end-to-end statement totals.
