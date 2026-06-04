# Assignment Textbook Attachment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user attach whole-book textbook scope to an assignment (at solve time and editable later) so `search_textbook` works during solve and rerun — and suppress the tool entirely when no textbook is attached.

**Architecture:** The assignment is the source of truth for scope, stored JSON-encoded in the existing `assignments.grounding_scope` column. At solve/rerun, `solveItem` copies the scope onto each item's conversation (`SetConversationTextbooks`) and passes the same `chatStoreResolver` chat uses, so resolution is identical to chat. `search_textbook` is registered for an item only when the scope is non-empty. Selected books are indexed (idempotently) via a new scope-based `EnsureIndexedScope`.

**Tech Stack:** Go (SQLite via `database/sql`), Wails v2 bindings, TypeScript + Vite frontend.

**Design spec:** `docs/superpowers/specs/2026-06-04-assignment-textbook-attachment-design.md`

**Branch:** `assignment-textbook-attachment` (already created).

**Assumptions:** Pre-existing uncommitted tree changes (`docs/SMOKE.md`, `frontend/dist`, `frontend/wailsjs/runtime`) remain untouched; never stage them. `store.TextbookScope` already exists in `frontend/wailsjs/go/models.ts` (used by `Set/GetConversationScope`).

---

### Task 1: Store — assignment scope get/set

Persist `[]TextbookScope` as JSON in `assignments.grounding_scope`.

**Files:**
- Modify: `internal/store/assignments.go`
- Test: `internal/store/assignments_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/assignments_test.go`:

```go
func TestAssignmentScope_RoundTrip(t *testing.T) {
	st := openTestStore(t)
	if err := st.CreateAssignment(Assignment{
		ID: "a1", SourceDir: "/d", Title: "t", ManifestHash: "h",
		Model: "m", Status: "in_progress", TotalItems: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// Default: no scope.
	got, err := st.GetAssignmentScope("a1")
	if err != nil || got != nil {
		t.Fatalf("want nil scope, got %v err %v", got, err)
	}

	// Set two whole-book scopes.
	if err := st.SetAssignmentScope("a1", []TextbookScope{{Name: "blaw"}, {Name: "audit"}}); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetAssignmentScope("a1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "blaw" || got[1].Name != "audit" {
		t.Fatalf("unexpected scope: %+v", got)
	}

	// Empty clears it back to NULL → nil.
	if err := st.SetAssignmentScope("a1", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetAssignmentScope("a1"); got != nil {
		t.Fatalf("want nil after clear, got %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestAssignmentScope_RoundTrip -v`
Expected: FAIL — `st.SetAssignmentScope undefined` / `st.GetAssignmentScope undefined`.

- [ ] **Step 3: Add the `encoding/json` import**

In `internal/store/assignments.go`, change the import block from:

```go
import (
	"database/sql"
	"time"
)
```

to:

```go
import (
	"database/sql"
	"encoding/json"
	"time"
)
```

- [ ] **Step 4: Write minimal implementation**

Add to `internal/store/assignments.go` (after `GetAssignmentItem`):

```go
// SetAssignmentScope stores the assignment's textbook scope as JSON in
// grounding_scope. An empty slice clears it (stored as NULL).
func (s *Store) SetAssignmentScope(asgID string, scopes []TextbookScope) error {
	var js string
	if len(scopes) > 0 {
		b, err := json.Marshal(scopes)
		if err != nil {
			return err
		}
		js = string(b)
	}
	_, err := s.db.Exec(`UPDATE assignments SET grounding_scope=?, updated_at=? WHERE id=?`,
		nullIfEmpty(js), time.Now().UnixMilli(), asgID)
	return err
}

// GetAssignmentScope returns the assignment's textbook scope (nil if none).
func (s *Store) GetAssignmentScope(asgID string) ([]TextbookScope, error) {
	var js string
	if err := s.db.QueryRow(
		`SELECT COALESCE(grounding_scope,'') FROM assignments WHERE id=?`, asgID).Scan(&js); err != nil {
		return nil, err
	}
	if js == "" {
		return nil, nil
	}
	var scopes []TextbookScope
	if err := json.Unmarshal([]byte(js), &scopes); err != nil {
		return nil, err
	}
	return scopes, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestAssignmentScope_RoundTrip -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/assignments.go internal/store/assignments_test.go
git commit -m "feat(store): assignment textbook scope in grounding_scope"
```

