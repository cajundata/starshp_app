# Rerun Item Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a per-item "Rerun" action that re-solves a single assignment item in place, overwriting its prior answer.

**Architecture:** A new `Orchestrator.RerunItem` reloads one question from the assignment's folder and calls the existing `solveItem` against the existing item row (overwrite semantics, fresh conversation/run). A new `appapi.RerunAssignmentItem` runs it synchronously with a no-op event emitter (decoupled from batch progress) and returns the new conversation id. The frontend adds a "↻ Rerun" button to the detail pane that calls it and refreshes. Idle-only: rejected while a batch runs, while the item is solving, or for unsupported items.

**Tech Stack:** Go (SQLite via `database/sql`), Wails v2 bindings, TypeScript + Vite frontend.

**Design spec:** `docs/superpowers/specs/2026-06-04-rerun-item-design.md`

**Assumptions:** The earlier uncommitted bug fixes (stemTable rendering in `render.go`/`question.go`, CSS contrast in `style.css`) remain in the working tree; they are unrelated to these tasks.

---

### Task 1: Store — `GetAssignmentItem`

Single-item getter used by the orchestrator guard and to return post-rerun state. Mirrors `ListAssignmentItems`' column list.

**Files:**
- Modify: `internal/store/assignments.go` (add method after `ListAssignmentItems`, ~line 161)
- Test: `internal/store/assignments_test.go` (add test)

- [ ] **Step 1: Write the failing test**

Add to `internal/store/assignments_test.go`:

```go
func TestGetAssignmentItem(t *testing.T) {
	st := openTestStore(t)
	if err := st.CreateAssignment(Assignment{
		ID: "a1", SourceDir: "/d", Title: "t", ManifestHash: "h",
		Model: "m", Status: "completed", TotalItems: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAssignmentItem(AssignmentItem{
		ID: "i1", AssignmentID: "a1", Seq: 3, SourcePath: "003.html",
		Type: "multipleChoice", Title: "Item 3", Status: "answered", Confidence: "low",
	}); err != nil {
		t.Fatal(err)
	}

	got, ok, err := st.GetAssignmentItem("a1", 3)
	if err != nil || !ok {
		t.Fatalf("expected found, got ok=%v err=%v", ok, err)
	}
	if got.ID != "i1" || got.SourcePath != "003.html" || got.Confidence != "low" {
		t.Fatalf("unexpected item: %+v", got)
	}

	if _, ok, _ := st.GetAssignmentItem("a1", 99); ok {
		t.Fatal("expected ok=false for missing seq")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestGetAssignmentItem -v`
Expected: FAIL — `st.GetAssignmentItem undefined (type *Store has no field or method GetAssignmentItem)`

- [ ] **Step 3: Write minimal implementation**

Add to `internal/store/assignments.go` immediately after `ListAssignmentItems` (after line 161):

```go
// GetAssignmentItem returns the item at (assignmentID, seq), or ok=false if none.
func (s *Store) GetAssignmentItem(assignmentID string, seq int) (AssignmentItem, bool, error) {
	var it AssignmentItem
	err := s.db.QueryRow(
		`SELECT id, assignment_id, seq, source_path, type, COALESCE(title,''),
                COALESCE(run_id,''), COALESCE(conversation_id,''), status,
                COALESCE(confidence,''), COALESCE(answer_json,''), COALESCE(flags_json,''),
                COALESCE(answer_path,''), COALESCE(error,''), created_at, updated_at
           FROM assignment_items WHERE assignment_id=? AND seq=?`, assignmentID, seq).Scan(
		&it.ID, &it.AssignmentID, &it.Seq, &it.SourcePath, &it.Type, &it.Title,
		&it.RunID, &it.ConversationID, &it.Status, &it.Confidence,
		&it.AnswerJSON, &it.FlagsJSON, &it.AnswerPath, &it.Error,
		&it.CreatedAt, &it.UpdatedAt)
	if err == sql.ErrNoRows {
		return AssignmentItem{}, false, nil
	}
	if err != nil {
		return AssignmentItem{}, false, err
	}
	return it, true, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestGetAssignmentItem -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/assignments.go internal/store/assignments_test.go
git commit -m "feat(store): GetAssignmentItem single-item getter"
```

