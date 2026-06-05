# Assignment Library Prompts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user select library prompt/context items for an assignment (at solve time and editable later); their bodies are prepended to each item's system prompt during solve and rerun.

**Architecture:** Selected library item filenames are stored JSON-encoded in a new `assignments.library_items` column. `appapi` assembles the items' bodies into a "library preamble" string (reusing the chat assembly logic) and injects it via `Options.LibraryPreamble`; `solveItem` prepends it to the question's base system prompt. The orchestrator never gains library access. Library items are plain text — no indexing.

**Tech Stack:** Go (SQLite via `database/sql`), Wails v2 bindings, TypeScript + Vite frontend.

**Design spec:** `docs/superpowers/specs/2026-06-05-assignment-library-prompts-design.md`

**Branch:** `assignment-library-prompts` (already created).

**Assumptions:** Pre-existing uncommitted tree changes (`docs/SMOKE.md`, `frontend/dist`, `frontend/wailsjs/runtime`) remain untouched; never stage them. `encoding/json` is already imported in `internal/store/assignments.go`. `store.TextbookScope` already exists in `models.ts`.

---

### Task 1: Store — `library_items` column + get/set

**Files:**
- Modify: `internal/store/schema.go` (assignments DDL)
- Modify: `internal/store/migrate.go` (ALTER for existing DBs)
- Modify: `internal/store/assignments.go` (two methods)
- Test: `internal/store/assignments_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/assignments_test.go`:

```go
func TestAssignmentLibraryItems_RoundTrip(t *testing.T) {
	st := openTestStore(t)
	if err := st.CreateAssignment(Assignment{
		ID: "a1", SourceDir: "/d", Title: "t", ManifestHash: "h",
		Model: "m", Status: "in_progress", TotalItems: 1,
	}); err != nil {
		t.Fatal(err)
	}

	if got, err := st.GetAssignmentLibraryItems("a1"); err != nil || got != nil {
		t.Fatalf("want nil default, got %v err %v", got, err)
	}

	if err := st.SetAssignmentLibraryItems("a1", []string{"tone.md", "rubric.md"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetAssignmentLibraryItems("a1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "tone.md" || got[1] != "rubric.md" {
		t.Fatalf("unexpected: %+v", got)
	}

	if err := st.SetAssignmentLibraryItems("a1", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetAssignmentLibraryItems("a1"); got != nil {
		t.Fatalf("want nil after clear, got %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestAssignmentLibraryItems_RoundTrip -v`
Expected: FAIL — `st.SetAssignmentLibraryItems undefined` / `st.GetAssignmentLibraryItems undefined`.

- [ ] **Step 3: Add the column to the fresh-DB schema**

In `internal/store/schema.go`, in the `CREATE TABLE IF NOT EXISTS assignments` block, add a `library_items` column right after the `grounding_scope TEXT,` line. Change:

```sql
  grounding_scope TEXT,
  status          TEXT NOT NULL CHECK (status IN (
```

to:

```sql
  grounding_scope TEXT,
  library_items   TEXT,
  status          TEXT NOT NULL CHECK (status IN (
```

- [ ] **Step 4: Add the migration for existing DBs**

In `internal/store/migrate.go`, in `migrate(db)`, add a column-add block immediately after the `assignment_id` ALTER block (after its closing `}`, before the `// conversation_events, runs, ...` comment):

```go
	has, err = columnExists(db, "assignments", "library_items")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE assignments ADD COLUMN library_items TEXT`); err != nil {
			return err
		}
	}
```

- [ ] **Step 5: Add the store methods**

Add to `internal/store/assignments.go` (after `GetAssignmentScope`):

```go
// SetAssignmentLibraryItems stores the assignment's selected library item
// filenames as JSON in library_items. An empty slice clears it (NULL).
func (s *Store) SetAssignmentLibraryItems(asgID string, items []string) error {
	var js string
	if len(items) > 0 {
		b, err := json.Marshal(items)
		if err != nil {
			return err
		}
		js = string(b)
	}
	_, err := s.db.Exec(`UPDATE assignments SET library_items=?, updated_at=? WHERE id=?`,
		nullIfEmpty(js), time.Now().UnixMilli(), asgID)
	return err
}

