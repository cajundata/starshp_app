# Business Pipeline Foundation — Design

**Date:** 2026-06-13
**Status:** Approved for planning
**Spec:** 1 of a multi-spec expansion (see Roadmap)

## Context

Starshp is pivoting from accounting-homework assistance to business planning.
The accounting features are frozen, not removed. The expansion's first
capability is a persistent **idea pipeline**: business ideas as tracked
entities that move through a status lifecycle, carry kill criteria with review
dates that resurface on schedule, and (in a later spec) run through a
conversational C-Suite review.

This spec covers the **foundation only**: the data model every later capability
consumes, the pipeline UI to create and move ideas, kill-criteria storage, the
on-launch Reviews Due sweep, and seeding the operator's three live portfolio
ideas. It deliberately stops short of the review driver and RAG grounding,
which are Spec 2.

The decisions below were settled during brainstorming and are not re-opened
here:

- **App-owned storage.** Pipeline state lives in Starshp's SQLite, not in the
  Obsidian vault. The vault is an import source, not a live-linked store.
- **Persistent entities** with status: raw, triaged, in_review, validating, go,
  parked, killed. Killed keeps a documented reason; parked is distinct from
  killed.
- **Conversational review** (Spec 2), not a batch orchestrator. The review
  tables are defined here as foundation but populated by Spec 2.
- **On-launch Reviews Due sweep** for kill-criteria resurfacing — no OS-level
  scheduling.

## Roadmap (context for this spec's boundary)

- **Spec 1 (this doc):** data model + pipeline UI + kill criteria + Reviews Due
  sweep + portfolio seeding.
- **Spec 2:** conversational review driver, Side Business role prompts,
  `submit_role_verdict` tool, BLUF synthesis, document renderer, RAG grounding
  over imported knowledge, confidentiality enforcement.
- **Spec 3:** Opportunity Scout + brainstorming from the imported prompt
  library; capture candidates as `raw` ideas.
- **Spec 4+:** remaining pathways, per-role model selection, optional vault
  export.

## Architecture

A new `internal/pipeline` package owns the idea-pipeline domain logic and a
`store`-backed repository for the new tables. The existing `internal/store`
package gains the schema and CRUD methods (consistent with how `assignments`
lives in `store`). The Wails `appapi.API` gains bound methods for the frontend.
The frontend gains a Pipeline view and a Reviews Due panel.

```
frontend (Pipeline view, Reviews Due panel)
        │  Wails-bound calls
        ▼
internal/appapi (API methods: ideas, kill criteria, reviews-due)
        │
        ▼
internal/pipeline (domain: status transitions, sweep logic)
        │
        ▼
internal/store (schema + CRUD for ideas, history, reviews, roles,
                kill_criteria, send_backs)
        ▼
   modernc.org/sqlite
```

The full schema (including the review tables Spec 2 populates) ships now so the
foundation is complete and later specs add no migrations to core tables.

## Data Model

Added to `internal/store/schema.go`, following the existing
`CREATE TABLE IF NOT EXISTS` idempotent-apply pattern. TEXT UUID primary keys
and `CHECK` constraints on status enums — matching the `assignments` /
`assignment_items` precedent. Timestamps are `time.Now().UnixMilli()`: the
store is not uniform (`conversations` uses seconds, but `assignments`, `runs`,
and `events` use milliseconds), and the pipeline tables follow the
`assignments` millisecond convention they most resemble. `review_date` is the
millisecond epoch of the review day, compared against `time.Now().UnixMilli()`
in the sweep.

