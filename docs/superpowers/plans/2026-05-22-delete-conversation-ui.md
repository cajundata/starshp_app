# Delete-conversation UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a hover-revealed delete (✕) control to each conversation row in the sidebar, wired to the existing backend delete.

**Architecture:** Frontend-only change to `frontend/src/main.ts` and `frontend/src/style.css`. Each `.conv` row becomes a flex container with a title span and a hover-revealed ✕ button; the button calls a new `deleteConversation(id)` that confirms, calls `App.DeleteConversation`, and refreshes the sidebar. The embedded `frontend/dist/` bundle is rebuilt so `wails build` includes the feature. The backend is unchanged — `store.DeleteConversation` already cascades via SQLite `ON DELETE CASCADE`.

**Tech Stack:** TypeScript, Vite, Wails v2.

**Spec:** `docs/superpowers/specs/2026-05-22-delete-conversation-ui-design.md`

---

## File Structure

- `frontend/src/style.css` — Modify: `.conv` becomes a flex row; add `.conv-title` and `.conv-del` rules with hover reveal.
- `frontend/src/main.ts` — Modify: rebuild each row in `loadConversations()`; add a `deleteConversation(id)` function.
- `frontend/dist/**` — Regenerated: the embedded production bundle (`main.go` embeds `frontend/dist`).

**Testing note:** the frontend has no automated test framework — per `README.md`, UI is verified via `docs/SMOKE.md`. Tasks are verified by a TypeScript type-check and a successful build; functional verification is the manual SMOKE step 9 at the end.

**Commit hygiene:** commit only the files each task names. The working tree contains unrelated uncommitted changes (`frontend/wailsjs/*`, `go.mod`, `docs/SMOKE.md`) that are NOT part of this feature — never `git add` them.

---

### Task 1: Add the delete-conversation UI to the sidebar

**Files:**
- Modify: `frontend/src/style.css` (the `#convList .conv` rules, currently lines 7-8)
- Modify: `frontend/src/main.ts` (`loadConversations`, currently lines 61-72; new function after it)

- [ ] **Step 1: Update the sidebar CSS**

In `frontend/src/style.css`, replace these two lines:

```css
#convList .conv { padding: 7px 9px; border-radius: 6px; color: #a9a9ad; cursor: pointer; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
#convList .conv.active { background: #202024; color: #e7e7e8; }
```

with:

```css
#convList .conv { display: flex; align-items: center; gap: 6px; padding: 7px 9px; border-radius: 6px; color: #a9a9ad; cursor: pointer; }
#convList .conv.active { background: #202024; color: #e7e7e8; }
#convList .conv-title { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
#convList .conv-del { flex: none; visibility: hidden; border: 0; background: transparent; color: #6f6f76; cursor: pointer; font-size: 13px; line-height: 1; padding: 2px 4px; border-radius: 4px; }
#convList .conv:hover .conv-del { visibility: visible; }
#convList .conv-del:hover { color: #f0f0f2; background: #2b2b30; }
```

The ellipsis truncation moves off `.conv` (now a flex row) onto the new `.conv-title` span. `.conv-del` is hidden until its row is hovered. Colors are taken from the existing palette already used elsewhere in this file.

- [ ] **Step 2: Rebuild each row in `loadConversations()`**

In `frontend/src/main.ts`, replace the entire `loadConversations` function (currently lines 61-72):

```ts
async function loadConversations() {
  const list = $('convList')
  list.innerHTML = ''
  const convs = (await App.ListConversations()) || []
  for (const c of convs) {
    const d = document.createElement('div')
    d.className = 'conv' + (c.id === activeConv ? ' active' : '')
    d.textContent = c.title
    d.onclick = () => openConversation(c.id)
    list.appendChild(d)
  }
}
```

with:

```ts
async function loadConversations() {
  const list = $('convList')
  list.innerHTML = ''
  const convs = (await App.ListConversations()) || []
  for (const c of convs) {
    const d = document.createElement('div')
    d.className = 'conv' + (c.id === activeConv ? ' active' : '')
    d.onclick = () => openConversation(c.id)

    const title = document.createElement('span')
    title.className = 'conv-title'
    title.textContent = c.title
    d.appendChild(title)

    const del = document.createElement('button')
    del.className = 'conv-del'
    del.textContent = '✕'
    del.title = 'Delete conversation'
    del.onclick = (e) => {
      e.stopPropagation()
      void deleteConversation(c.id)
    }
    d.appendChild(del)

    list.appendChild(d)
  }
}
```

