# Gemini Smoke-67 Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the two 400s surfaced by SMOKE 67 (Gemini `thought_signature`, OpenAI `reasoning_effort` + tools) and add the model-modalities registry schema the operator requested.

**Architecture:** Three independent tasks. (1) Gemini 3 thought signatures are captured from the stream, persisted in the existing `tool_metadata` column on `assistant_tool_call` rows (no schema migration), and re-attached on replay. (2) `models.yaml` gains optional `reasoning_effort`, threaded `registry → appapi → chat.SendParams → ChatRequest → openai adapter`. (3) `models.yaml` gains optional `input_modalities`/`output_modalities` (default `[text]`), validated to a closed set, with a startup gate disabling personas pinned to models that cannot output text.

**Tech Stack:** Go 1.25; `google.golang.org/genai` v1.63.0; `github.com/openai/openai-go/v3`.

**Root-cause references:** Gemini 3 hard-400s when a functionCall part of the current turn is replayed without its `thoughtSignature` (our adapter dropped it at stream, store, and replay). GPT-5.6 Sol's default reasoning effort is incompatible with function tools on `/v1/chat/completions`; OpenAI's fix is `reasoning_effort: "none"` (or the Responses API, out of scope).

## Global Constraints

- Pure Go only — no CGO. All tests offline (httptest fakes) — never real API calls.
- Never modify anything under `internal/rag/{embedding,chunker,ragindex}/`.
- `gofmt` all new/changed Go files (`internal/rag/{chunker,embedding,ragindex}` drift is permanent — never touch).
- Work directly on `master`. No frontend/wailsjs changes (no new bound methods; `ModelInfo` JSON changes flow through the existing `Models()` binding untouched).
- NO database schema changes — `conversation_events.tool_metadata` already exists (`internal/store/schema.go:34`) and is unused on `assistant_tool_call` rows.
- Verified SDK facts: `genai.Part.ThoughtSignature []byte` with `json:"thoughtSignature,omitempty"` (Go marshals `[]byte` as std-base64 — round-tripping a received signature through base64 text is exact by construction); `openai.ChatCompletionNewParams.ReasoningEffort shared.ReasoningEffort` with `json:"reasoning_effort,omitzero"` (a plain string type — `shared.ReasoningEffort("none")` is valid).
- Signature policy (from Google docs): echo the REAL signature on every replayed gemini functionCall part that has one; when an event has no stored signature (pre-fix rows, calls made by other model families), OMIT the field entirely — strict validation applies to the current turn only, and after this fix the current turn always has real signatures. Do NOT invent dummy values (the SDK's `[]byte` field cannot produce Google's documented literal bypass strings on the wire).
- `store.GetProviderReplayEvents` already SELECTs `tool_metadata` into `store.ConversationEvent.ToolMetadata` (`internal/store/replay.go:142,173-175`) — the read path needs NO store changes.

---

### Task 1: Gemini thought_signature — capture, persist, replay

**Files:**
- Modify: `internal/provider/provider.go` (two struct fields)
- Modify: `internal/provider/gemini.go` (capture + re-attach)
- Modify: `internal/store/events.go:95-117` (`AppendAssistantToolCall` gains a metadata param)
- Modify: `internal/chat/chat.go:289` (pass `tc.Metadata`), `canonicalEvents` (~line 470-540: copy `ToolMetadata` onto the provider Event for same-persona `assistant_tool_call` rows)
- Tests: `internal/provider/gemini_test.go`, `internal/store/events_test.go` (or the store test file that covers appends), plus compile-fixes for any test calling `AppendAssistantToolCall`

**Interfaces:**
- Produces: `provider.ToolCall.Metadata json.RawMessage` (adapter-emitted, opaque to chat/store; gemini writes `{"thought_signature":"<std-base64>"}`); `provider.Event.ToolMetadata json.RawMessage` (assistant_tool_call rows; replayed verbatim from the store); `store.AppendAssistantToolCall(convID, turnID, runID, toolCallID, toolName string, input, metadata json.RawMessage)` — metadata nullable, written to `tool_metadata`.
- Key invariant: base64(receive) → store text → base64-decode(replay) is byte-exact, so the wire signature we echo is identical to the one Gemini sent.

