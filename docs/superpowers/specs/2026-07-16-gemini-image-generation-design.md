# Gemini Image Generation — Design (Spec B)

**Date:** 2026-07-16
**Status:** Approved; not yet implemented
**Builds on:** the native Gemini provider from
[Spec A](2026-07-15-gemini-text-provider-design.md) (shipped 2026-07-16) and
the persona/team machinery from the
[persona foundation](2026-07-13-persona-foundation-design.md) and
[multi-persona threads](2026-07-13-multi-persona-threads-design.md) specs.
Spec B's core decisions were banked in Spec A's Out of Scope section and are
honored here.

## Context

Starshp personas pin models; every reply so far has been text. Nano Banana 2
(`gemini-3-pro-image`) generates images interleaved with text, and the
operator wants an Artist/Visual Designer persona whose deliverables are the
images themselves, rendered in the chat thread, refinable across turns
("make the sky darker" edits the last image), and visible to the rest of the
team through the existing baton-pass protocol.

This spec ships the image spine: generation, storage, serving, rendering,
iterative refinement, and team fit. It is generation-only — the only images
in the system are ones a model produced. Operator image upload is deferred
(BACKLOG Someday).

## Decisions

Banked from Spec A's brainstorming:

- **Image models are pinnable like any model.** An image persona's replies
  render as images in the chat thread. No separate panel.
- **Iterative refinement:** the image persona's prior generated images ride
  back into its own context so follow-up prompts edit prior output.
