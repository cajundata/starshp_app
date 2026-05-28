# Manual Smoke Checklist

Prereq: `.env` with OPENAI_API_KEY (+ ANTHROPIC_API_KEY to use Claude models),
`textbooks.yaml` pointing at a markdown textbook dir, `models.yaml` present.

Run: `wails dev`

## Core

1. [x] App launches; if keys/configs missing, a setup notice lists the issues.
2. [x] "+ New chat" creates a conversation; it appears in the sidebar.
3. [x] Type a message, Send → assistant reply streams token-by-token.
4. [x] Switch the model dropdown mid-conversation; next reply uses the new model.
5. [x] Click 📚 Textbooks, attach a book, Save → "Indexing… N/total" then "ready".
6. [x] Ask a question answerable from the textbook → reply is grounded.
7. [x] Click Stop during streaming → stream is cancelled; the partial reply is persisted.
8. [x] Close and relaunch → conversation history is intact; reopening restores messages.
9. [x] Delete a conversation → it and its messages disappear (no orphan rows).

## Library

10. [x] Click ≡ Library → the panel opens and lists existing items (empty on first run).
11. [x] "+ New item" → the editor opens; saving content with no H1 is rejected with a clear message.
12. [x] Add an H1 (`# My Item`) and a body, Save → the item appears in the panel list.
13. [x] Edit an item and change its H1 → the display name updates; the `.md` file on disk keeps its original name.
14. [x] Toggle items active with the checkboxes; the checked state reflects the selection.
15. [x] Send a message → the active items' bodies reach the model in the system prompt, with the H1 stripped.
16. [x] Switch conversations and relaunch the app → each conversation restores its own active set (sticky).
17. [x] Delete an active item's `.md` file on disk → it drops from the panel and is skipped on send (a soft notice appears), no crash.

## Textbooks

18. [x] Rename `<app-dir>/textbooks/<book>/` so the manifest path is broken → 📚 Textbooks still opens; that book renders as `(unavailable: …)` with its checkbox disabled; other books remain selectable.
19. [x] Restore the folder → reopen the picker; the book is selectable again with its chapter count.

## Context tracking footer

20. [x] **Footer renders after first reply on a fresh conversation.** Create a new conversation, send a short message, wait for the reply. The strip below the thread shows `ctx N / M · cache K` (with denominator if the active model has `max_context` set in `models.yaml`).
21. [x] **Footer updates across model switches mid-conversation.** Switch the model picker mid-conversation, send a follow-up. The denominator shifts to the new model's `max_context`; values keep growing turn over turn.
22. [x] **`~` marker after Stop.** Start a long reply, click Stop. The footer keeps the previous turn's values, prefixed with `~`.
23. [x] **No denominator when `max_context` is omitted.** Remove `max_context` from one model in `models.yaml`, restart, send a message with that model. Footer shows `ctx N · cache K` (no `/ M` segment).
24. [x] **Footer survives conversation switches.** Open a conversation with prior history; the footer seeds from the last assistant message's recorded tokens. Switch to another conversation, then back.