- [ ] **Step 1: Write the failing adapter tests**

In `internal/provider/gemini_test.go`:

(a) Extend `TestGeminiStreamToolCall`'s fake frame so the FIRST functionCall part carries `"thoughtSignature":"c2lnLWJ5dGVzLTE="` (base64 of `sig-bytes-1`) and the second carries none. Assert `calls[0].Metadata` equals `{"thought_signature":"c2lnLWJ5dGVzLTE="}` (compare via unmarshal, not raw bytes) and `calls[1].Metadata == nil`.

(b) New test `TestGeminiContentsThoughtSignatureRoundTrip`: build Events containing an `assistant_tool_call` with `ToolMetadata: json.RawMessage(`{"thought_signature":"c2lnLWJ5dGVzLTE="}`)` plus its `tool_result`, run a Stream against the fake capturing the request body, and assert the posted body contains `"thoughtSignature":"c2lnLWJ5dGVzLTE="`.

(c) New test `TestGeminiContentsNoSignatureOmitsField`: same but `ToolMetadata` nil — assert the body does NOT contain `"thoughtSignature"`.

- [ ] **Step 2: Run to verify they fail**

`go test ./internal/provider/ -run TestGemini -v` — (a) fails to compile (`Metadata` undefined) or asserts nil; that failure is the RED.

- [ ] **Step 3: Implement the provider side**

`provider.go`: add `Metadata json.RawMessage` to `ToolCall` (comment: provider-specific opaque payload persisted to the event log and replayed to the same provider; gemini stores its thought signature here) and `ToolMetadata json.RawMessage` to `Event` (comment: assistant_tool_call rows: replayed tool metadata).

`gemini.go` Stream, functionCall branch: after computing `input`/`id`, when `len(fc.ThoughtSignature) > 0` marshal `map[string]string{"thought_signature": base64.StdEncoding.EncodeToString(fc.ThoughtSignature)}` into the delta's `ToolCall.Metadata`. (`part.FunctionCall` does not carry the signature — it sits on the enclosing `genai.Part.ThoughtSignature`; capture from the Part.)

`gemini.go` `geminiContentsFromEvents`, assistant_tool_call branch: parse `e.ToolMetadata` for `thought_signature`, base64-decode, and set the created `genai.Part.ThoughtSignature`. Absent/unparseable → leave the field unset (omit policy, per Global Constraints).

- [ ] **Step 4: Wire persistence**

`store/events.go`: `AppendAssistantToolCall` gains trailing `metadata json.RawMessage`; write it (nullable: empty → NULL) into the INSERT's `tool_metadata` column and set `ev.ToolMetadata`. Update every caller (grep `AppendAssistantToolCall` — chat.go:289 passes `tc.Metadata`; tests pass `nil`).

`chat.go` `canonicalEvents`: in the same-persona replay path, copy the store event's `ToolMetadata` onto the emitted `provider.Event` for `assistant_tool_call` kind. (Foreign-persona turns already omit tool blocks entirely — no change there.)

Add a store test: append an assistant_tool_call with metadata, read back via `GetProviderReplayEvents`, assert `ToolMetadata` round-trips; and a chat-level check only if an existing canonicalEvents test structure makes it cheap (assert ToolMetadata survives into the provider events for the current turn).

- [ ] **Step 5: Verify** — `go test ./internal/provider/ ./internal/store/ ./internal/chat/ -count=1` all PASS; then full `go test ./...`; `gofmt -l internal/` (only permanent rag drift listed).

- [ ] **Step 6: Commit** — `fix(provider): persist gemini thought signatures through the event log`

---

### Task 2: Per-model reasoning_effort

**Files:**
- Modify: `internal/provider/registry.go` (field + validation), `internal/provider/provider.go` (`ChatRequest.ReasoningEffort`), `internal/provider/openai.go` (apply), `internal/chat/chat.go` (`SendParams.ReasoningEffort` → both `ChatRequest{}` build sites, lines ~233 and ~348), `internal/appapi/api.go` (set from registry at the SendParams build)
- Tests: `internal/provider/registry_test.go`, `internal/provider/openai_test.go`
- Docs: `models.example.yaml` comment block, README `models.yaml` section one-liner