---

### Task 2: Orchestrator — `RerunItem`

Re-solves one item in place. Loads the question from the assignment folder and calls the existing `solveItem` with the existing item ID. Enforces item-level + batch-level guards, returning typed `provider.AppError`s.

**Files:**
- Modify: `internal/assignment/orchestrator.go` (add method; e.g. after `Run`, ~line 163)
- Test: `internal/assignment/orchestrator_test.go` (add tests; imports `context`, `store`, `provider` already present)

- [ ] **Step 1: Write the failing tests**

Add to `internal/assignment/orchestrator_test.go`:

```go
func TestRerunItem_OverwritesInPlace(t *testing.T) {
	st := openStore(t)
	dir := tmpAssignmentDir(t)
	orc := newTestOrchestrator(t, st, scriptedFactory(`{"confidence":"low","answerIndex":0}`))

	asgID, err := orc.Run(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	var seq int
	var oldRun, oldConv, itemID string
	found := false
	items, _ := st.ListAssignmentItems(asgID)
	for _, it := range items {
		if it.SourcePath == "001.html" {
			seq, oldRun, oldConv, itemID, found = it.Seq, it.RunID, it.ConversationID, it.ID, true
		}
	}
	if !found {
		t.Fatal("001.html item not created")
	}

	updated, err := orc.RerunItem(context.Background(), asgID, seq)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != itemID || updated.Seq != seq {
		t.Fatalf("item identity changed: %+v", updated)
	}
	if updated.Status != "answered" {
		t.Fatalf("want answered, got %q", updated.Status)
	}
	if updated.RunID == "" || updated.RunID == oldRun {
		t.Fatalf("expected fresh RunID, old=%q new=%q", oldRun, updated.RunID)
	}
	if updated.ConversationID == "" || updated.ConversationID == oldConv {
		t.Fatalf("expected fresh ConversationID, old=%q new=%q", oldConv, updated.ConversationID)
	}
}

func TestRerunItem_RejectsUnsupported(t *testing.T) {
	st := openStore(t)
	if err := st.CreateAssignment(store.Assignment{
		ID: "a1", SourceDir: "/nope", Title: "t", ManifestHash: "h",
		Model: "m", Status: "completed", TotalItems: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAssignmentItem(store.AssignmentItem{
		ID: "i1", AssignmentID: "a1", Seq: 0, SourcePath: "x.html",
		Type: string(TypeUnsupported), Status: "unsupported",
	}); err != nil {
		t.Fatal(err)
	}
	orc := newTestOrchestrator(t, st, scriptedFactory(`{}`))

	_, err := orc.RerunItem(context.Background(), "a1", 0)
	ae, ok := err.(provider.AppError)
	if !ok || ae.Code != "unsupported" {
		t.Fatalf("want unsupported AppError, got %v", err)
	}
}

func TestRerunItem_RejectsWhileBatchInProgress(t *testing.T) {
	st := openStore(t)
	if err := st.CreateAssignment(store.Assignment{
		ID: "a1", SourceDir: "/nope", Title: "t", ManifestHash: "h",
		Model: "m", Status: "in_progress", TotalItems: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAssignmentItem(store.AssignmentItem{
		ID: "i1", AssignmentID: "a1", Seq: 0, SourcePath: "001.html",
		Type: "multipleChoice", Status: "answered",
	}); err != nil {
		t.Fatal(err)
	}
	orc := newTestOrchestrator(t, st, scriptedFactory(`{}`))

	_, err := orc.RerunItem(context.Background(), "a1", 0)
	ae, ok := err.(provider.AppError)
	if !ok || ae.Code != "busy" {
		t.Fatalf("want busy AppError, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/assignment/ -run TestRerunItem -v`
Expected: FAIL — `orc.RerunItem undefined (type *Orchestrator has no field or method RerunItem)`

- [ ] **Step 3: Write minimal implementation**

