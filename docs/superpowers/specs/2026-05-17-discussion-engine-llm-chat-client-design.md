# Discussion Engine — LLM Chat Client Design

**Date:** 2026-05-17
**Status:** Approved design, pre-implementation

## Purpose

A simple, Grok-style LLM chat client. Primary use: drafting initial discussion
posts and replies for a post-baccalaureate accounting certificate program,
grounded in course textbooks. Built generic so it expands to other prompt
workflows later.

## Scope

### In scope (MVP)

- Grok-style desktop chat UI (dark theme, history sidebar, centered thread,
  bottom-toolbar composer).
- Per-message model selection from a configurable registry; OpenAI + Anthropic
  models available at launch.
- Persistent, browsable conversation history (survives restarts).
- Saved system-prompt presets (named, pickable per conversation).
- Textbook context: config points at a markdown textbook directory (chapters
  already split into per-chapter `.md` files); attach none / one / multiple
  books, optionally specific chapters, per conversation.
- RAG over textbooks by **copying** acctutor's `embedding`, `chunker`,
  `ragindex` packages verbatim behind a thin adapter.
- Streaming responses with a Stop control.
- Cost controls: prompt caching of the stable system+context block,
  configurable context-token budget, configurable top-K.
- API keys via `.env` (godotenv), mirroring acctutor.

### Out of scope (explicitly deferred)

- Visual/color polish — Variant B layout is a starting point; a dedicated
  design-refinement pass comes later.
- Cross-platform packaging — Windows-first; code kept portable, macOS/Linux
  later.
- Extracting a shared `acct-rag` Go module — future consolidation once both
  apps need shared evolution.
- Local/offline embedding models — embedding cost is negligible; revisit only
  if it becomes material.
- Conversation rename UI — auto-title only for MVP.
- Automated frontend/UI testing — manual smoke checklist for MVP.
- Soft-delete / trash-restore for conversations.

## Architecture Decisions

1. **App shell: Wails v2.** Decisive because acctutor's reusable RAG code is
   pure Go with no CGO (`modernc.org/sqlite`), binding directly into a Wails
   backend. Single binary, native window, no port/browser/CORS management,
   cross-compile remains realistic later. A local web app wins only with
   remote/multi-device needs or a non-Go RAG — neither applies.

2. **RAG reuse: copy behind an adapter.** acctutor's RAG packages live under
   `internal/`, which Go forbids importing across modules. Copy `embedding`,
   `chunker`, `ragindex` verbatim into `internal/rag/`; all app code talks only
   to `rag/adapter.go`. Zero risk to acctutor, fully independent. Future path:
   extract a shared `acct-rag` module (noted, not now).

3. **Frontend stack:** lightweight SPA in the Wails frontend (plain HTML/CSS/TS
   or a minimal framework); finalized during implementation. Layout follows
   approved Grok-style Variant B.

## Module Layout

```
discussion_engine/
├── main.go                  # Wails bootstrap
├── .env / .env.example      # OPENAI_API_KEY, ANTHROPIC_API_KEY, *_MODEL, EMBEDDING_MODEL
├── textbooks.yaml           # textbook root + book definitions (acctutor-compatible)
├── models.yaml              # selectable model registry (provider, display name, model ID)
├── internal/
│   ├── app/                 # Wails-bound API; error normalization boundary
│   ├── chat/                # conversation orchestration: assemble → stream → persist
│   ├── provider/            # NEW generic streaming chat: OpenAI + Anthropic
│   ├── store/               # SQLite: conversations, messages, presets, scopes
│   ├── presets/             # system-prompt preset CRUD
│   ├── textbooks/           # textbook dir scan + chapter listing
│   └── rag/
│       ├── embedding/       # ← copied from acctutor (verbatim, incl. tests)
│       ├── chunker/         # ← copied from acctutor (verbatim, incl. tests)
│       ├── ragindex/        # ← copied from acctutor (verbatim, incl. tests)
│       └── adapter.go       # generic: Index(dir), Retrieve(query, scope) → context
└── frontend/                # Wails frontend assets
```

