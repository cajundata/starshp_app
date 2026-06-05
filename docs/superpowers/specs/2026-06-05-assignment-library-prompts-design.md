# Assignment Library Prompts Implementation — Design

**Date:** 2026-06-05
**Status:** Approved (pre-implementation)

## Problem

The prompt/context library lets a user attach reusable prompt snippets to a chat
conversation; their bodies are assembled into the system prompt
(`appapi.assembleSystemPrompt`). Assignment runs have no equivalent — there's no
way to include library prompts/context when the solver works a folder. This
feature lets the user select library items for an assignment (at solve time and
editable later), applied to every item run and to reruns.

This mirrors the just-shipped **textbook attachment** feature (selection stored
on the assignment, applied at solve/rerun, picker + editable header button).

## How the library reaches the model today (the seam)

- Library items are markdown files in `cfg.LibraryDir`
  (`internal/library/library.go`: `Item{Filename, Name, Error}`; `List`, `Read`,
  `Create`, `Save`, `Delete`).
- Per-conversation selection lives in the `conversation_library_items` table
  (`store.GetActiveItems`/`SetActiveItems`).
- `appapi.assembleSystemPrompt(convID)` (`internal/appapi/library.go`) reads the
  active items, `StripH1`s each, sorts by display name, and joins the bodies with
  `\n\n` — the result is passed as `SendParams.SystemPrompt` to `chat.Send`.
- In chat the library bodies **are** the entire system prompt. For assignments
  there is already a base system prompt (`mcSystem`/`worksheetSystem` in
  `internal/assignment/render.go`), so the library text must be **combined** with
  it, not replace it.

Assembly needs `a.lib` and `a.st`, which live in `appapi`, not the orchestrator.
So `appapi` pre-assembles the **library preamble** string and injects it into the
orchestrator via `Options` (exactly as it injects the textbook `Resolver` and
`SearchTool`). The orchestrator never gains library access. Library items are
plain text — **no indexing step** (unlike textbooks).

## Decisions

- **Storage: new JSON column `library_items TEXT` on the `assignments` table**
  (JSON `[]string` of item filenames). One migration. Keeps assignment run-config
  on the assignment row alongside `grounding_scope`. (Alternative — an
  `assignment_library_items` table — rejected as heavier for no functional gain.)
- **Selection point: solve-time picker + editable header button**, a separate
  picker mirroring the chat library modal, shown after the textbook picker.
- **Prompt placement: prepend** the library preamble before the base system
  prompt (`system = preamble + "\n\n" + base`), so the question type's operative
  instructions ("call `submit_answer` exactly once … after calling, stop") remain
  **last** for recency. Library context is background/guidance.
- **Missing items are skipped** (not fatal), mirroring chat. No selection → empty
  preamble → base prompt unchanged.

## Architecture

```
[Solve a folder] → dir → 📚 Textbooks picker → 📝 Prompts picker
   → App.SolveAssignment(dir, scopes, libraryItems)
      → orc.Start(..., scopes, libraryItems, ...)
        → prepare: persist library_items (nil-guard, like textbook scope)
      → appapi assembles preamble from the items → Options.LibraryPreamble
      → solveItem: system, user := RenderPrompt(q)
                   if preamble != "" { system = preamble + "\n\n" + system }
                   chat.Send(SystemPrompt: system, ...)

[📝 Prompts button] → App.GetAssignmentLibraryItems(id) → picker
                    → App.SetAssignmentLibraryItems(id, items)

[Rerun] → appapi reads stored items → assembles preamble → Options.LibraryPreamble
        → RerunItem → solveItem applies it
```

### Store — `internal/store`
- Migration: add `library_items TEXT` to `assignments` (forward-only, in the
  existing migration path; `CREATE TABLE` for fresh DBs also includes it).
- `SetAssignmentLibraryItems(asgID string, items []string) error` — JSON-marshal
  (empty → NULL via `nullIfEmpty`), bump `updated_at`.
- `GetAssignmentLibraryItems(asgID string) ([]string, error)` — read
  `COALESCE(library_items,'')`; ""→nil; else JSON-unmarshal. (Same shape as
  `Get/SetAssignmentScope`.)