Add to `internal/assignment/orchestrator.go` after the `Run` method (after line 163). All imports used (`context`, `fmt`, `provider`, `store`) are already in the file.

```go
// RerunItem re-solves a single item in place, overwriting its prior answer, and
// returns the updated item. Idle-only: rejects unsupported items, items still
// running, and items whose batch is in progress. Errors are typed
// provider.AppError so the API boundary can surface them verbatim.
func (o *Orchestrator) RerunItem(ctx context.Context, asgID string, seq int) (store.AssignmentItem, error) {
	item, ok, err := o.st.GetAssignmentItem(asgID, seq)
	if err != nil {
		return store.AssignmentItem{}, err
	}
	if !ok {
		return store.AssignmentItem{}, provider.AppError{Code: "not_found", UserMessage: "That item no longer exists.", Retryable: false}
	}
	if item.Type == string(TypeUnsupported) {
		return store.AssignmentItem{}, provider.AppError{Code: "unsupported", UserMessage: "This item type can't be solved.", Retryable: false}
	}
	if item.Status == "solving" || item.Status == "pending" {
		return store.AssignmentItem{}, provider.AppError{Code: "busy", UserMessage: "This item is still being solved.", Retryable: false}
	}

	asg, err := o.st.GetAssignment(asgID)
	if err != nil {
		return store.AssignmentItem{}, err
	}
	if asg.Status == "in_progress" {
		return store.AssignmentItem{}, provider.AppError{Code: "busy", UserMessage: "A solve is already running — wait for it to finish.", Retryable: false}
	}

	loaded, err := Load(asg.SourceDir)
	if err != nil {
		return store.AssignmentItem{}, err
	}
	var q Question
	found := false
	for _, cand := range loaded.Questions {
		if cand.Path == item.SourcePath {
			q, found = cand, true
			break
		}
	}
	if !found {
		return store.AssignmentItem{}, provider.AppError{Code: "not_found", UserMessage: "That question is no longer in the folder.", Retryable: false}
	}

	if err := o.opts.Grounding.Ensure(ctx); err != nil {
		return store.AssignmentItem{}, fmt.Errorf("grounding: %w", err)
	}
	o.solveItem(ctx, asg.SourceDir, asgID, item.ID, seq, q)

	updated, _, err := o.st.GetAssignmentItem(asgID, seq)
	if err != nil {
		return store.AssignmentItem{}, err
	}
	return updated, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/assignment/ -run TestRerunItem -v`
Expected: PASS (all three)