---

### Task 2: API — `EnsureIndexedScope` + assignment scope passthroughs

Extract the book-indexing core so it can run from a scope (not just a conversation), and expose the assignment scope getters/setters.

**Files:**
- Modify: `internal/appapi/api.go`
- Test: `internal/appapi/ensure_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/appapi/ensure_test.go`:

```go
func TestEnsureIndexedScope_EmptyIsNoop(t *testing.T) {
	a := &API{} // ragAdpt is nil; an empty scope must return before touching it
	if err := a.EnsureIndexedScope(nil); err != nil {
		t.Fatalf("empty scope should be a no-op, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/appapi/ -run TestEnsureIndexedScope_EmptyIsNoop -v`
Expected: FAIL — `a.EnsureIndexedScope undefined`.

- [ ] **Step 3: Refactor `EnsureIndexed` to delegate to a scope-based core**

In `internal/appapi/api.go`, replace the whole `EnsureIndexed` function (currently starts at the `// EnsureIndexed indexes (idempotently)...` comment) with:

```go
// EnsureIndexed indexes (idempotently) every attached book for a conversation,
// emitting "rag:index" progress events. Safe to call before each send.
func (a *API) EnsureIndexed(convID string) error {
	scopes, err := a.st.GetConversationTextbooks(convID)
	if err != nil {
		return provider.NormalizeError(err)
	}
	return a.ensureBooksIndexed(scopes)
}

// EnsureIndexedScope indexes the given textbook scope directly (no conversation).
// Used by the assignment flow, which has no single conversation. Idempotent.
func (a *API) EnsureIndexedScope(scopes []store.TextbookScope) error {
	return a.ensureBooksIndexed(scopes)
}

// ensureBooksIndexed indexes (idempotently) every requested book, emitting
// "rag:index" progress events. Empty scope is a no-op.
func (a *API) ensureBooksIndexed(scopes []store.TextbookScope) error {
	if len(scopes) == 0 {
		return nil
	}
	if a.ragAdpt == nil {
		return provider.AppError{Code: "rag_unavailable", UserMessage: "Textbook indexing is unavailable (RAG not initialized — check OPENAI_API_KEY).", Retryable: false}
	}
	books, err := textbooks.Scan(a.cfg.TextbooksConfig)
	if err != nil {
		return provider.NormalizeError(err)
	}
	var configured, requested []string
	byName := map[string]textbooks.Book{}
	for _, b := range books {
		configured = append(configured, b.Name)
		byName[b.Name] = b
	}
	for _, s := range scopes {
		requested = append(requested, s.Name)
	}
	for _, name := range booksToIndex(configured, requested) {
		b := byName[name]
		// Scan flags an unreadable chapter_dir on the book itself. Indexing
		// would otherwise silently no-op and the user would later get empty
		// retrieval with no explanation.
		if b.Error != "" {
			return provider.AppError{
				Code:        "textbook_unavailable",
				UserMessage: "Textbook " + name + " is unavailable: " + b.Error,
				Retryable:   false,
			}
		}
		_, err := a.ragAdpt.IndexBook(a.ctx, b, func(done, total int) {
			wruntime.EventsEmit(a.ctx, "rag:index", map[string]any{"book": name, "done": done, "total": total})
		})
		if err != nil {
			return provider.NormalizeError(err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Add the assignment scope passthrough methods**

In `internal/appapi/api.go`, add right after `GetConversationScope` (the method ending around line 165):

```go
func (a *API) SetAssignmentScope(asgID string, scopes []store.TextbookScope) error {
	return a.st.SetAssignmentScope(asgID, scopes)
}
func (a *API) GetAssignmentScope(asgID string) ([]store.TextbookScope, error) {
	return a.st.GetAssignmentScope(asgID)
}
```

- [ ] **Step 5: Run test + full package to verify**

Run: `go test ./internal/appapi/ -run 'TestEnsureIndexedScope_EmptyIsNoop|TestBooksToIndex' -v && go test ./internal/appapi/ -count=1`
Expected: PASS (the new test and the existing `TestBooksToIndex`); package `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/appapi/api.go internal/appapi/ensure_test.go
git commit -m "feat(appapi): EnsureIndexedScope + assignment scope passthroughs"
```

---

### Task 3: Orchestrator — thread scope through solve/rerun, gate tool, attach to conversation

Add the resolver option, thread the scope, attach it to each item's conversation, and register `search_textbook` only when a scope is present. Keep the API building by updating its two call sites in the same commit (without changing `SolveAssignment`'s public signature — that's Task 4).

**Files:**
- Modify: `internal/assignment/orchestrator.go`
- Modify: `internal/appapi/api.go` (call-site fixes + resolver injection)
- Test: `internal/assignment/orchestrator_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/assignment/orchestrator_test.go` (add `"time"` and `"github.com/cajundata/starshp_app/internal/tools"` to its imports):

```go
// fakeTool is a minimal tools.Tool stand-in for asserting registry gating.
type fakeTool struct{ name string }