// GetAssignmentLibraryItems returns the assignment's selected library item
// filenames (nil if none).
func (s *Store) GetAssignmentLibraryItems(asgID string) ([]string, error) {
	var js string
	if err := s.db.QueryRow(
		`SELECT COALESCE(library_items,'') FROM assignments WHERE id=?`, asgID).Scan(&js); err != nil {
		return nil, err
	}
	if js == "" {
		return nil, nil
	}
	var items []string
	if err := json.Unmarshal([]byte(js), &items); err != nil {
		return nil, err
	}
	return items, nil
}
```

- [ ] **Step 6: Run test + full package**

Run: `go test ./internal/store/ -run TestAssignmentLibraryItems_RoundTrip -v && go test ./internal/store/ -count=1`
Expected: PASS; package `ok` (the round-trip exercises the column through a fresh `openTestStore`, which runs schema + migrate).

- [ ] **Step 7: Commit**

```bash
git add internal/store/schema.go internal/store/migrate.go internal/store/assignments.go internal/store/assignments_test.go
git commit -m "feat(store): assignment library_items column + get/set"
```

---

### Task 2: appapi — `assembleLibraryPreamble` core + passthroughs

**Files:**
- Modify: `internal/appapi/library.go`
- Test: `internal/appapi/library_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/appapi/library_test.go`:

```go
package appapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/library"
)

