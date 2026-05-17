# Discussion Engine

Grok-style desktop LLM chat client (Wails + Go) for drafting accounting
discussion posts, with per-message model choice (OpenAI/Anthropic), persistent
history, system-prompt presets, and textbook-grounded RAG (reused from acctutor).

## Setup
1. `cp .env.example .env` and fill in API keys.
2. Copy/point `textbooks.yaml` at your markdown textbook directory
   (`<book>/chapter-NN.md` layout).
3. `wails dev` (development) or `wails build` (release binary).

## Architecture
See `docs/superpowers/specs/2026-05-17-discussion-engine-llm-chat-client-design.md`
and `docs/superpowers/plans/2026-05-17-discussion-engine.md`.

RAG packages under `internal/rag/{embedding,chunker,ragindex}` are copied
verbatim from acctutor and used only via `internal/rag/adapter.go`. Do not
modify the copied files; add scope logic as new files in our copy.