func (f fakeTool) Name() string                { return f.name }
func (f fakeTool) Description() string          { return "fake" }
func (f fakeTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f fakeTool) Execute(context.Context, tools.ExecContext, json.RawMessage) (tools.ExecResult, error) {
	return tools.ExecResult{}, nil
}
func (f fakeTool) Timeout() time.Duration { return 0 }

func hasTool(cat []provider.ToolDef, name string) bool {
	for _, d := range cat {
		if d.Name == name {
			return true
		}
	}
	return false
}

func TestBuildRegistry_GatesSearchToolOnScope(t *testing.T) {
	st := openStore(t)
	orc := New(st, chat.New(st), scriptedFactory(`{}`), Options{
		Model: "m", Concurrency: 1, Grounding: NoGrounding{},
		Emit: func(string, any) {}, SearchTool: fakeTool{name: "search_textbook"},
	})
	q := Question{Type: TypeMultipleChoice, Title: "t", MultipleChoice: &MultipleChoiceBody{Stem: "s"}}

	if !hasTool(orc.buildRegistry(q, true).Catalog(), "search_textbook") {
		t.Error("search_textbook must be registered when scope is present")
	}
	if hasTool(orc.buildRegistry(q, false).Catalog(), "search_textbook") {
		t.Error("search_textbook must NOT be registered when scope is empty")
	}
}

func TestSolve_AttachesScopeToItemConversation(t *testing.T) {
	st := openStore(t)
	dir := tmpAssignmentDir(t)
	orc := newTestOrchestrator(t, st, scriptedFactory(`{"confidence":"low","answerIndex":0}`))

	asgID, err := orc.Run(context.Background(), dir, []store.TextbookScope{{Name: "blaw"}})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := st.ListAssignmentItems(asgID)
	var convID string
	for _, it := range items {
		if it.SourcePath == "001.html" {
			convID = it.ConversationID
		}
	}
	if convID == "" {
		t.Fatal("001.html item has no conversation")
	}
	tb, _ := st.GetConversationTextbooks(convID)
	if len(tb) != 1 || tb[0].Name != "blaw" {
		t.Fatalf("expected blaw attached to item conversation, got %+v", tb)
	}
}

