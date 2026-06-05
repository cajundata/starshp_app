# Assignment Picker Pre-fill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Pre-fill the solve-time textbook + library pickers from the most recent assignment for the chosen folder, so re-solving the same folder doesn't default to empty and wipe a stored selection.

**Architecture:** A `source_dir`-keyed store lookup (`FindLatestAssignmentBySourceDir`) feeds two best-effort appapi methods (`GetAssignmentScopeForDir`, `GetAssignmentLibraryItemsForDir`) that the frontend calls before opening the pickers, passing the prior selection as each picker's `current`. No new storage; reuses `grounding_scope` / `library_items`.

**Tech Stack:** Go (SQLite via `database/sql`), Wails v2 bindings, TypeScript + Vite frontend.

**Design spec:** `docs/superpowers/specs/2026-06-05-assignment-picker-prefill-design.md`

**Branch:** `assignment-picker-prefill` (already created).

**Assumptions:** Pre-existing uncommitted tree changes (`docs/SMOKE.md`, `frontend/dist`, `frontend/wailsjs/runtime`) remain untouched; never stage them.

---

### Task 1: Store — `FindLatestAssignmentBySourceDir`

**Files:**
- Modify: `internal/store/assignments.go`
- Test: `internal/store/assignments_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/assignments_test.go`:

