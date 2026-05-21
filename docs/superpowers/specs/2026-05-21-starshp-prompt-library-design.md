# Prompt / Context Library — Design

**Date:** 2026-05-21
**Status:** Approved — ready for implementation plan
**Scope:** Replaces the SQLite presets feature. Backend (new
`internal/library/` package, store schema change, appapi surface) +
frontend (library panel + in-app editor).
**Prerequisite:** The app rename (`discussion_engine` → Starshp /
`starshp_app`) lands first on its own branch. This feature is built
afterward, against the renamed module path and the fresh
`%APPDATA%\starshp_app\` data directory.

## Goal

Replace the system-prompt presets with a markdown-backed prompt/context
library: a multi-select list of reusable prompt/context snippets the
user toggles per conversation. Active snippets feed the current
discussion's system prompt. Items are authored and edited in an in-app
raw-markdown editor.

## Data model & storage

- **Item content** — one `.md` file per item in
  `%APPDATA%\starshp_app\library\`. The folder is the single source of
  truth for item content.
- **Filename** — a no-space slug, generated from the H1 at creation,
  then frozen. It is the stable item ID. Editing the H1 never renames
  the file.
- **Display name** — the file's single H1. Exactly one H1 per file (the
  name); all in-file structure is H2 or deeper. Read fresh on panel
  load. No H1 → fall back to the filename stem.
- **Listing** — scan-on-demand, no persisted index. Reads each file's
  first line for its H1. An mtime-keyed in-memory cache is the only
  escape hatch, and only if a measurement ever demands it.
- **Activation** — `conversation_library_items(conversation_id,
  item_name)` table in `app.db`, with a cascade-delete FK to
  conversations. Sticky per-conversation: reopening a conversation
  restores its active set. Replaces the conversation `preset_id`.
- **Order** — alphabetical by display name, for both the list and the
  concatenation order.
- **On send** — each active item's leading H1 is stripped, then the
  bodies concatenate into one block that becomes the system-prompt slot
  of `CachedPrefix` (`chat.go:48`). Because activation is sticky, that
  prefix stays stable within a conversation, so prompt caching keeps
  working.

## Components

### Backend (Go)

- **New `internal/library/` package** — pure file I/O, no DB. `List()`,
  `Read(filename)`, `Create(content)` (slugify H1 → unique filename,
  numeric suffix on collision), `Save(filename, content)`,
  `Delete(filename)`. Helpers: extract-H1, strip-H1, slugify. The
  H1-required rule is enforced in `Create`/`Save`. Returns raw errors —
  normalized at the appapi boundary.
- **`internal/store/`** — delete `presets.go`. Add the
  `conversation_library_items` table plus `GetActiveItems(convID)` and
  `SetActiveItems(convID, names)` (replace-all, transactional). Drop the
  conversation `preset_id` column.
- **`internal/appapi/`** — remove `ListPresets` / `CreatePreset` /
  `UpdatePreset` / `DeletePreset`. Add `ListLibraryItems`,
  `ReadLibraryItem`, `CreateLibraryItem`, `SaveLibraryItem`,
  `DeleteLibraryItem`, `GetActiveItems`, `SetActiveItems`. `SendMessage`
  drops its `systemPrompt` parameter — the backend assembles the system
  prompt from the conversation's active items. `SetConversationMeta`
  drops `presetID`.
- **`internal/chat/`** — unchanged. `Send` still takes
  `SystemPrompt string`; the seam is clean.

### Frontend (TS — `main.ts` / `style.css`)

- **Library panel** — replaces the preset dropdown and preset modal.
  Toolbar-toggled, like the existing `#tbModal`. On open:
  `ListLibraryItems` + `GetActiveItems`, then render the list with a
  checkbox / highlight active state. A toggle calls `SetActiveItems`.
  Switching conversations re-renders from that conversation's active
  set.
- **In-app markdown editor** — a full-surface in-app view that reads as
  a separate window but stays inside the single Wails window (Wails v2
  multi-window was rejected as flaky). A raw-markdown textarea;
  create / edit / save / delete. Save calls
  `CreateLibraryItem` / `SaveLibraryItem`, then refreshes the list.
  Syntax highlighting is deferred (Someday backlog item).

## Data flow

1. **Authoring** — editor → `ReadLibraryItem` (for edits) → user edits
   raw markdown → `Create` / `Save` → backend validates the H1 and
   writes the file → list refreshes.
