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

[ui] add syntax highlighting to the library's raw-markdown editor
[feat] auto-detect a running Ollama at its default port on startup and surface a "Local models detected" panel listing installed models, with a one-click option to register them in `models.yaml`
[feat] per-model "Test connection" button in a model-registry settings UI so a user can validate a local entry's `base_url` without sending a real chat turn
[feat] curated starter-model recommendations for local entries (e.g., suggested Ollama IDs by Apple Silicon RAM tier and by Windows GPU VRAM tier) shown inline when a user adds a new local model

- Mention polish (from multi-persona final review): Ctrl/Cmd+Enter with the @-popup open should send, not insert; click-away should dismiss the popup; auto-title could strip a leading @mention.
- Mention test hardening: parser rows for `@scout!` / `@scout,`; a dedicated first-turn/no-predecessor canonicalEvents test; `found` flag in TestSendMessageWithMentionDoesNotRepin.