Then full package: `go test ./internal/assignment/ -count=1`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/assignment/orchestrator.go internal/assignment/orchestrator_test.go
git commit -m "feat(assignment): RerunItem re-solves one item in place"
```

---

### Task 3: API — `RerunAssignmentItem`

Synchronous Wails-bound method. Builds solver options like `SolveAssignment` but with a no-op emitter, guards against a concurrent rerun, and returns the new conversation id. Returns typed `AppError`s directly (because `provider.NormalizeError` would otherwise re-wrap them as `unknown`).

**Files:**
- Modify: `internal/appapi/api.go` (add `rerunning bool` field to `API` struct ~line 42; add method near `SolveAssignment` ~line 325)

- [ ] **Step 1: Add the `rerunning` guard field**

In `internal/appapi/api.go`, change the `API` struct (lines 40-43) from:

```go
	assignmentFactory assignment.ProviderFactory     // overridable in tests
	emit              func(name string, payload any) // wruntime.EventsEmit wrapper; overridable in tests
	asgCancel         context.CancelFunc
}
```

to:

```go
	assignmentFactory assignment.ProviderFactory     // overridable in tests
	emit              func(name string, payload any) // wruntime.EventsEmit wrapper; overridable in tests
	asgCancel         context.CancelFunc
	rerunning         bool // guards against concurrent single-item reruns (a.mu)
}
```

- [ ] **Step 2: Add the method**

In `internal/appapi/api.go`, add immediately after `SolveAssignment` (after line 325). All imports used (`assignment`, `provider`, `tools`, `safemath`, `searchtextbook`) are already present.

```go
// RerunAssignmentItem re-solves a single item in place and returns the new
// conversation id. Idle-only: errors if another rerun is already in flight or a
// batch is running. The prior answer (and its _answers/NNN.json file) is
// overwritten. Runs synchronously: it returns when the item has been re-solved.
func (a *API) RerunAssignmentItem(asgID string, seq int) (string, error) {
	a.mu.Lock()
	if a.rerunning {
		a.mu.Unlock()
		return "", provider.AppError{Code: "busy", UserMessage: "Another rerun is already running.", Retryable: false}
	}
	a.rerunning = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.rerunning = false
		a.mu.Unlock()
	}()

	model := a.defaultModelID()
	if model == "" {
		return "", provider.AppError{Code: "config", UserMessage: "No model configured.", Retryable: false}
	}
	var search tools.Tool
	if a.ragAdpt != nil {
		search = searchtextbook.New(ragRetrieverShim{a: a}, chatStoreResolver{st: a.st}, 4000)
	}
	opts := assignment.Options{
		Model:       model,
		Concurrency: 1,
		Grounding:   assignment.NoGrounding{},
		SafeMath:    safemath.New(),
		SearchTool:  search,
		Emit:        func(string, any) {}, // decoupled from batch progress events
	}
	orc := assignment.New(a.st, a.chatSvc, a.assignmentFactory, opts)

	updated, err := orc.RerunItem(a.ctx, asgID, seq)
	if err != nil {
		if ae, ok := err.(provider.AppError); ok {
			return "", ae // preserve typed code; NormalizeError would mask it as "unknown"
		}
		return "", provider.NormalizeError(err)
	}
	return updated.ConversationID, nil
}
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./...`
Expected: no output (success)

- [ ] **Step 4: Verify the package still vets and tests**

Run: `go vet ./internal/appapi/ && go test ./internal/appapi/ -count=1`
Expected: `ok` (no new test added here; guard logic is covered by Task 2's orchestrator tests, and the mutex/wiring is verified by the manual smoke in Task 6)

- [ ] **Step 5: Commit**

```bash
git add internal/appapi/api.go
git commit -m "feat(appapi): RerunAssignmentItem synchronous single-item rerun"
```

---

### Task 4: Wails bindings — expose `RerunAssignmentItem`

The generated binding files must include the new method so the frontend can call `App.RerunAssignmentItem`. Hand-add the entry to both files (running `wails dev`/`wails build` would regenerate identical content).

**Files:**
- Modify: `frontend/wailsjs/go/appapi/API.js`
- Modify: `frontend/wailsjs/go/appapi/API.d.ts`

- [ ] **Step 1: Add the JS wrapper**

In `frontend/wailsjs/go/appapi/API.js`, immediately after the `SolveAssignment` export (the block ending at line 111), add:

```js
export function RerunAssignmentItem(arg1, arg2) {
  return window['go']['appapi']['API']['RerunAssignmentItem'](arg1, arg2);
}
```

- [ ] **Step 2: Add the TS declaration**

In `frontend/wailsjs/go/appapi/API.d.ts`, immediately after the `SolveAssignment` declaration (line 61), add:

```ts
export function RerunAssignmentItem(arg1:string,arg2:number):Promise<string>;
```

- [ ] **Step 3: Verify the frontend typechecks**

Run: `cd frontend && npx tsc --noEmit`
Expected: no output (success). (If `App.RerunAssignmentItem` were missing, Task 5's usage would error here.)

- [ ] **Step 4: Commit**

```bash
git add frontend/wailsjs/go/appapi/API.js frontend/wailsjs/go/appapi/API.d.ts
git commit -m "chore(bindings): expose RerunAssignmentItem to frontend"
```

---

### Task 5: Frontend — Rerun button in the detail pane

Track the selected item and the assignment status, render a "↻ Rerun" button in the detail header when the item is rerunnable, and wire the click handler to call `App.RerunAssignmentItem` then refresh.

**Files:**
- Modify: `frontend/src/main.ts` (module vars ~line 628; `selectAssignment` ~line 690; `renderItemRow` onclick ~line 799; `openItemDetail` ~line 834-918)
- Modify: `frontend/src/style.css` (append detail-header + button styles)

- [ ] **Step 1: Add module-level state**

In `frontend/src/main.ts`, just after `let currentAssignmentId: string | null = null` (line 628), add:

```ts
let selectedItem: store.AssignmentItem | null = null
let currentAssignmentStatus = ''
const RERUNNABLE_STATUSES = ['answered', 'no_answer', 'errored', 'cancelled']
```

- [ ] **Step 2: Track assignment status in `selectAssignment`**

In `selectAssignment`, find the line that assigns the loaded assignment (around line 696, `asg = await App.GetAssignment(id)` inside the try). Immediately after the try/catch that loads `asg` and `items` succeeds — i.e. right before the code renders the header — add an assignment-status capture. Concretely, locate:

```ts
    asg = await App.GetAssignment(id)
    items = (await App.ListAssignmentItems(id)) || []
