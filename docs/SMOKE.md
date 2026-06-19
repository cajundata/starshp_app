# Manual Smoke Checklist

Prereq: `.env` with OPENAI_API_KEY (+ ANTHROPIC_API_KEY to use Claude models),
`textbooks.yaml` pointing at a markdown textbook dir, `models.yaml` present.

Run: `wails dev`

## Core

1. [x] App launches; if keys/configs missing, a setup notice lists the issues.
2. [x] "+ New chat" creates a conversation; it appears in the sidebar.
3. [x] Type a message, Send → assistant reply streams token-by-token.
4. [x] Switch the model dropdown mid-conversation; next reply uses the new model.
5. [x] Click 📚 Textbooks, attach a book, Save → "Indexing… N/total" then "ready".
6. [x] Ask a question answerable from the textbook → reply is grounded.
7. [x] Click Stop during streaming → stream is cancelled; the partial reply is persisted.
8. [x] Close and relaunch → conversation history is intact; reopening restores messages.
9. [x] Delete a conversation → it and its messages disappear (no orphan rows).

## Library

10. [x] Click ≡ Library → the panel opens and lists existing items (empty on first run).
11. [x] "+ New item" → the editor opens; saving content with no H1 is rejected with a clear message.
12. [x] Add an H1 (`# My Item`) and a body, Save → the item appears in the panel list.
13. [x] Edit an item and change its H1 → the display name updates; the `.md` file on disk keeps its original name.
14. [x] Toggle items active with the checkboxes; the checked state reflects the selection.
15. [x] Send a message → the active items' bodies reach the model in the system prompt, with the H1 stripped.
16. [x] Switch conversations and relaunch the app → each conversation restores its own active set (sticky).
17. [x] Delete an active item's `.md` file on disk → it drops from the panel and is skipped on send (a soft notice appears), no crash.

## Textbooks

18. [x] Rename `<app-dir>/textbooks/<book>/` so the manifest path is broken → 📚 Textbooks still opens; that book renders as `(unavailable: …)` with its checkbox disabled; other books remain selectable.
19. [x] Restore the folder → reopen the picker; the book is selectable again with its chapter count.

## Context tracking footer

20. [x] **Footer renders after first reply on a fresh conversation.** Create a new conversation, send a short message, wait for the reply. The strip below the thread shows `ctx N / M · cache K` (with denominator if the active model has `max_context` set in `models.yaml`).
21. [x] **Footer updates across model switches mid-conversation.** Switch the model picker mid-conversation, send a follow-up. The denominator shifts to the new model's `max_context`; values keep growing turn over turn.
22. [x] **`~` marker after Stop.** Start a long reply, click Stop. The footer keeps the previous turn's values, prefixed with `~`.
23. [x] **No denominator when `max_context` is omitted.** Remove `max_context` from one model in `models.yaml`, restart, send a message with that model. Footer shows `ctx N · cache K` (no `/ M` segment).
24. [x] **Footer survives conversation switches.** Open a conversation with prior history; the footer seeds from the last assistant message's recorded tokens. Switch to another conversation, then back.

## Tool calling

For each step, observe the assistant bubble in addition to the listed expectation.

25. [x] **Pure-text turn (no tools).** With no textbooks attached, ask "what is the realization principle?". The bubble shows streamed text only — no tool blocks. The context footer shows usage.
26. [x] **`search_textbook` escalation.** Attach a textbook, then ask a question needing a chapter the pre-turn grounding did not cover. The bubble shows one or more collapsed `🔍 search_textbook` inline blocks, each annotated with its source count and latency once the result lands. Clicking a block expands the summary.
27. [x] **`safe_math` invocation.** Ask "tax on $5,000 at 8.25% — verify with a calculator." The bubble shows a `🧮 safe_math` block followed by the final answer.
28. [x] **Errored tool result.** Detach all textbooks, then ask the model to search the textbooks for something. The `search_textbook` block renders in red with `error · no_textbooks_attached`, and the model answers from background knowledge.
29. [x] **Stop mid-loop.** Click Stop after the model emits a tool call but before the final answer. The bubble keeps any partial text and completed tool blocks and gains a `cancelled` tag. Reopen the conversation — the partial output is still visible (display events, not provider replay).
30. [x] **Conversation reopen replays display events.** Open a prior conversation containing a completed run with tool calls. The bubble rebuilds with text + collapsed tool blocks; clicking a block expands its summary.
31. [x] **Grounding header.** Ask any question with textbooks attached. A dim `↳ grounded · N sources` line appears above the bubble after `chat:grounding_ready`.
32. [x] **`STARSHP_SKIP_AUTO_GROUNDING`.** Set the env var to `1` and relaunch. Ask a question with textbooks attached. No grounding header appears (the run reports `not_required`); the model must call `search_textbook` itself if it wants context.
33. [x] **Max-iterations cap (forces a final answer, not an error).** Set `STARSHP_MAX_TOOL_ITERATIONS=2`, attach a textbook, ask a complex multi-hop question. After two tool-use cycles the loop withholds tools and the model synthesizes a final answer from the gathered results — the run completes (not an error bubble) with `terminal_reason=max_iterations` (visible in the structured logs).

## Assignment solver