- **Storage:** PNG files under `<app-dir>/images/`, content-hash named
  (sha256 hex, matching the RAG index's hash pattern), referenced from the
  event log; served to the UI via a Wails asset handler; a deleted file
  renders as a placeholder on replay.
- **Team fit: full citizen.** `@visual-designer` works mid-thread; when a
  text persona follows an image turn, the baton-pass block describes the
  image textually. Text personas never receive raw image bytes.

New decisions made in this brainstorm:

- **Refinement context is capped at the last 6 images.** Gemini's inline
  request payload tops out around 20 MB and each PNG runs 1–2 MB, so "all
  prior images" would eventually hard-fail a long session. Walking backward
  through the image persona's own history, the 6 most recent
  `assistant_image` events are inflated to inline bytes; older ones degrade
  to the placeholder text `[earlier image omitted]`. The model's own
  interleaved commentary stays in context as ordinary `assistant_text`, so
  degradation is graceful.
- **Generation-only in v1.** No operator image upload (file picker,
  input-side storage, and input-modality plumbing are a separable follow-up
  → BACKLOG Someday).
- **Images are a first-class event kind** (`assistant_image`), not an
  inline token inside `assistant_text` and not a synthetic tool call.
  Every layer needs an explicit image branch anyway — provider replay
  re-inflates bytes, display replay emits an image element, the baton relay
  textualizes — so the kind is explicit and testable rather than implicit
  and leaky.
- **Image runs have no tools.** The Gemini API does not support function
  calling alongside image response modalities; the adapter omits
  `functionDeclarations` when in image mode. Image personas simply have no
  tool access in v1.
- **PNG is assumed, mime is not stored.** Nano Banana 2 emits PNG; the
  store carries only `image_hash`. If a future model emits another format,
  a mime column is a small additive migration then.
- **Click-to-enlarge, save-as, and other viewer polish → BACKLOG Someday.**
- **Post-smoke corrections (2026-07-17, smoke 73):** Nano Banana 2 attaches a
  thought signature to its image parts; multi-turn editing requires echoing it
  verbatim, so `assistant_image` rows persist `{"thought_signature","mime"}` in
  the existing `tool_metadata` column and the adapter echoes both on replay.
  NB2 also emits `image/jpeg`, superseding "PNG is assumed" — the mime is
  stored in metadata and the asset handler sniffs the served content type;
  `.png` file naming remains a content-hash convention only. The refinement
  cap dropped 6 → 4 to keep signature-laden requests under the payload limit.

## User-visible surface

`models.example.yaml` gains:

```yaml
- display: Nano Banana 2
  id: gemini-3-pro-image
  provider: gemini
  max_context: 32768
  input_modalities: [text, image]
  output_modalities: [text, image]
```

(`max_context` per the operator's registry; the value above is the example.)

The uncommitted `personas.example/visual-designer.md` — which pins
`gemini-3-pro-image` — ships with this spec.

In the thread, an image persona's turn renders as interleaved text and
images inside the normal run bubble, with the standard persona dot and
model chip. Images render inline at a bounded width. A missing file (user
deleted the PNG) renders an "image unavailable" placeholder. Everything
else — `@` mentions, turn context overrides, Stop, the context footer —
works unchanged.

## Persona gating

`disableNonTextOutputPersonas` (appapi) relaxes and is renamed accordingly.
A persona is disabled at startup only when its pinned model's explicit
output modalities:

- contain **neither** `text` **nor** `image`, or
- contain `image` **without** `text` on a **non-`gemini`** provider (the
  gemini adapter is the only one that can render an image-only model
  useful).

Nano Banana 2 declares `[text, image]`, so it passes under both the old and
new gate; the relaxation exists for hypothetical image-only entries. Models
with no explicit modalities remain treated as text-capable, unchanged.

## Provider layer

`provider.Delta` gains one field:

```go
Image *ImageBlob // ImageBlob{MIME string, Data []byte}
```

`provider.Event` gains two fields: `ImageHash string` (authoritative
reference, persisted) and `ImageData []byte` (transient inflation, set by
the chat engine just before the call, never persisted).

The gemini adapter:

- Consults its registry entry at construction. When output modalities
  include `image`, it sets `ResponseModalities: ["TEXT", "IMAGE"]` on the
  generate request and omits `functionDeclarations` entirely.
- The streaming part switch gains an `InlineData` case: blob part →
  `Delta{Image: &ImageBlob{MIME, Data}}`. Text and image deltas are
  emitted in wire order, preserving interleaving.
- `geminiContentsFromEvents` gains an `assistant_image` case: when
  `ImageData` is non-empty, emit a `RoleModel` part with inline data; when
  empty (older than the cap, or file deleted), emit the placeholder text
  `[earlier image omitted]` as a `RoleModel` text part.

The anthropic and openai adapters skip `assistant_image` events
defensively (the engine never sends them inflated ones — images reach
non-gemini models only as baton text).

## Storage

**New package `internal/imagestore`** — one purpose: content-addressed PNG
files under `<app-dir>/images/`.

- `Put(data []byte) (hash string, err error)` — sha256-hex names the file;
  write is idempotent (an existing file with the same hash is a no-op), so
  identical re-generations dedupe for free.
- `Read(hash string) ([]byte, error)` — returns `fs.ErrNotExist` for a
  deleted/missing file.
- `Path(hash string) string`.
- Hash format is validated (`^[0-9a-f]{64}$`) on the way in and out.

`main.go` creates `images/` alongside the existing `library/`, `personas/`,
`textbooks/` dirs.

**Store migration.** SQLite cannot alter a `CHECK` constraint, so the
migration rebuilds `conversation_events`: create the new table with
`assistant_image` added to the kind check and a nullable `image_hash TEXT`
column, copy all rows, drop, rename — inside one transaction, following
SQLite's documented table-rebuild recipe. Existing data is untouched.

- `AppendAssistantImage(convID, runID, hash)` appends the new kind.
- `GetProviderReplayEvents` and `GetConversationDisplayEvents` both gain
  the kind; the display DTO gains `imageHash`.

## Refinement (chat engine)

In `canonicalEvents`, when assembling the image persona's own history:

1. Walk the persona's `assistant_image` events newest-first.
2. For the first 6, `imagestore.Read` the bytes into `Event.ImageData`.
   A read failure (deleted file) degrades that event to the placeholder
   text instead of failing the run.
3. Events 7+ get no bytes; the adapter renders them as
   `[earlier image omitted]`.

The cap is a package constant (`maxInlineImages = 6`), not operator
config, in v1.

## Serving and frontend rendering

**Asset handler.** `assetserver.Options.Handler` in `main.go` gains an
`http.Handler` for `/appimages/<hash>.png`: the hash segment must match
`^[0-9a-f]{64}$` exactly (rejecting traversal by construction), then the
file is served from the imagestore with `Content-Type: image/png`; anything
else is 404. All other unmatched paths keep the current behavior.

**Live path.** New `chat.SinkEventKind` for images → `chat:image` in the
appapi sink map, payload `{runId, hash}`. On receipt of `Delta.Image`, the
engine `Put`s the bytes, appends `assistant_image`, and emits the sink
event — so the store, not the frontend, is the source of truth.

**Frontend (`main.ts`).**

- `appendRunImage(runId, hash)` — parallel to `appendRunText` — appends an
  `<img src="/appimages/<hash>.png" alt="Generated image">` segment to the
  run bubble in arrival order, styled to a bounded width consistent with
  bubble layout.
- `EventsOn('chat:image', …)` wires the live path.
- The replay switch gains an `assistant_image` branch reading
  `ev.imageHash`.
- `onerror` on the `<img>` swaps in an "image unavailable" placeholder
  element — covering the deleted-file case identically for live and
  replay.

## Team fit

In `canonicalEvents`' `attributed()` relay, a predecessor persona's
`assistant_image` event textualizes inside the existing
`From <name> (<model>):` block as:

```
[image — generated from: "<triggering user prompt, truncated to ~120 chars>"]
```

The triggering prompt is the nearest preceding `user_message` in that run's
context. The predecessor's interleaved commentary text rides in the block
as it always has. Raw bytes never cross a persona boundary.

## Error handling

- Gemini finish reasons `SAFETY`, `IMAGE_SAFETY`, and `PROHIBITED_CONTENT`
  map to the existing `error` stop path with the reason carried in the
  message — no new `AppError` code in v1.
- Auth / rate-limit / network errors are already normalized by Spec A and
  apply unchanged.
- `imagestore.Put` failure (disk full, permissions) fails the run through
  the normal error path; the event is not appended, so the log never
  references a file that was not written.

## Testing

All pure Go against the Spec A `httptest` fake; no network.

- **gemini adapter:** image-mode request shape (responseModalities present,
  functionDeclarations absent); inline-data part → `Delta.Image`;
  interleaved text/image wire order preserved; contents building emits
  inline data for events with `ImageData` and placeholder text without.
- **chat engine:** last-6 inflation with 7+ images (oldest degrades);
  deleted-file degradation; baton textualization of an image event with
  prompt truncation.
- **imagestore:** put/read round trip; idempotent re-put; missing-hash
  read; hash validation.
- **store:** migration on a populated legacy DB preserves rows and accepts
  the new kind; `AppendAssistantImage`; both replay queries return the
  kind; display DTO carries `imageHash`.
- **asset handler:** serves a stored hash; 404 on unknown hash; rejects
  malformed/traversal paths.
- **appapi:** relaxed persona gate — `[image]`-only on gemini passes,
  `[image]`-only on openai is disabled, `[]`-neither is disabled.

**SMOKE.md additions (~6 steps):** pin Visual Designer and generate an
image (interleaved text+image renders in the bubble); refine ("make the sky
darker") and confirm the edit applies to the prior image; `@`-mention a
text persona after an image turn and confirm the textual baton block;
delete a PNG from `images/` and confirm the placeholder on replay; restart
the app and confirm images replay; footer shows sane token counts on an
image run.

**Verification gate:** `go test ./...`, `wails build`, smoke on both
Windows and macOS against the real API before merge.

## Out of scope

- **Operator image upload** (sketch/logo input for refinement) → BACKLOG
  Someday.
- **Viewer polish** — click-to-enlarge, save-as, copy-image → BACKLOG
  Someday.
- **Tool calling for image personas** — unsupported by the API alongside
  image output; revisit if the API gains it.
- **Non-PNG output formats / mime column** — additive migration when a
  model needs it.
- **Configurable refinement cap** — constant `6` in v1.
- **Image generation on non-gemini providers.**
