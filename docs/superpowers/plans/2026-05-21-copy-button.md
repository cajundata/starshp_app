# Copy Button Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an icon-only copy button to each assistant reply, revealed on hover in an action row below the bubble, that copies the reply's plain text to the clipboard.

**Architecture:** Frontend-only change. The message bubble DOM is restructured from a single `.msg` element into `.msg` (positioning wrapper) → `.msg-text` (styled bubble) + `.msg-actions` (hover-revealed action row). Task 1 does the restructure as a behavior-neutral refactor; Task 2 adds the copy button on top of it.

**Tech Stack:** Vanilla TypeScript + Vite frontend (no framework, no component library), Wails v2. No automated frontend tests exist — the project verifies UI via a manual smoke checklist (`docs/SMOKE.md`). This plan therefore uses a TypeScript typecheck plus manual smoke verification in `wails dev` in place of automated tests.

**Spec:** `docs/superpowers/specs/2026-05-21-copy-button-design.md`

---

## File Structure

- `frontend/src/main.ts` — message rendering (`addMsg`), streaming token handler, RAG status handlers, send flow. Gains the `msgText` helper, the `attachCopyButton` helper, and the copy-icon SVG constants.
- `frontend/src/style.css` — message bubble styling. `.msg` becomes a flex-column wrapper; new `.msg-text`, `.msg-actions`, `.copy-btn` rules.

No other files change. No Go, store, RAG, or Wails-binding changes.

---

## Task 1: Restructure message DOM into `.msg` wrapper + `.msg-text` bubble

A behavior-neutral refactor. After this task the app looks and behaves exactly as before — no copy button yet. This isolates the risky streaming-path change so it can be verified on its own.

**Files:**
- Modify: `frontend/src/style.css:11-13`
- Modify: `frontend/src/main.ts` (`addMsg`, helper additions, `chat:token` handler, `rag:index` handler, `send`, `showTextbooks`)

- [ ] **Step 1: Restructure the message CSS**

In `frontend/src/style.css`, replace these three lines (11-13):

```css
.msg { max-width: 78%; padding: 10px 13px; border-radius: 12px; line-height: 1.45; white-space: pre-wrap; }
.msg.user { align-self: flex-end; background: #2f6df0; color: #fff; }
.msg.assistant { align-self: flex-start; background: #1d1d20; border: 1px solid #2b2b30; }
```

with:

```css
.msg { max-width: 78%; display: flex; flex-direction: column; }
.msg.user { align-self: flex-end; align-items: flex-end; }
.msg.assistant { align-self: flex-start; align-items: flex-start; }
.msg-text { padding: 10px 13px; border-radius: 12px; line-height: 1.45; white-space: pre-wrap; }
.msg.user .msg-text { background: #2f6df0; color: #fff; }
.msg.assistant .msg-text { background: #1d1d20; border: 1px solid #2b2b30; }
```

The bubble styling (padding, radius, background, border, `pre-wrap`) moves onto `.msg-text`. `.msg` is now a flex column; `align-items` keeps each bubble shrink-to-content and aligned to the correct edge.

- [ ] **Step 2: Rewrite `addMsg` and add the `msgText` helper**

In `frontend/src/main.ts`, replace the `addMsg` function (lines 17-24):

```ts
function addMsg(role: string, text: string): HTMLElement {
  const el = document.createElement('div')
  el.className = `msg ${role}`
  el.textContent = text
  thread.appendChild(el)
  thread.scrollTop = thread.scrollHeight
  return el
}
```

with:

```ts
function addMsg(role: string, text: string): HTMLElement {
  const el = document.createElement('div')
  el.className = `msg ${role}`
  const txt = document.createElement('div')
  txt.className = 'msg-text'
  txt.textContent = text
  el.appendChild(txt)
  thread.appendChild(el)
  thread.scrollTop = thread.scrollHeight
  return el
}

const msgText = (el: HTMLElement) => el.querySelector('.msg-text') as HTMLElement
```

`addMsg` still returns the outer `.msg` wrapper (so `.remove()` keeps working). `msgText` returns the inner `.msg-text` element for callers that read or write the bubble text.

- [ ] **Step 3: Update the `chat:token` streaming handler**

In `frontend/src/main.ts`, replace the `chat:token` handler (lines 110-113):

```ts
EventsOn('chat:token', (tok: string) => {
  const last = thread.querySelector('.msg.assistant:last-child')
  if (last) { last.textContent += tok; thread.scrollTop = thread.scrollHeight }
})
```

