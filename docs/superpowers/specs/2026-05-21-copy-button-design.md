# Copy Button — Design

**Date:** 2026-05-21
**Status:** Approved — ready for implementation plan
**Scope:** Single small feature. Frontend only.

## Goal

Let the user copy an assistant reply to the clipboard with one click, so a
finished discussion-post draft can be pasted into an LMS or forum without
manual text selection.

## Behavior

- Every **assistant** reply gets one **icon-only** copy button, placed in an
  action row directly **below** the message bubble.
- The action row is hidden by default and **fades in on hover** of the
  message (CSS `:hover`). User messages get no button.
- Clicking the button copies the reply's **full plain text** to the clipboard
  via `navigator.clipboard.writeText`.
- On a successful copy, the icon swaps to a **checkmark for ~1.5s**, then
  reverts to the copy icon. No toast, no other notification.
- The icon is a small inline SVG styled to match the existing muted-grey pill
  palette (foreground `#a9a9ad`, subtle border, transparent background).

## When the button appears

- **History messages** (rendered by `openConversation`): action row attached
  immediately when the message is created.
- **Live send**: the action row is attached when streaming **ends** —
  whether it ended on success, on Stop, or on an error that left a partial
  reply persisted. It is **not** present during streaming.
- An assistant message with **empty** text gets no copy button.

## DOM restructure (the one invasive part)

Currently `.msg` *is* the text node: `addMsg` does `el.textContent = text`,
and the `chat:token` stream handler does `last.textContent += tok` against
`.msg.assistant:last-child`. Adding a child action row would break both.

The fix:

- `.msg` becomes a **wrapper** containing two children:
  - `.msg-text` — the actual text node (`white-space: pre-wrap`, carries the
    bubble text and existing line-height).
  - `.msg-actions` — the hover-revealed action row (assistant messages only).
- `addMsg` writes text into `.msg-text` instead of `.msg`.
- The `chat:token` handler appends tokens into
  `.msg.assistant:last-child .msg-text` instead of `.msg`.
- The bubble's visual styling (background, border, border-radius, padding,
  `align-self`) stays on `.msg`; `.msg-text` is an unstyled inner span/div.

This touches the streaming token-append path, so a streaming smoke-test is
required before shipping (see Verification).

## Files touched

- `frontend/src/main.ts` — restructure `addMsg` into `.msg > .msg-text`;
  add a helper that builds and attaches the `.msg-actions` row with the copy
  button; call it for history messages and from `send()`'s `finally` block;
  update the `chat:token` handler's selector.
- `frontend/src/style.css` — `.msg-text` (move `white-space: pre-wrap` here),
  `.msg-actions` (hidden, hover-reveal), and the icon-button styling.

No Go changes. No new Wails bindings. No backend, store, or RAG changes.

## Out of scope

- Copying user messages.
- A "copy whole conversation" button.
- Copying as Markdown or any rich format — replies render as plain text
  today, so the copy is plain text.
- A toast / notification system.

## Verification

Manual smoke test (frontend has no automated tests — see `docs/SMOKE.md`):

1. Send a message; while it streams, confirm no copy button is visible and
   tokens still append correctly to the bubble.
2. After streaming completes, hover the assistant reply — the copy button
   fades in. Click it — clipboard holds the full reply text; icon shows the
   checkmark, then reverts.
3. Press Stop mid-stream — the partial reply keeps a working copy button.
4. Trigger an error send (e.g. invalid model) — a partial/error reply still
   gets a copy button if it has text.
5. Reopen a conversation from the sidebar — historical assistant messages
   all show the copy button on hover; user messages show none.
6. Confirm no regression in streaming, RAG indexing banners, or the existing
   message layout.
