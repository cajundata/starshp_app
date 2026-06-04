# Rerun Item — Design

**Date:** 2026-06-04
**Status:** Approved (pre-implementation)

## Problem

After solving a folder, an item can land in a poor state — low confidence, a
flagged answer, an error, or no answer — and there is currently no way to
re-solve a single item. The only entry point is "Solve a folder"
(`App.SolveAssignment`), which re-runs the whole directory and, crucially,
**skips items already in `answered` status** (orchestrator.go:112). So a
low-confidence-but-`answered` item cannot be redone without re-pointing the
solver at a copy of the folder (forcing a brand-new assignment and re-solving
everything).

This feature adds a per-item **Rerun** action so a single item can be re-solved
in place.

Out of scope (tracked separately): attaching textbook scope to assignment runs
so `search_textbook` stops failing with `no_textbooks_attached`. That is its own
brainstorm → spec → plan cycle.

## Decisions

- **Previous run handling: overwrite.** The new run replaces the item's status,
  confidence, flags, `AnswerJSON`, and the `_answers/NNN.json` file, and points
  the item at a fresh conversation/run. The old conversation row remains in the
  DB but is unlinked from the item. No schema change. Matches the existing
  one-answer-per-item model.
- **Availability: idle-only.** Rerun is allowed only when nothing is actively
  running: the assignment is not `in_progress` and the item is not currently
  `solving`/`pending`. Hidden/blocked for `unsupported` items (cannot be solved).
- **Synchronous, decoupled from batch events.** The API call blocks until the
  single-item solve finishes, then the UI refreshes explicitly. The rerun does
  **not** emit the batch `assignment:item_started/done` events (which would
  pollute the progress bar via `updateProgress(1)` and carry no new
  `conversationId`). This matches existing UX: the detail pane always renders
  *persisted* events after the fact, never live-streams.
- **Cancel: not in v1.** The button is disabled while the rerun runs. A single
  item is short; explicit cancel can be added later.

## Architecture

A rerun reuses the existing per-item solver, `Orchestrator.solveItem`, against
the **existing item row**. `solveItem` already:

- reuses the item by ID (`UpdateAssignmentItem`),
- sets status `solving` → `answered` / `no_answer` / `errored`,
- overwrites `_answers/NNN.json` by path (`writeAnswerFile`),
- creates a fresh conversation/run and stores its events.

So the rerun is: load the assignment's `SourceDir`, load the question for the
item, ensure grounding, and call `solveItem` with the existing item ID — under
guards, with a no-op event emitter.

### Backend — `internal/assignment`

```
func (o *Orchestrator) RerunItem(ctx context.Context, asgID string, seq int) (store.AssignmentItem, error)
```

- Loads the assignment (for `SourceDir`); `Load(dir)` the folder.
- Finds the loaded `Question` whose `Path` matches the item's `SourcePath`.
- Ensures grounding (`o.opts.Grounding.Ensure`).
- Calls `solveItem(ctx, dir, asgID, item.ID, seq, q)`.
- Returns the re-fetched item.
- Rejects `Type == unsupported` before solving.

The orchestrator used for rerun is constructed with `Options.Emit` = no-op so
no batch item events fire.

### API — `internal/appapi`

```
func (a *API) RerunAssignmentItem(asgID string, seq int) (string, error)  // returns new ConversationID
```

- Builds `Options` like `SolveAssignment` (current default model, `search_textbook`
  tool when RAG is present, `safe_math`), but with a no-op `Emit`.
- Guards (idle-only), returning typed `provider.AppError`:
  - assignment `Status == "in_progress"` → `busy`.
  - item not found → error.
  - item `Status` ∈ {`solving`, `pending`} → `busy`.
  - item `Type == "unsupported"` → `unsupported`.
  - a per-API mutex/flag prevents two concurrent reruns.
- Runs `RerunItem` synchronously on an `a.ctx`-derived context; returns the
  updated item's `ConversationID`.

### Store — `internal/store`

```
func (s *Store) GetAssignmentItem(asgID string, seq int) (AssignmentItem, bool, error)
```

Single-item getter (mirrors existing `ListAssignmentItems`), used by the guard
and to return the post-rerun state.

### Frontend — `frontend/src/main.ts` + `style.css`

- A slim header inside `#asgDetail` containing a **"↻ Rerun"** button.
- Shown only when the selected item is terminal (`answered` / `no_answer` /
  `errored` / `cancelled`), `Type != unsupported`, and the assignment is not
  `in_progress`.
- Click handler:
  1. disable the button; optimistically set the item row's status pill to
     `solving`.
  2. `await App.RerunAssignmentItem(asgId, seq)`.
  3. on success: `selectAssignment(asgId)` (rebuilds rows from the store), then
     re-open `openItemDetail(newConversationId, seq)`.
  4. on error: surface `userMessage` inline and restore the prior pill.

## Data flow

```
[Rerun ↻ click]
  → App.RerunAssignmentItem(asgId, seq)
    → guards (assignment idle, item terminal, not unsupported, no concurrent rerun)
    → Orchestrator.RerunItem
      → Load(dir) → find Question by SourcePath
      → solveItem(existing itemID)  // overwrites item + _answers/NNN.json, new conversation
    → return new ConversationID
  → selectAssignment(asgId) + openItemDetail(newConversationId, seq)
```

## Error handling

| Condition | Error code | UI message |
|-----------|-----------|-----------|
| Batch solve in progress | `busy` | "A solve is already running — wait for it to finish." |
| Item currently solving/pending | `busy` | same |
| Unsupported item type | `unsupported` | "This item type can't be solved." |
| Load/solve failure | normalized | shown inline; prior item state preserved |

The prior item state is preserved if the rerun fails before `solveItem` mutates
the row.

## Testing (TDD — failing test first)

- **Orchestrator `RerunItem`** (scripted provider factory, as in
  `orchestrator_test.go`):
  - reuses the same item row (ID and seq stable across rerun).
  - overwrites status/confidence/`AnswerJSON` and rewrites `_answers/NNN.json`.
  - sets a new `RunID`/`ConversationID` distinct from the prior run.
  - rejects an `unsupported` item with the typed error.
- **Store `GetAssignmentItem`**: round-trip by `(asgID, seq)`, and `found=false`
  for a missing seq.
- Backend guards covered at the orchestrator/API seam where practical.

## Files touched

- `internal/store/assignments.go` — add `GetAssignmentItem`.
- `internal/assignment/orchestrator.go` — add `RerunItem`.
- `internal/appapi/api.go` — add `RerunAssignmentItem` + guards.
- `frontend/src/main.ts` — detail-pane Rerun button + handler.
- `frontend/src/style.css` — button/header styling.
- Tests alongside each.

## Follow-ups (not this spec)

- Attach textbook scope to assignment runs (fixes `no_textbooks_attached`).
- Optional cancel of an in-flight rerun (reuse the Stop button).