with:

```ts
EventsOn('chat:token', (tok: string) => {
  const last = thread.querySelector('.msg.assistant:last-child .msg-text')
  if (last) { last.textContent += tok; thread.scrollTop = thread.scrollHeight }
})
```

The selector now targets the `.msg-text` child so streamed tokens append into the text node, not the wrapper.

- [ ] **Step 4: Update the `rag:index` handler**

In `frontend/src/main.ts`, replace the `rag:index` handler (lines 115-117):

```ts
EventsOn('rag:index', (p: any) => {
  if (ragStatusEl) ragStatusEl.textContent = `Indexing ${p.book}… ${p.done}/${p.total} chapters`
})
```

with:

```ts
EventsOn('rag:index', (p: any) => {
  if (ragStatusEl) msgText(ragStatusEl).textContent = `Indexing ${p.book}… ${p.done}/${p.total} chapters`
})
```

- [ ] **Step 5: Update the text writes inside `send()`**

In `frontend/src/main.ts`, in the `send()` function, replace this catch block (lines 84-87):

```ts
  } catch (e: any) {
    idxStatus.textContent = `Cannot send: textbook indexing failed — ${e?.userMessage || e}`
    return
  } finally {
```

with:

```ts
  } catch (e: any) {
    msgText(idxStatus).textContent = `Cannot send: textbook indexing failed — ${e?.userMessage || e}`
    return
  } finally {
```

Then, in the same function, replace this catch block (lines 100-101):

```ts
  } catch (e: any) {
    asst.textContent += `\n\n[${e?.code || 'error'}] ${e?.userMessage || e}`
  } finally {
```

with:

```ts
  } catch (e: any) {
    msgText(asst).textContent += `\n\n[${e?.code || 'error'}] ${e?.userMessage || e}`
  } finally {
```

- [ ] **Step 6: Update the text writes inside `showTextbooks()`**

In `frontend/src/main.ts`, in the `showTextbooks()` function, replace these two lines (139-140):

```ts
    try { await App.EnsureIndexed(activeConv!); banner.textContent = 'Textbooks ready.' }
    catch (e: any) { banner.textContent = `Indexing failed: ${e?.userMessage || e}` }
```

with:

```ts
    try { await App.EnsureIndexed(activeConv!); msgText(banner).textContent = 'Textbooks ready.' }
    catch (e: any) { msgText(banner).textContent = `Indexing failed: ${e?.userMessage || e}` }
```

- [ ] **Step 7: Typecheck**

Run from the `frontend` directory:

```bash
npx tsc --noEmit
```

Expected: no output, exit code 0. If `tsc` reports errors, fix them before continuing — likely a missed `.textContent` call that still targets a `.msg` wrapper instead of `msgText(...)`.

- [ ] **Step 8: Manual verification in `wails dev`**

Run `wails dev` from the project root. Verify the refactor changed nothing visible:

1. App launches; existing conversations render with bubbles looking identical to before (same padding, colors, rounded corners, left/right alignment).
2. Send a message → the reply streams token-by-token into the bubble correctly (no tokens lost, no layout jump).
3. Attach a textbook via the 📚 button → the "Indexing… N/total" status text updates in its bubble, then "Textbooks ready.".
4. Trigger an error (e.g. pick a model with no API key configured) → the error text appends to the assistant bubble.
5. Reopen a conversation from the sidebar → historical messages render correctly.

If anything looks different from before this task, fix it before committing — this task must be visually and behaviorally neutral.

- [ ] **Step 9: Commit**

```bash
git add frontend/src/main.ts frontend/src/style.css
git commit -m "refactor: split .msg into .msg wrapper + .msg-text bubble"
```

---

## Task 2: Add the copy button

Builds the hover-revealed action row and copy button on top of the Task 1 structure.

**Files:**
- Modify: `frontend/src/main.ts` (icon constants, `attachCopyButton` helper, `openConversation`, `send`)
- Modify: `frontend/src/style.css` (append `.msg-actions` / `.copy-btn` rules)

- [ ] **Step 1: Add the icon constants and `attachCopyButton` helper**

In `frontend/src/main.ts`, directly below the `msgText` helper added in Task 1, add:

