# Manual Smoke Checklist

Prereq: `.env` with OPENAI_API_KEY (+ ANTHROPIC_API_KEY to use Claude models),
`textbooks.yaml` pointing at a markdown textbook dir, `models.yaml` present.

Run: `wails dev`

## Core

1. [ ] App launches; if keys/configs missing, a setup notice lists the issues.
2. [ ] "+ New chat" creates a conversation; it appears in the sidebar.
3. [ ] Type a message, Send → assistant reply streams token-by-token.
4. [ ] Switch the model dropdown mid-conversation; next reply uses the new model.
5. [ ] Click 📚 Textbooks, attach a book, Save → "Indexing… N/total" then "ready".
6. [ ] Ask a question answerable from the textbook → reply is grounded.
7. [ ] Click Stop during streaming → stream is cancelled; the partial reply is persisted.
8. [ ] Close and relaunch → conversation history is intact; reopening restores messages.
9. [ ] Delete a conversation → it and its messages disappear (no orphan rows).

## Library

10. [ ] Click ≡ Library → the panel opens and lists existing items (empty on first run).
11. [ ] "+ New item" → the editor opens; saving content with no H1 is rejected with a clear message.
12. [ ] Add an H1 (`# My Item`) and a body, Save → the item appears in the panel list.
13. [ ] Edit an item and change its H1 → the display name updates; the `.md` file on disk keeps its original name.
14. [ ] Toggle items active with the checkboxes; the checked state reflects the selection.
15. [ ] Send a message → the active items' bodies reach the model in the system prompt, with the H1 stripped.
16. [ ] Switch conversations and relaunch the app → each conversation restores its own active set (sticky).
17. [ ] Delete an active item's `.md` file on disk → it drops from the panel and is skipped on send (a soft notice appears), no crash.