```

and change it to:

```ts
    asg = await App.GetAssignment(id)
    items = (await App.ListAssignmentItems(id)) || []
    currentAssignmentStatus = asg.Status || ''
```

- [ ] **Step 3: Capture the clicked item in `renderItemRow`**

In `renderItemRow`, change the drill-in handler (lines 797-800) from:

```ts
  if (it.ConversationID) {
    row.classList.add('drillable')
    row.onclick = () => void openItemDetail(it.ConversationID, it.Seq)
  }
```

to:

```ts
  if (it.ConversationID) {
    row.classList.add('drillable')
    row.onclick = () => {
      selectedItem = it
      void openItemDetail(it.ConversationID, it.Seq)
    }
  }
```

- [ ] **Step 4: Render the detail header + add the rerun helpers**

In `openItemDetail`, find the line `asgDetail.innerHTML = ''` (line 837) and add a header render right after it:

```ts
  asgDetail.innerHTML = ''
  renderDetailHeader()
```

Then, also in `openItemDetail`, change the empty-run branch (lines 916-918) so it does not wipe the header. From:

```ts
  if (bubbles.size === 0) {
    asgDetail.innerHTML = '<p class="asg-empty">No worked run recorded for this item.</p>'
  }
```

to:

```ts
  if (bubbles.size === 0) {
    const empty = document.createElement('p')
    empty.className = 'asg-empty'
    empty.textContent = 'No worked run recorded for this item.'
    asgDetail.appendChild(empty)
  }
```

Finally, add these three functions immediately after `openItemDetail` (after line 919):

```ts
function itemRerunnable(it: store.AssignmentItem | null): boolean {
  return !!it
    && it.Type !== 'unsupported'
    && RERUNNABLE_STATUSES.includes(it.Status)
    && currentAssignmentStatus !== 'in_progress'
}

function renderDetailHeader() {
  const header = document.createElement('div')
  header.className = 'asg-detail-header'
  if (itemRerunnable(selectedItem)) {
    const btn = document.createElement('button')
    btn.className = 'asg-rerun-btn'
    btn.textContent = '↻ Rerun'
    btn.onclick = () => void rerunSelectedItem(btn)
    header.appendChild(btn)
  }
  const msg = document.createElement('span')
  msg.className = 'asg-rerun-msg'
  header.appendChild(msg)
  asgDetail.appendChild(header)
}

