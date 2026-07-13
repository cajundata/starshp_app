# Manual Smoke Checklist

Prereq: `.env` with OPENAI_API_KEY (+ ANTHROPIC_API_KEY to use Claude models),
`textbooks.yaml` pointing at a markdown textbook dir, `models.yaml` present.

Run: `wails dev`

## Core

1. [x] App launches; if keys/configs missing, a setup notice lists the issues.
2. [x] "+ New chat" creates a conversation; it appears in the sidebar.
3. [x] Type a message, Send → assistant reply streams token-by-token.
4. [x] Click 📚 Textbooks, attach a book, Save → "Indexing… N/total" then "ready".
5. [x] Ask a question answerable from the textbook → reply is grounded.
6. [x] Click Stop during streaming → stream is cancelled; the partial reply is persisted.
7. [x] Close and relaunch → conversation history is intact; reopening restores messages.
8. [x] Delete a conversation → it and its messages disappear (no orphan rows).

## Library

9. [x] Click ≡ Library → the panel opens and lists existing items (empty on first run).
10. [x] "+ New item" → the editor opens; saving content with no H1 is rejected with a clear message.
11. [x] Add an H1 (`# My Item`) and a body, Save → the item appears in the panel list.
12. [x] Edit an item and change its H1 → the display name updates; the `.md` file on disk keeps its original name.
13. [x] Toggle items active with the checkboxes; the checked state reflects the selection.
14. [x] Send a message → the active items' bodies reach the model in the system prompt, with the H1 stripped.
15. [x] Switch conversations and relaunch the app → each conversation restores its own active set (sticky).
16. [x] Delete an active item's `.md` file on disk → it drops from the panel and is skipped on send (a soft notice appears), no crash.

## Textbooks

17. [x] Rename `<app-dir>/textbooks/<book>/` so the manifest path is broken → 📚 Textbooks still opens; that book renders as `(unavailable: …)` with its checkbox disabled; other books remain selectable.
18. [x] Restore the folder → reopen the picker; the book is selectable again with its chapter count.

## Context tracking footer

19. [x] **Footer renders after first reply on a fresh conversation.** Create a new conversation, send a short message, wait for the reply. The strip below the thread shows `context N / M · this turn I→O · cache K` (with denominator if the active model has `max_context` set in `models.yaml`).
20. [x] **Footer updates across persona switches mid-conversation.** Switch the persona dropdown mid-conversation to a persona pinned to a different model, send a follow-up. The denominator shifts to the new model's `max_context`; values keep growing turn over turn.
21. [x] **`~` marker after Stop.** Start a long reply, click Stop. The footer keeps the previous turn's values, prefixed with `~`.
22. [x] **No denominator when `max_context` is omitted.** Remove `max_context` from one model in `models.yaml`, restart, send a message with that model. Footer shows `context N · this turn I→O · cache K` (no `/ M` segment).
23. [x] **Footer survives conversation switches.** Open a conversation with prior history; the footer seeds from the last assistant message's recorded tokens. Switch to another conversation, then back.
24. [ ] **Occupancy diverges from this-turn on tool turns.** Attach a textbook, ask a question that triggers a search (multi-iteration). The `context` occupancy number is visibly smaller than the `this turn` input (which sums every iteration). On a no-tool turn the occupancy ≈ this-turn input+output.
25. [ ] **Footer shows the active persona name.** Send a message and check the strip's tail: `... · cache K · <PersonaName>` matches the persona currently selected in the picker. Switch personas and send again — the suffix updates to the new persona's name.

## Tool calling

For each step, observe the assistant bubble in addition to the listed expectation.

26. [x] **Pure-text turn (no tools).** With no textbooks attached, ask "what is the realization principle?". The bubble shows streamed text only — no tool blocks. The context footer shows usage.
27. [x] **`search_textbook` escalation.** Attach a textbook, then ask a question needing a chapter the pre-turn grounding did not cover. The bubble shows one or more collapsed `🔍 search_textbook` inline blocks, each annotated with its source count and latency once the result lands. Clicking a block expands the summary.
28. [x] **`safe_math` invocation.** Ask "tax on $5,000 at 8.25% — verify with a calculator." The bubble shows a `🧮 safe_math` block followed by the final answer.
29. [x] **Errored tool result.** Detach all textbooks, then ask the model to search the textbooks for something. The `search_textbook` block renders in red with `error · no_textbooks_attached`, and the model answers from background knowledge.
30. [x] **Stop mid-loop.** Click Stop after the model emits a tool call but before the final answer. The bubble keeps any partial text and completed tool blocks and gains a `cancelled` tag. Reopen the conversation — the partial output is still visible (display events, not provider replay).
31. [x] **Conversation reopen replays display events.** Open a prior conversation containing a completed run with tool calls. The bubble rebuilds with text + collapsed tool blocks; clicking a block expands its summary.
32. [x] **Grounding header.** Ask any question with textbooks attached. A dim `↳ grounded · N sources` line appears above the bubble after `chat:grounding_ready`.
33. [x] **`STARSHP_SKIP_AUTO_GROUNDING`.** Set the env var to `1` and relaunch. Ask a question with textbooks attached. No grounding header appears (the run reports `not_required`); the model must call `search_textbook` itself if it wants context.
34. [x] **Max-iterations cap (forces a final answer, not an error).** Set `STARSHP_MAX_TOOL_ITERATIONS=2`, attach a textbook, ask a complex multi-hop question. After two tool-use cycles the loop withholds tools and the model synthesizes a final answer from the gathered results — the run completes (not an error bubble) with `terminal_reason=max_iterations` (visible in the structured logs).

## Personas

35. [ ] **Picker.** The composer's dropdown lists every persona by name. A persona
        file with a typo (unknown model, unknown tool, bad color) is *absent* from
        the list, and its rejection appears in the startup banner naming the file
        and the reason.