### appapi — `internal/appapi`
- Refactor `assembleSystemPrompt(convID)` to delegate to a names-based core:
  `assembleLibraryPreamble(names []string) (string, []string, error)` (read each
  item → `library.StripH1` → sort by `ExtractH1` → join non-empty bodies with
  `\n\n`; missing items returned in `skipped`). `assembleSystemPrompt` becomes
  `GetActiveItems` → `assembleLibraryPreamble`. Behavior for chat unchanged.
- `SetAssignmentLibraryItems(asgID, items)` / `GetAssignmentLibraryItems(asgID)`
  passthroughs to the store.
- `SolveAssignment` assembles the preamble from the **passed** `libraryItems`
  (no read-back) and sets `Options.LibraryPreamble`; it also forwards
  `libraryItems` to `Start` so `prepare` persists them. `RerunAssignmentItem`
  assembles from the **stored** items (`GetAssignmentLibraryItems`), since a rerun
  has no caller-supplied selection. Both set `Options.LibraryPreamble`. The
  frontend always passes the picker result (a non-nil array), so the real solve
  flow always assembles from a concrete selection.
- Missing-item `skipped` is logged/emitted as a soft notice (reuse the existing
  `library:notice` event pattern where practical).
- `SolveAssignment` signature gains `libraryItems []string`.

### Orchestrator — `internal/assignment`
- `Options` gains `LibraryPreamble string` (injected by appapi).
- `Start`/`Run`/`prepare` gain `libraryItems []string`; `prepare` persists them
  via `SetAssignmentLibraryItems` under the same **nil-guard** as the textbook
  scope (nil = no change; non-nil incl. empty = authoritative).
- `solveItem`: after `system, user := RenderPrompt(q)`, if
  `o.opts.LibraryPreamble != ""` then `system = o.opts.LibraryPreamble + "\n\n" + system`.
- `RerunItem` needs the preamble too; since assembly lives in appapi, the
  preamble arrives via `Options.LibraryPreamble` set by `RerunAssignmentItem`.

### Frontend — `frontend/src/main.ts` + `style.css`
- A reusable library picker (mirroring `openLibraryPanel`'s `ListLibraryItems`
  checkbox list) returning the selected filenames; invoked with a current
  selection + onConfirm callback (parallel to `pickTextbooks`).
- Solve flow: after the textbook picker confirms, open the library picker
  (default empty), then call `App.SolveAssignment(dir, scopes, items)`.
- A **📝 Prompts** button in the assignment header (next to 📚 Textbooks),
  pre-filled from `App.GetAssignmentLibraryItems(id)`, saving via
  `App.SetAssignmentLibraryItems(id, items)`.

## Error handling

| Condition | Behavior |
|-----------|----------|
| No items selected | Empty preamble → base system prompt unchanged |
| A selected item was deleted/unreadable | Skipped during assembly; soft notice; other items still applied |
| `GetAssignmentLibraryItems` on legacy assignment (NULL) | Returns nil → no preamble |

## Testing (TDD)

- **Store:** `Set/GetAssignmentLibraryItems` round-trip (nil default, set, clear).
- **appapi:** `assembleLibraryPreamble(names)` joins bodies, strips H1, skips
  missing names (returns them in `skipped`); `assembleSystemPrompt` still works
  via the refactored core.
- **Orchestrator:** with a non-empty `LibraryPreamble`, `solveItem`'s system
  prompt contains BOTH the preamble and the base instructions, preamble first;
  `prepare` nil-guard doesn't clobber a stored selection on re-solve.

## Out of scope / follow-ups

- Pre-filling the solve-time pickers from an existing assignment for the chosen
  directory (shared with the textbook follow-up — re-solve currently defaults
  pickers to empty).
- Per-item (vs per-assignment) library selection.

## Files touched

- `internal/store/schema.go`, `internal/store/migrate.go`,
  `internal/store/assignments.go` (+ test)
- `internal/appapi/library.go`, `internal/appapi/api.go`
- `internal/assignment/orchestrator.go` (+ test)
- `frontend/wailsjs/go/appapi/API.{js,d.ts}`
- `frontend/src/main.ts`, `frontend/src/style.css`