```sql
CREATE TABLE IF NOT EXISTS ideas (
  id              TEXT PRIMARY KEY,
  title           TEXT NOT NULL,
  summary         TEXT NOT NULL DEFAULT '',
  pathway         TEXT,                       -- nullable until routed
  status          TEXT NOT NULL CHECK (status IN (
                      'raw','triaged','in_review','validating',
                      'go','parked','killed')),
  kill_reason     TEXT,                        -- required when killed/parked
  financial_flag  INTEGER NOT NULL DEFAULT 0,  -- enforced in Spec 2
  source          TEXT NOT NULL DEFAULT 'manual' CHECK (source IN (
                      'manual','scout','import')),
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS idea_status_history (
  id          TEXT PRIMARY KEY,
  idea_id     TEXT NOT NULL REFERENCES ideas(id) ON DELETE CASCADE,
  from_status TEXT,
  to_status   TEXT NOT NULL,
  reason      TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS idea_reviews (          -- populated in Spec 2
  id              TEXT PRIMARY KEY,
  idea_id         TEXT NOT NULL REFERENCES ideas(id) ON DELETE CASCADE,
  conversation_id TEXT,
  pathway         TEXT NOT NULL,
  model           TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL CHECK (status IN (
                      'in_progress','completed','parked','cancelled','errored')),
  bluf_verdict    TEXT,
  bluf_json       TEXT,
  document_md     TEXT,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS idea_review_roles (     -- populated in Spec 2
  id            TEXT PRIMARY KEY,
  review_id     TEXT NOT NULL REFERENCES idea_reviews(id) ON DELETE CASCADE,
  seq           INTEGER NOT NULL,
  role_key      TEXT NOT NULL,
  role_name     TEXT NOT NULL,
  status        TEXT NOT NULL CHECK (status IN (
                    'pending','running','done','errored','cancelled')),
  verdict       TEXT,                            -- pass|kill|send_back
  findings_json TEXT,
  run_id        TEXT,
  error         TEXT,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS kill_criteria (
  id          TEXT PRIMARY KEY,
  idea_id     TEXT NOT NULL REFERENCES ideas(id) ON DELETE CASCADE,
  review_id   TEXT REFERENCES idea_reviews(id) ON DELETE SET NULL,
  metric      TEXT NOT NULL,
  threshold   TEXT NOT NULL,
  review_date INTEGER NOT NULL,                  -- the sweep queries this
  on_miss     TEXT NOT NULL CHECK (on_miss IN ('kill','park','halt')),
  status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN (
                  'pending','met','missed','resolved')),
  notes       TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS send_backs (            -- populated in Spec 2
  id          TEXT PRIMARY KEY,
  review_id   TEXT NOT NULL REFERENCES idea_reviews(id) ON DELETE CASCADE,
  from_role   TEXT NOT NULL,
  question    TEXT NOT NULL,
  answer      TEXT,
  effect      TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','answered')),
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idea_status_history_idea
  ON idea_status_history(idea_id, created_at);
CREATE INDEX IF NOT EXISTS idea_reviews_idea ON idea_reviews(idea_id);
CREATE INDEX IF NOT EXISTS idea_review_roles_review
  ON idea_review_roles(review_id, seq);
CREATE INDEX IF NOT EXISTS kill_criteria_idea ON kill_criteria(idea_id);
CREATE INDEX IF NOT EXISTS kill_criteria_due
  ON kill_criteria(review_date) WHERE status = 'pending';
```

**Spec 1 reads/writes** `ideas`, `idea_status_history`, and `kill_criteria`.
The review tables ship empty and are exercised by Spec 2.

## Components

### `internal/store` — repository methods

CRUD mirroring the existing assignment methods' style and error handling:

- `CreateIdea(Idea) error`, `GetIdea(id) (Idea, error)`, `ListIdeas() ([]Idea, error)`,
  `UpdateIdea(Idea) error`, `DeleteIdea(id) error`.
- `SetIdeaStatus(id, toStatus, reason string) error` — writes the new status on
  the idea *and* appends an `idea_status_history` row in one transaction.
  Rejects unknown statuses and requires a non-empty reason for `killed`/`parked`.
- `ListStatusHistory(ideaID) ([]StatusChange, error)`.
- `AddKillCriterion(KillCriterion) error`, `UpdateKillCriterion(KillCriterion) error`,
  `DeleteKillCriterion(id) error`, `ListKillCriteria(ideaID) ([]KillCriterion, error)`.
- `ListDueKillCriteria(asOf int64) ([]DueReview, error)` — the sweep query:
  `kill_criteria` with `status='pending' AND review_date <= asOf`, joined to the
  parent idea's title and status, ordered by `review_date` ascending.

### `internal/pipeline` — domain logic

- **Status-transition validation.** A small allowed-transition table. Not every
  status reaches every other (e.g. `killed` is terminal except an explicit
  un-kill that the UI does not expose in Spec 1). Invalid transitions return a
  typed `provider.AppError` with `code="invalid_transition"`.
- **Sweep assembly.** `DueReviews(asOf)` calls `ListDueKillCriteria` and shapes
  the result for the panel (idea title, metric, threshold, on_miss, how overdue).

