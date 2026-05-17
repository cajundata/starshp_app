# Manual Smoke Checklist (MVP)

Prereq: `.env` with OPENAI_API_KEY (+ ANTHROPIC_API_KEY to use Claude models),
`textbooks.yaml` pointing at a markdown textbook dir, `models.yaml` present.

Run: `wails dev`

1. [ ] App launches; if keys/configs missing, a setup notice lists the issues.
2. [ ] "+ New chat" creates a conversation; it appears in the sidebar.
3. [ ] Type a message, Send → assistant reply streams token-by-token.
4. [ ] Switch the model dropdown mid-conversation; next reply uses the new model.
5. [ ] Create a preset (via DB or a future settings UI); select it; reply reflects the system prompt.
6. [ ] Click 📚 Textbooks, attach a book, Save → "Indexing… N/total" then "ready".
7. [ ] Ask a question answerable from the textbook → reply is grounded.
8. [ ] Click Stop during streaming → stream is cancelled; the partial reply is persisted.
9. [ ] Close and relaunch → conversation history is intact; reopening restores messages.
10. [ ] Delete a conversation → it and its messages disappear (no orphan rows).
