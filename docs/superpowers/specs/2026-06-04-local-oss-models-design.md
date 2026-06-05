# Local / open-source models via OpenAI-compatible runtimes

**Status:** approved design, ready for plan
**Date:** 2026-06-04
**Scope:** chat-model support only; embeddings remain OpenAI (per project's existing "local embeddings out of scope" line).
**Platforms:** Windows and macOS, code identical across both; documentation covers each.

## Goal

Let any OpenAI-compatible local server appear as a normal entry in the
per-message model picker so a user can route turns to a local model and
reduce per-token API spend. Ollama is the reference runtime — the same
config form covers LM Studio, vLLM, and llama.cpp server unchanged.

A user who has registered only Anthropic and local-Ollama models must not
be nagged for an `OPENAI_API_KEY` at startup unless they also configure
textbooks (RAG embeddings stay OpenAI-only).

## Non-goals

- Local / offline **embeddings.** The project explicitly defers these;
  this design does not revisit the decision.
- A **routing layer** that auto-selects local vs cloud per turn. Routing
  remains the user's per-message choice via the existing picker.
- Auto-discovery, "test connection" UI, or curated starter-model lists.
  These live in `BACKLOG.md` under Someday and may be picked up later.
- A **native Ollama API** integration. Ollama's OpenAI-compatibility
  shim covers chat streaming + usage, so reusing the existing OpenAI
  code path is strictly cheaper than adding a third SDK.

## Architecture

### New provider type: `openai_compat`

`models.yaml` accepts a third value in the `provider` field. Entries
carry a required `base_url` and an optional `api_key_env`:

```yaml
- display: Llama 3.2 (local)
  id: llama3.2
  provider: openai_compat
  base_url: http://localhost:11434/v1
  max_context: 131072
```

The name `openai_compat` (rather than `local` or `ollama`) reflects what
the entry actually is — a Chat Completions endpoint — and applies
identically to a local Ollama, a LAN-hosted LM Studio, or a vLLM
deployment requiring auth.

### `ModelInfo` extension

Two new optional fields in `internal/provider/registry.go`:

| Go field    | YAML key       | JSON key     | Purpose |
| ----------- | -------------- | ------------ | ------- |
| `BaseURL`   | `base_url`     | `baseURL`    | Endpoint for `openai_compat`. Required when `provider: openai_compat`; rejected on `openai` / `anthropic` entries. |
| `APIKeyEnv` | `api_key_env`  | `apiKeyEnv`  | Name of an env var holding a bearer token, for shims that require one. Optional; if absent the factory sends a dummy `"local"` string (Ollama accepts it; the OpenAI SDK requires non-empty). |

### Registry validation

`LoadRegistry` enforces field-meaning invariants at config-load time:

1. `provider: openai_compat` ⇒ `base_url` must be present and parseable
   as an absolute URL.
2. `provider: openai` or `provider: anthropic` ⇒ `base_url` must be
   absent.

Violations surface as the same setup-notice path that an unreadable
`models.yaml` already uses.

## Code structure

### No new SDK

The existing `openai-go/v3` client supports a custom base URL via
`option.WithBaseURL(...)`, and `NewOpenAI(apiKey, baseURL)` in
`internal/provider/openai.go` already accepts that parameter — it is
the seam the existing httptest suite uses. The entire `Stream` code
path (streaming, usage capture, prompt-cache prefix, cancellation) is
reused unchanged.

### Factory branch

`internal/provider/factory.go` gains one case:

```go
case "openai_compat":
    if m.BaseURL == "" {
        return nil, fmt.Errorf("model %s: base_url required for openai_compat", m.ID)
    }
    key := "local" // dummy; the OpenAI SDK requires non-empty
    if m.APIKeyEnv != "" {
        if v := os.Getenv(m.APIKeyEnv); v != "" {
            key = v
        }
    }
    return NewOpenAI(key, m.BaseURL), nil
```

No `AppError{"auth", ...}` branch — local servers do not have API keys
in the OpenAI sense.

### Conditional startup validation

`internal/appapi` currently emits a setup notice when `OPENAI_API_KEY`
is missing. The check is updated so it fires only if **at least one
registry entry has `provider: openai`** (not `openai_compat`). Same
logic for `ANTHROPIC_API_KEY` and `provider: anthropic`. Net effect: a
user who registers Anthropic + Ollama only is no longer asked for an
OpenAI key.

The RAG-embeddings invariant is unchanged: when `textbooks.yaml` lists
any books, `OPENAI_API_KEY` is required regardless of chat-model
selection.

### Files touched

- `internal/provider/registry.go` — two new fields, `LoadRegistry`
  validation.
- `internal/provider/factory.go` — new case + key-resolution helper.
- `internal/provider/errors.go` — new `local_unreachable` error code +
  classifier.
- `internal/appapi/<startup-validation file>` — conditional checks for
  OpenAI and Anthropic keys.
- `internal/provider/registry_test.go` — new field parsing +
  validation tests.
- `internal/provider/factory_test.go` (new or extended) — new branch
  tests.
- `internal/provider/openai_test.go` — extend with an `openai_compat`
  end-to-end stream against an httptest server.
- `internal/provider/errors_test.go` — `local_unreachable` mapping.
- `internal/appapi/*_test.go` — conditional-key-warning tests.

## Runtime behavior

### Error handling

A new error code `local_unreachable` is introduced. The error
normalizer in `internal/provider/errors.go` routes any error returned
from an `openai_compat` provider that wraps a `net.OpError`, a DNS
failure, or `context.DeadlineExceeded` into it, with a user message
of the form:

> Local model server unreachable at `http://localhost:11434/v1`. Is
> Ollama running? (Run `ollama serve` or start the Ollama app.)

The `base_url` is interpolated so the user knows exactly what was
being called. `Retryable: true` so the UI shows the retry affordance.

For cloud `openai` / `anthropic` errors, the existing `network` /
`unknown` codes are unchanged.

### Token usage / footer HUD

Ollama's OpenAI-compat shim populates `prompt_tokens` and
`completion_tokens` in the final streamed chunk; the existing OpenAI
code reads these into `Usage.InputTokens` / `Usage.OutputTokens`
unchanged. `CachedInputTokens` will be `0` — Ollama does not surface
a cache-hit count through the shim. This is correct, not a defect.
The context-footer HUD will display 0 cached for local turns.

### Prompt caching

The OpenAI provider sends the cacheable prefix as a system message.
Ollama benefits from its own KV-cache on identical prefixes within
its `keep_alive` window but does not surface cache-hit stats. No
code change; documented in README.

### `max_context` denominator

The footer HUD reads `max_context` from `models.yaml` as-is. The
README "Local models" section documents that this number must match
the model's actual context window — for Ollama, that is the model's
modelfile `num_ctx` (default 2048 or 4096; overridable via the
`OLLAMA_NUM_CTX` env var or a custom modelfile). A mismatch produces
a misleading HUD only; it does not break sends. Per-runtime
introspection of `num_ctx` is a Someday-bin item.

### Cost ceiling / budget

`CONTEXT_TOKEN_BUDGET` already governs RAG prefix size and is
provider-agnostic. No change.

### Cancellation

The Stop button cancels via `ctx.Done()`. Ollama's OpenAI-compat
endpoint honors HTTP cancellation, so Stop works identically to
cloud providers — no extra wiring.

## Cross-platform setup & documentation

### README — new section "Local models via Ollama"

Inserted between "Configuration reference" and "Testing". Covers:

1. **Why.** Zero per-token cost; runs on Apple Silicon (Metal) and
   Windows (CUDA/DirectML auto-detected). Embeddings still use
   OpenAI per the project's scope.
2. **Install.**
   - macOS: `brew install ollama` then `brew services start ollama`,
     *or* drag the installer from ollama.com to Applications.
   - Windows: `winget install Ollama.Ollama`, *or* run the installer
     from ollama.com. Auto-starts as a background service.
3. **Pull a model.** `ollama pull llama3.2` (or `qwen2.5:7b`,
   `mistral`, etc.). The Ollama-side model name is what goes in the
   `id:` field of the yaml entry.
4. **Register.** Add an entry to `models.yaml` matching the snippet
   above. Restart Starshp. The model appears in the per-message
   picker.
5. **Troubleshooting.** "Local model server unreachable" → start
   Ollama. Wrong context-window in the HUD → `max_context` must
   match the model's actual `num_ctx`. Slow first token on a fresh
   model → Ollama is loading it into memory; subsequent turns within
   `keep_alive` (default 5m) are fast.
6. **Hardware sizing note.** Apple Silicon: usable RAM ≈ unified
   memory minus 8 GB for OS + app. Windows: usable RAM ≈ GPU VRAM.
   The "match the model size to available RAM/VRAM" rule of thumb
   is given inline; specific tier recommendations are a backlog
   item.

### `models.example.yaml`

Appended commented-out starter entry, OS-agnostic since
`http://localhost:11434/v1` works identically on both platforms:

```yaml
  # Local model via Ollama (https://ollama.com).
  # Install Ollama, run `ollama pull llama3.2`, then uncomment:
  # - display: Llama 3.2 (local)
  #   id: llama3.2
  #   provider: openai_compat
  #   base_url: http://localhost:11434/v1
  #   max_context: 131072
```

### README — macOS prerequisites refinement

The existing Prerequisites section already mentions Wails. Appended:

- Run `wails doctor` after installing Wails. On macOS it verifies
  Xcode Command Line Tools (`xcode-select --install` if missing)
  and WebKit. No additional steps for Apple Silicon vs Intel.
- The "Running" section currently states that `wails build`
  produces `build/bin/starshp_app.exe`. Reword as: produces
  `build/bin/starshp_app.exe` on Windows, `build/bin/starshp_app.app`
  on macOS, `build/bin/starshp_app` on Linux.

### README — "Out of scope" update

The bullet "Cross-platform packaging — Windows-first; macOS/Linux
later" is reworded to: "Linux packaging — Windows and macOS supported;
Linux builds work but are not smoke-tested." Active dev happens on
both Windows and macOS, so the original wording is stale.

### `docs/SMOKE.md` — local-model step

One step appended:

> **Local model end-to-end.** With Ollama installed and `ollama pull
> llama3.2` complete, register the Ollama entry from
> `models.example.yaml` in your `models.yaml`, restart Starshp, pick
> "Llama 3.2 (local)" in a new conversation, send a short prompt.
> Confirm streaming, the Stop button, the context-footer HUD
> (input/output tokens populate, cached shows 0), and that stopping
> Ollama mid-session yields the `local_unreachable` error with the
> base URL interpolated.

### `.env.example`

No change. Local models do not introduce new env vars in this MVP.

## Tests

All Go-side; project convention is no automated UI tests.

| Test | Location | What it covers |
| --- | --- | --- |
| Registry parses `openai_compat` entry with `base_url` + `api_key_env` | `internal/provider/registry_test.go` | New YAML fields round-trip. |
| Registry rejects `openai_compat` with missing `base_url` | `internal/provider/registry_test.go` | Config-load validation. |
| Registry rejects `openai` / `anthropic` with a stray `base_url` | `internal/provider/registry_test.go` | Field-meaning invariant. |
| Factory builds an OpenAI-SDK client pointed at the entry's `base_url` | `internal/provider/factory_test.go` | Branch wiring. |
| Factory falls back to dummy `"local"` key when `api_key_env` is absent | `internal/provider/factory_test.go` | Auth-bypass for keyless local servers. |
| Factory honors `api_key_env` when the env var is set | `internal/provider/factory_test.go` | Future-proofs LM Studio / vLLM-with-auth. |
| `openai_compat` end-to-end stream against an httptest server | `internal/provider/openai_test.go` (extend) | Streaming, usage capture, cancellation all work via `base_url`. |
| Connection-refused error normalizes to `local_unreachable` with interpolated `base_url` | `internal/provider/errors_test.go` | New error code. |
| Startup validator does not require `OPENAI_API_KEY` with only `openai_compat` + `anthropic` models | `internal/appapi/*_test.go` | Conditional-validation logic. |
| Startup validator still requires `OPENAI_API_KEY` when textbooks are configured | `internal/appapi/*_test.go` | RAG-embeddings invariant unchanged. |

## Implementation order

The plan that follows this spec will execute in this order, with each
step landing as an atomic commit:

1. Registry fields + validation.
2. Factory `openai_compat` branch + key-resolution helper.
3. `local_unreachable` error code + classifier.
4. Conditional startup validation.
5. Wails binding regeneration (`wails generate module` or `wails
   build`) — verify dropdown / frontend behavior unaffected.
6. `models.example.yaml` starter entry.
7. README updates (Local models section, macOS prerequisites, out-of-
   scope reword, `wails build` artifact reword).
8. `docs/SMOKE.md` step.
9. macOS manual smoke pass — execute the new step, tick the doc.

## Risks / open knobs

- **Ollama context-window mismatch.** If `max_context` in
  `models.yaml` does not match the model's actual `num_ctx`, the HUD
  denominator lies. Documented in the README; no code defense — the
  right fix is per-runtime introspection (Someday).
- **Streamed-usage absence.** A future OpenAI-compat shim that omits
  the final usage chunk would leave `CachedInputTokens` /
  `OutputTokens` at zero. The existing code already handles "no usage
  seen" via the `haveAny` flag — the HUD just shows zeros. No new
  defense needed.
- **Wails binding shape.** Adding `BaseURL` / `APIKeyEnv` fields to
  `ModelInfo` regenerates the TypeScript binding. The frontend reads
  `display`, `id`, `provider`, and `maxContext` only — new optional
  fields should not break anything. Smoke step verifies.

## References

- `docs/superpowers/specs/2026-05-17-discussion-engine-llm-chat-client-design.md` — frozen design for the chat client these changes extend.
- `docs/superpowers/specs/2026-05-27-context-tracking-design.md` — context-tracking footer / Usage capture, reused here unchanged.
- `internal/provider/openai.go` — the streaming/usage implementation reused by `openai_compat`.
- `internal/provider/factory.go` — the file the new case lives in.
- `BACKLOG.md` Someday — auto-detect, "test connection" button, and curated starter-model recommendations queued there.