func TestAssembleLibraryPreamble(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte("# Beta\nbeta body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# Alpha\nalpha body"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &API{lib: library.New(dir)}

	got, skipped, err := a.assembleLibraryPreamble([]string{"b.md", "a.md", "missing.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 || skipped[0] != "missing.md" {
		t.Fatalf("skipped = %v", skipped)
	}
	if !strings.Contains(got, "alpha body") || !strings.Contains(got, "beta body") {
		t.Fatalf("missing bodies: %q", got)
	}
	// sorted by display name (H1): Alpha before Beta
	if strings.Index(got, "alpha body") > strings.Index(got, "beta body") {
		t.Fatalf("expected Alpha before Beta in: %q", got)
	}
	// empty selection → empty preamble
	if p, _, _ := a.assembleLibraryPreamble(nil); p != "" {
		t.Fatalf("empty selection should yield empty preamble, got %q", p)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/appapi/ -run TestAssembleLibraryPreamble -v`
Expected: FAIL — `a.assembleLibraryPreamble undefined`.

- [ ] **Step 3: Refactor `assembleSystemPrompt` to delegate to a names-based core**

In `internal/appapi/library.go`, replace the entire `assembleSystemPrompt` function (from its `// assembleSystemPrompt builds...` comment through its closing `}`) with:

```go
// assembleSystemPrompt builds the system prompt for a conversation: it reads
// each active item, strips the H1, and concatenates the bodies in display-name
// order. Items whose files are missing on disk are skipped and returned in
// `skipped` (a missing snippet is not fatal). It reads a.st directly — not the
// pruning GetActiveItems above — to keep the send path lean.
func (a *API) assembleSystemPrompt(convID string) (prompt string, skipped []string, err error) {
	names, err := a.st.GetActiveItems(convID)
	if err != nil {
		return "", nil, err
	}
	return a.assembleLibraryPreamble(names)
}

// assembleLibraryPreamble reads each named library item, strips its H1, sorts by
// display name (case-insensitive), and joins the non-empty bodies with "\n\n".
// Missing/unreadable items are skipped and returned in `skipped`.
func (a *API) assembleLibraryPreamble(names []string) (prompt string, skipped []string, err error) {
	type entry struct{ display, body string }
	var entries []entry
	for _, name := range names {
		content, rerr := a.lib.Read(name)
		if rerr != nil {
			skipped = append(skipped, name)
			continue
		}
		// A readable item always has an H1 (Create/Save enforce it); if one
		// somehow lacks it, display is "" and it simply sorts first.
		entries = append(entries, entry{
			display: library.ExtractH1(content),
			body:    library.StripH1(content),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].display) < strings.ToLower(entries[j].display)
	})
	var bodies []string
	for _, e := range entries {
		if e.body != "" {
			bodies = append(bodies, e.body)
		}
	}
	return strings.Join(bodies, "\n\n"), skipped, nil
}
```

- [ ] **Step 4: Add the passthrough methods**

Add to `internal/appapi/library.go` (after `SetActiveItems`):

```go
func (a *API) SetAssignmentLibraryItems(asgID string, items []string) error {
	return a.st.SetAssignmentLibraryItems(asgID, items)
}
func (a *API) GetAssignmentLibraryItems(asgID string) ([]string, error) {
	return a.st.GetAssignmentLibraryItems(asgID)
}
```

- [ ] **Step 5: Run test + full package**

Run: `go test ./internal/appapi/ -run TestAssembleLibraryPreamble -v && go test ./internal/appapi/ -count=1`
Expected: PASS; package `ok` (existing tests still pass — `assembleSystemPrompt` behavior unchanged).

- [ ] **Step 6: Commit**

```bash
git add internal/appapi/library.go internal/appapi/library_test.go
git commit -m "feat(appapi): assembleLibraryPreamble core + assignment library passthroughs"
```

---

### Task 3: Orchestrator — preamble option + thread/persist items + prepend in solveItem

Keep the API building by updating its two call sites in the same commit (without changing `SolveAssignment`'s public signature — that's Task 4).

**Files:**
- Modify: `internal/assignment/orchestrator.go`
- Modify: `internal/appapi/api.go` (call-site fixes + rerun preamble)
- Test: `internal/assignment/orchestrator_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/assignment/orchestrator_test.go`:

```go
func TestWithLibraryPreamble(t *testing.T) {
	if got := withLibraryPreamble("", "BASE"); got != "BASE" {
		t.Fatalf("empty preamble should pass through, got %q", got)
	}
	got := withLibraryPreamble("PRE", "BASE")
	if got != "PRE\n\nBASE" {
		t.Fatalf("got %q", got)
	}
	// the operative base prompt must remain last (recency)
	if !strings.HasSuffix(got, "BASE") {
		t.Fatalf("base must be last, got %q", got)
	}
}

func TestPrepare_PersistsAndNilGuardsLibraryItems(t *testing.T) {
	st := openStore(t)
	dir := tmpAssignmentDir(t)
	orc := newTestOrchestrator(t, st, scriptedFactory(`{"confidence":"low","answerIndex":0}`))

	asgID, err := orc.Run(context.Background(), dir, nil, []string{"tone.md"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetAssignmentLibraryItems(asgID); len(got) != 1 || got[0] != "tone.md" {
		t.Fatalf("solve did not persist library items: %+v", got)
	}
	// Re-solve with nil library items must NOT wipe the stored selection.
	if _, err := orc.Run(context.Background(), dir, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetAssignmentLibraryItems(asgID); len(got) != 1 || got[0] != "tone.md" {
		t.Fatalf("nil re-solve clobbered library items: %+v", got)
	}
}
```

(`strings` is already imported in `orchestrator_test.go`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/assignment/ -run 'TestWithLibraryPreamble|TestPrepare_PersistsAndNilGuardsLibraryItems' -v`
Expected: FAIL — won't compile: `withLibraryPreamble` undefined / `orc.Run` takes 3 args / `GetAssignmentLibraryItems` (already exists from Task 1).

- [ ] **Step 3: Add `LibraryPreamble` to `Options`**

In `internal/assignment/orchestrator.go`, change the `Options` struct's `Resolver` field block. Find:

```go
	// Resolver resolves a conversation's attached textbooks into book scope for
	// the search_textbook tool. appapi injects chatStoreResolver; nil disables.
	Resolver chat.ScopeResolver
}
```

Replace with:

```go
	// Resolver resolves a conversation's attached textbooks into book scope for
	// the search_textbook tool. appapi injects chatStoreResolver; nil disables.
	Resolver chat.ScopeResolver
	// LibraryPreamble is the assembled text of the assignment's selected library
	// items; when non-empty it is prepended to each item's system prompt. appapi
	// assembles it (from passed items on solve, stored items on rerun).
	LibraryPreamble string
}
```

- [ ] **Step 4: Add the `withLibraryPreamble` helper and apply it in `solveItem`**

In `internal/assignment/orchestrator.go`, add this helper just above `solveItem` (above the `// solveItem runs one question...` comment):

```go
// withLibraryPreamble prepends a non-empty library preamble before the base
// system prompt, keeping the base (operative) instructions last for recency.
func withLibraryPreamble(preamble, system string) string {
	if preamble == "" {
		return system
	}
	return preamble + "\n\n" + system
}
```

Then in `solveItem`, change:

```go
	system, user := RenderPrompt(q)
```

to:

```go
	system, user := RenderPrompt(q)
	system = withLibraryPreamble(o.opts.LibraryPreamble, system)
```

- [ ] **Step 5: Thread `libraryItems` through `Run`, `Start`, `prepare` and persist them**

Change `prepare`'s signature. Find:

```go
func (o *Orchestrator) prepare(ctx context.Context, dir string, scopes []store.TextbookScope) (string, *Loaded, map[string]store.AssignmentItem, error) {
```

to:

```go
func (o *Orchestrator) prepare(ctx context.Context, dir string, scopes []store.TextbookScope, libraryItems []string) (string, *Loaded, map[string]store.AssignmentItem, error) {
```

In `prepare`, immediately after the existing scope-persist block (the `if scopes != nil { ... SetAssignmentScope ... }` block) and before `return asgID, loaded, priorByPath, nil`, add:

```go
	// Same authoritative-vs-nil contract as the textbook scope above.
	if libraryItems != nil {
		if err := o.st.SetAssignmentLibraryItems(asgID, libraryItems); err != nil {
			return "", nil, nil, err
		}
	}
```

Change `Run`:

```go
func (o *Orchestrator) Run(ctx context.Context, dir string, scopes []store.TextbookScope, libraryItems []string) (string, error) {
	asgID, loaded, prior, err := o.prepare(ctx, dir, scopes, libraryItems)
	if err != nil {
		return "", err
	}
	o.runItems(ctx, dir, asgID, loaded, prior)
	return asgID, nil
}
```

Change `Start` (find its current signature `func (o *Orchestrator) Start(ctx context.Context, dir string, scopes []store.TextbookScope, onDone func()) (string, error)`):

```go
func (o *Orchestrator) Start(ctx context.Context, dir string, scopes []store.TextbookScope, libraryItems []string, onDone func()) (string, error) {
	asgID, loaded, prior, err := o.prepare(ctx, dir, scopes, libraryItems)
	if err != nil {
		return "", err
	}
	go func() {
		if onDone != nil {
			defer onDone()
		}
		o.runItems(ctx, dir, asgID, loaded, prior)
	}()
	return asgID, nil
}
```

- [ ] **Step 6: Update existing orchestrator-test call sites**

In `internal/assignment/orchestrator_test.go` and `internal/assignment/quality_test.go`, every existing `orc.Run(ctx, dir, scopes)` / `orc.Run(ctx, dir, nil)` now needs a 4th arg. Search each file for `orc.Run(` and add `, nil` as the library-items argument to every existing call, e.g.:

```go
	asgID, err := orc.Run(context.Background(), dir, nil, nil)
```

and the textbook attach test's scoped call becomes:

```go
	asgID, err := orc.Run(context.Background(), dir, []store.TextbookScope{{Name: "blaw"}}, nil)
```

(Do NOT change the new Task-3 tests, which already pass 4 args.)

- [ ] **Step 7: Keep `appapi` compiling — call-site fixes + rerun preamble**

In `internal/appapi/api.go`, in `SolveAssignment`, change `id, err := orc.Start(cctx, dir, scopes, cancel)` to pass `nil` library items (its public signature is unchanged until Task 4):

```go
	id, err := orc.Start(cctx, dir, scopes, nil, cancel)
```

In `RerunAssignmentItem`, assemble the preamble from the assignment's stored library items and set it in `Options`. Immediately before the `opts := assignment.Options{` line, add:

```go
	libItems, _ := a.st.GetAssignmentLibraryItems(asgID)
	libPreamble, _, _ := a.assembleLibraryPreamble(libItems)
```

and add the field to that `opts` literal (after `Resolver: chatStoreResolver{st: a.st},`):

```go
		LibraryPreamble: libPreamble,
```

- [ ] **Step 8: Run tests + full build**

Run: `go build ./... && go test ./internal/assignment/ -count=1 && go test ./internal/appapi/ -count=1`
Expected: build clean; both packages `ok`.

- [ ] **Step 9: Commit**

```bash
git add internal/assignment/orchestrator.go internal/assignment/orchestrator_test.go internal/assignment/quality_test.go internal/appapi/api.go
git commit -m "feat(assignment): prepend library preamble to item system prompts"
```

---

### Task 4: API — `SolveAssignment(dir, scopes, libraryItems)`

**Files:**
- Modify: `internal/appapi/api.go`
- Modify: `internal/appapi/api_test.go`

- [ ] **Step 1: Change the signature; assemble + persist the passed items**

In `internal/appapi/api.go`, change `SolveAssignment`'s signature from:

```go
func (a *API) SolveAssignment(dir string, scopes []store.TextbookScope) (string, error) {
```

to:

```go
func (a *API) SolveAssignment(dir string, scopes []store.TextbookScope, libraryItems []string) (string, error) {
```

Immediately before the `opts := assignment.Options{` line, add the preamble assembly:

```go
	libPreamble, _, _ := a.assembleLibraryPreamble(libraryItems)
```

Add the field to the `opts` literal (after `Resolver: chatStoreResolver{st: a.st},`):

```go
		LibraryPreamble: libPreamble,
```

And change `id, err := orc.Start(cctx, dir, scopes, nil, cancel)` to forward the items:

```go
	id, err := orc.Start(cctx, dir, scopes, libraryItems, cancel)
```

- [ ] **Step 2: Update the test call site**

In `internal/appapi/api_test.go`, change `id, err := a.SolveAssignment(dir, nil)` to:

```go
	id, err := a.SolveAssignment(dir, nil, nil)
```

- [ ] **Step 3: Build + test**

Run: `go build ./... && go test ./internal/appapi/ -count=1`
Expected: build clean; `ok`.

- [ ] **Step 4: Commit**

```bash
git add internal/appapi/api.go internal/appapi/api_test.go
git commit -m "feat(appapi): SolveAssignment accepts library items"
```

---

### Task 5: Wails bindings

**Files:**
- Modify: `frontend/wailsjs/go/appapi/API.js`
- Modify: `frontend/wailsjs/go/appapi/API.d.ts`

- [ ] **Step 1: Update `SolveAssignment` + add new wrappers (JS)**

In `frontend/wailsjs/go/appapi/API.js`, change the existing `SolveAssignment` wrapper to take three args:

```js
export function SolveAssignment(arg1, arg2, arg3) {
  return window['go']['appapi']['API']['SolveAssignment'](arg1, arg2, arg3);
}
```

And add these two exports (near the other assignment entries):

```js
export function GetAssignmentLibraryItems(arg1) {
  return window['go']['appapi']['API']['GetAssignmentLibraryItems'](arg1);
}

export function SetAssignmentLibraryItems(arg1, arg2) {
  return window['go']['appapi']['API']['SetAssignmentLibraryItems'](arg1, arg2);
}
```

- [ ] **Step 2: Update `SolveAssignment` + add new declarations (d.ts)**

In `frontend/wailsjs/go/appapi/API.d.ts`, change the `SolveAssignment` declaration to:

```ts
export function SolveAssignment(arg1:string,arg2:Array<store.TextbookScope>,arg3:Array<string>):Promise<string>;
```

And add:

```ts
export function GetAssignmentLibraryItems(arg1:string):Promise<Array<string>>;

export function SetAssignmentLibraryItems(arg1:string,arg2:Array<string>):Promise<void>;
```

- [ ] **Step 3: Verify the frontend typechecks**

Run: `cd frontend && npx tsc --noEmit`
Expected: the ONLY new error is the existing two-arg `App.SolveAssignment(d, scopes)` call in `frontend/src/main.ts` (now requires 3 args), which Task 6 fixes. If that's the only new error, proceed. Any OTHER new error must be fixed here.

- [ ] **Step 4: Commit**

```bash
git add frontend/wailsjs/go/appapi/API.js frontend/wailsjs/go/appapi/API.d.ts
git commit -m "chore(bindings): library item methods + SolveAssignment items arg"
```

---

### Task 6: Frontend — library picker, solve flow, editable header button

**Files:**
- Modify: `frontend/src/main.ts`

- [ ] **Step 1: Add a reusable `pickLibraryItems` helper**

In `frontend/src/main.ts`, add this function immediately after the existing `pickTextbooks` function (it uses the `#libModal`/`#libModalInner` DOM):

```ts
// pickLibraryItems opens the library modal as a reusable picker. It lists items,
// pre-checks `current` (by filename), and on confirm calls onConfirm(selected
// filenames) — closing the modal on success, or showing the error inline.
async function pickLibraryItems(
  current: string[],
  confirmLabel: string,
  onConfirm: (items: string[]) => Promise<void>,
) {
  const inner = $('libModalInner')
  inner.innerHTML = '<h3>Prompt / context library</h3>'
  $('libModal').classList.remove('hidden')

  let items: Awaited<ReturnType<typeof App.ListLibraryItems>>
  try {
    items = (await App.ListLibraryItems()) || []
  } catch (e: any) {
    const err = document.createElement('p')
    err.className = 'lib-empty'
    err.textContent = `Could not load library: ${e?.userMessage || e}`
    inner.appendChild(err)
    return
  }

  if (items.length === 0) {
    const empty = document.createElement('p')
    empty.className = 'lib-empty'
    empty.textContent = 'No library items yet. Create one in the Library panel.'
    inner.appendChild(empty)
  }

  for (const it of items) {
    const row = document.createElement('div')
    row.className = 'lib-row'
    const label = document.createElement('label')
    const cb = document.createElement('input')
    cb.type = 'checkbox'
    cb.dataset.file = it.filename
    cb.checked = current.includes(it.filename)
    cb.disabled = !!it.error
    label.appendChild(cb)
    const span = document.createElement('span')
    span.textContent = it.error ? ` ${it.name} (unavailable)` : ` ${it.name}`
    label.appendChild(span)
    row.appendChild(label)
    inner.appendChild(row)
  }

  const status = document.createElement('p')
  status.className = 'lib-empty'
  inner.appendChild(status)

  const confirm = document.createElement('button')
  confirm.className = 'lib-new'
  confirm.textContent = confirmLabel
  confirm.onclick = async () => {
    const boxes = inner.querySelectorAll('input[type=checkbox]')
    const sel: string[] = []
    boxes.forEach((b: any) => { if (b.checked && b.dataset.file) sel.push(b.dataset.file) })
    confirm.disabled = true
    status.className = 'lib-empty'
    status.textContent = 'Working…'
    try {
      await onConfirm(sel)
      $('libModal').classList.add('hidden')
    } catch (e: any) {
      status.className = 'tb-error'
      status.textContent = `Failed: ${e?.userMessage || e}`
      confirm.disabled = false
    }
  }
  inner.appendChild(confirm)
}

// openAssignmentLibraryEditor edits the current assignment's library selection.
async function openAssignmentLibraryEditor() {
  const id = currentAssignmentId
  if (!id) return
  let current: string[]
  try {
    current = (await App.GetAssignmentLibraryItems(id)) || []
  } catch (e) {
    // Don't open with empty state — a Save would wipe the real (unloaded) selection.
    console.warn('GetAssignmentLibraryItems failed; not opening prompt editor', e)
    return
  }
  await pickLibraryItems(current, 'Save', async (items) => {
    await App.SetAssignmentLibraryItems(id, items)
  })
}
```

- [ ] **Step 2: Chain the library picker into `solveFolder` (after textbooks)**

In `frontend/src/main.ts`, replace the whole `solveFolder` function with:

```ts
async function solveFolder() {
  const dir = prompt('Folder to solve (absolute path):')
  if (!dir || !dir.trim()) return
  const d = dir.trim()
  await pickTextbooks([], 'Next: Prompts →', async (scopes) => {
    await pickLibraryItems([], 'Solve', async (items) => {
      asgDetail.innerHTML = ''
      asgHeader.innerHTML = '<p class="asg-empty">Preparing…</p>'
      try {
        await App.EnsureIndexedScope(scopes)
        const id = await App.SolveAssignment(d, scopes, items)
        currentAssignmentId = id
        asgItemRows.clear()
        asgStopBtn.classList.remove('hidden')
        await selectAssignment(id)
      } catch (e) {
        asgHeader.innerHTML = ''
        throw e
      }
    })
  })
}
```

- [ ] **Step 3: Add the 📝 Prompts button to the assignment header**

In `frontend/src/main.ts`, in `renderAssignmentHeader`, immediately after the `sub.appendChild(tbBtn)` line (the 📚 Textbooks button), add a sibling 📝 Prompts button reusing the same pill style:

```ts
  const libBtn = document.createElement('button')
  libBtn.className = 'asg-tb-btn'
  libBtn.textContent = '📝 Prompts'
  libBtn.onclick = () => void openAssignmentLibraryEditor()
  sub.appendChild(libBtn)
```

- [ ] **Step 4: Typecheck**

Run: `cd frontend && npx tsc --noEmit`
Expected: no output (the three-arg `SolveAssignment` call now matches; `pickLibraryItems` and the new App methods resolve).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/main.ts
git commit -m "feat(frontend): library prompt picker for assignment solve + editable button"
```

---

### Task 7: Full verification + manual smoke

- [ ] **Step 1: Backend build, vet, tests**

Run: `go build ./... && go vet ./... && go test ./... 2>&1 | tail -25`
Expected: build/vet silent; all packages `ok`.

- [ ] **Step 2: Frontend typecheck**

Run: `cd frontend && npx tsc --noEmit`
Expected: no output.

- [ ] **Step 3: Manual smoke (needs `wails dev`, a real `_json` folder, and at least one library item)**

1. Rebuild: `cd frontend && npm run build`, then `wails dev`.
2. **Solve with a prompt:** Assignments → Solve a folder → choose dir → (textbook picker) Next: Prompts → → (library picker) check an item → Solve. Open an item and confirm the run reflects the library guidance (e.g. the item's instructions visibly influence the answer/notes).
3. **No prompt:** Solve a folder, select no library items → Solve. Confirm items solve normally (base prompt unchanged).
4. **Edit + rerun:** On a solved assignment, click 📝 Prompts, attach an item, Save; Rerun a weak item and confirm the rerun reflects the prompt.
Expected: behavior matches; no console errors.

- [ ] **Step 4: Final commit (only if smoke fixes were needed)**

```bash
git add -A ':!docs/SMOKE.md' ':!frontend/dist' ':!frontend/wailsjs/runtime'
git commit -m "fix(library-prompts): address smoke findings"
```
(Skip if nothing changed.)

---

## Self-Review notes

- **Spec coverage:** column storage (Task 1); `assembleLibraryPreamble` refactor + passthroughs (Task 2); `Options.LibraryPreamble` + prepend + thread/persist items + rerun preamble (Task 3); `SolveAssignment(dir, scopes, libraryItems)` (Task 4); bindings (Task 5); picker + solve chain + 📝 button (Task 6); prepend-before-base placement (Task 3 `withLibraryPreamble`, base last); missing items skipped (Task 2 `skipped`); no indexing (correct — library is plain text).
- **Type consistency:** `[]string` (item filenames) ↔ binding `Array<string>` ↔ TS `string[]`. `SolveAssignment(dir string, scopes []store.TextbookScope, libraryItems []string)`, `Set/GetAssignmentLibraryItems(... []string ...)`, `Run/Start/prepare(..., libraryItems []string)`, `Options.LibraryPreamble string`, `withLibraryPreamble(preamble, system string) string`, `assembleLibraryPreamble(names []string) (string, []string, error)` are consistent across all tasks.
- **Build stays green between tasks:** Task 3 changes `Start`/`Run`/`prepare` signatures and fixes the appapi call sites in the same commit (SolveAssignment passes `nil`); Task 4 changes `SolveAssignment`'s own signature + its one test call site. Rerun gets library support in Task 3; solve in Task 4.
- **No placeholders:** every code step shows complete code; every run step shows the command and expected result.