async function rerunSelectedItem(btn: HTMLButtonElement) {
  if (!selectedItem || !currentAssignmentId) return
  const seq = selectedItem.Seq
  const prior = selectedItem
  const msg = asgDetail.querySelector('.asg-rerun-msg') as HTMLElement | null
  const prevLabel = btn.textContent
  btn.disabled = true
  btn.textContent = '↻ Rerunning…'
  if (msg) msg.textContent = ''
  const pill = asgItemRows.get(seq)?.querySelector('.status-pill') as HTMLElement | null
  if (pill) {
    pill.className = 'status-pill status-solving'
    pill.textContent = 'solving'
  }
  try {
    await App.RerunAssignmentItem(currentAssignmentId, seq)
    await selectAssignment(currentAssignmentId)
    const items = (await App.ListAssignmentItems(currentAssignmentId)) || []
    const fresh = items.find(i => i.Seq === seq) || null
    selectedItem = fresh
    if (fresh && fresh.ConversationID) {
      await openItemDetail(fresh.ConversationID, seq)
    }
  } catch (e: any) {
    if (pill) {
      pill.className = 'status-pill status-' + prior.Status
      pill.textContent = prior.Status
    }
    btn.disabled = false
    btn.textContent = prevLabel || '↻ Rerun'
    if (msg) msg.textContent = e?.userMessage || String(e)
  }
}
```

- [ ] **Step 5: Add styles**

Append to `frontend/src/style.css`:

```css
/* ---- Assignments rerun ---- */
.asg-detail-header { display: flex; align-items: center; gap: 10px; margin-bottom: 6px; }
.asg-rerun-btn { background: #2b2b30; color: #e7e7e8; border: 1px solid #34343a; border-radius: 7px; padding: 5px 12px; font-size: 12px; font-weight: 600; cursor: pointer; }
.asg-rerun-btn:hover:not(:disabled) { background: #34343a; }
.asg-rerun-btn:disabled { opacity: .6; cursor: default; }
.asg-rerun-msg { color: #e08585; font-size: 12px; }
```

- [ ] **Step 6: Verify the frontend builds (typecheck + bundle)**

Run: `cd frontend && npm run build`
Expected: `tsc` passes with no errors and `vite build` writes `dist/` assets.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/main.ts frontend/src/style.css frontend/dist
git commit -m "feat(frontend): Rerun button in assignment item detail"
```

---

### Task 6: Full verification + manual smoke

- [ ] **Step 1: Backend build, vet, and tests**

Run:
```bash
go build ./... && go vet ./... && go test ./internal/store/ ./internal/assignment/ ./internal/appapi/ -count=1
```
Expected: build/vet silent; all three packages report `ok`.

- [ ] **Step 2: Launch the app**

Run: `wails dev`
Expected: the app window opens with no console errors.

- [ ] **Step 3: Manual smoke — happy path**

1. Open **Assignments → Solve a folder…**, point at a real `_json` folder, let it finish.
2. Click an answered item (e.g. Item 1). Confirm the detail pane shows a **↻ Rerun** button in a header above the run, and the answer text is legible (light-on-dark).
3. Click **Rerun**. The button shows "Rerunning…"; when it returns, the detail re-renders the new run and the row's confidence/status update.
Expected: a new run appears (distinct from the prior one); no console errors.

- [ ] **Step 4: Manual smoke — guards**

1. Start a fresh **Solve a folder…** and, while it is running, open a (previously solved) item: the **Rerun** button is **absent** (assignment is `in_progress`).
2. After completion, open an **unsupported** item (if present): the **Rerun** button is **absent**.
Expected: button only appears for terminal, supported items while no batch runs.

- [ ] **Step 5: Final commit (if any smoke fixes were needed)**

```bash
git add -A
git commit -m "fix(rerun): address smoke-test findings"
```
(Skip if nothing changed.)

---

## Self-Review notes

- **Spec coverage:** overwrite semantics (Task 2 `solveItem` reuse + test), idle-only guards (Task 2 unsupported/busy tests + Task 3 mutex), synchronous/decoupled events (Task 3 no-op `Emit`), `GetAssignmentItem` (Task 1), button + refresh + errors (Task 5), follow-ups (textbook attach, cancel) intentionally excluded.
- **Type consistency:** `RerunItem(ctx, asgID string, seq int) (store.AssignmentItem, error)` and `RerunAssignmentItem(asgID string, seq int) (string, error)` are used identically across backend, bindings (`arg1:string,arg2:number):Promise<string>`), and frontend (`App.RerunAssignmentItem(currentAssignmentId, seq)`). Error codes (`busy`, `unsupported`, `not_found`, `config`) are `provider.AppError` throughout and surfaced to the UI via `e.userMessage`.
- **No placeholders:** every code step shows complete code; every run step shows the exact command and expected result.
