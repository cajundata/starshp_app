# Gemini Text Provider — Design (Spec A)

**Date:** 2026-07-15
**Status:** Approved; not yet implemented
**Builds on:** the provider seam established by
[Local OSS Models](2026-06-04-local-oss-models-design.md) (`openai_compat`,
conditional key validation, `local_unreachable` remapping).
**Sequenced with:** Spec B — Gemini image generation (Nano Banana 2) — which
builds on this provider client. Spec B's brainstormed decisions are recorded
in the Out of Scope section so they survive until its own design doc exists.

## Context

Starshp speaks to Anthropic, OpenAI, and any OpenAI-compatible local server.
Google's Gemini models are the missing major family, and the operator wants
them for two things: ordinary text chat now, and image generation with
Nano Banana 2 next. Image generation needs Gemini's native API regardless
(the OpenAI-compat shim's image support is not a foundation to build on), so
text support goes native too — one coherent provider instead of a
compat-endpoint bridge that would be deprecated a cycle later.

This spec covers text only. A persona pins a Gemini model exactly as it pins
any other; nothing else about the app changes.

## Decisions

- **`gemini` is a fourth first-class provider type**, alongside `anthropic`,
  `openai`, and `openai_compat` — not an `openai_compat` preset. Rationale:
  Spec B needs the native client anyway; native gets implicit caching stats,
  structured errors, and first-class function calling.
- **SDK: `google.golang.org/genai`** — Google's official Gen AI Go SDK, pure
  Go, no CGO. Consistent with the vendored Anthropic/OpenAI SDKs. API-key
  auth against `generativelanguage.googleapis.com` (the Gemini Developer
  API); Vertex AI auth is out of scope.
- **`GEMINI_API_KEY` follows conditional validation.** Required at startup
  only when `models.yaml` holds at least one `provider: gemini` entry —
  the same registry-only check as `needsOpenAIKey`/`needsAnthropicKey`;
  absent otherwise with no complaint. A missing key surfaces in the
  startup-issues banner.
- **`provider.New` takes a `Keys` struct.** The current
  `New(reg, model, openaiKey, anthropicKey)` signature would grow a
  positional parameter per provider family; it changes to
  `New(reg, model, Keys{OpenAI, Anthropic, Gemini string})`. A handful of
  call sites (appapi plus factory tests), all mechanical.
- **No thinking configuration in v1.** The adapter does not set
  `thinkingConfig`; thought summaries are not surfaced. Final text only.

## User-visible surface

`models.yaml` (and `models.example.yaml`, which gains a Pro and a Flash
entry):

```yaml
- display: Gemini 3 Pro
  id: gemini-3-pro
  provider: gemini
  max_context: 1000000
```

- Registry validation mirrors `anthropic`/`openai`: `base_url` and
  `api_key_env` are rejected on a `gemini` entry (those belong to
  `openai_compat`). Model IDs remain operator-editable, no recompile.
- `max_context` feeds the existing context-footer HUD unchanged.
- `.env.example` gains `GEMINI_API_KEY`. README gains the key in
  Prerequisites and a sentence in the `models.yaml` section.

Everything downstream — persona pinning, `@` mentions, baton-pass
attribution, turn context overrides, streaming, Stop, the prompt library —
works with a Gemini persona with zero changes, because it all sits above the
`ChatProvider` seam.

## Adapter design

New file `internal/provider/gemini.go` implementing
`ChatProvider.Stream(ctx, ChatRequest) (<-chan Delta, error)`, plus a
`case "gemini"` in the factory.

**Request mapping** (parallel to the two existing adapters):

- `System` + `Grounding` concatenate into `systemInstruction`.
- `Events` → Gemini `contents`: `user_message` → role `user`;
  `assistant_text` → role `model`; `assistant_tool_call` → a `model` turn
  carrying a `functionCall` part; `tool_result` → a `user` turn carrying a
  `functionResponse` part.
- `Tools` → `functionDeclarations`, passing our JSON Schema through the
  SDK's raw-schema field.

**Tool-call ID impedance.** Gemini matches function responses by *name*, not
call ID. `ToolCallID` stays authoritative in the store and event log; the
adapter drops it on the wire and relies on name + order. Safe because the
agentic loop pairs results one-to-one with the calls of the immediately
preceding assistant turn.

**Streaming.** The SDK's streaming iterator maps onto `Delta` frames: text
parts → `Delta.Text`; function calls arrive whole (Gemini does not fragment
them) → `Delta.ToolCall`; finish reason `STOP` → `end_turn` (or `tool_use`
when the turn emitted calls), `MAX_TOKENS` → `max_tokens`, anything else →
`error`. Stop-button cancellation rides the existing `ctx` propagation
through the iterator.

**Usage and caching.** Gemini 2.5+/3 performs implicit prompt caching —
nothing to manage, no `cache_control` analogue. The terminal frame reports
`usageMetadata.promptTokenCount` → `InputTokens`, `candidatesTokenCount` →
`OutputTokens`, `cachedContentTokenCount` → `CachedInputTokens`, so the
footer's cached-tokens readout works unchanged.

## Error normalization

`NormalizeError` gains the Gemini vocabulary, keyed off the SDK's structured
`APIError` HTTP status first, string sniffing only as fallback:

| Gemini signal | AppError code |
| --- | --- |
| 401/403, `PERMISSION_DENIED`, "API key not valid" | `auth` |
| 429, `RESOURCE_EXHAUSTED` | `rate_limit` |
| 400 token-limit / "exceeds the maximum number of tokens" | `context_length` |
| transport / connection failures | `network` |

`local_unreachable` remapping stays `openai_compat`-only.

## Testing

All pure Go, no network — the SDK accepts a `BaseURL` override, so an
`httptest` fake serves canned SSE:

- `gemini_test.go` mirroring `anthropic_test.go`/`openai_test.go`: text
  streaming; a tool-call round trip (functionCall out, functionResponse in
  the next request body); usage/cached-token reporting; finish-reason
  mapping; mid-stream `ctx` cancellation.
- Registry: `gemini` accepted; `base_url`/`api_key_env` on a `gemini` entry
  rejected.
- Factory: `Keys` struct refactor; `gemini` case returns the adapter.
- `NormalizeError`: table rows for the vocabulary above.
- Startup validation: `GEMINI_API_KEY` demanded only when the registry
  holds a `gemini` model.

**SMOKE.md additions (six steps):** pin a persona to a Gemini model and
stream a reply; Stop mid-stream persists the partial; a `safe_math` tool
call completes; `@`-mention a Gemini persona mid-thread and confirm the
attributed baton-pass block; footer shows sane token/cached counts; missing
`GEMINI_API_KEY` with a Gemini persona produces the startup banner.

**Verification gate:** `go test ./...`, `wails build`, smoke on both
Windows and macOS against the real API before merge.

## Out of scope

- **Spec B — image generation with Nano Banana 2.** Decisions already made
  in brainstorming, recorded here so they carry into Spec B's design:
  - Image models are pinnable like any model; an "Artist" persona's replies
    are images rendered in the chat thread. No separate panel.
  - Full iterative refinement: prior generated images ride back into the
    image persona's context so "make the sky darker" edits the last image.
  - Storage: PNG files under `<app-dir>/images/`, content-hash named
    (matching the RAG index's hash pattern), referenced from the event log;
    served to the UI via a Wails asset handler; a deleted file renders as a
    placeholder on replay.
  - Team fit: full citizen. `@artist` works mid-thread; when a *text*
    persona follows an image turn, the baton-pass block describes the image
    textually (`From Artist (model): [image] <prompt used>`) — text personas
    never receive raw image bytes in v1.
- Gemini embeddings — RAG stays OpenAI (existing intentional boundary).
- Vertex AI authentication — API-key auth only.
- Thinking budgets / thought summaries — final text only in v1.
