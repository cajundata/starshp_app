# Assignment Textbook Attachment — Design

**Date:** 2026-06-04
**Status:** Approved (pre-implementation)

## Problem

The assignment solver registers the `search_textbook` tool whenever RAG is
available, but each item is solved in a fresh conversation that has **no
textbooks attached**. The tool hard-errors with `no_textbooks_attached`
(`internal/tools/searchtextbook/tool.go:70-72`) on every call. There is no UI to
attach textbooks to an assignment run, so:

- Questions that genuinely need a textbook lookup can't get one.
- The model wastes calls on a tool that always fails (visible failed tool calls).

This feature lets the user attach whole-book textbook scope to an assignment,
applied to every item (and every rerun), and suppresses the tool entirely when
no textbook is selected.

## How scope resolves today (the integration point)

`search_textbook` uses scope two ways:
1. **Gate + book validation:** `ec.TextbookScope` (`[]string` book names), set from
   the run's `Resolver` via `bookNamesFromResolver` in `chat.Service.runLoop`
   (`internal/chat/chat.go:272-278, 434-443`). Empty → `no_textbooks_attached`.
2. **Retrieval filters:** the tool's own injected resolver
   (`chatStoreResolver`, built in `NewAPI`) calls
   `store.GetConversationTextbooks(convID)` (`tool.go:82`).

Both read the **conversation's** attached textbooks. In chat, the Textbooks modal
persists them via `SetConversationScope` → `store.SetConversationTextbooks`. In
assignments, `solveItem` passes `Resolver: nil`
(`internal/assignment/orchestrator.go:~304`) and never attaches textbooks to the
item conversation — so both paths come up empty.

**Fix:** make the assignment the source of truth for scope, copy it onto each
item's conversation at solve time, and pass the same `chatStoreResolver` the chat
path uses. No new resolution mechanism.

## Decisions

- **Storage: reuse `assignments.grounding_scope`** (already exists, currently
  always NULL, read by all assignment queries). Store JSON-encoded
  `[]store.TextbookScope`. No new table, no migration.
- **Granularity: whole-book only** (mirrors the chat Textbooks modal;
  `TextbookScope.Chapters = nil`). Chapter scoping is a future option.
- **Selection point: solve-time picker + editable later.** Pick after choosing the
  folder; a 📚 Textbooks button in the assignment header edits it afterward, and
  reruns pick up the current selection.
- **Gate the tool on scope-presence:** register `search_textbook` for an item run
  **only when the assignment has a non-empty scope**. No scope → tool absent →
  zero failed calls.

## Architecture

```
[Solve a folder] → choose dir → textbook picker (ListBooks + checkboxes)
   → App.SolveAssignment(dir, scopes)
      → prepare: write/refresh assignments.grounding_scope (JSON []TextbookScope)
      → per item: solveItem(... scope ...)
          → CreateConversation
          → if scope: SetConversationTextbooks(conv.ID, scope)
          → buildRegistry: register search_textbook ONLY if scope non-empty
          → chat.Send(Resolver: o.opts.Resolver=chatStoreResolver{st})
               → ec.TextbookScope populated; tool filters resolve per-conversation

[📚 Textbooks button] → App.GetAssignmentScope → picker → App.SetAssignmentScope
   → updates assignments.grounding_scope; next Rerun reads it
```

### Store — `internal/store/assignments.go`
- `SetAssignmentScope(asgID string, scopes []TextbookScope) error` — JSON-marshal
  `scopes` (empty slice → store NULL via `nullIfEmpty`) into `grounding_scope`,
  bump `updated_at`.
- `GetAssignmentScope(asgID string) ([]TextbookScope, error)` — read
  `grounding_scope`, JSON-unmarshal (empty/NULL → `nil`).
- Reuses existing `TextbookScope{Name string, Chapters []int}` and
  `Set/GetConversationTextbooks`.