2. **Send** — `SendMessage(convID, text, model)` → `GetActiveItems` →
   `library.Read` ×N → strip H1 → concatenate alphabetically →
   `SystemPrompt` → `chat.Send` →
   `prefix = SystemPrompt + "\n\n" + ragCtx` → stream. Downstream
   unchanged.
3. **Sticky restore** — open a conversation → `GetActiveItems` → panel
   renders the checkmarks.

## Migration

Collapses to a schema change. The rename hands the app a fresh, empty
`%APPDATA%\starshp_app\` data directory, and pre-release data is
disposable — so no preset content is carried over. A fresh `app.db` is
created with `conversation_library_items` and simply has no `presets`
table or `preset_id`. For any dev DB created in the gap between the
rename and this feature, a trivial migration drops the old `presets`
table and `preset_id` column — nothing preserved, nothing needed. There
is no file-writing migration.

## Error handling

- Library folder missing → created by the `library` package (as
  `dataDir()` does today).
- A file unreadable during listing → flag that row, list the rest,
  never crash.
- An active item's file deleted on disk → on send, skip it and continue
  (a missing snippet is not fatal, unlike RAG); surface a soft notice.
  Panel load prunes orphaned `conversation_library_items` rows —
  self-healing.
- Save without an H1, or a disk / permission failure → rejected or
  reported as a normalized `AppError`.
- Slug collision (two items, same H1) → numeric suffix for filename
  uniqueness.
- All backend errors normalize at the `appapi` boundary (architectural
  rule 2). `ValidateStartup` gains a "library folder writable" check.

## Files touched

- **New:** `internal/library/library.go` (+ `library_test.go`).
- `internal/store/` — delete `presets.go` / `presets_test.go`; add the
  `conversation_library_items` table and methods; `schema.go` migration.
- `internal/appapi/api.go` — swap the preset methods for the library
  methods; `SendMessage` signature change; system-prompt assembly
  helper.
- `internal/chat/` — unchanged.
- `frontend/src/main.ts` — remove the preset dropdown / modal; add the
  library panel and editor view; update the `SendMessage` call.
- `frontend/src/style.css` — library panel and editor styling.
- `frontend/wailsjs/` — regenerated bindings.
- `docs/SMOKE.md` — new library smoke section.

## Out of scope (deferred)

- Syntax highlighting in the editor — Someday backlog item.
- Manual reordering of items — alphabetical only.
- Frontmatter / metadata on items — raw markdown, filename is the ID.
- Tags, folders, or categories — one flat list.
- Filesystem watching / live disk sync — the panel re-scans on open.
- A search / filter box — defer until libraries get large.
- A persisted table-of-contents index.
- A true multi-window OS editor.

## Verification

**Backend** — `go test ./...`:

- `internal/library/` — scan, H1 extract / strip, slugify + collision
  suffixing, CRUD, edge cases (no H1, empty folder, unicode names,
  non-`.md` files ignored).
- `internal/store/` — `conversation_library_items` get / set
  (replace-all semantics, cascade delete with the conversation), the
  schema migration.
- `internal/appapi/` — send-path assembly: active items → read → strip
  H1 → concatenate in order → correct `SystemPrompt`.

**Frontend** — manual smoke (frontend has no automated tests; see
`docs/SMOKE.md`):

1. Create an item in the editor; saving requires an H1; it appears in
   the list.
2. Edit an item; an H1 change updates the display name while the
   filename stays fixed.
3. Toggle items active; the highlight reflects state.
4. Send a message; confirm the active items' content reaches the model
   in the system prompt, with the H1 stripped.
5. Switch conversations and restart the app — activation is sticky and
   restored per conversation.
6. Delete an item's file on disk — it drops from the list and an active
   reference is pruned without crashing.

## Design decisions & rationale

Captured from the brainstorming session that produced this design, so
the reasoning survives independent of any single conversation. Each
entry is the fork, the choice made, and what was rejected.

1. **Unified hybrid library.** One library combining insertable
   snippets and markdown-based presets — not two separate features.
   - *Why:* the user wanted both a selectable list of insertable
     prompt/context snippets and markdown-based presets, in one place.
   - *Rejected:* a snippet shelf alongside untouched presets; a
     markdown "presets v2" only; a passive browse-and-copy reference
     panel with no prompt integration.

2. **Full replacement of the SQLite presets feature.** The library
   retires the `presets` table, its CRUD, and the preset modal
   entirely.
   - *Why:* two overlapping prompt systems in the UI is poor UX, and
     "markdown-based presets" taken literally means presets *become*
     the library. Pre-release status makes removing the working code
     acceptable.
   - *Rejected:* a new library alongside presets (lasting overlap);
     replacing in two steps (library now, migrate presets later).

3. **Full rich editor in v1.** The separate-window-style raw-markdown
   editor ships in the first version, not as a later add-on.
   - *Why:* full replacement retires the preset modal — the only
     in-app create/edit path — so authoring must be solved up front;
     the user chose to land it polished in one pass.
   - *Rejected:* a basic textarea modal now with the rich editor
     deferred; on-disk file editing only, with no in-app authoring (a
     regression from today).

4. **Sticky per-conversation activation.** An item's active state
   belongs to the conversation and is restored when it reopens.
   - *Why:* mirrors the existing sticky model/preset behaviour, and
     keeps the cached prompt prefix stable within a conversation so
     prompt caching keeps working.
   - *Rejected:* transient per-message activation (defeats prompt
     caching, re-pick every send); a sticky default with per-message
     overrides (two activation states to surface — UI complexity).

5. **A single item type.** Every item is one kind of markdown snippet;
   no "prompt vs context" type field.
   - *Why:* all active items land in the same cached prefix regardless,
     so a type system adds a field, a two-section UI, and assembly
     branching for little functional gain — consistent with how the app
     already concatenates everything stable into one prefix.
   - *Rejected:* two types routing prompt items to the system slot and
     context items to a separate block.

6. **One `.md` file per item.** Item content lives as individual files
   in a `library/` folder under the app data dir.
   - *Why:* the cleanest match for a "list of items"; enables editing,
     adding, and removing items on disk with any editor and syncing via
     git or Dropbox; follows the existing textbooks-on-disk precedent.
   - *Rejected:* a single `library.md` split by `##` headings (app must
     parse and rewrite; messy rename/reorder; on-disk edits fight the
     in-app editor); a SQLite column (no on-disk editing).

