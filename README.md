# Discussion Engine

A Grok-style desktop LLM chat client (Wails v2 + Go) for drafting accounting
discussion posts. Per-message model choice across OpenAI and Anthropic,
persistent conversation history, system-prompt presets, and textbook-grounded
RAG retrieval reused verbatim from the acctutor project.

Single native binary, no browser, no server, no CGO — `modernc.org/sqlite`
plus pure-Go provider SDKs.

## Features

- **Per-message model picker.** Switch between OpenAI and Anthropic models
  mid-conversation; the choice is recorded per assistant message and pinned
  per conversation.
- **Streaming responses with Stop.** Tokens stream to the UI live via Wails
  runtime events; the Stop button cancels the in-flight stream and persists
  the partial reply.
- **System-prompt presets.** Named, reusable system prompts; selectable per
  conversation.
- **Textbook-grounded RAG.** Attach none / one / multiple books per
  conversation. Acctutor's `embedding`, `chunker`, and `ragindex` packages
  are reused verbatim behind a thin adapter (`internal/rag/adapter.go`),
  which adds scope-filtered + budget-trimmed retrieval and a content-hash
  idempotent index-on-attach path with progress events.
- **Auto-titled conversations.** First user message becomes the title
  (truncated to 60 chars). Rename UI is post-MVP.
- **Sticky per-conversation meta.** Reopening a conversation restores its
  last-used model and preset.
- **Prompt caching.** The stable system + textbook-context block is cached
  per provider — Anthropic via explicit `cache_control`, OpenAI via
  automatic prefix caching.
- **Hard cost ceilings.** Configurable context-token budget, top-K, and
  RAG over-fetch factor.
- **Normalized errors.** All errors surfaced to the UI as
  `{code, userMessage, retryable}` — auth / rate_limit / context_length /
  network / rag_unavailable / unknown.
- **Startup validation.** Missing keys, unreadable DB paths, or absent
  `models.yaml` produce a setup notice in the first message bubble rather
  than a silent failure.

## Prerequisites

- **Go 1.25** (toolchain pinned via `go.mod`).
- **Wails v2 CLI** — install with:
  ```bash
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
  ```
  Then verify with `wails doctor`.
- **OpenAI API key** — required for textbook embeddings even when chatting
  with an Anthropic model.
- **Anthropic API key** — optional, required only to chat with Claude
  models listed in `models.yaml`.
- A directory of per-chapter markdown textbooks
  (`<book>/chapter-NN.md` layout, acctutor-compatible) — optional, only
  needed for RAG features.

## Setup

```bash
cp .env.example .env
# Edit .env to fill in OPENAI_API_KEY (+ ANTHROPIC_API_KEY if using Claude).

# Point textbooks.yaml at your markdown textbook root, e.g.:
# textbooks:
#   - name: intermediate-accounting
#     chapter_dir: /path/to/textbooks/intermediate-accounting
```

`models.yaml` is committed with sensible defaults (Claude Opus/Sonnet/Haiku
4.x and `gpt-5.4-2026-03-05`); edit it freely as model IDs evolve — no
recompile needed.

## Running

```bash
wails dev      # hot-reload dev mode
wails build    # release binary at build/bin/discussion_engine.exe
```