**Interfaces:**
- Produces: `ModelInfo.ReasoningEffort string` (`yaml:"reasoning_effort,omitempty" json:"reasoningEffort,omitempty"`); `LoadRegistry` rejects `reasoning_effort` on providers other than `openai`/`openai_compat` (error style matching the existing base_url messages); `ChatRequest.ReasoningEffort string`; openai adapter sets `params.ReasoningEffort = shared.ReasoningEffort(req.ReasoningEffort)` only when non-empty.

- [ ] **Step 1: Failing tests** — registry: accepts `reasoning_effort: none` on an openai entry; rejects it on a gemini entry and an anthropic entry. openai adapter: with `ReasoningEffort: "none"` the posted body contains `"reasoning_effort":"none"` (capture body in the existing httptest fake pattern); without it the body does NOT contain `reasoning_effort`.
- [ ] **Step 2: RED** — `go test ./internal/provider/ -run 'TestLoadRegistry|TestOpenAI' -v`
- [ ] **Step 3: Implement** the four code changes above; thread `ReasoningEffort` from `appapi` (`reg.ByID(p.Model)` result, at the point SendParams is built in api.go SendMessage) through `chat.SendParams` into both `provider.ChatRequest{}` literals.
- [ ] **Step 4: Docs** — models.example.yaml comment: `reasoning_effort — (optional, openai/openai_compat only) forwarded verbatim; set "none" for models that reject function tools with reasoning on /v1/chat/completions (e.g. GPT-5.6 Sol)`. README models.yaml section: one sentence.
- [ ] **Step 5: Verify** — full `go test ./...`; gofmt check.
- [ ] **Step 6: Commit** — `feat(provider): per-model reasoning_effort forwarded to openai chat completions`

---

### Task 3: Model modalities schema + text-output persona gate

**Files:**
- Modify: `internal/provider/registry.go` (fields, normalization, validation)
- Modify: `internal/appapi/api.go` (post-load persona gate, near the `persona.LoadRegistry` call at line ~74)
- Tests: `internal/provider/registry_test.go`, `internal/appapi/persona_test.go` (or api_test.go, matching where persona-registry tests live)
- Docs: `models.example.yaml` comment block, README `models.yaml` section

**Interfaces:**
- Produces: `ModelInfo.InputModalities []string` / `OutputModalities []string` (`yaml:"input_modalities,omitempty" json:"inputModalities,omitempty"` etc.); `LoadRegistry` normalizes absent/empty to `["text"]` and rejects values outside `{text, image}`; personas pinned to a model whose OutputModalities lacks `"text"` are disabled at startup with Issue reason naming the persona file, the model, and why (text chat requires text output) — they drop out of `Personas` and appear in the startup banner like other persona issues.

- [ ] **Step 1: Failing tests** — registry: absent modalities normalize to `["text"]`/`["text"]`; explicit `input_modalities: [text, image]` + `output_modalities: [image]` load verbatim; `output_modalities: [audio]` rejected. appapi: a persona pinned to a model with `OutputModalities: ["image"]` lands in Issues (reason mentions the model and "text"), not in Personas; a persona on a default-modality model is unaffected.
- [ ] **Step 2: RED**, **Step 3: Implement** (normalization inside LoadRegistry after unmarshal, before per-entry validation; gate in appapi immediately after `persona.LoadRegistry`, filtering `a.personas.Personas` and appending to `a.personas.Issues`).
- [ ] **Step 4: Docs** — models.example.yaml comment documenting both fields, allowed values `text|image`, default `[text]`; annotate ONE example entry with explicit modalities; README sentence noting the default and that a persona pinned to a model without text output is disabled until image support ships (Spec B).
- [ ] **Step 5: Verify** — full `go test ./...`; gofmt check.
- [ ] **Step 6: Commit** — `feat(registry): input/output modalities with text-output persona gate`

---

### Task 4: Verification gate

- [ ] `go test ./... -count=1` all green; `wails build` succeeds; `chmod 644` any mode-flipped `frontend/wailsjs` files; confirm zero binding content diff.
- [ ] Operator: set `reasoning_effort: none` on the GPT-5.6 Sol entry in `<app-dir>/models.yaml`, relaunch, re-run SMOKE 67 with the Gemini persona AND the GPT persona; on pass, check the box and push.
