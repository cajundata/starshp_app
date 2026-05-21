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