On first launch the app creates its data directory at the OS user-config
location (`%APPDATA%\discussion_engine\` on Windows). Two SQLite DBs live
there: `app.db` (conversations, messages, presets, scope) and `rag.db`
(textbook chunks + embeddings). They are independent — rebuilding the RAG
index never endangers chat history.

Override either path via `APP_DB_PATH` / `RAG_DB_PATH` in `.env`.

## Configuration reference

All variables read from `.env` (and the environment); see `.env.example`.

| Variable | Default | Purpose |
| --- | --- | --- |
| `OPENAI_API_KEY` | — | OpenAI key (chat + embeddings). |
| `ANTHROPIC_API_KEY` | — | Anthropic key (chat only). |
| `EMBEDDING_MODEL` | `text-embedding-3-small` | OpenAI embedding model. |
| `APP_DB_PATH` | `<user-config>/discussion_engine/app.db` | Chat history DB. |
| `RAG_DB_PATH` | `<user-config>/discussion_engine/rag.db` | RAG index DB. |
| `TEXTBOOKS_CONFIG` | `textbooks.yaml` | Path to textbook YAML. |
| `MODELS_CONFIG` | `models.yaml` | Path to model registry. |
| `CONTEXT_TOKEN_BUDGET` | `2500` | Max textbook context tokens injected per turn. |
| `RAG_TOP_K` | `8` | Top-K passed to vector search (over-fetched ×6, then scope-filtered + budget-trimmed). |

## Testing

Backend test suite (the verbatim-copied acctutor packages keep their own
tests, which run as drift detection):

```bash
go test ./...
```

Frontend has no automated tests — use the manual smoke checklist in
[`docs/SMOKE.md`](docs/SMOKE.md) before shipping changes that touch UI,
streaming, or RAG.

## Project structure

```
discussion_engine/
├── main.go                     # Wails bootstrap (config → store → rag → api)
├── models.yaml                 # selectable model registry (display + id + provider)
├── wails.json
├── docs/
│   ├── SMOKE.md                # manual smoke checklist
│   └── superpowers/
│       ├── specs/              # frozen design doc
│       └── plans/              # implementation plan (all tasks complete)
├── frontend/                   # vanilla-TS Wails frontend (Grok-style Variant B)
│   └── src/{main.ts, style.css}
└── internal/
    ├── appapi/                 # Wails-bound API; error-normalization boundary
    ├── chat/                   # orchestration: persist → retrieve → stream → persist
    ├── config/                 # .env + env loader
    ├── provider/               # ChatProvider interface, OpenAI + Anthropic impls,
    │                           # factory, error normalization, model registry
    ├── rag/                    # adapter (scope filter, budget, content-hash index)
    │   ├── adapter.go          # ←  the only entry point app code uses
    │   ├── embedding/          # ←  copied from acctutor — VERBATIM, DO NOT MODIFY
    │   ├── chunker/            # ←  copied from acctutor — VERBATIM, DO NOT MODIFY
    │   ├── ragindex/           # ←  copied from acctutor — VERBATIM, DO NOT MODIFY
    │   └── REUSED.md           # boundary documentation
    ├── store/                  # SQLite: conversations, messages (cascade), presets, scope
    └── textbooks/              # textbooks.yaml scan + chapter listing
```

### Architectural rules

1. **RAG boundary is sacred.** Files under `internal/rag/{embedding,chunker,ragindex}/`
   are copied verbatim from acctutor — never modify them. Any new
   scope-aware query or extension belongs in a *new* file inside our copy,
   never a modification of an upstream file. This keeps a future shared-module
   extraction a one-shot replace.
2. **Errors normalize at `internal/appapi`.** Backend packages may return
   raw errors; `appapi` is the single boundary that converts everything to
   `provider.AppError{Code, UserMessage, Retryable}` before the UI sees it.
3. **RAG never silently degrades.** If textbooks are attached and retrieval
   fails (e.g. missing OpenAI key, embedding error), the send aborts with an
   explicit error — it does not silently fall back to an ungrounded prompt.
4. **Two DB files, separate concerns.** `app.db` (chat) and `rag.db` (index)
   are independent. Wiping or rebuilding one never affects the other.

## Architecture reference

The frozen design doc:
[`docs/superpowers/specs/2026-05-17-discussion-engine-llm-chat-client-design.md`](docs/superpowers/specs/2026-05-17-discussion-engine-llm-chat-client-design.md).

The implementation plan (all 20 tasks complete):
[`docs/superpowers/plans/2026-05-17-discussion-engine.md`](docs/superpowers/plans/2026-05-17-discussion-engine.md).

## Out of scope (deferred)

Tracked here so reviewers know absences are intentional:

- Visual / color refinement pass — Variant B layout is the starting point.
- Cross-platform packaging — Windows-first; macOS/Linux later.
- Extracting a shared `acct-rag` Go module — current copy-behind-adapter
  approach is by design.
- Local / offline embeddings — embedding cost is negligible.
- Conversation rename UI — auto-title only for MVP.
- Soft-delete / trash-restore for conversations.
- Automated frontend / UI testing — manual smoke checklist instead.

## Troubleshooting

- **"Setup" message lists missing keys/files at launch.** Fix the items
  listed and relaunch — `appapi.ValidateStartup` emits one issue per
  missing/broken item.
- **`rag_unavailable` errors when attaching a textbook.** RAG needs an
  `OPENAI_API_KEY` for embeddings even when chatting with Claude. Set it
  in `.env` and restart.
- **Empty model dropdown.** `models.yaml` is missing or unreadable — check
  `MODELS_CONFIG` and that the file is at the path it points to.
- **`go test ./...` is slow on first run.** Pure-Go `modernc.org/sqlite`
  compiles a lot on first build; subsequent runs are fast.
- **Wails bindings out of date after editing Go API.** Run `wails generate
  module` (or just `wails build`) to regenerate `frontend/wailsjs/`.
