# Assignment Picker Pre-fill — Design

**Date:** 2026-06-05
**Status:** Approved (pre-implementation)

## Problem

The "Solve a folder" flow opens the textbook and library pickers with empty
defaults: `pickTextbooks([], …)` then `pickLibraryItems([], …)`. The assignment
id is not known until *after* the orchestrator's `prepare` resolves it by
`source_dir` + manifest hash, so the frontend cannot pre-fill the pickers from
the existing assignment.

Consequence: **re-solving the same folder shows empty pickers**, and confirming
sends an empty (non-nil) selection, which `prepare`'s nil-guard treats as
authoritative and **clears the previously-set textbook scope / library items**.
This footgun affects both pickers (surfaced by the textbook and library
integration reviews).

## Fix

Before opening the pickers, look up the most recent assignment for the chosen
folder and pre-fill both pickers from its stored selection. Matching is by
**folder path only** (most recent `created_at`), so a folder whose questions
changed since last solve still pre-fills the user's textbook/prompt choices.

No new storage — reuses the existing `grounding_scope` and `library_items`
columns via the existing `Get/SetAssignmentScope` and `Get/SetAssignmentLibraryItems`.

## Architecture

```
[Solve a folder] → prompt dir
   → App.GetAssignmentScopeForDir(d)        → preScopes  (latest assignment's textbook scope, or [])
   → App.GetAssignmentLibraryItemsForDir(d) → preItems   (latest assignment's library items, or [])
   → pickTextbooks(preScopes, 'Next: Prompts →', → pickLibraryItems(preItems, 'Solve', → solve…))
```

### Store — `internal/store/assignments.go`
- `FindLatestAssignmentBySourceDir(dir string) (Assignment, bool, error)` —
  `SELECT <assignment columns> FROM assignments WHERE source_dir=? ORDER BY
  created_at DESC LIMIT 1`. Returns `ok=false` (no error) when none. Mirrors the
  existing `FindAssignmentByManifest` minus the hash predicate.

### appapi — `internal/appapi/api.go`
- A small unexported helper `latestAssignmentIDForDir(dir string) (string, bool)`
  wrapping `FindLatestAssignmentBySourceDir` (best-effort; swallows the lookup
  error into `found=false` so pre-fill never blocks solving).
- `GetAssignmentScopeForDir(dir string) ([]store.TextbookScope, error)` — if a
  latest assignment exists for `dir`, return `a.st.GetAssignmentScope(id)`; else
  return `nil, nil`.
- `GetAssignmentLibraryItemsForDir(dir string) ([]string, error)` — if a latest
  assignment exists, return `a.st.GetAssignmentLibraryItems(id)`; else `nil, nil`.
- Both return existing types, so no new Wails model type is needed.

### Bindings — `frontend/wailsjs/go/appapi/API.{js,d.ts}`
- Add `GetAssignmentScopeForDir(arg1:string):Promise<Array<store.TextbookScope>>`
  and `GetAssignmentLibraryItemsForDir(arg1:string):Promise<Array<string>>`.

### Frontend — `frontend/src/main.ts` (`solveFolder`)
Fetch the prior selection (best-effort) before opening the pickers and pass it as
the `current` argument:

```ts
const d = dir.trim()
let preScopes: any[] = []
let preItems: string[] = []
try {
  preScopes = (await App.GetAssignmentScopeForDir(d)) || []
  preItems  = (await App.GetAssignmentLibraryItemsForDir(d)) || []
} catch { /* default to empty pre-fill */ }
await pickTextbooks(preScopes, 'Next: Prompts →', async (scopes) => {
  await pickLibraryItems(preItems, 'Solve', async (items) => {
    // …unchanged solve body…
  })
})
```

The textbook picker already pre-checks `current` books and preserves
stored-but-unlisted books on confirm; the library picker pre-checks `current`
filenames. The editable 📚 / 📝 header buttons are unaffected (they already
pre-fill from `GetAssignmentScope` / `GetAssignmentLibraryItems`).

## Error handling

| Condition | Behavior |
|-----------|----------|
| No assignment exists for the dir | Methods return empty → pickers default empty (current behavior) |
| Lookup/DB error during pre-fill | Helper swallows to `found=false`; frontend `catch` keeps empty pre-fill; solving proceeds |

Pre-fill is strictly best-effort and never blocks solving.

## Testing (TDD)

- **Store:** `FindLatestAssignmentBySourceDir` returns the most-recent row for a
  `source_dir` (insert two with different `created_at`, assert the newer is
  returned); `ok=false` for an unknown dir.
- **appapi:** `GetAssignmentScopeForDir` / `GetAssignmentLibraryItemsForDir`
  return the latest assignment's stored selection for a dir; empty (nil) when no
  assignment exists for the dir.
- **Frontend:** manual smoke — solve a folder with selections, re-solve the same
  folder, confirm both pickers open with the prior selections checked.

## Out of scope

- Pre-filling the per-item rerun (rerun already reads the stored selection; no
  picker involved).
- Changing the dir+hash keying of the solve path itself (only the *pre-fill
  lookup* is dir-only; the actual assignment resolution is unchanged).

## Files touched

- `internal/store/assignments.go` (+ test)
- `internal/appapi/api.go` (+ test)
- `frontend/wailsjs/go/appapi/API.{js,d.ts}`
- `frontend/src/main.ts`
