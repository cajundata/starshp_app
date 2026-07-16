# Starshp

A desktop LLM chat client (Wails v2 + Go) built around a roster of named
personas instead of one generic assistant. Each persona is a markdown file
that pins a model, a system prompt, an optional tool whitelist, and library
items that attach automatically whenever it runs. One persona is pinned per
conversation, a leading `@persona` mention hands any single turn to a
different one, and every assistant message is color-coded so who said what
stays clear across a live session and after a restart.

Single native binary, no browser, no server, no CGO — `modernc.org/sqlite`
plus pure-Go provider SDKs.

## Features

- **A team of personas, not one assistant.** Each conversation is assigned a
  single persona — a markdown file under `<app-dir>/personas/` that pins a
  model, a system prompt, an optional tool whitelist, and library items that
  attach automatically whenever it runs. Assistant bubbles carry the
  persona's name, color, and model chip, live and on replay alike. See
  [Personas](#personas).
- **Multi-persona threads via `@` mentions.** Start a message with
  `@persona-id` to route just that turn to another assistant — the
  conversation's pinned persona is untouched, and the next unmentioned
  message goes back to it. Typing `@` as the first character of the composer
  opens an autocomplete of the roster. A handoff arrives attributed
  (`From Scout (model): …`), never disguised as the recipient's own words,
  and a mention that matches no persona errors without sending.
- **Streaming responses with Stop.** Tokens stream to the UI live via Wails
  runtime events; the Stop button cancels the in-flight stream and persists
  the partial reply.
- **Prompt library.** Reusable markdown snippets (an H1 title plus a body),
  toggled active per conversation and folded into the system prompt after
  the persona's own body. A persona's `library:` list attaches
  automatically; the conversation's own selections are appended after.
- **Textbook-grounded RAG.** Attach none / one / multiple books per
  conversation. Acctutor's `embedding`, `chunker`, and `ragindex` packages
  are reused verbatim behind a thin adapter (`internal/rag/adapter.go`),
  which adds scope-filtered + budget-trimmed retrieval and a content-hash
  idempotent index-on-attach path with progress events.
- **Auto-titled conversations.** First user message becomes the title
  (truncated to 60 chars). Rename UI is post-MVP.
- **Sticky per-conversation meta.** Reopening a conversation restores its
  pinned persona, and with it the model that persona uses.
- **Prompt caching.** The stable system + textbook-context block is cached
  per provider — Anthropic via explicit `cache_control`, OpenAI via
  automatic prefix caching.
- **Hard cost ceilings.** Configurable context-token budget, top-K, and
  RAG over-fetch factor.
- **Normalized errors.** All errors surfaced to the UI as
  `{code, userMessage, retryable}` — auth / rate_limit / context_length /
  network / rag_unavailable / unknown.
- **Startup validation.** Missing keys, unreadable DB paths, an absent
  `models.yaml`, or a persona file that fails validation produce a setup
  notice — in the first message bubble, and in a startup banner for persona
  issues — rather than a silent failure.

## Prerequisites

- **Go 1.25** (toolchain pinned via `go.mod`).
- **Wails v2 CLI** — install with:
  ```bash
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
  ```
  Then verify with `wails doctor`.
- On macOS, `wails doctor` verifies Xcode Command Line Tools
  (`xcode-select --install` if missing) and WebKit. Apple Silicon and
  Intel both work without extra setup.
- **OpenAI API key** — required for textbook embeddings even when chatting
  with an Anthropic model.
- **Anthropic API key** — optional, required only to chat with Claude
  models listed in `models.yaml`.
- **Gemini API key** — optional, required only to chat with Gemini models
  listed in `models.yaml`.
- A directory of per-chapter markdown textbooks
  (`<book>/chapter-NN.md` layout, acctutor-compatible) — optional, only
  needed for RAG features.

## Setup

Starshp reads its configuration from a per-user **app directory**, created
automatically on first launch. Everything needed to build a full one ships
in the repo as copyable examples:

| Repo source | Copies to | Purpose |
| --- | --- | --- |
| `.env.example` | `<app-dir>/.env` | API keys, path overrides, behavior toggles |
| `models.example.yaml` | `<app-dir>/models.yaml` | Model registry personas may reference |
| `textbooks.example.yaml` | `<app-dir>/textbooks.yaml` | Textbook manifest (optional, RAG only) |
| `personas.example/` | `<app-dir>/personas/` | Starter roster: Assistant, Scout, Skeptic |
| `library.example/` | `<app-dir>/library/` | Starter prompt-library item (`style-guide`) |

Copy each into place (drop the `.example` from the three single files) and
fill in your API keys. Every model the example personas pin exists in
`models.example.yaml`, so a directory built entirely from these examples
validates cleanly at first launch.

If `<app-dir>/personas/` does not exist at launch, it is seeded with a
single generic persona (`assistant.md`, pinned to the first model in
`models.yaml`) so the app is never without an assistant — the
`personas.example/` roster is the fuller starting point.

See [Config files and textbooks](#config-files-and-textbooks) for where the
app directory is, the copy commands, and the YAML formats, and
[Personas](#personas) for the persona file format and `@` mention routing.

## Config files and textbooks

Starshp keeps every per-user file in one **app directory**:

| OS | App directory |
| --- | --- |
| Windows | `%APPDATA%\starshp_app` |
| Linux | `~/.config/starshp_app` |
| macOS | `~/Library/Application Support/starshp_app` |

It holds `.env`, `models.yaml`, `textbooks.yaml`, the `textbooks/` folder for
your chapter directories, the `personas/` folder for your persona roster, and
the runtime data (`app.db`, `rag.db`, `library/`). The directory is created
automatically on first launch, along with the `textbooks/`, `personas/`, and
`library/` subdirectories. Set the `STARSHP_HOME` environment variable (an
absolute path) to override its location — handy for tests or a portable
install.

Copy the repo examples into the app directory and edit:

```bash
cp .env.example           <app-dir>/.env
cp models.example.yaml    <app-dir>/models.yaml
cp textbooks.example.yaml <app-dir>/textbooks.yaml
cp personas.example/*.md  <app-dir>/personas/
cp library.example/*.md   <app-dir>/library/
```

Edit `.env` to fill in your API keys. None of these files require a
recompile. Library items are re-read on every send; `.env`, `models.yaml`,
`textbooks.yaml`, and the persona roster are read at launch, so changes to
those take effect on the next restart.

Typical app-directory layout:

```
%APPDATA%\starshp_app\
├── .env
├── models.yaml
├── textbooks.yaml
├── app.db                        (created at runtime)
├── rag.db                        (created at runtime)
├── library/                      (created at runtime; copy library.example/ items here)
│   └── style-guide.md
├── personas/                     (seeded with assistant.md when absent)
│   ├── assistant.md
│   ├── scout.md
│   └── skeptic.md
└── textbooks/                    (created at runtime)
    └── intermediate-accounting/  (your textbook chapter folders)
        ├── chapter-01.md
        └── ...
```

### `textbooks.yaml`

Each entry names a book and points at its directory of chapter markdown:

```yaml
textbooks:
  - name: intermediate-accounting
    chapter_dir: ./textbooks/intermediate-accounting
  - name: financial-accounting
    chapter_dir: /absolute/path/to/financial-accounting
```

- `chapter_dir` is resolved **relative to the directory containing
  `textbooks.yaml`** (the app directory) — not the working directory. The
  convention is `./textbooks/<book>/`; the `textbooks/` parent is pre-created
  on first launch. Absolute paths are accepted for corpora that live
  elsewhere.
- A chapter folder holds files named `chapter-1.md`, `chapter-2.md`, … —
  leading zeros optional (`chapter-01.md` is equivalent). Files that do not
  match that pattern are ignored.
- `name` is the label in the per-conversation textbook picker and the key
  used to scope RAG retrieval.
- `textbooks.yaml` is optional: if absent, RAG is unavailable and chat still
  works. If a `chapter_dir` cannot be read, the picker still opens and that
  book renders as `(unavailable: <reason>)` with its checkbox disabled;
  attaching it via an existing scope returns a `textbook_unavailable` error
  on the next send. Fix the path and the entry recovers.

### `models.yaml`

The list of models a persona's `model:` field may reference:

```yaml
models:
  - display: Claude Opus 4.7      # label shown in the UI
    id: claude-opus-4-7           # identifier sent to the provider
    provider: anthropic           # "anthropic", "openai", "gemini", or "openai_compat"
  - display: GPT-5.4
    id: gpt-5.4-2026-03-05
    provider: openai
```

Edit it freely as model IDs evolve — no recompile. A missing or unreadable
`models.yaml` produces a setup notice at launch and an empty model dropdown.
An `openai`/`openai_compat` entry may also set `reasoning_effort` (forwarded
verbatim to the chat-completions request) to work around models — like GPT-5.6
Sol — whose default reasoning effort rejects function tools. Entries may also
set `input_modalities` / `output_modalities` (each a list of `text`/`image`,
defaulting to `[text]`); since the app is text-only today, a persona pinned to
a model whose `output_modalities` excludes `text` is disabled at startup until
image output ships.

### Personas

Starshp assigns one persona per conversation instead of picking a raw model.
The pinned persona answers every unmentioned message. To hand a single turn
to a different assistant, start the message with its ID — `@skeptic poke
holes in that` — and that turn alone is answered by Skeptic while the picker
(and the pin) stay put; the next unmentioned message returns to the pinned
persona. Typing `@` as the first character of the composer opens an
autocomplete of the roster. Mentions count only at the very start of a
message, so pasted code, emails, and mid-sentence `@`s are always literal
text. A mention that matches no persona is a hard `config` error listing the
available IDs, and nothing is sent — there is deliberately no fuzzy matching
or silent substitution. When one persona follows another, the previous
persona's reply reaches it as an attributed `From Scout (<model>):` block
(final text only, without the other persona's tool activity), so an
assistant never mistakes a teammate's words for its own.

Each persona is a single markdown file in `<app-dir>/personas/`; the filename
stem is its stable ID (`scout.md` → `scout`). Override the directory with the
`PERSONA_DIR` environment variable (an absolute path). Frontmatter is YAML,
the body is the system prompt:

```markdown
---
name: Scout
model: claude-sonnet-4-6
color: "#4fb3ff"
tools: [safe_math]
library: [style-guide]
---
You are Scout. You find the angle nobody else is looking at.
...
```

- `name` — required, shown in the picker and on assistant bubbles.
- `model` — required, must match an `id` in `models.yaml`.
- `color` — optional 6-digit hex. Omit it and one is assigned deterministically
  from a contrast-checked palette, keyed on the persona's ID (the filename
  stem) — the same persona always gets the same color, across restarts and
  machines.
- `tools` — optional whitelist of tool names the persona may call
  (`safe_math`, `search_textbook`). Omit it to allow every registered tool.

  **`tools` gates what an assistant can *do*, not what it can *see*.** Attaching
  a textbook to a conversation injects relevant passages into every turn, before
  the model runs, and that path does not consult the persona at all. So an
  assistant restricted to `tools: [safe_math]` cannot go searching the textbook —
  but it can still be handed passages from it. That is deliberate: a textbook
  scopes the *conversation*, not the assistant's capabilities. If you want an
  assistant with no textbook access whatsoever, don't attach a textbook to the
  conversation (or set `STARSHP_SKIP_AUTO_GROUNDING=1` to disable pre-turn
  injection globally).
- `library` — optional list of library item names this persona always
  carries; the `.md` extension may be omitted. These attach to every turn
  this persona runs, ahead of whatever the conversation has toggled on — an
  item claimed by both appears once, in the persona's position. The
  `library: [style-guide]` line above resolves once you copy
  `library.example/style-guide.md` into `<app-dir>/library/`; a named item
  that is missing is skipped with a soft notice, never an error.

`personas.example/` in the repo ships three starter personas (Assistant,
Scout, Skeptic) — copy them into `<app-dir>/personas/` as a starting point.

A persona file that fails to parse or references an unknown model or tool is
**disabled and reported in the startup banner**, naming the file and the
reason — it never blocks the app from launching. An unknown persona ID at
send time (for example, its file was deleted mid-session) is a hard
`config` error naming the assistant; there is deliberately no fallback to
another persona, since that would attribute output to an assistant the
operator never chose.

## Running

```bash
wails dev      # hot-reload dev mode
wails build    # release binary: build/bin/starshp_app.exe (Windows),
               #                  build/bin/starshp_app.app (macOS),
               #                  build/bin/starshp_app    (Linux)
```

`app.db` (conversations, runs, the event log, textbook/library scope) and
`rag.db` (textbook chunks + embeddings) are created in the app directory on
first launch. They are independent — rebuilding the RAG index never
endangers chat history. Override either path via `APP_DB_PATH` /
`RAG_DB_PATH` in `.env`.

## Configuration reference

All variables below are read from `.env` (and the OS environment); see
`.env.example`. `STARSHP_HOME` is the exception — it must be a real
environment variable, since it determines where `.env` itself is found.

| Variable | Default | Purpose |
| --- | --- | --- |
| `STARSHP_HOME` | OS app directory | Overrides the app directory. Real env var only (absolute path), not a `.env` entry. |
| `OPENAI_API_KEY` | — | OpenAI key (chat + embeddings). |
| `ANTHROPIC_API_KEY` | — | Anthropic key (chat only). |
| `GEMINI_API_KEY` | — | Gemini key (chat only). |
| `EMBEDDING_MODEL` | `text-embedding-3-small` | OpenAI embedding model. |
| `APP_DB_PATH` | `<app-dir>/app.db` | Chat history DB. |
| `RAG_DB_PATH` | `<app-dir>/rag.db` | RAG index DB. |
| `LIBRARY_DIR` | `<app-dir>/library` | Prompt library storage directory. |
| `PERSONA_DIR` | `<app-dir>/personas` | Persona roster directory. |
| `TEXTBOOKS_CONFIG` | `textbooks.yaml` | Textbook manifest; a relative value resolves inside the app directory. |
| `MODELS_CONFIG` | `models.yaml` | Model registry; a relative value resolves inside the app directory. |
| `CONTEXT_TOKEN_BUDGET` | `2500` | Max textbook context tokens injected per turn. |
| `RAG_TOP_K` | `8` | Top-K passed to vector search (over-fetched ×6, then scope-filtered + budget-trimmed). |
| `STARSHP_SKIP_AUTO_GROUNDING` | — | Set to `1` to disable pre-turn textbook injection globally (the `search_textbook` tool still works). |
| `STARSHP_MAX_TOOL_ITERATIONS` | `16` | Cap on tool-dispatch rounds per turn; at the cap the model is asked for a final answer with tools withheld. |

## Local models via Ollama

Starshp talks to any OpenAI-compatible local server. Ollama is the
reference runtime — same simple install on Windows and macOS.

### Why

Zero per-token cost and zero network round-trip for chat. RAG textbook
embeddings still use OpenAI (see the project's intentional "out of
scope: local embeddings" line); only chat traffic moves local.

### Install

| OS | Command |
| --- | --- |
| macOS | `brew install ollama && brew services start ollama`, or installer from ollama.com |
| Windows | `winget install Ollama.Ollama`, or installer from ollama.com (auto-starts as a service) |

### Pull a model

```bash
ollama pull llama3.2
# or: ollama pull qwen2.5:7b
# or: ollama pull mistral
```

The exact model name passed to `ollama pull` is the value that goes in
the `id:` field of the `models.yaml` entry.

### Register it in `models.yaml`

```yaml
- display: Llama 3.2 (local)
  id: llama3.2
  provider: openai_compat
  base_url: http://localhost:11434/v1
  max_context: 131072
```

`provider: openai_compat` is the new third provider type, covering any
OpenAI-Chat-Completions-compatible endpoint (Ollama, LM Studio, vLLM,
llama.cpp server). `base_url` is required. `api_key_env` (not shown) is
an optional name of an env var holding a bearer token, for shims that
require one.

Restart Starshp. Point a persona's `model:` field at the new `id` (or add a
new persona file that does) and pick that persona on your next turn — done.

### Hardware sizing

Match the model size to the available RAM/VRAM. On Apple Silicon,
usable RAM ≈ unified memory minus ~8 GB reserved for the OS and the
app; on Windows the bound is GPU VRAM. A more detailed tier-by-tier
recommendation is queued in `BACKLOG.md` Someday.

### Troubleshooting

| Symptom | Fix |
| --- | --- |
| "Local model server unreachable at …" in the UI | Start Ollama (`ollama serve` or the Ollama menu-bar/system-tray app). |
| Context-footer HUD denominator looks wrong | `max_context` in `models.yaml` must match the model's actual `num_ctx`. Ollama's default is small (2048 or 4096 depending on model). Override with `OLLAMA_NUM_CTX` env var or a custom modelfile. |
| Slow first token after a model has been idle | Ollama is loading the model into memory. Subsequent turns within `keep_alive` (default 5 minutes) are fast. |
| Cached tokens in the footer always show 0 | Ollama's OpenAI-compat shim does not surface cache-hit stats. Not a bug. |

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
├── models.example.yaml         # template — copy into your app directory
├── textbooks.example.yaml      # template — copy into your app directory
├── personas.example/           # starter personas — copy into <app-dir>/personas/
├── library.example/            # starter library items — copy into <app-dir>/library/
├── wails.json
├── docs/
│   ├── SMOKE.md                # manual smoke checklist
│   └── superpowers/
│       ├── specs/              # frozen design docs
│       └── plans/              # implementation plans
├── frontend/                   # vanilla-TS Wails frontend (Grok-style Variant B)
│   └── src/{main.ts, style.css}
└── internal/
    ├── appapi/                 # Wails-bound API; error-normalization boundary
    ├── chat/                   # orchestration: persist → retrieve → stream → persist
    ├── config/                 # .env + env loader
    ├── library/                # prompt/context snippet storage (markdown, H1-named)
    ├── persona/                # <app-dir>/personas/ registry: parsing, validation,
    │                           # deterministic color assignment
    ├── provider/               # ChatProvider interface, OpenAI + Anthropic impls,
    │                           # factory, error normalization, model registry
    ├── rag/                    # adapter (scope filter, budget, content-hash index)
    │   ├── adapter.go          # ←  the only entry point app code uses
    │   ├── embedding/          # ←  copied from acctutor — VERBATIM, DO NOT MODIFY
    │   ├── chunker/            # ←  copied from acctutor — VERBATIM, DO NOT MODIFY
    │   ├── ragindex/           # ←  copied from acctutor — VERBATIM, DO NOT MODIFY
    │   └── REUSED.md           # boundary documentation
    ├── store/                  # SQLite: conversations (persona pin), runs, scope
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

The persona system (color-coded multi-model assistant team) has its own
design doc and plan:
[`docs/superpowers/specs/2026-07-13-persona-foundation-design.md`](docs/superpowers/specs/2026-07-13-persona-foundation-design.md),
[`docs/superpowers/plans/2026-07-13-persona-foundation.md`](docs/superpowers/plans/2026-07-13-persona-foundation.md).

Multi-persona threads (`@` mention routing and attributed handoffs):
[`docs/superpowers/specs/2026-07-13-multi-persona-threads-design.md`](docs/superpowers/specs/2026-07-13-multi-persona-threads-design.md),
[`docs/superpowers/plans/2026-07-14-multi-persona-threads.md`](docs/superpowers/plans/2026-07-14-multi-persona-threads.md).

## Out of scope (deferred)

Tracked here so reviewers know absences are intentional:

- Visual / color refinement pass — Variant B layout is the starting point.
- Linux packaging — Windows and macOS supported; Linux builds work but are not smoke-tested.
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
- **A persona is missing from the picker.** Its file failed validation (bad
  `model`, bad `tools` entry, bad `color`, missing `name`, or a duplicate
  ID). The reason is listed in the same startup banner, naming the file.
  Fix the file and relaunch — a broken persona file never blocks the app.
- **`rag_unavailable` errors when attaching a textbook.** RAG needs an
  `OPENAI_API_KEY` for embeddings even when chatting with Claude. Set it
  in `.env` and restart.
- **Empty model dropdown.** `models.yaml` is missing or unreadable — check
  `MODELS_CONFIG` and that the file is at the path it points to.
- **`go test ./...` is slow on first run.** Pure-Go `modernc.org/sqlite`
  compiles a lot on first build; subsequent runs are fast.
- **Wails bindings out of date after editing Go API.** Run `wails generate
  module` (or just `wails build`) to regenerate `frontend/wailsjs/`.