**Boundary rule:** copied acctutor packages stay verbatim and untouched. Any
scope-query addition is a *new* file in our copy, never a modification of an
upstream file. This keeps the future shared-module swap a localized change.

## UI

Approved layout: **Grok-style Variant B**.

- Dark theme; left sidebar with `+ New chat` and conversation history list.
- Centered message thread; user messages right-aligned (blue), assistant
  left-aligned.
- Composer: text input with a compact toolbar **below** it — model picker,
  preset picker, textbook-scope picker on the left; **Send** on the right
  (Grok-like).
- Live token streaming (Grok-style typing); Stop control during generation.
- Indexing progress indicator shown on first attach of an unindexed/stale book
  (e.g. "Indexing Intermediate Accounting… 14/21 chapters").

Color/visual refinement is a deliberate later pass, not MVP.

## Data Model

One SQLite DB (`modernc.org/sqlite`) at the OS user-config dir
(e.g. `%APPDATA%\discussion_engine\app.db`). Separate from the RAG index DB.

```
conversations
  id, title, created_at, updated_at,
  preset_id        (nullable — preset active when created)
  pinned_model     (nullable — last model used in this conversation)

messages
  id, conversation_id, role (user|assistant),
  content, model,            -- model that produced an assistant message
  created_at,
  rag_context   (nullable)   -- textbook context injected this turn (audit)
  rag_sources   (nullable)   -- JSON: chapters/chunks used

presets
  id, name, system_prompt, created_at, updated_at

conversation_textbooks
  conversation_id, textbook_name, chapter_nums (JSON, null = whole book)
```

Decisions:
- **Auto-title:** first user message, truncated. Rename UI is post-MVP.
- **Per-message model recorded** on each assistant message.
- **Sticky per conversation:** reopening restores last model, preset, and
  textbook scope. New chat uses app defaults.
- **`rag_context`/`rag_sources` persisted** for auditability/reproducibility.
- **Hard delete with cascade** (conversation → messages). No trash/restore.

## RAG & Textbook Context

**Reused verbatim:** `embedding.Embedder` (query embedding), `chunker`
(markdown → chunks), `ragindex.Store` + staleness detection. **Not reused:**
acctutor's `ragindex.Retrieve()` (coupled to acctutor domain types). The
adapter reimplements the generic slice: embed query → `Store.QueryTopK` →
scope-filter → format context block.

**Config:** `.env` + `textbooks.yaml`, pointing at the markdown textbook root;
each book a subdir of per-chapter `.md` files (acctutor's existing
`textbooks/<book>/chapter-NN.md` layout — already compatible). Per conversation:
attach none / one / multiple books, optionally specific chapters.

**Indexing lifecycle:**
- RAG index is a separate SQLite DB using acctutor's `ragindex` schema,
  untouched.
- On attaching a book, check acctutor staleness (`ChunkingPolicyVersion` +
  source hashes). If missing/stale, index: chunk → batch-embed → store, with a
  one-time progress indicator. Only changed books re-index.
- Indexing is resumable (per-chapter writes; staleness check resumes).

**Per-message retrieval:**
1. No textbook attached → skip RAG entirely (pure chat, zero embedding cost).
2. Otherwise: embed the user message (`text-embedding-3-small`), `QueryTopK`
   with a generous K, filter results to attached book/chapter scope via chunk
   metadata, trim to the context-token budget.
3. Format surviving chunks into a context block prepended to the system prompt;
   persist to `messages.rag_context`/`rag_sources`.

**Scope-filter tradeoff:** `Store.QueryTopK(ctx, vec, k)` has no scope param and
copied code stays verbatim, so scoping is **over-fetch + filter** in the adapter
(fetch K×overfetch, keep in-scope). Adequate for course-sized corpora. Future
tuning knob if recall degrades with many attached books: a scoped SQL query as
a *new* file in our copy.