```ts
const COPY_ICON = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>`
const CHECK_ICON = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>`

function attachCopyButton(msgEl: HTMLElement) {
  if (msgEl.querySelector('.msg-actions')) return
  const row = document.createElement('div')
  row.className = 'msg-actions'
  const btn = document.createElement('button')
  btn.className = 'copy-btn'
  btn.title = 'Copy'
  btn.innerHTML = COPY_ICON
  btn.onclick = async () => {
    try {
      await navigator.clipboard.writeText(msgText(msgEl).textContent || '')
      btn.classList.add('copied')
      btn.innerHTML = CHECK_ICON
      setTimeout(() => { btn.classList.remove('copied'); btn.innerHTML = COPY_ICON }, 1500)
    } catch {
      // clipboard unavailable — leave the icon unchanged, no crash
    }
  }
  row.appendChild(btn)
  msgEl.appendChild(row)
}
```

The `if (msgEl.querySelector('.msg-actions')) return` guard makes a repeat call on the same message a no-op.

- [ ] **Step 2: Attach the button to history messages in `openConversation`**

In `frontend/src/main.ts`, in `openConversation`, replace this line (43):

```ts
  for (const m of msgs) addMsg(m.role, m.content)
```

with:

```ts
  for (const m of msgs) {
    const el = addMsg(m.role, m.content)
    if (m.role === 'assistant' && m.content.trim()) attachCopyButton(el)
  }
```

Assistant messages loaded from history get a copy button immediately; user messages and empty messages do not.

- [ ] **Step 3: Attach the button to the live reply in `send()`**

In `frontend/src/main.ts`, in `send()`, replace the `finally` block (lines 102-107):

```ts
  } finally {
    streaming = false
    sendBtn.textContent = 'Send ▸'
    sendBtn.classList.remove('streaming')
    await loadConversations()
  }
```

with:

```ts
  } finally {
    streaming = false
    sendBtn.textContent = 'Send ▸'
    sendBtn.classList.remove('streaming')
    if (msgText(asst).textContent?.trim()) attachCopyButton(asst)
    await loadConversations()
  }
```

After streaming ends — on success, Stop, or an error that left text in the bubble — the button is attached if the reply has any text.

- [ ] **Step 4: Add the action-row and copy-button CSS**

In `frontend/src/style.css`, append these rules at the end of the file (after the `#tbModalInner label` rule on line 24):

```css
.msg-actions { display: flex; gap: 6px; margin-top: 5px; opacity: 0; transition: opacity .12s; }
.msg:hover .msg-actions { opacity: 1; }
.copy-btn { display: inline-flex; background: #202024; border: 1px solid #34343a; color: #a9a9ad; border-radius: 6px; padding: 3px 6px; cursor: pointer; }
.copy-btn:hover { color: #e7e7e8; }
.copy-btn.copied { color: #3fb950; border-color: #2ea043; }
.copy-btn svg { display: block; }
```

The row is invisible (`opacity: 0`) until the message is hovered. `.copied` turns the checkmark green.

- [ ] **Step 5: Typecheck**

Run from the `frontend` directory:

```bash
npx tsc --noEmit
```

Expected: no output, exit code 0. Fix any reported errors before continuing.

- [ ] **Step 6: Manual verification in `wails dev`**

Run `wails dev` from the project root and walk the spec's verification list:

1. Send a message; while it streams, confirm **no** copy button is visible and tokens still append correctly.
2. After streaming completes, hover the assistant reply → the copy button fades in. Click it → the clipboard holds the full reply text (paste into any editor to confirm); the icon shows a green checkmark, then reverts after ~1.5s.
3. Press Stop mid-stream → the partial reply keeps a working copy button.
4. Trigger an error send (e.g. a model with no API key) → the partial/error reply still gets a working copy button.
5. Reopen a conversation from the sidebar → every historical assistant message shows the copy button on hover; user messages show none.
6. Confirm no regression: streaming, RAG indexing status banners, and message layout all behave as before. The textbook "Indexing…" / "Textbooks ready." status bubbles must **not** show a copy button.

Fix any failures before committing.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/main.ts frontend/src/style.css
git commit -m "feat: copy button on assistant replies"
```

---

## Notes for the engineer

- **Do not stage `frontend/dist/`.** That directory holds Wails build artifacts regenerated by `wails build`; the commits above stage only `frontend/src/` source files. Leave any pre-existing `dist/` changes alone.
- **`npx tsc --noEmit` is a typecheck only** — it does not bundle or write files. The real bundle is produced later by `wails build`.
- If `wails dev` is not already running, it needs a working `.env` (at least `OPENAI_API_KEY`), `models.yaml`, and `textbooks.yaml` — see `docs/SMOKE.md`.