36. [ ] **Attribution.** Send a message. The bubble carries a colored dot, the
        persona name in that color, a muted model chip, and a colored left stripe
        — all present before the first token arrives.
37. [ ] **Two personas.** Send as persona A in one conversation and persona B in
        another. Distinct colors, correct names, correct model chips.
38. [ ] **Persona switch + replay parity (the important one).** In one
        conversation, send turn 1 as persona A, then switch the dropdown and send
        turn 2 as persona B — turn 2's bubble picks up the new color/name/model
        chip while turn 1's bubble keeps its original attribution. Close the
        conversation and reopen it: each bubble must come back with **its own**
        run's persona, not the conversation's `pinned_persona` (whichever persona
        sent last). This is the regression the replay `LEFT JOIN` on
        `runs.persona_id` exists to prevent — a version that colors every bubble
        from the pinned persona instead of each run's own attribution can slip
        past a single-persona conversation, so this step requires at least two
        distinct assistants across the two turns. Live/replay divergence here is
        the failure this design exists to prevent — if it happens, stop and fix it.
39. [ ] **`tools:` subsetting.** Attach a textbook, then send as an assistant
        scoped to `tools: [safe_math]` (e.g. Skeptic) and ask something that would
        need a textbook search. It cannot call `search_textbook` — no such tool
        block appears, and the model either says it cannot search or answers from
        background knowledge. Switch to an assistant with no `tools:` restriction
        and ask the same question — `search_textbook` is available again.
40. [ ] **`library:` frontmatter auto-attachment.** Add a library item with a
        distinctive fact (e.g. a made-up rule), leave it **unchecked** in the
        conversation's active-items panel, then give a persona
        `library: [that-item]` in its frontmatter and send a message as that
        persona asking about the fact. The model answers correctly — the persona
        pulled the item in on its own; the panel checkbox was never involved. Now
        also check that same item active in the panel and send again: no
        duplicate-content error, no crash (the persona's claim and the
        conversation's active set dedup to one copy).
41. [ ] **Deleted persona.** Delete a persona's markdown file, relaunch, open a
        conversation it spoke in. Its bubbles render neutral gray with the literal
        persona ID as the name. No error, no blank thread.
42. [ ] **Recolor.** Change a persona's `color:` in its file and relaunch. That
        persona's *history* recolors, not just new messages.
43. [ ] **Legacy run.** Open a conversation from before personas existed. Its
        bubbles are neutral gray and carry only a model chip — no persona name.
44. [ ] **Errored run on reopen.** Temporarily corrupt the API key for the active
        persona's provider in `.env` (change a character — don't blank it, a
        blank key is rejected before any run starts) and relaunch. Send as that
        persona: the run starts (bubble appears, attributed to the persona), then
        errors when the provider rejects the bad key. Close and reopen the
        conversation — the synthetic `run_error` bubble reappears with the same
        persona attribution (color, name, chip) the failed run had live. This is
        the error-path counterpart to the tool-calling section's cancel-and-reopen
        check, now exercising `PersonaID`/`Model` carried on `run_error` events.
        Restore the correct key afterward.
45. [ ] **Unknown persona.** Introduce a typo in the `model:` of every persona
        file (or move the valid ones out of the folder) so none load, relaunch,
        and send. The send fails with a config error listing each file and its
        validation failure — it does not silently fall back to another assistant.

## Local models (Ollama)

46. [x] **Local model end-to-end.** With Ollama installed and `ollama pull
llama3.2` complete, register the Llama 3.2 entry from
        `models.example.yaml` in your `models.yaml`, restart Starshp, pick
        "Llama 3.2 (local)" in a new conversation, send a short prompt.
        Confirm streaming, the Stop button, the context-footer HUD
        (input/output tokens populate, cached shows 0), and that stopping
        Ollama mid-session yields the `local_unreachable` error with the
        base URL interpolated into the message.

## Business pipeline

47. [x] **Pipeline view opens.** The sidebar shows a "🎯 Pipeline" button;
        clicking it opens the full-screen Pipeline view. "← Chat" returns to
        the chat view. The view matches the app dark theme.
48. [x] **Create an idea.** "+ New idea" prompts for title/summary/pathway/
        financial flag; the new idea appears in the list at status `raw`.
        Selecting it opens the detail pane. A blank title is rejected with a
        clear message.
49. [x] **Status transitions.** The detail "Move to…" dropdown changes status.
        A legal move (e.g. `raw → triaged`) succeeds; an illegal move (e.g.
        `raw → go`) is rejected with the `invalid_transition` user message.
        Moving to `killed` or `parked` prompts for a reason and rejects an
        empty one (`reason_required`).
50. [x] **Kill criteria.** "+ Add kill criterion" adds a row (metric,
        threshold, review date, on-miss); the ✕ control deletes it. The row
        persists across a reopen of the detail pane.
51. [x] **Reviews Due launch sweep.** Add a kill criterion with a review date
        in the past (e.g. yesterday). Stop and restart `wails dev`. On launch
        the "🎯 Pipeline" button shows a red badge with count ≥ 1, and the
        Pipeline view shows the Reviews Due panel listing that criterion with
        "Nd overdue".
52. [x] **Future-dated criterion excluded.** Add a second criterion dated in
        the future. Restart. The badge count does not include it.
53. [x] **Killed ideas excluded from the sweep.** Move an idea with a
        past-due pending criterion to `killed`. Restart. The badge no longer
        counts that criterion (killed ideas drop out of the reviews-due sweep).
54. [x] **Delete an idea.** The detail pane's delete control removes the idea
        (and cascades its status history and kill criteria); it disappears
        from the list with no orphan rows or crash.