**Failure mode:** textbook attached but `OPENAI_API_KEY` missing → clear,
specific error ("Textbook context requires an OpenAI key for embeddings"). RAG
failure never silently degrades to an ungrounded prompt.

## Provider Layer

New code (acctutor's provider is tutor-specific). One interface, two impls.

```go
type ChatProvider interface {
    Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error)
}

type ChatRequest struct {
    Model        string
    CachedPrefix string    // system prompt + textbook context — cacheable
    Messages     []Message
}
type Delta struct { Text string; Done bool; Err error }
```

- **OpenAI:** reuses `github.com/openai/openai-go/v3` (already in acctutor's
  dependency set). Streaming via SDK stream API; relies on automatic prefix
  caching for `CachedPrefix`.
- **Anthropic:** adds official `anthropic-sdk-go`. Streaming via message-stream
  API; explicit `cache_control` breakpoint at the end of `CachedPrefix`.
- `CachedPrefix` is the prompt-caching seam (the primary cost lever).

**Model registry — `models.yaml`, not hardcoded.** Lists selectable models per
provider (display name, model ID, provider); populates the composer picker.
Editing YAML beats recompiling as model IDs churn. Seeded defaults:
- Anthropic: Claude Opus 4.7 (`claude-opus-4-7`), Claude Sonnet 4.6
  (`claude-sonnet-4-6`), Claude Haiku 4.5 (`claude-haiku-4-5-20251001`).
- OpenAI: `gpt-5.4-2026-03-05` (acctutor default) plus a cheaper tier; IDs
  adjusted in YAML to available access.

**Streaming to UI:** Go reads the delta channel, emits Wails runtime events;
frontend appends tokens progressively. Stop cancels the `context.Context`,
cleanly aborting the SDK stream; partial assistant message persisted as-is.

**Errors normalized:** auth/invalid key, rate limit (with retry-after when
present), context-length exceeded (suggests trimming textbook scope), network
failure. No raw SDK errors to the UI.

## Cost Controls

- **Prompt caching** of the stable system+context block (Anthropic explicit
  `cache_control`; OpenAI automatic prefix caching). Largest lever — far
  outweighs any chunker choice.
- **Context-token budget:** configurable cap (default ~2,500 tokens of textbook
  context); retrieved chunks trimmed to the ceiling.
- **Configurable top-K** exposed in config.
- **Per-message model picker** as a cost control (draft cheap, escalate only
  when needed).
- Chunking is local and free; acctutor's structure-aware chunker is kept (a
  generic OSS splitter would be a downgrade for accounting textbooks and break
  the pure-Go story).

## Error Handling

- Layered, normalized at the `internal/app` boundary; UI receives only
  `{code, userMessage, retryable}`.
- Mid-stream failure: partial text persisted, marked incomplete
  ("⚠ response interrupted").
- RAG degradation explicit, never silent.
- Indexing resumable via staleness check.
- Startup validation: `.env` keys, textbook dir reachable, DB writable →
  single setup screen if misconfigured.
- App DB and RAG DB are separate files; RAG rebuild never endangers history.

## Testing Strategy

- Copied acctutor packages keep their existing `_test.go` files verbatim; run
  in CI to prove the copy works and detect upstream drift.
- `rag/adapter.go`: fake embedder + in-memory SQLite — scope filtering,
  over-fetch+filter correctness, budget trimming, no-textbook skip path.
- `provider`: table-driven tests with mocked HTTP transports (OpenAI +
  Anthropic) — delta assembly, cancellation aborts cleanly, `CachedPrefix`
  placement, error normalization.
- `store`: CRUD + cascade-delete on temp SQLite; sticky model/preset/scope
  restore.
- `chat`: integration with fake provider + fake RAG — prompt assembly order
  (CachedPrefix = system + textbook context, then messages), `rag_context`/
  `rag_sources` persisted.
- TDD throughout for new code (tests before implementation).
- Frontend: manual smoke checklist (send/stream/stop, mid-conversation model
  switch, preset apply, textbook attach + index progress, history restore).
