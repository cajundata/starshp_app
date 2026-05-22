# Starshp

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

Starshp reads its configuration from a per-user **app directory**, created
automatically on first launch. Copy the three committed templates
(`.env.example`, `models.example.yaml`, `textbooks.example.yaml`) into that
directory, drop the `.example` from each name, and fill in your API keys.

See [Config files and textbooks](#config-files-and-textbooks) for where the
app directory is, the copy commands, and the YAML formats.

## Config files and textbooks

Starshp keeps every per-user file in one **app directory**:

| OS | App directory |
| --- | --- |
| Windows | `%APPDATA%\starshp_app` |
| Linux | `~/.config/starshp_app` |
| macOS | `~/Library/Application Support/starshp_app` |

It holds `.env`, `models.yaml`, `textbooks.yaml`, your textbook chapter
folders, and the runtime data (`app.db`, `rag.db`, `library/`). The directory
is created automatically on first launch. Set the `STARSHP_HOME` environment
variable (an absolute path) to override its location — handy for tests or a
portable install.

Three templates ship in the repo. Copy each into the app directory and edit:

```bash
cp .env.example           <app-dir>/.env
cp models.example.yaml    <app-dir>/models.yaml
cp textbooks.example.yaml <app-dir>/textbooks.yaml
```

Edit `.env` to fill in your API keys. None of these files require a recompile.

Typical app-directory layout:

```
%APPDATA%\starshp_app\
├── .env
├── models.yaml
├── textbooks.yaml
├── app.db                    (created at runtime)
├── rag.db                    (created at runtime)
├── library/                  (created at runtime)
└── intermediate-accounting/  (your textbook chapter folders)
    ├── chapter-01.md
    └── ...
```

### `textbooks.yaml`

Each entry names a book and points at its directory of chapter markdown:

```yaml
textbooks:
  - name: intermediate-accounting
    chapter_dir: ./intermediate-accounting
  - name: financial-accounting
    chapter_dir: /absolute/path/to/financial-accounting
```

- `chapter_dir` is resolved **relative to the directory containing
  `textbooks.yaml`** (the app directory) — not the working directory. Keep it
  `./<book>` and store the folders alongside the file, or give an absolute
  path.
- A chapter folder holds files named `chapter-1.md`, `chapter-2.md`, … —
  leading zeros optional (`chapter-01.md` is equivalent). Files that do not
  match that pattern are ignored.
- `name` is the label in the per-conversation textbook picker and the key
  used to scope RAG retrieval.
- `textbooks.yaml` is optional: if absent, RAG is unavailable and chat still
  works. If present but a `chapter_dir` cannot be read, opening the textbook
  picker or attaching a book returns an error — fix the path and try again.

### `models.yaml`

The list of models offered in the per-message picker:

```yaml
models:
  - display: Claude Opus 4.7      # label shown in the UI
    id: claude-opus-4-7           # identifier sent to the provider
    provider: anthropic           # "anthropic" or "openai"
  - display: GPT-5.4
    id: gpt-5.4-2026-03-05
    provider: openai
```

Edit it freely as model IDs evolve — no recompile. A missing or unreadable
`models.yaml` produces a setup notice at launch and an empty model dropdown.

## Running

```bash
wails dev      # hot-reload dev mode
wails build    # release binary at build/bin/starshp_app.exe
```

`app.db` (conversations, messages, presets, scope) and `rag.db` (textbook
chunks + embeddings) are created in the app directory on first launch. They
are independent — rebuilding the RAG index never endangers chat history.
Override either path via `APP_DB_PATH` / `RAG_DB_PATH` in `.env`.

## Configuration reference

All variables below are read from `.env` (and the OS environment); see
`.env.example`. `STARSHP_HOME` is the exception — it must be a real
environment variable, since it determines where `.env` itself is found.

| Variable | Default | Purpose |
| --- | --- | --- |
| `STARSHP_HOME` | OS app directory | Overrides the app directory. Real env var only (absolute path), not a `.env` entry. |
| `OPENAI_API_KEY` | — | OpenAI key (chat + embeddings). |
| `ANTHROPIC_API_KEY` | — | Anthropic key (chat only). |
| `EMBEDDING_MODEL` | `text-embedding-3-small` | OpenAI embedding model. |
| `APP_DB_PATH` | `<app-dir>/app.db` | Chat history DB. |
| `RAG_DB_PATH` | `<app-dir>/rag.db` | RAG index DB. |
| `LIBRARY_DIR` | `<app-dir>/library` | Prompt library storage directory. |
| `TEXTBOOKS_CONFIG` | `textbooks.yaml` | Textbook manifest; a relative value resolves inside the app directory. |
| `MODELS_CONFIG` | `models.yaml` | Model registry; a relative value resolves inside the app directory. |
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
starshp_app/
├── main.go                     # Wails bootstrap (config → store → rag → api)
├── models.yaml                 # selectable model registry (display + id + provider)
├── models.example.yaml         # template — copy into your app directory
├── textbooks.example.yaml      # template — copy into your app directory
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
