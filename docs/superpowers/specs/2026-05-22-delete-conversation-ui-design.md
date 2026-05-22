# Delete-conversation UI — Design

**Date:** 2026-05-22
**Status:** Approved (design)

## Problem

The conversation sidebar lists conversations but offers no way to delete one.
The backend is fully capable — `appapi.API.DeleteConversation(id)` exists and
is Wails-bound, and `store.DeleteConversation` runs
`DELETE FROM conversations WHERE id=?`, which cascades to `messages`,
`conversation_textbooks`, and `conversation_library_items` (each declares
`REFERENCES conversations(id) ON DELETE CASCADE`, and `store.Open` opens
SQLite with `foreign_keys(on)`). Only the frontend affordance is missing:
`frontend/src/main.ts` never calls `DeleteConversation`, and
`loadConversations()` renders each row as a plain `<div>` with only a title
and an open-on-click handler. SMOKE checklist step 9 ("Delete a conversation
→ it and its messages disappear, no orphan rows") cannot be performed.

## Goals

- Add a per-row delete affordance to the conversation sidebar.
- Permanently delete the conversation and — via the existing cascade — all of
  its messages and scope rows.
- Guard against accidental deletion.
- Handle deletion of the currently-open conversation gracefully.

## Non-goals

- Soft-delete / trash / undo (the README lists this as deferred).
- Any backend change — the cascade already produces no orphan rows.
- Bulk delete, conversation rename, or other sidebar management.

## Design

Frontend only: `frontend/src/main.ts` and `frontend/src/style.css`.

### Conversation rows — `loadConversations()`

`loadConversations()` (currently `main.ts:61-72`) renders each conversation
as a `<div class="conv">` with `textContent = title` and
`onclick = openConversation(id)`. Each row becomes a flex container with two
children:

- a **title `<span>`** holding the title text and the ellipsis-truncation
  styling
- a **`✕` delete `<button class="conv-del">`**, hidden until the row is
  hovered

The row `<div>` keeps `onclick → openConversation(id)`. The `✕` button has its
own click handler that calls `event.stopPropagation()` — so clicking `✕` does
not also open the conversation — and then invokes the delete handler.

### Delete handler — `deleteConversation(id)`

A new async function:

1. `if (!confirm('Delete this conversation? This cannot be undone.')) return`
2. `await App.DeleteConversation(id)`
3. On success: if `id === activeConv`, set `activeConv = null` and clear the
   thread pane (`thread.innerHTML = ''`); then `await loadConversations()` to
   refresh the sidebar.
4. On error: `alert()` the error's `userMessage` (falling back to the raw
   error). No state changes — nothing was deleted.

### Styling — `style.css`

- `.conv` gains `display: flex; align-items: center; gap: 6px;`. The
  `overflow`, `text-overflow`, and `white-space` ellipsis rules move from
  `.conv` onto the title span, which also gets `flex: 1; min-width: 0;`.
- `.conv-del` is small and subtle, `visibility: hidden` by default, revealed
  by `.conv:hover .conv-del { visibility: visible; }`.

### Behavior

| Action | Result |
| --- | --- |
| Delete a non-open conversation | Sidebar refreshes; the thread pane is untouched. |
| Delete the open conversation | Thread pane clears, `activeConv = null`; the next action (message / + New chat / 📚 Textbooks) starts a fresh conversation. |
| Delete the last conversation | Empty sidebar (and empty thread if it was open). |
| Cancel the confirm dialog | No-op. |
| `DeleteConversation` returns an error | `alert()` the message; no state change. |

The `activeConv === null` empty state is already fully supported —
`sendMessage`, `showTextbooks`, and the library panel all guard with
`if (!activeConv) await newChat()`.

## Testing

The frontend has no automated test framework; the project verifies UI
manually via `docs/SMOKE.md`. No automated tests are added. Verification is
SMOKE step 9: delete a conversation, confirm the row disappears and — because
`DeleteConversation` cascades — its messages do not orphan.

## Decisions

- Delete affordance: a `✕` button revealed on row hover.
- Confirmation: a native `confirm()` dialog.
- Deleting the open conversation clears the thread to the empty state
  (`activeConv = null`), rather than auto-opening another conversation.