func TestRerunItem_AttachesStoredScope(t *testing.T) {
	st := openStore(t)
	dir := tmpAssignmentDir(t)
	orc := newTestOrchestrator(t, st, scriptedFactory(`{"confidence":"low","answerIndex":0}`))

	asgID, err := orc.Run(context.Background(), dir, nil) // solve with no scope
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAssignmentScope(asgID, []store.TextbookScope{{Name: "blaw"}}); err != nil {
		t.Fatal(err)
	}
	items, _ := st.ListAssignmentItems(asgID)
	var seq int
	for _, it := range items {
		if it.SourcePath == "001.html" {
			seq = it.Seq
		}
	}
	updated, err := orc.RerunItem(context.Background(), asgID, seq)
	if err != nil {
		t.Fatal(err)
	}
	tb, _ := st.GetConversationTextbooks(updated.ConversationID)
	if len(tb) != 1 || tb[0].Name != "blaw" {
		t.Fatalf("rerun should attach stored scope, got %+v", tb)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/assignment/ -run 'TestBuildRegistry_GatesSearchToolOnScope|TestSolve_AttachesScopeToItemConversation|TestRerunItem_AttachesStoredScope' -v`
Expected: FAIL — won't compile: `orc.Run` takes 2 args / `buildRegistry` takes 1 arg / `SetAssignmentScope` etc. (signatures don't exist yet).

- [ ] **Step 3: Add `Resolver` to `Options`**

In `internal/assignment/orchestrator.go`, change the `Options` struct's tool fields. Find:

```go
	SafeMath   tools.Tool
	SearchTool tools.Tool
}
```

Replace with:

```go
	SafeMath   tools.Tool
	SearchTool tools.Tool
	// Resolver resolves a conversation's attached textbooks into book scope for
	// the search_textbook tool. appapi injects chatStoreResolver; nil disables.
	Resolver chat.ScopeResolver
}
```

- [ ] **Step 4: Thread `scopes` through `Run`, `Start`, `prepare`**

Change `Run`:

```go
func (o *Orchestrator) Run(ctx context.Context, dir string, scopes []store.TextbookScope) (string, error) {
	asgID, loaded, prior, err := o.prepare(ctx, dir, scopes)
	if err != nil {
		return "", err
	}
	o.runItems(ctx, dir, asgID, loaded, prior)
	return asgID, nil
}
```

Change `Start`:

```go
func (o *Orchestrator) Start(ctx context.Context, dir string, scopes []store.TextbookScope, onDone func()) (string, error) {
	asgID, loaded, prior, err := o.prepare(ctx, dir, scopes)
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

Change `prepare`'s signature and persist the scope. Change the signature line:

```go
func (o *Orchestrator) prepare(ctx context.Context, dir string, scopes []store.TextbookScope) (string, *Loaded, map[string]store.AssignmentItem, error) {
```

and change its final return block from:

```go
	return asgID, loaded, priorByPath, nil
}
```

to:

```go
	// The solve-time selection is authoritative for this run (create or resume).
	if err := o.st.SetAssignmentScope(asgID, scopes); err != nil {
		return "", nil, nil, err
	}
	return asgID, loaded, priorByPath, nil
}
```

- [ ] **Step 5: Read the scope in `runItems` and pass it to `solveItem`**

In `runItems`, add a scope read just after the `o.opts.Emit("assignment:started", ...)` call:

```go
	scope, _ := o.st.GetAssignmentScope(asgID)
```

and change the goroutine body call from:

```go
			o.solveItem(ctx, dir, asgID, itemID, seq, q)
```

to:

```go
			o.solveItem(ctx, dir, asgID, itemID, seq, q, scope)
```

- [ ] **Step 6: Update `solveItem` — signature, conversation attach, registry gating, resolver**

Change the `solveItem` signature:

```go
func (o *Orchestrator) solveItem(ctx context.Context, dir, asgID, itemID string, seq int, q Question, scope []store.TextbookScope) {
```

After the line `_ = o.st.SetConversationAssignment(conv.ID, asgID)`, add:

```go
	if len(scope) > 0 {
		_ = o.st.SetConversationTextbooks(conv.ID, scope)
	}
```

Change `reg := o.buildRegistry(q)` to:

```go
	reg := o.buildRegistry(q, len(scope) > 0)
```

In the `o.chat.Send(ctx, chat.SendParams{...})` literal, change `Resolver: nil,` to:

```go
		Resolver: o.opts.Resolver,
```

- [ ] **Step 7: Gate the tool in `buildRegistry`**

Change `buildRegistry`:

```go
func (o *Orchestrator) buildRegistry(q Question, hasScope bool) *tools.Registry {
	reg := tools.NewRegistry(30 * time.Second)
	_ = reg.Register(NewSubmitAnswer(q))
	if o.opts.SafeMath != nil {
		_ = reg.Register(o.opts.SafeMath)
	}
	if hasScope && o.opts.SearchTool != nil {
		_ = reg.Register(o.opts.SearchTool)
	}
	return reg
}
```

- [ ] **Step 8: Pass the stored scope through `RerunItem`**

In `RerunItem`, find:

```go
	if err := o.opts.Grounding.Ensure(ctx); err != nil {
		return store.AssignmentItem{}, fmt.Errorf("grounding: %w", err)
	}
	o.solveItem(ctx, asg.SourceDir, asgID, item.ID, seq, q)
```

Replace with:

```go
	if err := o.opts.Grounding.Ensure(ctx); err != nil {
		return store.AssignmentItem{}, fmt.Errorf("grounding: %w", err)
	}
	scope, _ := o.st.GetAssignmentScope(asgID)
	o.solveItem(ctx, asg.SourceDir, asgID, item.ID, seq, q, scope)
```

- [ ] **Step 9: Update existing `Run` call sites in tests**

In `internal/assignment/orchestrator_test.go`, the existing tests call `orc.Run(context.Background(), dir)` / `orc.Run(context.Background(), tmpAssignmentDir(t))`. Add a `nil` scope arg to each existing call (the new tests above already pass the scope). Search the file for `orc.Run(` and ensure every call has three args, e.g.:

```go
	asgID, err := orc.Run(context.Background(), dir, nil)
```

(Applies to `TestRerunItem_OverwritesInPlace` and the batch test `TestOrchestrator...`/`Run`-based tests. Do NOT change the new Task-3 tests, which already pass a scope or `nil`.)

- [ ] **Step 10: Keep `appapi` compiling — fix call sites + inject resolver**

In `internal/appapi/api.go`, in `SolveAssignment`, add the resolver to the options literal and pass `nil` scopes to `Start` (its public signature is unchanged until Task 4). Change the `opts := assignment.Options{...}` block to include `Resolver`:

```go
	opts := assignment.Options{
		Model:       model,
		Concurrency: assignmentConcurrency(),
		Grounding:   assignment.NoGrounding{},
		SafeMath:    safemath.New(),
		SearchTool:  search,
		Resolver:    chatStoreResolver{st: a.st},
		Emit:        a.emit,
	}
```

and change `id, err := orc.Start(cctx, dir, cancel)` to:

```go
	id, err := orc.Start(cctx, dir, nil, cancel)
```

In `RerunAssignmentItem`, add the resolver to its options literal:

```go
	opts := assignment.Options{
		Model:       model,
		Concurrency: 1,
		Grounding:   assignment.NoGrounding{},
		SafeMath:    safemath.New(),
		SearchTool:  search,
		Resolver:    chatStoreResolver{st: a.st},
		Emit:        func(_ string, _ any) {}, // decoupled from batch progress events
	}
```

- [ ] **Step 11: Run tests + full build**

Run: `go build ./... && go test ./internal/assignment/ -count=1 && go test ./internal/appapi/ -count=1`
Expected: build clean; both packages `ok` (new gating/attach/rerun-scope tests pass; existing tests still pass).

- [ ] **Step 12: Commit**

```bash
git add internal/assignment/orchestrator.go internal/assignment/orchestrator_test.go internal/appapi/api.go
git commit -m "feat(assignment): apply textbook scope to item runs; gate search_textbook"
```

---

### Task 4: API — `SolveAssignment(dir, scopes)`

Change the public method to accept and persist the solve-time selection.

**Files:**
- Modify: `internal/appapi/api.go`
- Modify: `internal/appapi/api_test.go`

- [ ] **Step 1: Change the signature and thread the scopes**

In `internal/appapi/api.go`, change `SolveAssignment`'s signature from:

```go
func (a *API) SolveAssignment(dir string) (string, error) {
```

to:

```go
func (a *API) SolveAssignment(dir string, scopes []store.TextbookScope) (string, error) {
```

and change `id, err := orc.Start(cctx, dir, nil, cancel)` to:

```go
	id, err := orc.Start(cctx, dir, scopes, cancel)
```

- [ ] **Step 2: Update the test call site**

In `internal/appapi/api_test.go`, change `id, err := a.SolveAssignment(dir)` to:

```go
	id, err := a.SolveAssignment(dir, nil)
```

- [ ] **Step 3: Build + test**

Run: `go build ./... && go test ./internal/appapi/ -count=1`
Expected: build clean; `ok`.

- [ ] **Step 4: Commit**

```bash
git add internal/appapi/api.go internal/appapi/api_test.go
git commit -m "feat(appapi): SolveAssignment accepts textbook scope"
```

---

### Task 5: Wails bindings

Expose the changed/added methods to the frontend.

**Files:**
- Modify: `frontend/wailsjs/go/appapi/API.js`
- Modify: `frontend/wailsjs/go/appapi/API.d.ts`

- [ ] **Step 1: Update `SolveAssignment` + add new wrappers (JS)**

In `frontend/wailsjs/go/appapi/API.js`, change the existing `SolveAssignment` wrapper to take two args:

```js
export function SolveAssignment(arg1, arg2) {
  return window['go']['appapi']['API']['SolveAssignment'](arg1, arg2);
}
```

And add these three exports (place near the other assignment/textbook entries):

```js
export function EnsureIndexedScope(arg1) {
  return window['go']['appapi']['API']['EnsureIndexedScope'](arg1);
}

export function GetAssignmentScope(arg1) {
  return window['go']['appapi']['API']['GetAssignmentScope'](arg1);
}

export function SetAssignmentScope(arg1, arg2) {
  return window['go']['appapi']['API']['SetAssignmentScope'](arg1, arg2);
}
```

- [ ] **Step 2: Update `SolveAssignment` + add new declarations (d.ts)**

In `frontend/wailsjs/go/appapi/API.d.ts`, change the existing `SolveAssignment` declaration to:

```ts
export function SolveAssignment(arg1:string,arg2:Array<store.TextbookScope>):Promise<string>;
```

And add:

```ts
export function EnsureIndexedScope(arg1:Array<store.TextbookScope>):Promise<void>;

export function GetAssignmentScope(arg1:string):Promise<Array<store.TextbookScope>>;

export function SetAssignmentScope(arg1:string,arg2:Array<store.TextbookScope>):Promise<void>;
```

(`store` is already imported at the top of `API.d.ts`; `store.TextbookScope` already exists in `models.ts`.)

- [ ] **Step 3: Verify the frontend still typechecks**

Run: `cd frontend && npx tsc --noEmit`
Expected: no output. (The existing `solveFolder` calls `App.SolveAssignment(dir.trim())` with one arg — this will now be a TS error, which Task 6 fixes. If `tsc` reports ONLY that one-arg `SolveAssignment` error, that's expected at this step; proceed to Task 6. Any other new error must be fixed here.)

- [ ] **Step 4: Commit**

```bash
git add frontend/wailsjs/go/appapi/API.js frontend/wailsjs/go/appapi/API.d.ts
git commit -m "chore(bindings): textbook scope methods + SolveAssignment scope arg"
```

---

### Task 6: Frontend — textbook picker, solve flow, editable header button

**Files:**
- Modify: `frontend/src/main.ts`
- Modify: `frontend/src/style.css`

- [ ] **Step 1: Add a reusable `pickTextbooks` helper**

In `frontend/src/main.ts`, add this function immediately after the existing `showTextbooks` function (it reuses the same `#tbModal`/`#tbModalInner` DOM):

```ts
// pickTextbooks opens the textbook modal as a reusable picker. It lists books,
// pre-checks `current`, and on confirm calls onConfirm(selected) — closing the
// modal on success, or showing the error inline on failure.
async function pickTextbooks(
  current: any[],
  confirmLabel: string,
  onConfirm: (scopes: any[]) => Promise<void>,
) {
  const inner = $('tbModalInner')
  inner.innerHTML = '<h3>Attach textbooks</h3>'
  $('tbModal').classList.remove('hidden')

  let books: Awaited<ReturnType<typeof App.ListBooks>>
  try {
    books = (await App.ListBooks()) || []
  } catch (e: any) {
    const err = document.createElement('p')
    err.className = 'tb-error'
    err.textContent = `Could not load textbooks: ${e?.userMessage || e}`
    inner.appendChild(err)
    return
  }

  if (books.length === 0) {
    const empty = document.createElement('p')
    empty.className = 'tb-empty'
    empty.textContent = 'No textbooks configured. Add entries to textbooks.yaml in your app directory.'
    inner.appendChild(empty)
  }

  for (const b of books) {
    const label = document.createElement('label')
    const cb = document.createElement('input')
    cb.type = 'checkbox'
    cb.dataset.book = b.name
    cb.checked = current.some((s: any) => s.name === b.name)
    cb.disabled = !!b.error
    label.appendChild(cb)
    const span = document.createElement('span')
    span.textContent = b.error
      ? ` ${b.name} (unavailable: ${b.error})`
      : ` ${b.name} (${b.chapters.length} ch)`
    label.appendChild(span)
    inner.appendChild(label)
  }

  const status = document.createElement('p')
  status.className = 'tb-empty'
  inner.appendChild(status)

  const confirm = document.createElement('button')
  confirm.textContent = confirmLabel
  confirm.onclick = async () => {
    const boxes = inner.querySelectorAll('input[type=checkbox]')
    const scopes: any[] = []
    boxes.forEach((b: any) => { if (b.checked) scopes.push({ name: b.dataset.book, chapters: null }) })
    confirm.disabled = true
    status.className = 'tb-empty'
    status.textContent = scopes.length ? 'Indexing textbooks…' : ''
    try {
      await onConfirm(scopes)
      $('tbModal').classList.add('hidden')
    } catch (e: any) {
      status.className = 'tb-error'
      status.textContent = `Failed: ${e?.userMessage || e}`
      confirm.disabled = false
    }
  }
  inner.appendChild(confirm)
}
```

- [ ] **Step 2: Wire the picker into `solveFolder`**

In `frontend/src/main.ts`, replace the whole `solveFolder` function with:

```ts
async function solveFolder() {
  const dir = prompt('Folder to solve (absolute path):')
  if (!dir || !dir.trim()) return
  const d = dir.trim()
  await pickTextbooks([], 'Solve', async (scopes) => {
    asgDetail.innerHTML = ''
    asgHeader.innerHTML = '<p class="asg-empty">Preparing…</p>'
    await App.EnsureIndexedScope(scopes)
    const id = await App.SolveAssignment(d, scopes)
    currentAssignmentId = id
    asgItemRows.clear()
    asgStopBtn.classList.remove('hidden')
    await selectAssignment(id)
  })
}
```

- [ ] **Step 3: Add the 📚 Textbooks button to the assignment header**

In `frontend/src/main.ts`, in `renderAssignmentHeader`, after the block that appends the status `pill` to `sub` (the line `sub.appendChild(pill)`), add a button into the same `sub` row:

```ts
  const tbBtn = document.createElement('button')
  tbBtn.className = 'asg-tb-btn'
  tbBtn.textContent = '📚 Textbooks'
  tbBtn.onclick = () => {
    const id = currentAssignmentId
    if (!id) return
    void (async () => {
      let current: any[] = []
      try { current = (await App.GetAssignmentScope(id)) || [] } catch {}
      await pickTextbooks(current, 'Save', async (scopes) => {
        await App.EnsureIndexedScope(scopes)
        await App.SetAssignmentScope(id, scopes)
      })
    })()
  }
  sub.appendChild(tbBtn)
```

- [ ] **Step 4: Add styles**

Append to `frontend/src/style.css`:

```css
/* ---- Assignments textbook button ---- */
.asg-tb-btn { margin-left: 10px; background: #202024; color: #cfcfd3; border: 1px solid #34343a; border-radius: 999px; padding: 4px 11px; font-size: 12px; cursor: pointer; }
.asg-tb-btn:hover { color: #e7e7e8; }
```

- [ ] **Step 5: Typecheck**

Run: `cd frontend && npx tsc --noEmit`
Expected: no output (the two-arg `SolveAssignment` call now matches the binding; `pickTextbooks` and the new App methods resolve).

- [ ] **Step 6: Commit**

```bash
git add frontend/src/main.ts frontend/src/style.css
git commit -m "feat(frontend): textbook picker for assignment solve + editable button"
```

---

### Task 7: Full verification + manual smoke

- [ ] **Step 1: Backend build, vet, tests**

Run: `go build ./... && go vet ./... && go test ./... 2>&1 | tail -25`
Expected: build/vet silent; all packages `ok`.

- [ ] **Step 2: Frontend typecheck**

Run: `cd frontend && npx tsc --noEmit`
Expected: no output.

- [ ] **Step 3: Manual smoke (needs `wails dev` + a real `_json` folder and a configured `textbooks.yaml`)**

1. Rebuild: `cd frontend && npm run build`, then `wails dev`.
2. **Solve with a textbook:** Assignments → Solve a folder → choose dir → in the picker, check a book → Solve. Confirm "Indexing textbooks…" then the batch runs; open an item and confirm `search_textbook` calls succeed (no `no_textbooks_attached`).
3. **No textbook:** Solve a different folder, select none → Solve. Confirm items solve with **no** `search_textbook` tool calls at all (tool not offered).
4. **Edit + rerun:** On a solved assignment, click **📚 Textbooks**, attach a book, Save; then Rerun a weak item and confirm it can now search the textbook.
Expected: behavior matches; no console errors.

- [ ] **Step 4: Final commit (only if smoke fixes were needed)**

```bash
git add -A ':!docs/SMOKE.md' ':!frontend/dist' ':!frontend/wailsjs/runtime'
git commit -m "fix(textbook-attachment): address smoke findings"
```
(Skip if nothing changed. Never stage the pre-existing `docs/SMOKE.md` / `dist` / `wailsjs/runtime` churn.)

---

## Self-Review notes

- **Spec coverage:** storage reuse of `grounding_scope` (Task 1); resolution via conversation-attach + injected resolver (Task 3 solveItem + appapi resolver); tool gating on scope-presence (Task 3 buildRegistry + test); solve-time picker + editable button (Task 6); `SolveAssignment(dir, scopes)` (Task 4); `Set/GetAssignmentScope` (Tasks 1–2, 5); whole-book only (`chapters: null` everywhere). **Indexing prerequisite** — discovered during planning, not in the original spec — is covered by `EnsureIndexedScope` (Task 2) called from both frontend entry points (Task 6).
- **Type consistency:** `[]store.TextbookScope` ↔ binding `Array<store.TextbookScope>` ↔ TS `any[]` of `{name, chapters:null}`. `SolveAssignment(dir string, scopes []store.TextbookScope)`, `SetAssignmentScope(asgID string, scopes []store.TextbookScope)`, `GetAssignmentScope(asgID string) ([]store.TextbookScope, error)`, `EnsureIndexedScope(scopes []store.TextbookScope)`, `buildRegistry(q Question, hasScope bool)`, `solveItem(..., scope []store.TextbookScope)`, `Run/Start/prepare(..., scopes []store.TextbookScope)` are consistent across all tasks.
- **Build stays green between tasks:** Task 3 changes `Start`/`Run` signatures and fixes the appapi call sites in the same commit (SolveAssignment passes `nil`); Task 4 then changes `SolveAssignment`'s own signature and its one test call site.
- **No placeholders:** every code step shows complete code; every run step shows the command and expected result.