### Orchestrator — `internal/assignment/orchestrator.go`
- `Options` gains `Resolver chat.ScopeResolver` (injected by appapi).
- **Scope flow (explicit):**
  - `Start`/`Run` gain a `scopes []store.TextbookScope` parameter.
  - `prepare` persists it: on create, set `grounding_scope` in `CreateAssignment`;
    on resume (existing assignment found), call `SetAssignmentScope(asgID, scopes)`
    so re-picking takes effect.
  - `runItems` reads the persisted scope back once via `GetAssignmentScope(asgID)`
    and passes it to each `solveItem`. (Single source of truth = the DB column;
    avoids threading the slice through redundantly.)
- `solveItem` gains a `scope []store.TextbookScope` parameter. After
  `CreateConversation`: if `len(scope) > 0`, `SetConversationTextbooks(conv.ID, scope)`.
  Pass `Resolver: o.opts.Resolver` to `chat.Send`.
- `buildRegistry` gains the scope (or a `hasScope bool`) and registers
  `o.opts.SearchTool` **only when `len(scope) > 0`**. `safe_math` registration is
  unchanged.
- `RerunItem` already loads the assignment; it reads the scope via
  `GetAssignmentScope(asgID)` and passes it into `solveItem` — so reruns use the
  current selection automatically.

### API — `internal/appapi/api.go`
- `SolveAssignment(dir string, scopes []store.TextbookScope) (string, error)` —
  signature gains `scopes`; passes them to `orc.Start` (which persists via
  `prepare`) and injects `Resolver: chatStoreResolver{st: a.st}` into `Options`.
- `RerunAssignmentItem` — inject the same `Resolver` into its `Options` (scope is
  read from the stored assignment inside `RerunItem`).
- New: `SetAssignmentScope(asgID string, scopes []store.TextbookScope) error`,
  `GetAssignmentScope(asgID string) ([]store.TextbookScope, error)`.
- `ListBooks()` already exists for the picker.
- Wails bindings regenerated for the changed/added methods.

### Frontend — `frontend/src/main.ts` + `style.css`
- **Picker component:** factor the chat Textbooks-modal pattern (`ListBooks()` →
  checkbox per book, returning selected `{name, chapters:null}[]`) into a small
  reusable picker invoked with a current selection + an onSave callback.
- **Solve flow:** in `solveFolder`, after the directory prompt, open the picker
  (default empty), then call `App.SolveAssignment(dir, scopes)`.
- **Editable button:** a 📚 Textbooks button in the assignment header
  (`renderAssignmentHeader`) opens the picker pre-filled from
  `App.GetAssignmentScope(id)`, saving via `App.SetAssignmentScope(id, scopes)`.

## Error handling

| Condition | Behavior |
|-----------|----------|
| No textbooks selected | `search_textbook` not registered → model can't call it → zero failed calls |
| Textbooks selected | Tool registered; resolves per item conversation like chat |
| Model passes an unattached book name | Existing `invalid_book` tool error (unchanged) |
| `GetAssignmentScope` on legacy assignment (NULL scope) | Returns `nil` → behaves as "no scope" |

## Testing (TDD)

- **Store:** `SetAssignmentScope`/`GetAssignmentScope` round-trip through
  `grounding_scope`; empty slice stores NULL and reads back `nil`.
- **Orchestrator:**
  - With a scope: after a solve, the item's conversation has the textbooks
    attached (`GetConversationTextbooks`) and the registry built for that item
    includes `search_textbook`.
  - With no scope: the registry omits `search_textbook`.
  - `RerunItem` reads the stored assignment scope and attaches it on rerun.
- **API:** `SolveAssignment` persists the passed scope (and refreshes on resolve);
  `SetAssignmentScope` updates it; `GetAssignmentScope` returns it.

## Out of scope / follow-ups

- Chapter-level scoping (model already supports it; deferred).
- Pre-turn grounding for assignments (still `NoGrounding`; the tool is the only
  retrieval path, matching v1 chat).

## Files touched

- `internal/store/assignments.go` (+ test)
- `internal/assignment/orchestrator.go` (+ test)
- `internal/appapi/api.go`
- `frontend/wailsjs/go/appapi/API.{js,d.ts}`, `models.ts`
- `frontend/src/main.ts`, `frontend/src/style.css`
