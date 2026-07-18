# Backlog

Capture format: append a line under **Inbox** as you think of things. Triage into **Next** or **Someday** when starting a new cycle. Move completed items' lines to the commit/PR they shipped in and delete from here.

Tags: `[feat]` new feature · `[chg]` change to existing behavior · `[fix]` known bug · `[chore]` maintenance · `[ui]` visual/UX

---

## Inbox

<!-- raw capture, untriaged. one line each. -->

## Next

<!-- triaged, picked for the next cycle -->

## Someday

<!-- maybe-later, not committed to a cycle -->

[feat] control model reasoning level, temperature, and other fine tune controls
[ui] add syntax highlighting to the library's raw-markdown editor
[feat] auto-detect a running Ollama at its default port on startup and surface a "Local models detected" panel listing installed models, with a one-click option to register them in `models.yaml`
[feat] per-model "Test connection" button in a model-registry settings UI so a user can validate a local entry's `base_url` without sending a real chat turn
[feat] curated starter-model recommendations for local entries (e.g., suggested Ollama IDs by Apple Silicon RAM tier and by Windows GPU VRAM tier) shown inline when a user adds a new local model

[chore] guard the error-path channel sends in all three streaming adapters with select/ctx.Done() — gemini, anthropic, and openai share the unguarded terminal-send pattern; benign with current draining callers (from gemini final review)
[chore] gemini test: assert functionDeclarations reach the wire in TestGeminiStreamToolCall's posted body (from gemini final review)
[chore] chat-level regression test: assert SendParams.ReasoningEffort reaches the ChatRequest via fakeprovider, covering the finalizeWithoutTools literal and appapi wiring (from smoke-fix final review)
[chore] validate (or warn on) unknown reasoning_effort values in LoadRegistry — today a typo like "nonee" only surfaces as a runtime provider 400 (from smoke-fix final review)

- Mention polish (from multi-persona final review): Ctrl/Cmd+Enter with the @-popup open should send, not insert; click-away should dismiss the popup; auto-title could strip a leading @mention.
- Mention test hardening: parser rows for `@scout!` / `@scout,`; a dedicated first-turn/no-predecessor canonicalEvents test; `found` flag in TestSendMessageWithMentionDoesNotRepin.

- Operator image upload (attach a sketch/logo for the Visual Designer to refine) — Spec B deferred.
- Image viewer polish: click-to-enlarge, save-as, copy image, bottom-pin rescroll on image load — Spec B deferred.
- Configurable refinement image cap (constant 6 in v1) — Spec B deferred.
- Persona re-pin edge: a persona moved off gemini loses its historical images from its own context (adapter skip); a gemini model without image input could reject inflated bytes — Spec B deferred.
- Multimodal baton: pass real image bytes to text personas whose models declare image input (smoke 74 — critique personas currently see only the textual image note) — Spec B deferred.