The row `<div>` keeps its open-on-click handler; the ✕ button's handler calls `e.stopPropagation()` so clicking it never also opens the conversation.

- [ ] **Step 3: Add the `deleteConversation` function**

In `frontend/src/main.ts`, immediately after the `loadConversations` function (before `openConversation`), add:

```ts
async function deleteConversation(id: string) {
  if (!confirm('Delete this conversation? This cannot be undone.')) return
  try {
    await App.DeleteConversation(id)
  } catch (e: any) {
    alert(`Could not delete the conversation: ${e?.userMessage || e}`)
    return
  }
  if (id === activeConv) {
    activeConv = null
    thread.innerHTML = ''
  }
  await loadConversations()
}
```

`activeConv` (declared near the top of `main.ts`) and `thread` (`$('thread')`) are already in module scope. `App.DeleteConversation(arg1: string): Promise<void>` is already present in the Wails bindings (`frontend/wailsjs/go/appapi/API`). The `e?.userMessage || e` shape matches how errors are already handled elsewhere in `main.ts`. A forward reference to `deleteConversation` from `loadConversations` is fine — it is a hoisted function declaration, and the reference inside `del.onclick` only runs at click time (the existing code forward-references `openConversation` the same way).

- [ ] **Step 4: Type-check**

Run: `cd frontend && npx tsc --noEmit`
Expected: no output, exit 0. (A type error here means a typo in the new code — fix it before committing.)

- [ ] **Step 5: Commit**

```bash
git add frontend/src/main.ts frontend/src/style.css
git commit -m "feat: add delete-conversation control to the sidebar"
```

---

### Task 2: Rebuild the embedded frontend bundle

`main.go` embeds `frontend/dist` via `//go:embed all:frontend/dist`, so a `wails build` binary uses the prebuilt bundle. Rebuild it so the feature ships. This mirrors the existing `chore: rebuild embedded frontend bundle` commit in the project history.

**Files:**
- Regenerated: `frontend/dist/**`

- [ ] **Step 1: Build the frontend bundle**

Run: `cd frontend && npm run build`
Expected: `tsc` reports no errors, then `vite build` completes with a `✓ built in …` summary and writes hashed asset files under `frontend/dist/`.

- [ ] **Step 2: Commit the rebuilt bundle**

```bash
git add frontend/dist
git commit -m "chore: rebuild embedded frontend bundle for the delete-conversation UI"
```

Do not `git add` anything else — `frontend/wailsjs/`, `go.mod`, and `docs/SMOKE.md` have unrelated uncommitted changes that are not part of this feature.

---

## Final verification (manual)

After both tasks, verify via `wails dev` — SMOKE checklist step 9:

1. Hover a conversation row → a ✕ appears at its right edge.
2. Click ✕ → a confirm dialog appears; Cancel → nothing happens.
3. Click ✕ again, confirm → the row disappears from the sidebar.
4. Delete the currently-open conversation → the thread pane also clears.
5. Orphan check: the backend cascade removes the conversation's messages — relaunching and reopening other conversations shows no stray data.

---

## Self-Review

**Spec coverage:**
- ✕-on-hover affordance → Task 1 Steps 1-2.
- Native confirm dialog → Task 1 Step 3 (`confirm(...)`).
- `App.DeleteConversation` call; backend cascade unchanged → Task 1 Step 3.
- Deleting the open conversation clears the thread, `activeConv = null` → Task 1 Step 3.
- Error handling via `alert()` → Task 1 Step 3.
- CSS (`.conv` flex, `.conv-title`, `.conv-del` hover reveal) → Task 1 Step 1.
- Feature ships in the embedded bundle → Task 2.
- Manual verification via SMOKE step 9 → Final verification section.
All spec sections are covered.

**Placeholder scan:** No TBD/TODO/vague steps; every code and command step is concrete.

**Type consistency:** `deleteConversation(id: string)` is defined in Task 1 Step 3 and called in Task 1 Step 2 with `c.id` (a string). The class names `conv-title` and `conv-del` match between the CSS (Step 1) and the JS (Step 2). `loadConversations` is referenced consistently across steps.