### `internal/appapi` — bound API

New methods returning the normalized `{code, userMessage, retryable}` error
envelope on failure, consistent with existing API methods:

`CreateIdea`, `UpdateIdea`, `ListIdeas`, `GetIdea`, `SetIdeaStatus`,
`DeleteIdea`, `AddKillCriterion`, `UpdateKillCriterion`, `DeleteKillCriterion`,
`ListKillCriteria`, `ListReviewsDue`. Regenerate Wails bindings
(`wailsjs/go/...`) after adding them.

### Frontend — Pipeline view + Reviews Due panel

- **Pipeline view:** ideas listed and groupable by status, showing title,
  pathway, status, a financial-data marker, and next review date. Create/edit
  form (title, summary, pathway, financial flag). A status control that prompts
  for a reason and calls `SetIdeaStatus`. A kill-criteria editor per idea
  (metric, threshold, review date, on_miss).
- **Reviews Due panel:** populated from `ListReviewsDue` on app launch; shows a
  badge with the due/overdue count and a list of each gate with its idea and
  on-miss action. Follows the existing `main.ts` / `style.css` patterns; no new
  framework.

### Portfolio seeding

The operator's three live ideas — HDPE cooler mounts (Small Project),
aging-in-place home automation (Side Business), Bayou Wildlife Systems (Full
Startup) — are entered through the new create paths during implementation,
sourced from their vault notes, with `source='import'`. The home-automation
idea also seeds the sample's kill criteria (paid installs, referral conversion,
install labor time, capital ceiling, support hours, and the day-120 retention
gate) so the Reviews Due sweep has real data to surface. This is guided manual
entry, not an auto-parser — three heterogeneous ideas do not justify one.

## Data Flow

**Create idea:** UI form → `appapi.CreateIdea` → `store.CreateIdea` (status
`raw`, timestamps set) → returns the idea → UI appends to the list.

**Move status:** UI status control (captures reason) → `appapi.SetIdeaStatus` →
`pipeline` validates the transition → `store.SetIdeaStatus` (transactional:
update idea + append history) → UI reflects new status.

**Add kill criterion:** UI editor → `appapi.AddKillCriterion` →
`store.AddKillCriterion` → UI lists it under the idea.

**Launch sweep:** app start → frontend calls `appapi.ListReviewsDue` (asOf =
now) → `pipeline.DueReviews` → `store.ListDueKillCriteria` → panel renders badge
+ list.

## Error Handling

- All API methods return the normalized error envelope already used across
  Starshp (`provider.AppError` → `{code, userMessage, retryable}`).
- Status transitions validate server-side; the UI never assumes a transition
  is legal.
- `killed`/`parked` without a reason is rejected with a clear `userMessage`.
- Missing/locked DB surfaces through the existing startup-validation path; the
  pipeline adds no new failure modes there.
- A malformed `review_date` (non-epoch) is rejected at the API boundary before
  reaching the store.

## Testing

- **Store:** table-driven Go tests in `internal/store` mirroring
  `assignments_test.go` — round-trip CRUD, cascade deletes, the transactional
  `SetIdeaStatus` (idea row and history row both written, or neither on error),
  and `ListDueKillCriteria` boundary cases (due today, overdue, future,
  non-pending excluded).
- **Pipeline:** unit tests for the transition table (every allowed and a
  representative set of disallowed transitions) and for `DueReviews` shaping.
- **API:** a thin test that the bound methods map store errors to the
  normalized envelope.
- **Manual UAT:** seed the three portfolio ideas, set statuses with reasons,
  add the home-automation kill criteria with a past review date, restart the
  app, and confirm the Reviews Due badge and panel surface the overdue gates.

## Out of Scope (deferred to later specs)

- The conversational review driver, role prompts, `submit_role_verdict`, BLUF
  synthesis, and the document renderer (Spec 2).
- RAG import/grounding over vault knowledge docs and confidentiality
  *enforcement* — the `financial_flag` field ships here; blocking non-local
  models and the visible indicator land in Spec 2 where inference occurs.
- Opportunity Scout and library-based brainstorming (Spec 3).
- Pathway and role-prompt import from the vault (Spec 2, which consumes them).
- Vault export of rendered reviews (Spec 4+).
```
