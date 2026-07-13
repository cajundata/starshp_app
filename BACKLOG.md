# Backlog

Capture format: append a line under **Inbox** as you think of things. Triage into **Next** or **Someday** when starting a new cycle. Move completed items' lines to the commit/PR they shipped in and delete from here.

Tags: `[feat]` new feature · `[chg]` change to existing behavior · `[fix]` known bug · `[chore]` maintenance · `[ui]` visual/UX

---

## Inbox

<!-- raw capture, untriaged. one line each. -->

## Next

<!-- triaged, picked for the next cycle -->

[feat] markdown prompt/context library, replacing the SQLite presets system: a multi-select list of saved prompts/context (toggle via checkbox/click, active items highlighted) feeding the current discussion's system prompt, plus a full-surface in-app raw-markdown editor. Design: docs/superpowers/specs/2026-05-21-starshp-prompt-library-design.md. Ready to implement — app rename complete.

## Someday

<!-- maybe-later, not committed to a cycle -->

[ui] add syntax highlighting to the library's raw-markdown editor
[feat] auto-detect a running Ollama at its default port on startup and surface a "Local models detected" panel listing installed models, with a one-click option to register them in `models.yaml`
[feat] per-model "Test connection" button in a model-registry settings UI so a user can validate a local entry's `base_url` without sending a real chat turn
[feat] curated starter-model recommendations for local entries (e.g., suggested Ollama IDs by Apple Silicon RAM tier and by Windows GPU VRAM tier) shown inline when a user adds a new local model

- **Multi-persona threads (Spec 2).** `@Persona` routing within one conversation,
  with baton-pass context: a persona receives the operator's messages plus the
  immediately preceding persona's output, not the full shared thread. Requires
  deciding how a mid-thread persona switch interacts with the `active_for_replay`
  run model. Spec 1 (one persona per conversation) shipped first deliberately, so
  personas could be lived with before this design risk is taken.