```go
func TestFindLatestAssignmentBySourceDir(t *testing.T) {
	st := openTestStore(t)

	// No assignment for the dir.
	if _, ok, err := st.FindLatestAssignmentBySourceDir("/nope"); err != nil || ok {
		t.Fatalf("want ok=false err=nil, got ok=%v err=%v", ok, err)
	}

	// Two assignments for the same dir; the most-recent created_at wins.
	if err := st.CreateAssignment(Assignment{
		ID: "old", SourceDir: "/d", Title: "t", ManifestHash: "h1",
		Model: "m", Status: "completed", TotalItems: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAssignment(Assignment{
		ID: "new", SourceDir: "/d", Title: "t", ManifestHash: "h2",
		Model: "m", Status: "completed", TotalItems: 1,
	}); err != nil {
		t.Fatal(err)
	}
	// Force deterministic ordering (CreateAssignment stamps now(), which can
	// collide within the same millisecond).
	if _, err := st.db.Exec(`UPDATE assignments SET created_at=? WHERE id=?`, int64(1000), "old"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE assignments SET created_at=? WHERE id=?`, int64(2000), "new"); err != nil {
		t.Fatal(err)
	}

	got, ok, err := st.FindLatestAssignmentBySourceDir("/d")
	if err != nil || !ok {
		t.Fatalf("want found, got ok=%v err=%v", ok, err)
	}
	if got.ID != "new" {
		t.Fatalf("want latest 'new', got %q", got.ID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestFindLatestAssignmentBySourceDir -v`
Expected: FAIL — `st.FindLatestAssignmentBySourceDir undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/store/assignments.go` immediately after `FindAssignmentByManifest`:

```go
// FindLatestAssignmentBySourceDir returns the most recently created assignment
// for a source dir (any manifest hash), or ok=false if none. Used to pre-fill
// the solve-time pickers.
func (s *Store) FindLatestAssignmentBySourceDir(sourceDir string) (Assignment, bool, error) {
	var a Assignment
	err := s.db.QueryRow(
		`SELECT id, source_dir, title, manifest_hash, model,
                COALESCE(grounding_scope,''), status, total_items, created_at, updated_at
           FROM assignments WHERE source_dir=?
          ORDER BY created_at DESC LIMIT 1`, sourceDir).Scan(
		&a.ID, &a.SourceDir, &a.Title, &a.ManifestHash, &a.Model,
		&a.GroundingScope, &a.Status, &a.TotalItems, &a.CreatedAt, &a.UpdatedAt)
	if err == sql.ErrNoRows {
		return Assignment{}, false, nil
	}
	if err != nil {
		return Assignment{}, false, err
	}
	return a, true, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestFindLatestAssignmentBySourceDir -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/assignments.go internal/store/assignments_test.go
git commit -m "feat(store): FindLatestAssignmentBySourceDir"
```

---

### Task 2: appapi — `…ForDir` pre-fill lookups

**Files:**
- Modify: `internal/appapi/api.go`
- Test: `internal/appapi/api_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/appapi/api_test.go` (its imports already include `store`, `path/filepath`, `testing`):

```go
func TestGetSelectionForDir(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &API{st: st}

	// No assignment for the dir → empty, no error.
	if sc, err := a.GetAssignmentScopeForDir("/d"); err != nil || sc != nil {
		t.Fatalf("want nil scope, got %v err %v", sc, err)
	}
	if it, err := a.GetAssignmentLibraryItemsForDir("/d"); err != nil || it != nil {
		t.Fatalf("want nil items, got %v err %v", it, err)
	}

	// Create an assignment for /d with a scope + library items.
	if err := st.CreateAssignment(store.Assignment{
		ID: "a1", SourceDir: "/d", Title: "t", ManifestHash: "h",
		Model: "m", Status: "completed", TotalItems: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAssignmentScope("a1", []store.TextbookScope{{Name: "blaw"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAssignmentLibraryItems("a1", []string{"tone.md"}); err != nil {
		t.Fatal(err)
	}

	sc, err := a.GetAssignmentScopeForDir("/d")
	if err != nil || len(sc) != 1 || sc[0].Name != "blaw" {
		t.Fatalf("scope = %+v err %v", sc, err)
	}
	it, err := a.GetAssignmentLibraryItemsForDir("/d")
	if err != nil || len(it) != 1 || it[0] != "tone.md" {
		t.Fatalf("items = %+v err %v", it, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/appapi/ -run TestGetSelectionForDir -v`
Expected: FAIL — `a.GetAssignmentScopeForDir undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/appapi/api.go` immediately after `RerunAssignmentItem` (before `CancelAssignment`):

```go
// latestAssignmentIDForDir returns the id of the most recent assignment for dir.
// Best-effort: any lookup error is swallowed into found=false, so solve-time
// pre-fill never blocks solving.
func (a *API) latestAssignmentIDForDir(dir string) (string, bool) {
	asg, ok, err := a.st.FindLatestAssignmentBySourceDir(dir)
	if err != nil || !ok {
		return "", false
	}
	return asg.ID, true
}

// GetAssignmentScopeForDir returns the textbook scope of the most recent
// assignment for dir (nil if none) — used to pre-fill the solve-time picker.
func (a *API) GetAssignmentScopeForDir(dir string) ([]store.TextbookScope, error) {
	id, ok := a.latestAssignmentIDForDir(dir)
	if !ok {
		return nil, nil
	}
	return a.st.GetAssignmentScope(id)
}

// GetAssignmentLibraryItemsForDir returns the library item selection of the most
// recent assignment for dir (nil if none) — used to pre-fill the solve-time picker.
func (a *API) GetAssignmentLibraryItemsForDir(dir string) ([]string, error) {
	id, ok := a.latestAssignmentIDForDir(dir)
	if !ok {
		return nil, nil
	}
	return a.st.GetAssignmentLibraryItems(id)
}
```

- [ ] **Step 4: Run test + package**

Run: `go test ./internal/appapi/ -run TestGetSelectionForDir -v && go test ./internal/appapi/ -count=1`
Expected: PASS; package `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/appapi/api.go internal/appapi/api_test.go
git commit -m "feat(appapi): GetAssignment{Scope,LibraryItems}ForDir pre-fill lookups"
```

---

### Task 3: Wails bindings

**Files:**
- Modify: `frontend/wailsjs/go/appapi/API.js`
- Modify: `frontend/wailsjs/go/appapi/API.d.ts`

- [ ] **Step 1: Add the JS wrappers**

In `frontend/wailsjs/go/appapi/API.js`, add these two exports (near the other assignment entries):

```js
export function GetAssignmentLibraryItemsForDir(arg1) {
  return window['go']['appapi']['API']['GetAssignmentLibraryItemsForDir'](arg1);
}

export function GetAssignmentScopeForDir(arg1) {
  return window['go']['appapi']['API']['GetAssignmentScopeForDir'](arg1);
}
```

- [ ] **Step 2: Add the TS declarations**

In `frontend/wailsjs/go/appapi/API.d.ts`, add:

```ts
export function GetAssignmentLibraryItemsForDir(arg1:string):Promise<Array<string>>;

export function GetAssignmentScopeForDir(arg1:string):Promise<Array<store.TextbookScope>>;
```

(`store` is already imported at the top of `API.d.ts`.)

- [ ] **Step 3: Verify the frontend typechecks**

Run: `cd frontend && npx tsc --noEmit`
Expected: no output (the new exports are just added; nothing references them yet).

- [ ] **Step 4: Commit**

```bash
git add frontend/wailsjs/go/appapi/API.js frontend/wailsjs/go/appapi/API.d.ts
git commit -m "chore(bindings): GetAssignment{Scope,LibraryItems}ForDir"
```

---

### Task 4: Frontend — pre-fill the pickers in `solveFolder`

**Files:**
- Modify: `frontend/src/main.ts`

- [ ] **Step 1: Replace `solveFolder` to pre-fetch and pre-fill**

In `frontend/src/main.ts`, replace the whole `solveFolder` function with:

```ts
async function solveFolder() {
  const dir = prompt('Folder to solve (absolute path):')
  if (!dir || !dir.trim()) return
  const d = dir.trim()
  // Pre-fill the pickers from the most recent assignment for this folder so a
  // re-solve doesn't default to empty (and wipe a stored selection). Best-effort.
  let preScopes: any[] = []
  let preItems: string[] = []
  try {
    preScopes = (await App.GetAssignmentScopeForDir(d)) || []
    preItems = (await App.GetAssignmentLibraryItemsForDir(d)) || []
  } catch { /* default to empty pre-fill */ }
  await pickTextbooks(preScopes, 'Next: Prompts →', async (scopes) => {
    await pickLibraryItems(preItems, 'Solve', async (items) => {
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

- [ ] **Step 2: Typecheck**

Run: `cd frontend && npx tsc --noEmit`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/main.ts
git commit -m "feat(frontend): pre-fill solve-time pickers from prior assignment selection"
```

---

### Task 5: Full verification + manual smoke

- [ ] **Step 1: Backend build, vet, tests**

Run: `go build ./... && go vet ./... && go test ./... 2>&1 | tail -25`
Expected: build/vet silent; all packages `ok`.

- [ ] **Step 2: Frontend typecheck**

Run: `cd frontend && npx tsc --noEmit`
Expected: no output.

- [ ] **Step 3: Manual smoke (needs `wails dev` + a real `_json` folder)**

1. Rebuild: `cd frontend && npm run build`, then `wails dev`.
2. Solve a folder; in the pickers, select a textbook and a library prompt; finish the solve.
3. Solve the **same folder** again. Confirm the textbook picker opens with the previously-selected book **checked**, and (after "Next: Prompts →") the library picker opens with the previously-selected item **checked**.
4. Confirm both — the selection is preserved (not wiped), and solving proceeds normally.
5. Solve a **brand-new** folder (never solved) → both pickers open empty (no regression).
Expected: behavior matches; no console errors.

- [ ] **Step 4: Final commit (only if smoke fixes were needed)**

```bash
git add -A ':!docs/SMOKE.md' ':!frontend/dist' ':!frontend/wailsjs/runtime'
git commit -m "fix(picker-prefill): address smoke findings"
```
(Skip if nothing changed.)

---

## Self-Review notes

- **Spec coverage:** `FindLatestAssignmentBySourceDir` (Task 1); `GetAssignmentScopeForDir`/`GetAssignmentLibraryItemsForDir` + best-effort helper (Task 2); bindings (Task 3); `solveFolder` pre-fill (Task 4); error handling = empty pre-fill, never blocks (helper swallows error → Task 2; frontend `catch` → Task 4); dir-only match with latest `created_at` (Task 1 query + test). No new storage (reuses existing columns/methods). Editable 📚/📝 buttons untouched (they already pre-fill).
- **Type consistency:** `FindLatestAssignmentBySourceDir(sourceDir string) (Assignment, bool, error)`; `GetAssignmentScopeForDir(dir string) ([]store.TextbookScope, error)` ↔ binding `Array<store.TextbookScope>` ↔ TS `any[]`/`preScopes`; `GetAssignmentLibraryItemsForDir(dir string) ([]string, error)` ↔ `Array<string>` ↔ `string[]`/`preItems`. The `…ForDir` names are identical across store-helper usage, appapi, bindings, and the `solveFolder` calls.
- **No placeholders:** every code step shows complete code; every run step shows command + expected result.