34. [x] **Solve a folder.** Open the Assignments view, choose a companion `_json`
        directory and start. A progress bar advances `done/total`; items flip from
        solving → answered/no_answer/errored as the batch runs.
        NOTE: point the picker at the `_json` dir itself (contains manifest.json),
        not its parent. BUG FIXED: `#tbModal`/`#libModal` lacked a z-index, so the
        textbook/library pickers opened _behind_ the `z-index:10` Assignments view
        and the solve flow appeared dead (style.css: added `z-index: 20`).
35. [x] **Review an item.** Click an answered item → its run opens with the
        worked reasoning, tool calls (safe_math / search_textbook), and the
        submit_answer payload (MC choice or worksheet cell map).
        BUG FIXED: the detail pane never showed tool-call _input_ (only results)
        because `bytesToText()` returned '' for `toolInput`, which crosses the wails
        bridge as a parsed JSON object (json.RawMessage), not a string/byte array.
        Replaced with `toolInputText()` + `argPreview(ev.toolInput)` (main.ts).
36. [ ] **Confidence & flags.** Low-confidence and flagged items are
        highlighted; a worksheet with uncaptured dropdown options shows an
        `uncaptured_dropdown_options` flag; a question missing data shows
        `missing_information`.
37. [x] **Answer files written.** A sibling `_answers/NNN.json` exists for each
        answered question, mirroring the input file names, with the answer payload
        and runId. NOTE: `submit_answer` tool result is the constant
        `{"status":"answer_recorded"}` ack by design — the real answer is recovered
        from the tool-call input args (GetSubmittedAnswer), not the result.
38. [ ] **Stop mid-batch.** Start a large folder, click Stop. In-flight items
        finish or cancel; pending items become `cancelled`; answered items persist.
        BUG FIXED: in-flight solves cut off by Stop were recorded as `no_answer`
        instead of `cancelled`. solveItem's empty-answer branch (orchestrator.go)
        only special-cased an `errored` run; a `cancelled` run (chat marks it
        `user_cancelled`, Send returns nil) fell through to `no_answer`. Added a
        `cancelled` case keyed on `ctx.Err()`/`run.Status=="cancelled"`. Regression
        test: TestSolveItem_CancelledMidSolveMarksItemCancelled.
        NOTE: items never scheduled before Stop have no rows yet (rows are created
        lazily at schedule time), so they don't render as `cancelled` — they're
        simply absent; the batch-level pill shows `cancelled`.
39. [x] **Resume.** Re-run the same folder. Already-answered items are skipped
        (no new runs); only pending/errored/no_answer/cancelled items re-solve.
        NOTE: resume = re-trigger the same Solve action with the same dir; there is
        no separate "Resume" control. Skip is keyed on item status == "answered"
        (orchestrator.go:144). Verified on qz05: items 1–5 (answered) skipped, 6–20
        re-solved. An item that hits `max_iterations` and answers in prose without
        calling submit_answer is correctly `no_answer` (no answer was submitted).
40. [ ] **Sidebar isolation.** Item conversations do not appear in the normal
        conversation sidebar; they are reachable only via the assignment view.
41. [ ] **Concurrency env.** Set `STARSHP_ASSIGNMENT_CONCURRENCY=2`, re-run, and
        confirm no SQLITE_BUSY errors in logs (busy_timeout + WAL cover contention).

## Local models (Ollama)

42. [x] **Local model end-to-end.** With Ollama installed and `ollama pull
llama3.2` complete, register the Llama 3.2 entry from
        `models.example.yaml` in your `models.yaml`, restart Starshp, pick
        "Llama 3.2 (local)" in a new conversation, send a short prompt.
        Confirm streaming, the Stop button, the context-footer HUD
        (input/output tokens populate, cached shows 0), and that stopping
        Ollama mid-session yields the `local_unreachable` error with the
        base URL interpolated into the message.

## Business pipeline

43. [x] **Pipeline view opens.** The sidebar shows a "🎯 Pipeline" button;
        clicking it opens the full-screen Pipeline view. "← Chat" returns to
        the chat view. The view matches the app dark theme.
44. [x] **Create an idea.** "+ New idea" prompts for title/summary/pathway/
        financial flag; the new idea appears in the list at status `raw`.
        Selecting it opens the detail pane. A blank title is rejected with a
        clear message.
45. [x] **Status transitions.** The detail "Move to…" dropdown changes status.
        A legal move (e.g. `raw → triaged`) succeeds; an illegal move (e.g.
        `raw → go`) is rejected with the `invalid_transition` user message.
        Moving to `killed` or `parked` prompts for a reason and rejects an
        empty one (`reason_required`).
46. [x] **Kill criteria.** "+ Add kill criterion" adds a row (metric,
        threshold, review date, on-miss); the ✕ control deletes it. The row
        persists across a reopen of the detail pane.
47. [x] **Reviews Due launch sweep.** Add a kill criterion with a review date
        in the past (e.g. yesterday). Stop and restart `wails dev`. On launch
        the "🎯 Pipeline" button shows a red badge with count ≥ 1, and the
        Pipeline view shows the Reviews Due panel listing that criterion with
        "Nd overdue".
48. [x] **Future-dated criterion excluded.** Add a second criterion dated in
        the future. Restart. The badge count does not include it.
49. [x] **Killed ideas excluded from the sweep.** Move an idea with a
        past-due pending criterion to `killed`. Restart. The badge no longer
        counts that criterion (killed ideas drop out of the reviews-due sweep).
50. [x] **Delete an idea.** The detail pane's delete control removes the idea
        (and cascades its status history and kill criteria); it disappears
        from the list with no orphan rows or crash.