7. **H1 as display name, filename a frozen slug.** The filename is a
   no-space slug generated from the H1 once at creation and then frozen;
   the file's single H1 is the display name.
   - *Why:* the user wants no spaces in filenames but readable, spaced
     display names. Splitting identity (filename) from display (H1)
     means editing the display name never breaks activation references.
     Freezing the filename keeps the `conversation_library_items`
     references stable — auto-renaming would turn every H1 edit into a
     multi-step file-rename plus DB update that can half-fail.
   - *Rejected:* filename = display name (forces spaces in filenames or
     unreadable names, and renaming orphans activation); auto-renaming
     the file whenever the H1 changes.

8. **Scan-on-demand listing, no persisted index.** The item list is
   built by scanning the folder and reading each file's first-line H1.
   - *Why:* an index (TOC file or DB table) becomes a second source of
     truth that drifts from the filesystem, and specifically breaks the
     on-disk editing feature — out-of-app edits, adds, and deletes go
     unnoticed. Keeping an index correct needs filesystem watching or a
     reconcile-scan, and a reconcile-scan *is* the full scan. The reads
     are imperceptible: the first line of tens-to-hundreds of files,
     only on panel open, never on the send path.
   - *Escape hatch:* an mtime-keyed in-memory cache — only if a
     measurement ever demands it.
   - *Rejected:* a master table-of-contents file or database table
     updated on every save.

9. **Full-surface in-app editor, not a real OS window.** The editor
   reads as a separate window but stays inside the single Wails window.
   - *Why:* Wails v2's multi-window support is weak and historically
     platform-flaky; true multi-window is effectively a Wails v3
     feature. The in-app approach carries zero framework risk.
   - *Rejected:* a true second OS window via Wails v2 multi-window
     (would need a spike, with a real chance it fails on Windows).

10. **Migration collapses to a schema change.** No data migration of
    presets; the feature ships only a schema change.
    - *Why:* the app rename gives a fresh, empty
      `%APPDATA%\starshp_app\` data dir and pre-release data is
      disposable, so no preset content carries over. A fresh `app.db`
      starts with `conversation_library_items` and no `presets` table;
      a dev DB created between the rename and this feature gets a
      trivial drop of the old objects.
    - *Rejected:* a one-time presets→files migration copying preset
      content into markdown files and converting each conversation's
      `preset_id` into an active-set row.
