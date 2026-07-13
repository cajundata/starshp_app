# Persona Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Starshp's accounting surface with a team of file-backed personas — each with its own model, color, and system prompt — and color-code every assistant bubble by persona with a muted model chip.

**Architecture:** A new `internal/persona` package loads markdown-with-frontmatter files from `<app-dir>/personas/` into a registry, mirroring `internal/provider`'s `models.yaml` registry. `internal/appapi` resolves a persona ID into a model, a system prompt, and a tool subset before calling `chat.Send`. The persona ID is recorded on the `runs` row next to the existing `provider`/`model` columns, emitted on `chat:run_started`, and joined into the replay query — so a live bubble and a replayed bubble derive attribution from the same two fields and cannot diverge.

**Tech Stack:** Go 1.26, Wails v2.12, SQLite (modernc.org/sqlite), `gopkg.in/yaml.v3`, vanilla TypeScript + Vite 3, plain global CSS.

**Spec:** `docs/superpowers/specs/2026-07-13-persona-foundation-design.md`

## Global Constraints

- **Go module:** `github.com/cajundata/starshp_app`. Go 1.26.
- **`internal/appapi` is the error-normalization boundary.** Every error crossing it is a `provider.AppError`. Never return a bare `error` from a Wails-bound method.
- **`internal/rag` is VERBATIM / DO-NOT-MODIFY** (see `internal/rag/REUSED.md`). No task touches it.
- **Two SQLite files, never merged:** `app.db` (chat/ideas) and `rag.db` (vector index).
- **The frontend has no framework and no test harness.** Vanilla TS, imperative DOM, one global `frontend/src/style.css`. Frontend verification is `npm run build` (which runs `tsc && vite build`) plus manual smoke steps.
- **Wails bindings in `frontend/wailsjs/` are generated and committed.** After changing any bound method signature, regenerate with `wails generate module`.
- **Persona colors must reach a WCAG contrast ratio of at least 4.5 against the assistant-bubble background `#1d1d20`.**
- **Bubble background:** `#1d1d20`. **Bubble border:** `#2b2b30`. **Accent orange:** `#ff5714`. **Muted text:** `#6f6f76`.
- Every task ends with a commit. Run `go build ./... && go test ./...` before every Go commit.

---

### Task 1: Remove the accounting surface from the Go backend

The assignment solver is a *client* of `chat.Service`, not part of it — `internal/chat` and `internal/provider` contain zero accounting references. This deletion therefore cannot break the chat loop. Doing it first means the persona work in Tasks 5–7 is written once instead of twice (the frontend has a duplicated copy of the run-bubble logic in the assignment pane, deleted in Task 2).

**Files:**
- Delete: `internal/assignment/` (entire directory, including `testdata/`)
- Delete: `internal/store/assignments.go`, `internal/store/assignments_test.go`
- Delete: `internal/store/answers.go`, `internal/store/answers_test.go` (`GetSubmittedAnswer` reads the `submit_answer` tool call; that tool lived only in `internal/assignment`)
- Modify: `internal/store/schema.go` — remove the `assignments` / `assignment_items` DDL and their indexes
- Modify: `internal/store/migrate.go:49-66` — replace the `assignment_id` / `library_items` column-add blocks with drops
- Modify: `internal/store/conversations.go:46` — remove `WHERE assignment_id IS NULL`
- Modify: `internal/appapi/api.go` — remove all assignment methods and fields
- Modify: `internal/appapi/library.go:92-103` — remove `SetAssignmentLibraryItems` / `GetAssignmentLibraryItems`
- Modify: `internal/chat/chat.go:22-27` — remove the stale coursework comment
- Rewrite: `internal/eval/testdata/fixtures/definition-from-grounding.yaml`, `multi-hop-search.yaml`, `no-textbooks-attached.yaml`
- Check: `internal/appapi/api_test.go`, `api_compat_test.go`, `ensure_test.go`, `library_test.go`, `internal/store/migrate_test.go` for assignment references

**Interfaces:**
- Consumes: nothing.
- Produces: a `store.Store` with no assignment methods; an `appapi.API` whose only remaining assignment-shaped field is gone. Task 4 edits `schema.go` and `migrate.go` again — this task must leave both compiling and passing.

**Expected intermediate state:** this task's commit deletes Go methods that `frontend/src/main.ts` still calls (`App.SolveAssignment` and friends). The frontend keeps *compiling*, because `frontend/wailsjs/` holds committed, now-stale bindings — but those calls would fail at runtime. That is fine and expected; Task 2 deletes the callers and regenerates the bindings. Do not try to fix the frontend here.

- [ ] **Step 1: Confirm nothing outside the assignment surface depends on it**

Run:
```bash
grep -rn "internal/assignment" --include=*.go .
grep -rln "GetSubmittedAnswer\|store.Assignment\|ListAssignments\|SetAssignmentScope" --include=*.go .
```
Expected: `internal/assignment` is imported only by `internal/appapi/api.go`. The store methods appear only in `internal/store/assignments.go`, `internal/store/answers.go`, their tests, `internal/appapi/api.go`, `internal/appapi/library.go`, and `internal/assignment/orchestrator.go`. If anything else appears, stop and report — the plan's coupling assumption is wrong.

- [ ] **Step 2: Delete the assignment package and its store layer**

```bash
rm -rf internal/assignment
rm internal/store/assignments.go internal/store/assignments_test.go
rm internal/store/answers.go internal/store/answers_test.go
```

- [ ] **Step 3: Remove the assignment DDL from the schema**

In `internal/store/schema.go`, delete this entire block (currently lines 70–106) — the two `CREATE TABLE` statements and the two indexes:

```sql
CREATE TABLE IF NOT EXISTS assignments ( ... );
CREATE TABLE IF NOT EXISTS assignment_items ( ... );
CREATE INDEX IF NOT EXISTS assignment_items_assignment
  ON assignment_items(assignment_id, seq);
CREATE INDEX IF NOT EXISTS assignment_items_run ON assignment_items(run_id);
```

Leave `conversations`, `conversation_events`, `runs`, the `ideas`/pipeline tables, and every other index untouched.

- [ ] **Step 4: Turn the migration's assignment column-adds into drops**

In `internal/store/migrate.go`, replace the block at lines 49–66 (the `assignment_id` add and the `assignments.library_items` add) with:

```go
	// Accounting removal: the assignment surface is gone. Drop its tables and the
	// conversations column that pointed at them. Idempotent — a fresh database
	// never had them.
	if _, err := db.Exec(`DROP TABLE IF EXISTS assignment_items`); err != nil {
		return err
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS assignments`); err != nil {
		return err
	}
	has, err = columnExists(db, "conversations", "assignment_id")
	if err != nil {
		return err
	}
	if has {
		if _, err := db.Exec(`ALTER TABLE conversations DROP COLUMN assignment_id`); err != nil {
			return err
		}
	}
```

`ALTER TABLE ... DROP COLUMN` is already used in this file (line 16, for `preset_id`), so the SQLite build supports it.

- [ ] **Step 5: Drop the assignment filter from ListConversations**

In `internal/store/conversations.go:46`, change:

```go
	rows, err := s.db.Query(`SELECT id,title,created_at,updated_at,COALESCE(pinned_model,'') FROM conversations WHERE assignment_id IS NULL ORDER BY updated_at DESC`)
```

to:

```go
	rows, err := s.db.Query(`SELECT id,title,created_at,updated_at,COALESCE(pinned_model,'') FROM conversations ORDER BY updated_at DESC`)
```

- [ ] **Step 6: Strip the assignment API from appapi**

In `internal/appapi/api.go`:

Remove the `"github.com/cajundata/starshp_app/internal/assignment"` import and the `"log/slog"`, `"os"`, `"fmt"` imports if they become unused after the deletions below.

From the `API` struct, remove the fields `assignmentFactory`, `asgCancel`, `rerunning`.

From `NewAPI`, remove the `a.assignmentFactory = func(modelID string) ... }` assignment block.

Delete these methods entirely: `SetAssignmentScope`, `GetAssignmentScope`, `SolveAssignment`, `RerunAssignmentItem`, `latestAssignmentIDForDir`, `GetAssignmentScopeForDir`, `GetAssignmentLibraryItemsForDir`, `CancelAssignment`, `ListAssignments`, `GetAssignment`, `ListAssignmentItems`, `defaultModelID`, `assignmentConcurrency`.

`EnsureIndexedScope` (line 488) exists only for the assignment flow — delete it too. `ensureBooksIndexed` stays; `EnsureIndexed` still calls it.

In `internal/appapi/library.go`, delete `SetAssignmentLibraryItems` and `GetAssignmentLibraryItems` (lines 92–103).

Keep `assembleLibraryPreamble` — Task 6 uses it for persona library items.

- [ ] **Step 7: Remove the stale coursework comment**

In `internal/chat/chat.go`, replace the comment at lines 22–26 with:

```go
// MaxIterationsDefault caps the number of tool-dispatch rounds in the agentic
// loop. STARSHP_MAX_TOOL_ITERATIONS overrides it. Runs have been observed making
// 9 distinct, productive tool calls, so the cap must comfortably exceed that.
// When the cap is reached the loop does not error — it forces one final
// tool-free answer (see finalizeWithoutTools).
```

- [ ] **Step 8: Replace the accounting eval fixtures**

`internal/eval/quality_test.go:44` calls `t.Fatal("no quality fixtures found under testdata/fixtures")` when the directory is empty, so the fixtures cannot simply be deleted. Three of the five are accounting-flavoured (they all turn on the "realization principle"); the two arithmetic ones are domain-neutral and stay as they are.

Rewrite `internal/eval/testdata/fixtures/definition-from-grounding.yaml`:

```yaml
name: definition-from-grounding
prompt: |
  Define opportunity cost in one sentence. Do not call any tools; the
  definition should come from your training knowledge.
expected_substrings:
  - "alternative"
expected_min_tool_calls: 0
expected_tools_called_at_least_once: []
max_iterations: 2
```

Rewrite `internal/eval/testdata/fixtures/no-textbooks-attached.yaml`:

```yaml
name: no-textbooks-attached
prompt: |
  Search the attached reference books for the definition of opportunity
  cost and cite the passage. (No books are attached, so the tool will
  return no_textbooks_attached — answer from background knowledge.)
expected_substrings:
  - "opportunity cost"
expected_min_tool_calls: 0
expected_tools_called_at_least_once: []
max_iterations: 3
```

Rewrite `internal/eval/testdata/fixtures/multi-hop-search.yaml`:

```yaml
name: multi-hop-search
prompt: |
  Explain how fixed costs relate to operating leverage. If your initial
  context is insufficient, call search_textbook to find a relevant
  passage. (No books are attached in this fixture, so search_textbook is
  expected to return no_textbooks_attached and you should answer from
  background knowledge.)
expected_substrings:
  - "fixed"
  - "leverage"
expected_min_tool_calls: 0
expected_tools_called_at_least_once: []
max_iterations: 3
```

Leave `arithmetic-self-correction.yaml` and `percent-of-subtotal.yaml` alone — they exercise `safe_math` and contain no domain content.

- [ ] **Step 9: Fix any tests that referenced the assignment surface**

Run:
```bash
go build ./... && go test ./...
```
Expected: compile errors or failures only in test files that named assignment types. Delete those test functions (not the whole file, unless the file is entirely assignment-focused). `internal/appapi/api_compat_test.go` likely asserts the bound-method surface — remove the assignment entries from its expected list.

- [ ] **Step 10: Verify the whole suite passes**

Run:
```bash
go build ./... && go test ./...
```
Expected: PASS across every package. No `assignment` symbol remains:
```bash
grep -rni "assignment" --include=*.go . | grep -v "_test.go"
```
Expected: no output.

- [ ] **Step 11: Commit**

```bash
git add -A
git commit -m "refactor: remove the accounting assignment surface from the backend

The accounting courses are finished. internal/assignment was a client of
chat.Service, not part of it, so this deletion does not touch the agentic
loop. Drops the assignments tables, the assignment_id column, and the
accounting eval fixtures (rewritten domain-neutral so the quality harness
keeps its coverage)."
```

---

### Task 2: Remove the accounting surface from the frontend

**Files:**
- Modify: `frontend/index.html` — remove `#asgBtn` and the entire `#asgView` block
- Modify: `frontend/src/main.ts` — remove the assignments view, its event handlers, its wiring, and the assignment-only pickers
- Modify: `frontend/src/style.css` — remove the three assignment style blocks
- Regenerate: `frontend/wailsjs/go/appapi/API.js`, `API.d.ts`, `frontend/wailsjs/go/models.ts`

**Interfaces:**
- Consumes: Task 1's Go API (no assignment methods remain, so the regenerated bindings will not contain them).
- Produces: a `main.ts` with exactly one copy of the run-bubble logic (`ensureRunBubble` and friends, lines 95–223). Tasks 7 modifies that copy.

- [ ] **Step 1: Remove the assignments markup**

In `frontend/index.html`, delete line 8:
```html
      <button id="asgBtn">📂 Assignments</button>
```
and delete the whole `#asgView` block (lines 29–44), from `<div id="asgView" class="hidden">` through its closing `</div>`.

- [ ] **Step 2: Remove the assignments code from main.ts**

In `frontend/src/main.ts`, delete:

- `openAssignmentTextbookEditor` (lines 593–610) and `openAssignmentLibraryEditor` (lines 685–700).
- The entire `// ---- Assignments review view ----` section (lines 816 through the end of `rerunSelectedItem`, ~line 1220): the `asgView`/`asgHeader`/`asgItems`/`asgDetail`/`asgStopBtn` element handles, `currentAssignmentId`, `selectedItem`, `currentAssignmentStatus`, `RERUNNABLE_STATUSES`, `asgItemRows`, `asgProgressDone`, `asgProgressTotal`, and every function in it — `openAssignments`, `closeAssignments`, `loadAssignmentsHome`, `solveFolder`, `selectAssignment`, `renderAssignmentHeader`, `updateProgress`, `confidenceClass`, `renderItemRow`, `applyItemDecorations`, `flagCountFromJSON`, `toolInputText`, **`openItemDetail`**, `itemRerunnable`, `renderDetailHeader`, `rerunSelectedItem`.
- The `// ---- Live progress events ----` section: the `EventsOn('assignment:started' …)`, `'assignment:item_started'`, `'assignment:item_done'`, `'assignment:completed'`, `'assignment:cancelled'` handlers (lines ~1222–1280).
- The wiring at lines 1282–1287: `$('asgBtn').onclick`, `$('asgBack').onclick`, `$('asgSolveBtn').onclick`, `asgStopBtn.onclick`.
- The now-unused `import { store } from '../wailsjs/go/models'` on line 3, if nothing else references `store.` (the pipeline module has its own imports).

`openItemDetail` (lines 1072–1161) is the duplicated run-bubble builder. Deleting it is the point of sequencing this task before the persona work.

Keep `pickTextbooks` and `pickLibraryItems` — they are generic pickers, and `showTextbooks` / `openLibraryPanel` still use the modals.

- [ ] **Step 3: Remove the assignment CSS**

In `frontend/src/style.css`, delete these three blocks:
- `/* ---- Assignments review view ---- */` (lines 77–119)
- `/* ---- Assignments rerun ---- */` (lines 121–126)
- `/* ---- Assignments textbook button ---- */` (lines 128–130)

Keep everything else, including the `/* ---- Agentic run bubbles ---- */` block — Task 7 extends it.

- [ ] **Step 4: Regenerate the Wails bindings**

Run:
```bash
wails generate module
```
Expected: `frontend/wailsjs/go/appapi/API.js`, `API.d.ts`, and `models.ts` are rewritten with no `SolveAssignment`, `ListAssignments`, `RerunAssignmentItem`, `GetAssignment`, `ListAssignmentItems`, `CancelAssignment`, `SetAssignmentScope`, `GetAssignmentScope`, `EnsureIndexedScope`, `GetAssignmentScopeForDir`, `GetAssignmentLibraryItemsForDir`, `SetAssignmentLibraryItems`, or `GetAssignmentLibraryItems`, and no `Assignment` / `AssignmentItem` model.

Verify:
```bash
grep -i assignment frontend/wailsjs/go/appapi/API.d.ts frontend/wailsjs/go/models.ts
```
Expected: no output.

- [ ] **Step 5: Verify the frontend compiles**

Run:
```bash
cd frontend && npm run build
```
Expected: `tsc` reports no errors and `vite build` writes `dist/`. A TS error naming an assignment symbol means a reference was missed — remove it.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(frontend): remove the assignments view

Deletes the assignment view, its live-progress handlers, and openItemDetail —
a second, duplicated copy of the run-bubble building logic. The chat view's
copy is now the only one, so the persona bubble changes land once."
```

---

### Task 3: The `internal/persona` package

Pure, no wiring. A registry of markdown-with-frontmatter files, with validation, deterministic color assignment, and a contrast-verified palette.

**Files:**
- Create: `internal/persona/persona.go`
- Create: `internal/persona/color.go`
- Create: `internal/persona/seed.go`
- Create: `internal/persona/persona_test.go`
- Create: `internal/persona/color_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Persona struct { ID, Name, Model, Color string; Tools, Library []string; Prompt string }` — `Prompt` is `json:"-"`.
  - `type Issue struct { File, Reason string }`
  - `type Registry struct { Personas []Persona; Issues []Issue }`
  - `func LoadRegistry(dir string, knownModels, knownTools []string) Registry` — never returns an error; an unreadable directory becomes an `Issue`. Never writes.
  - `func (r Registry) ByID(id string) (Persona, bool)`
  - `func Seed(dir, defaultModelID string) error` — writes `assistant.md` **only if `dir` does not exist**. No-op if `defaultModelID` is empty.
  - `func ContrastRatio(hexA, hexB string) (float64, error)` — WCAG 2.x relative-luminance ratio.
  - `const BubbleBG = "#1d1d20"`

- [ ] **Step 1: Write the failing registry tests**

Create `internal/persona/persona_test.go`:

```go
package persona

import (
	"os"
	"path/filepath"
	"testing"
)

var (
	models = []string{"claude-opus-4-8", "gpt-5"}
	toolset = []string{"safe_math", "search_textbook"}
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRegistryValid(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "scout.md", `---
name: Scout
model: claude-opus-4-8
color: "#4fb3ff"
tools: [safe_math]
library: [style-guide]
---
You are Scout.
You find opportunities.
`)
	r := LoadRegistry(dir, models, toolset)
	if len(r.Issues) != 0 {
		t.Fatalf("unexpected issues: %v", r.Issues)
	}
	if len(r.Personas) != 1 {
		t.Fatalf("want 1 persona, got %d", len(r.Personas))
	}
	p := r.Personas[0]
	if p.ID != "scout" {
		t.Errorf("ID = %q, want scout", p.ID)
	}
	if p.Name != "Scout" {
		t.Errorf("Name = %q, want Scout", p.Name)
	}
	if p.Model != "claude-opus-4-8" {
		t.Errorf("Model = %q", p.Model)
	}
	if p.Color != "#4fb3ff" {
		t.Errorf("Color = %q", p.Color)
	}
	if len(p.Tools) != 1 || p.Tools[0] != "safe_math" {
		t.Errorf("Tools = %v", p.Tools)
	}
	if len(p.Library) != 1 || p.Library[0] != "style-guide" {
		t.Errorf("Library = %v", p.Library)
	}
	if p.Prompt != "You are Scout.\nYou find opportunities." {
		t.Errorf("Prompt = %q", p.Prompt)
	}
	got, ok := r.ByID("scout")
	if !ok || got.ID != "scout" {
		t.Errorf("ByID(scout) = %v, %v", got, ok)
	}
}

func TestLoadRegistryRejections(t *testing.T) {
	cases := []struct{ file, body, wantReason string }{
		{"nomodel.md", "---\nname: X\nmodel: nope-9\n---\nbody\n", "unknown model"},
		{"badtool.md", "---\nname: X\nmodel: gpt-5\ntools: [teleport]\n---\nbody\n", "unknown tool"},
		{"badcolor.md", "---\nname: X\nmodel: gpt-5\ncolor: \"blurple\"\n---\nbody\n", "invalid color"},
		{"noname.md", "---\nmodel: gpt-5\n---\nbody\n", "name is required"},
		{"nofm.md", "no frontmatter here\n", "missing frontmatter"},
		{"Bad Name.md", "---\nname: X\nmodel: gpt-5\n---\nbody\n", "invalid persona id"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, c.file, c.body)
			r := LoadRegistry(dir, models, toolset)
			if len(r.Personas) != 0 {
				t.Fatalf("want persona rejected, got %v", r.Personas)
			}
			if len(r.Issues) != 1 {
				t.Fatalf("want 1 issue, got %v", r.Issues)
			}
			if r.Issues[0].Reason != c.wantReason {
				t.Errorf("Reason = %q, want %q", r.Issues[0].Reason, c.wantReason)
			}
		})
	}
}

func TestLoadRegistryOneBadFileDoesNotDisableTheRest(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "good.md", "---\nname: Good\nmodel: gpt-5\n---\nbody\n")
	write(t, dir, "bad.md", "---\nname: Bad\nmodel: nope-9\n---\nbody\n")
	r := LoadRegistry(dir, models, toolset)
	if len(r.Personas) != 1 || r.Personas[0].ID != "good" {
		t.Fatalf("want only good to load, got %v", r.Personas)
	}
	if len(r.Issues) != 1 || r.Issues[0].File != "bad.md" {
		t.Fatalf("want one issue for bad.md, got %v", r.Issues)
	}
}

func TestLoadRegistryMissingDirIsEmptyNotAnError(t *testing.T) {
	r := LoadRegistry(filepath.Join(t.TempDir(), "absent"), models, toolset)
	if len(r.Personas) != 0 || len(r.Issues) != 0 {
		t.Fatalf("want empty registry with no issues, got %+v", r)
	}
}

func TestLoadRegistryAssignsColorWhenOmitted(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "skeptic.md", "---\nname: Skeptic\nmodel: gpt-5\n---\nbody\n")
	r := LoadRegistry(dir, models, toolset)
	if len(r.Personas) != 1 {
		t.Fatalf("want 1 persona, got %v", r.Issues)
	}
	c := r.Personas[0].Color
	if c == "" {
		t.Fatal("color was not auto-assigned")
	}
	// Deterministic: a second load of the same ID yields the same color.
	r2 := LoadRegistry(dir, models, toolset)
	if r2.Personas[0].Color != c {
		t.Errorf("color not deterministic: %q then %q", c, r2.Personas[0].Color)
	}
}

func TestSeedWritesAssistantOnlyWhenDirAbsent(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "personas")

	if err := Seed(dir, "gpt-5"); err != nil {
		t.Fatal(err)
	}
	r := LoadRegistry(dir, models, toolset)
	if len(r.Personas) != 1 || r.Personas[0].ID != "assistant" {
		t.Fatalf("want a seeded assistant, got %+v", r)
	}
	if r.Personas[0].Model != "gpt-5" {
		t.Errorf("seeded model = %q, want gpt-5", r.Personas[0].Model)
	}

	// An existing directory is never written to, even if the user emptied it.
	if err := os.Remove(filepath.Join(dir, "assistant.md")); err != nil {
		t.Fatal(err)
	}
	if err := Seed(dir, "gpt-5"); err != nil {
		t.Fatal(err)
	}
	if r := LoadRegistry(dir, models, toolset); len(r.Personas) != 0 {
		t.Fatalf("Seed re-seeded an existing directory: %+v", r)
	}
}

func TestSeedNoModelsIsANoOp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "personas")
	if err := Seed(dir, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("Seed created a directory with no model to point at")
	}
}
```

- [ ] **Step 2: Write the failing color tests**

Create `internal/persona/color_test.go`:

```go
package persona

import (
	"math"
	"testing"
)

func TestContrastRatioKnownValues(t *testing.T) {
	// White on black is the maximum, 21:1.
	got, err := ContrastRatio("#ffffff", "#000000")
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-21) > 0.01 {
		t.Errorf("white/black = %.2f, want 21", got)
	}
	// A color against itself is 1:1.
	got, err = ContrastRatio("#4fb3ff", "#4fb3ff")
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-1) > 0.001 {
		t.Errorf("self contrast = %.3f, want 1", got)
	}
}

func TestContrastRatioRejectsBadHex(t *testing.T) {
	for _, bad := range []string{"", "blurple", "#12", "#12345", "1d1d20", "#ggg000"} {
		if _, err := ContrastRatio(bad, BubbleBG); err == nil {
			t.Errorf("ContrastRatio(%q) accepted an invalid color", bad)
		}
	}
}

// Every palette entry must be legible as text on the assistant bubble.
func TestPaletteMeetsContrastFloor(t *testing.T) {
	if len(palette) == 0 {
		t.Fatal("palette is empty")
	}
	for _, c := range palette {
		r, err := ContrastRatio(c, BubbleBG)
		if err != nil {
			t.Fatalf("palette color %q: %v", c, err)
		}
		if r < 4.5 {
			t.Errorf("palette color %s has contrast %.2f against %s, want >= 4.5", c, r, BubbleBG)
		}
	}
}

func TestAssignColorIsDeterministicAndInPalette(t *testing.T) {
	inPalette := map[string]bool{}
	for _, c := range palette {
		inPalette[c] = true
	}
	for _, id := range []string{"scout", "skeptic", "editor", "assistant"} {
		a, b := assignColor(id), assignColor(id)
		if a != b {
			t.Errorf("assignColor(%q) not deterministic: %q vs %q", id, a, b)
		}
		if !inPalette[a] {
			t.Errorf("assignColor(%q) = %q, not a palette color", id, a)
		}
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/persona/...`
Expected: FAIL — the package does not compile (`undefined: LoadRegistry`, `undefined: palette`, etc.).

- [ ] **Step 4: Implement the color module**

Create `internal/persona/color.go`:

```go
package persona

import (
	"fmt"
	"hash/fnv"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// BubbleBG is the assistant bubble background (style.css). Palette colors are
// verified legible against it.
const BubbleBG = "#1d1d20"

// palette holds the auto-assignment colors. Every entry is verified at test
// time to clear a 4.5:1 contrast ratio against BubbleBG, so a persona that
// omits `color:` still gets a legible one without the author thinking about it.
var palette = []string{
	"#4fb3ff", // blue
	"#5ddc9a", // mint
	"#ffb454", // amber
	"#ff7b72", // salmon
	"#c792ea", // lavender
	"#7ee787", // green
	"#f2cc60", // yellow
	"#ff9ec7", // pink
	"#56d4dd", // cyan
	"#d3b58d", // sand
}

var hexRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ValidColor reports whether s is a 6-digit hex color. Shorthand (#abc) is
// rejected so the stored value is always directly usable as a CSS custom
// property and directly parseable here.
func ValidColor(s string) bool { return hexRe.MatchString(s) }

// assignColor picks a stable palette entry for a persona ID. Same ID, same
// color, across restarts and machines — so history does not recolor itself.
func assignColor(id string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return palette[int(h.Sum32())%len(palette)]
}

// ContrastRatio returns the WCAG 2.x contrast ratio between two hex colors,
// from 1 (identical) to 21 (black on white).
func ContrastRatio(hexA, hexB string) (float64, error) {
	la, err := relativeLuminance(hexA)
	if err != nil {
		return 0, err
	}
	lb, err := relativeLuminance(hexB)
	if err != nil {
		return 0, err
	}
	hi, lo := math.Max(la, lb), math.Min(la, lb)
	return (hi + 0.05) / (lo + 0.05), nil
}

func relativeLuminance(hex string) (float64, error) {
	if !ValidColor(hex) {
		return 0, fmt.Errorf("invalid hex color %q", hex)
	}
	s := strings.TrimPrefix(hex, "#")
	ch := make([]float64, 3)
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseUint(s[i*2:i*2+2], 16, 8)
		if err != nil {
			return 0, fmt.Errorf("invalid hex color %q: %w", hex, err)
		}
		ch[i] = linearize(float64(v) / 255)
	}
	return 0.2126*ch[0] + 0.7152*ch[1] + 0.0722*ch[2], nil
}

func linearize(c float64) float64 {
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}
```

- [ ] **Step 5: Implement the registry**

Create `internal/persona/persona.go`:

```go
// Package persona is the registry of named assistants. Each persona is one
// markdown file with YAML frontmatter in <app-dir>/personas/: the filename stem
// is the stable ID, the frontmatter carries the assigned model, display name,
// color, tool whitelist, and auto-attached library items, and the body is the
// system prompt.
//
// A persona that fails validation is disabled and reported as an Issue, never
// fatal: a typo in one file must not lock the operator out of the app.
package persona

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Persona is one named assistant. Prompt is excluded from JSON: the frontend
// renders names and colors, and has no use for the system prompt.
type Persona struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Model   string   `json:"model"`
	Color   string   `json:"color"`
	Tools   []string `json:"tools,omitempty"`
	Library []string `json:"library,omitempty"`
	Prompt  string   `json:"-"`
}

// Issue is one rejected persona file, surfaced to the operator so a persona
// that silently vanished from the picker is explainable.
type Issue struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

type Registry struct {
	Personas []Persona `json:"personas"`
	Issues   []Issue   `json:"issues"`
}

func (r Registry) ByID(id string) (Persona, bool) {
	for _, p := range r.Personas {
		if p.ID == id {
			return p, true
		}
	}
	return Persona{}, false
}

type frontmatter struct {
	Name    string   `yaml:"name"`
	Model   string   `yaml:"model"`
	Color   string   `yaml:"color"`
	Tools   []string `yaml:"tools"`
	Library []string `yaml:"library"`
}

var idRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// LoadRegistry reads every .md file in dir. It never writes and never returns
// an error: a missing directory is an empty registry, and every other failure
// becomes an Issue. knownModels and knownTools are the names a persona may
// reference; anything else is a typo and disables that persona.
func LoadRegistry(dir string, knownModels, knownTools []string) Registry {
	var r Registry
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return r
		}
		return Registry{Issues: []Issue{{File: dir, Reason: "cannot read personas folder: " + err.Error()}}}
	}
	modelOK := set(knownModels)
	toolOK := set(knownTools)
	seen := map[string]string{} // id -> file that claimed it

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.EqualFold(filepath.Ext(name), ".md") {
			continue
		}
		p, reason := parseFile(dir, name, modelOK, toolOK)
		if reason != "" {
			r.Issues = append(r.Issues, Issue{File: name, Reason: reason})
			continue
		}
		if prior, dup := seen[p.ID]; dup {
			r.Issues = append(r.Issues, Issue{File: name, Reason: "duplicate persona id, already defined by " + prior})
			continue
		}
		seen[p.ID] = name
		r.Personas = append(r.Personas, p)
	}
	sort.Slice(r.Personas, func(i, j int) bool {
		return strings.ToLower(r.Personas[i].Name) < strings.ToLower(r.Personas[j].Name)
	})
	sort.Slice(r.Issues, func(i, j int) bool { return r.Issues[i].File < r.Issues[j].File })
	return r
}

// parseFile validates one persona file. It returns a non-empty reason instead
// of an error because every rejection is reported, not propagated.
func parseFile(dir, filename string, modelOK, toolOK map[string]bool) (Persona, string) {
	id := strings.TrimSuffix(filename, filepath.Ext(filename))
	if !idRe.MatchString(id) {
		return Persona{}, "invalid persona id"
	}
	raw, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		return Persona{}, "cannot read file: " + err.Error()
	}
	fmText, body, ok := splitFrontmatter(string(raw))
	if !ok {
		return Persona{}, "missing frontmatter"
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
		return Persona{}, "invalid frontmatter: " + err.Error()
	}
	if strings.TrimSpace(fm.Name) == "" {
		return Persona{}, "name is required"
	}
	if !modelOK[fm.Model] {
		return Persona{}, "unknown model"
	}
	for _, t := range fm.Tools {
		if !toolOK[t] {
			return Persona{}, "unknown tool"
		}
	}
	color := strings.TrimSpace(fm.Color)
	if color == "" {
		color = assignColor(id)
	} else if !ValidColor(color) {
		return Persona{}, "invalid color"
	}
	return Persona{
		ID:      id,
		Name:    strings.TrimSpace(fm.Name),
		Model:   fm.Model,
		Color:   color,
		Tools:   fm.Tools,
		Library: fm.Library,
		Prompt:  body,
	}, ""
}

// splitFrontmatter separates a leading `---`-fenced YAML block from the body.
// The body is returned trimmed. ok is false when there is no opening fence or
// no closing fence.
func splitFrontmatter(raw string) (fm, body string, ok bool) {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	const open = "---\n"
	if !strings.HasPrefix(s, open) {
		return "", "", false
	}
	rest := s[len(open):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", false
	}
	after := strings.TrimPrefix(rest[end+len("\n---"):], "\n")
	return rest[:end], strings.TrimSpace(after), true
}

func set(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
```

- [ ] **Step 6: Implement seeding**

Create `internal/persona/seed.go`:

```go
package persona

import (
	"os"
	"path/filepath"
	"strings"
)

// seedAssistant is the out-of-the-box persona. It reproduces today's plain-chat
// behavior — no tool restriction, no auto-attached library — so a fresh install
// behaves the way the app did before personas existed.
const seedAssistant = `---
name: Assistant
model: %MODEL%
---
You are a capable, direct assistant. Answer the question that was asked.
State your reasoning when it is load-bearing and skip it when it is not.
If you are uncertain, say so plainly rather than hedging.
`

// Seed writes a single starter persona, but only when dir does not exist. An
// existing directory is never written to: if the operator emptied it or every
// file in it is invalid, a surprise default persona appearing would attribute
// output to an assistant they never configured.
//
// A no-op when defaultModelID is empty — there would be no valid model to point
// the seeded persona at.
func Seed(dir, defaultModelID string) error {
	if defaultModelID == "" {
		return nil
	}
	if _, err := os.Stat(dir); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := strings.ReplaceAll(seedAssistant, "%MODEL%", defaultModelID)
	return os.WriteFile(filepath.Join(dir, "assistant.md"), []byte(body), 0o644)
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/persona/... -v`
Expected: PASS for every test, including `TestPaletteMeetsContrastFloor`. If a palette color fails the 4.5 floor, replace it with a lighter shade — do not lower the threshold.

- [ ] **Step 8: Commit**

```bash
git add internal/persona
git commit -m "feat(persona): file-backed persona registry with contrast-checked colors

One markdown file per persona in <app-dir>/personas/: frontmatter carries the
assigned model, color, tool whitelist, and auto-attached library items; the body
is the system prompt. A persona that fails validation is disabled and reported,
never fatal. Colors omitted from frontmatter are assigned deterministically from
a palette whose every entry is test-verified to clear 4.5:1 against the bubble
background."
```

---

### Task 4: Store — persona columns, migration, replay join

**Files:**
- Modify: `internal/store/schema.go` — `runs.persona_id`, `conversations.pinned_persona`
- Modify: `internal/store/migrate.go` — add both columns to existing databases
- Modify: `internal/store/runs.go` — `CreateRun` signature, `Run.PersonaID`, `GetRun` scan
- Modify: `internal/store/conversations.go` — `Conversation.PinnedPersona`, `SetConversationPinned`
- Modify: `internal/store/events.go` — `ConversationEvent.PersonaID`, `ConversationEvent.Model`
- Modify: `internal/store/replay.go` — join `runs` in `eventsForRunsPlusUserMessages`
- Modify: `internal/store/runs_test.go`, `internal/store/migrate_test.go`, `internal/store/conversations_test.go`
- Modify: any existing caller of `CreateRun` (after Task 1, only `internal/chat/chat.go:114` and store tests)

**Interfaces:**
- Consumes: Task 1's schema (no assignment tables).
- Produces:
  - `func (s *Store) CreateRun(convID, turnID, runID, providerName, model, mode, personaID string) error` — **one new trailing parameter.**
  - `store.Run` gains `PersonaID string`.
  - `store.Conversation` gains `PinnedPersona string` (`json:"pinnedPersona"`).
  - `func (s *Store) SetConversationPinned(id, pinnedModel, pinnedPersona string) error` — **replaces `SetConversationMeta`.**
  - `store.ConversationEvent` gains `PersonaID string` (`json:"personaId,omitempty"`) and `Model string` (`json:"model,omitempty"`), populated on every event that has a `run_id`. Empty on `user_message` events and on runs that predate this feature.

- [ ] **Step 1: Write the failing store tests**

Add to `internal/store/runs_test.go`:

```go
func TestCreateRunRecordsPersona(t *testing.T) {
	s := testStore(t)
	c, err := s.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.AppendUserMessage(c.ID, "hi")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRun(c.ID, u.TurnID, "run-1", "anthropic", "claude-opus-4-8", "auto_grounded_default", "scout"); err != nil {
		t.Fatal(err)
	}
	run, err := s.GetRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.PersonaID != "scout" {
		t.Errorf("PersonaID = %q, want scout", run.PersonaID)
	}
	if run.Model != "claude-opus-4-8" {
		t.Errorf("Model = %q", run.Model)
	}
}

func TestCreateRunAcceptsEmptyPersona(t *testing.T) {
	s := testStore(t)
	c, _ := s.CreateConversation("t")
	u, _ := s.AppendUserMessage(c.ID, "hi")
	if err := s.CreateRun(c.ID, u.TurnID, "run-1", "openai", "gpt-5", "auto_grounded_default", ""); err != nil {
		t.Fatal(err)
	}
	run, err := s.GetRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.PersonaID != "" {
		t.Errorf("PersonaID = %q, want empty", run.PersonaID)
	}
}
```

If `testStore(t)` does not already exist in the store tests, use whatever helper the existing tests use to open a temp store (check the top of `internal/store/store_test.go`) and match it.

Add to `internal/store/replay_test.go` (create the file if absent, `package store`):

```go
func TestDisplayEventsCarryPersonaAndModel(t *testing.T) {
	s := testStore(t)
	c, _ := s.CreateConversation("t")
	u, _ := s.AppendUserMessage(c.ID, "hi")
	if err := s.CreateRun(c.ID, u.TurnID, "run-1", "anthropic", "claude-opus-4-8", "auto_grounded_default", "scout"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendAssistantText(c.ID, u.TurnID, "run-1", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteRun("run-1", RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}

	events, err := s.GetConversationDisplayEvents(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawAssistant bool
	for _, e := range events {
		switch e.Kind {
		case EventKindUserMessage:
			if e.PersonaID != "" || e.Model != "" {
				t.Errorf("user_message carries attribution: persona=%q model=%q", e.PersonaID, e.Model)
			}
		case EventKindAssistantText:
			sawAssistant = true
			if e.PersonaID != "scout" {
				t.Errorf("assistant_text PersonaID = %q, want scout", e.PersonaID)
			}
			if e.Model != "claude-opus-4-8" {
				t.Errorf("assistant_text Model = %q", e.Model)
			}
		}
	}
	if !sawAssistant {
		t.Fatal("no assistant_text event returned")
	}
}

func TestDisplayEventsTolerateRunsWithoutAPersona(t *testing.T) {
	s := testStore(t)
	c, _ := s.CreateConversation("t")
	u, _ := s.AppendUserMessage(c.ID, "hi")
	if err := s.CreateRun(c.ID, u.TurnID, "run-1", "openai", "gpt-5", "auto_grounded_default", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendAssistantText(c.ID, u.TurnID, "run-1", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteRun("run-1", RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}
	events, err := s.GetConversationDisplayEvents(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Kind != EventKindAssistantText {
			continue
		}
		if e.PersonaID != "" {
			t.Errorf("PersonaID = %q, want empty", e.PersonaID)
		}
		if e.Model != "gpt-5" {
			t.Errorf("Model = %q, want gpt-5 (the model is known even when the persona is not)", e.Model)
		}
	}
}
```

Add to `internal/store/migrate_test.go`:

```go
// A database created before personas existed — and still carrying the retired
// assignment surface — must migrate cleanly and gain both new columns.
func TestMigrateLegacyDatabaseGainsPersonaColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE conversations (
  id TEXT PRIMARY KEY, title TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  pinned_model TEXT, assignment_id TEXT
);
CREATE TABLE assignments (id TEXT PRIMARY KEY, title TEXT NOT NULL);
CREATE TABLE assignment_items (id TEXT PRIMARY KEY, assignment_id TEXT NOT NULL);
INSERT INTO conversations(id,title,created_at,updated_at) VALUES('c1','old',1,1);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open (which runs schema+migrate): %v", err)
	}
	defer s.Close()

	for _, tc := range []struct{ table, col string }{
		{"runs", "persona_id"},
		{"conversations", "pinned_persona"},
	} {
		has, err := columnExists(s.db, tc.table, tc.col)
		if err != nil {
			t.Fatal(err)
		}
		if !has {
			t.Errorf("%s.%s was not added by migrate", tc.table, tc.col)
		}
	}
	if has, _ := columnExists(s.db, "conversations", "assignment_id"); has {
		t.Error("conversations.assignment_id was not dropped")
	}
	if has, _ := tableExists(s.db, "assignments"); has {
		t.Error("assignments table was not dropped")
	}

	// The pre-existing conversation survives and is listable.
	convs, err := s.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	if len(convs) != 1 || convs[0].ID != "c1" {
		t.Errorf("ListConversations = %+v, want the legacy row", convs)
	}
}
```

Match the imports and the `Open`/`Close` helper names to what `internal/store/store_test.go` and `migrate_test.go` already use. If `Store` has no exported `Close`, drop the `defer`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/...`
Expected: FAIL — `CreateRun` takes 6 args not 7; `Run` has no field `PersonaID`; `ConversationEvent` has no field `PersonaID`.

- [ ] **Step 3: Add the columns to the schema**

In `internal/store/schema.go`, add `pinned_persona TEXT` to `conversations`:

```sql
CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY, title TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  pinned_model TEXT,
  pinned_persona TEXT,
  retrieval_mode TEXT NOT NULL DEFAULT 'auto_grounded_default'
);
```

and `persona_id TEXT` to `runs`, immediately after `model`:

```sql
  provider                  TEXT NOT NULL,
  model                     TEXT NOT NULL,
  persona_id                TEXT,
  retrieval_mode            TEXT NOT NULL,
```

`persona_id` is nullable on purpose: runs that predate personas have no persona, and inventing one would attribute output to an assistant that never produced it.

- [ ] **Step 4: Add the columns in the migration**

In `internal/store/migrate.go`, immediately before the `migrateMessagesToEvents(db)` call, add:

```go
	// Persona foundation: additive, nullable. Pre-persona runs keep persona_id
	// NULL and render as a neutral bubble carrying only the model chip.
	has, err = columnExists(db, "runs", "persona_id")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN persona_id TEXT`); err != nil {
			return err
		}
	}
	has, err = columnExists(db, "conversations", "pinned_persona")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE conversations ADD COLUMN pinned_persona TEXT`); err != nil {
			return err
		}
	}
```

- [ ] **Step 5: Thread the persona through runs.go**

In `internal/store/runs.go`, add the field to `Run` after `Model`:

```go
	Model                  string
	PersonaID              string
```

Change `CreateRun`:

```go
func (s *Store) CreateRun(convID, turnID, runID, providerName, model, mode, personaID string) error {
	_, err := s.db.Exec(
		`INSERT INTO runs
            (id, conversation_id, turn_id, status, active_for_replay,
             provider, model, persona_id, retrieval_mode, started_at)
         VALUES (?,?,?,'in_progress',0,?,?,?,?,?)`,
		runID, convID, turnID, providerName, model, nullIfEmpty(personaID), mode,
		time.Now().UnixMilli())
	return err
}
```

`nullIfEmpty` (stores `""` as SQL NULL, so "no persona" is one value in the column rather than two) **already exists** in `internal/store/store.go:34` — Task 1 relocated it there from the deleted `assignments.go` because `ideas.go` also uses it. Do **not** redefine it; just call it.

Change `GetRun`'s query and scan to include `persona_id`:

```go
		`SELECT id, conversation_id, turn_id, status, active_for_replay,
                provider, model, COALESCE(persona_id,''), retrieval_mode, grounding_meta,
                started_at, ended_at, terminal_reason, error_code, error_message,
                total_input_tokens, total_output_tokens, total_cached_input_tokens,
                total_tool_calls, total_iterations
           FROM runs WHERE id = ?`, runID,
	).Scan(
		&r.ID, &r.ConversationID, &r.TurnID, &r.Status, &r.ActiveForReplay,
		&r.Provider, &r.Model, &r.PersonaID, &r.RetrievalMode, &meta,
		&r.StartedAt, &r.EndedAt, &r.TerminalReason, &r.ErrorCode, &r.ErrorMessage,
		&r.TotalInputTokens, &r.TotalOutputTokens, &r.TotalCachedInputTokens,
		&r.TotalToolCalls, &r.TotalIterations)
```

- [ ] **Step 6: Add the pinned persona to conversations.go**

In `internal/store/conversations.go`, add the field:

```go
type Conversation struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
	PinnedModel   string `json:"pinnedModel"`
	PinnedPersona string `json:"pinnedPersona"`
}
```

Update `ListConversations` to select and scan it:

```go
	rows, err := s.db.Query(`SELECT id,title,created_at,updated_at,COALESCE(pinned_model,''),COALESCE(pinned_persona,'') FROM conversations ORDER BY updated_at DESC`)
	...
		if err := rows.Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt, &c.PinnedModel, &c.PinnedPersona); err != nil {
```

Replace `SetConversationMeta` with:

```go
// SetConversationPinned records which persona the operator last used in this
// conversation, and the model that persona resolved to. Both are written: the
// persona is what the picker restores, and the model keeps pinned_model
// meaningful for rows that predate personas.
func (s *Store) SetConversationPinned(id, pinnedModel, pinnedPersona string) error {
	_, err := s.db.Exec(
		`UPDATE conversations SET pinned_model=?, pinned_persona=?, updated_at=? WHERE id=?`,
		pinnedModel, pinnedPersona, time.Now().Unix(), id)
	return err
}
```

- [ ] **Step 7: Add attribution to the event struct**

In `internal/store/events.go`, add two fields to `ConversationEvent`, after `RunID`:

```go
	RunID          string          `json:"runId,omitempty"`
	PersonaID      string          `json:"personaId,omitempty"`
	Model          string          `json:"model,omitempty"`
```

These are joined in from `runs` at read time — they are not columns on `conversation_events`, and nothing writes them.

- [ ] **Step 8: Join runs into the replay query**

In `internal/store/replay.go`, rewrite the query inside `eventsForRunsPlusUserMessages`:

```go
	rows, err := s.db.Query(
		`SELECT e.id, e.conversation_id, e.turn_id, COALESCE(e.run_id,''),
                e.sequence_index, e.kind, COALESCE(e.text,''),
                COALESCE(e.tool_call_id,''), COALESCE(e.tool_name,''),
                COALESCE(e.tool_input,''), COALESCE(e.tool_metadata,''),
                COALESCE(e.tool_result_hash,''),
                COALESCE(e.tool_latency_ms,0), e.is_error, e.created_at,
                COALESCE(r.persona_id,''), COALESCE(r.model,'')
           FROM conversation_events e
           LEFT JOIN runs r ON r.id = e.run_id
          WHERE e.conversation_id = ?
          ORDER BY e.sequence_index`, convID)
```

and extend the scan to match:

```go
		if err := rows.Scan(
			&ev.ID, &ev.ConversationID, &ev.TurnID, &ev.RunID,
			&ev.SequenceIndex, &ev.Kind, &ev.Text,
			&ev.ToolCallID, &ev.ToolName, &input, &meta,
			&ev.ToolResultHash, &ev.ToolLatencyMs, &isErrInt, &ev.CreatedAt,
			&ev.PersonaID, &ev.Model,
		); err != nil {
			return nil, err
		}
```

The `LEFT JOIN` is what makes `user_message` events (which have no `run_id`) come back with empty attribution rather than being dropped.

`GetProviderReplayEvents` calls the same function; the two extra fields are ignored by `chat.canonicalEvents`, so the provider payload is unchanged.

In `GetConversationDisplayEvents`, the synthetic `run_error` event must carry attribution too, so an errored bubble is colored like any other. Change its construction:

```go
		events = append(events, ConversationEvent{
			ConversationID: convID,
			TurnID:         run.TurnID,
			RunID:          runID,
			PersonaID:      run.PersonaID,
			Model:          run.Model,
			Kind:           "run_error",
			Text:           runErrorDisplayText(run),
		})
```

- [ ] **Step 9: Update the one production caller of CreateRun**

In `internal/chat/chat.go:114`, add a trailing `""` for now — Task 5 replaces it with the real persona ID:

```go
	if err := s.st.CreateRun(p.ConversationID, user.TurnID, runID,
		providerName, p.Model, string(mode), ""); err != nil {
```

- [ ] **Step 10: Run the tests to verify they pass**

Run:
```bash
go build ./... && go test ./...
```
Expected: PASS. Any other compile error is a `CreateRun` or `SetConversationMeta` caller in a test — update it to the new signature.

- [ ] **Step 11: Commit**

```bash
git add -A
git commit -m "feat(store): record the persona on runs and join it into replay

runs.persona_id and conversations.pinned_persona, both nullable and additive.
GetConversationDisplayEvents now LEFT JOINs runs, so every assistant event
carries the persona and model that produced it — history can be colored the
same way live output is. Pre-persona runs come back with an empty persona and
a known model, which is exactly what is true of them."
```

---

### Task 5: Chat — persona plumbing and the `run_started` payload

**Files:**
- Modify: `internal/chat/chat.go` — `SendParams.PersonaID`, pass it to `CreateRun`, add attribution to the `run_started` emit
- Modify: `internal/eval/loop_test.go` — if it constructs `chat.SendParams`, it still compiles (new field is optional), but add one assertion
- Create: `internal/chat/persona_test.go`

**Interfaces:**
- Consumes: Task 4's `store.CreateRun(..., personaID string)`.
- Produces:
  - `chat.SendParams` gains `PersonaID string`.
  - The `chat:run_started` sink payload gains `"personaID"`, `"modelID"`, and `"provider"` keys (in addition to the existing `"retrievalMode"` and `"grounding"`).

- [ ] **Step 1: Write the failing test**

Create `internal/chat/persona_test.go`. Model it on the existing loop tests — check `internal/eval/loop_test.go` for how a `chat.Service` and a fake provider are wired, and reuse `internal/eval/fakeprovider`. If importing `internal/eval` from `internal/chat` creates an import cycle, put this test in `internal/eval/persona_test.go` instead (package `eval`), which is where the loop-level integration tests already live.

```go
package eval

import (
	"context"
	"testing"

	"github.com/cajundata/starshp_app/internal/chat"
)

// The run_started event must carry attribution, so the frontend can color the
// bubble the moment it appears rather than after the run completes.
func TestRunStartedCarriesPersonaAndModel(t *testing.T) {
	st := testStore(t)          // match the helper used by loop_test.go
	svc := chat.New(st)
	c, err := st.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	sink := &CaptureSink{}

	_, err = svc.Send(context.Background(), chat.SendParams{
		ConversationID: c.ID,
		UserText:       "hello",
		SystemPrompt:   "be brief",
		Model:          "claude-opus-4-8",
		PersonaID:      "scout",
		Provider:       scriptedProvider(t, "hi"), // match loop_test.go's fake-provider helper
		ProviderName:   "anthropic",
		RetrievalMode:  chat.RetrievalAutoGroundedDefault,
		Sink:           sink,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var started *chat.SinkEvent
	for i := range sink.Events {
		if sink.Events[i].Kind == chat.SinkRunStarted {
			started = &sink.Events[i]
			break
		}
	}
	if started == nil {
		t.Fatal("no run_started event emitted")
	}
	if got := started.Payload["personaID"]; got != "scout" {
		t.Errorf("personaID = %v, want scout", got)
	}
	if got := started.Payload["modelID"]; got != "claude-opus-4-8" {
		t.Errorf("modelID = %v, want claude-opus-4-8", got)
	}
	if got := started.Payload["provider"]; got != "anthropic" {
		t.Errorf("provider = %v, want anthropic", got)
	}

	// And it is persisted, so a reopened conversation agrees with the live view.
	run, err := st.GetRun(started.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.PersonaID != "scout" {
		t.Errorf("runs.persona_id = %q, want scout", run.PersonaID)
	}
}
```

Adapt `testStore` and `scriptedProvider` to the exact helper names in `internal/eval/loop_test.go`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/eval/... -run TestRunStartedCarriesPersonaAndModel -v`
Expected: FAIL — `unknown field PersonaID in struct literal of type chat.SendParams`.

- [ ] **Step 3: Add PersonaID to SendParams**

In `internal/chat/chat.go`, add the field to `SendParams` after `Model`:

```go
	Model          string
	PersonaID      string // recorded on runs; "" for a run with no persona
	Provider       provider.ChatProvider
```

- [ ] **Step 4: Persist it and emit it**

In `chat.go`, in `Send`, pass the persona to `CreateRun` and add attribution to the emit:

```go
	runID := uuid.NewString()
	if err := s.st.CreateRun(p.ConversationID, user.TurnID, runID,
		providerName, p.Model, string(mode), p.PersonaID); err != nil {
		return RunResult{}, fmt.Errorf("create run: %w", err)
	}
	// Attribution rides on run_started so the bubble is colored the instant it
	// appears — no uncolored flash, no post-hoc recolor.
	emit(p.Sink, SinkRunStarted, p.ConversationID, runID, user.TurnID,
		map[string]any{
			"retrievalMode": string(mode),
			"personaID":     p.PersonaID,
			"modelID":       p.Model,
			"provider":      providerName,
			"grounding": map[string]any{
				"status": initialGroundingStatus(mode, p.Retriever),
			},
		})
```

- [ ] **Step 5: Run the tests to verify they pass**

Run:
```bash
go build ./... && go test ./...
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat(chat): carry the persona through the run and onto run_started

SendParams.PersonaID is persisted on the run and emitted with the model and
provider on chat:run_started, so the frontend can attribute and color a bubble
at creation rather than deriving it from chat:usage after the fact."
```

---

### Task 6: appapi — persona resolution, tool subsetting, bindings

This is where a persona ID becomes a model, a system prompt, and a tool set.

**Files:**
- Modify: `internal/tools/registry.go` — add `Subset`
- Create: `internal/tools/subset_test.go`
- Modify: `internal/config/config.go` — `PersonaDir`
- Modify: `internal/appapi/api.go` — persona registry field, `Personas()`, `SendMessage`, `SetConversationPersona`, `EventDTO`, `StartupIssues`, `allToolNames`
- Modify: `internal/appapi/library.go` — `assembleSystemPrompt` takes a persona
- Modify: `main.go` — default `PersonaDir`
- Modify: `internal/appapi/api_test.go`, `library_test.go`
- Create: `internal/appapi/persona_test.go`

**Interfaces:**
- Consumes: `persona.LoadRegistry`, `persona.Seed`, `persona.Persona` (Task 3); `store.SetConversationPinned` (Task 4); `chat.SendParams.PersonaID` (Task 5).
- Produces:
  - `func (r *Registry) Subset(names []string) *Registry` in `internal/tools`.
  - `config.Config` gains `PersonaDir string` (env `PERSONA_DIR`).
  - `func (a *API) Personas() []persona.Persona` — Wails-bound.
  - `func (a *API) SendMessage(convID, userText, personaID string) error` — **third parameter changes meaning from model ID to persona ID.**
  - `func (a *API) SetConversationPersona(convID, personaID string) error` — **replaces the bound `SetConversationMeta`.**
  - `appapi.EventDTO` gains `PersonaID string` (`json:"personaId,omitempty"`) and `ModelID string` (`json:"modelId,omitempty"`).
  - `StartupIssues()` additionally returns one line per rejected persona file.

- [ ] **Step 1: Write the failing tools.Subset test**

Create `internal/tools/subset_test.go`:

```go
package tools

import (
	"sort"
	"testing"
	"time"
)

func names(r *Registry) []string {
	var out []string
	for _, d := range r.Catalog() {
		out = append(out, d.Name)
	}
	sort.Strings(out)
	return out
}

func TestSubsetKeepsOnlyNamedTools(t *testing.T) {
	r := NewRegistry(time.Second)
	if err := r.Register(fakeTool{name: "safe_math"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(fakeTool{name: "search_textbook"}); err != nil {
		t.Fatal(err)
	}

	got := names(r.Subset([]string{"safe_math"}))
	if len(got) != 1 || got[0] != "safe_math" {
		t.Errorf("Subset([safe_math]) = %v", got)
	}
}

// An empty whitelist means "no restriction" — a persona that omits `tools:`
// gets every tool.
func TestSubsetEmptyMeansEverything(t *testing.T) {
	r := NewRegistry(time.Second)
	_ = r.Register(fakeTool{name: "safe_math"})
	_ = r.Register(fakeTool{name: "search_textbook"})

	got := names(r.Subset(nil))
	if len(got) != 2 {
		t.Errorf("Subset(nil) = %v, want both tools", got)
	}
}

// search_textbook is only registered when RAG is available. A persona naming it
// on a RAG-less run must still work — with that tool simply absent.
func TestSubsetIgnoresUnregisteredNames(t *testing.T) {
	r := NewRegistry(time.Second)
	_ = r.Register(fakeTool{name: "safe_math"})

	got := names(r.Subset([]string{"safe_math", "search_textbook"}))
	if len(got) != 1 || got[0] != "safe_math" {
		t.Errorf("Subset = %v, want just safe_math", got)
	}
}
```

`fakeTool` must satisfy `tools.Tool`. If the tools package tests already define one, reuse it. Otherwise add to the same file:

```go
type fakeTool struct{ name string }

func (f fakeTool) Name() string                 { return f.name }
func (f fakeTool) Description() string          { return "fake" }
func (f fakeTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f fakeTool) Timeout() time.Duration       { return 0 }
func (f fakeTool) Execute(ctx context.Context, ec ExecContext, in json.RawMessage) (ExecResult, error) {
	return ExecResult{Output: "ok"}, nil
}
```
(with `context` and `encoding/json` imported).

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/tools/...`
Expected: FAIL — `r.Subset undefined`.

- [ ] **Step 3: Implement tools.Subset**

Append to `internal/tools/registry.go`:

```go
// Subset returns a registry holding only the named tools. An empty list means
// no restriction and returns the receiver unchanged.
//
// Names that are not registered are silently ignored rather than treated as an
// error: search_textbook is registered only when RAG is available, and a
// persona that names it must still run when RAG is down — with that tool
// absent, not with the persona disabled. Typos are caught earlier, when the
// persona registry validates tool names against the set of tools the app can
// register.
func (r *Registry) Subset(names []string) *Registry {
	if len(names) == 0 {
		return r
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	out := NewRegistry(r.defaultTimeout)
	for name, t := range r.tools {
		if want[name] {
			out.tools[name] = t
			out.schemas[name] = r.schemas[name]
		}
	}
	return out
}
```

- [ ] **Step 4: Add PersonaDir to config**

In `internal/config/config.go`, add the field to `Config` after `LibraryDir`:

```go
	LibraryDir         string
	PersonaDir         string
```

and to `Load`:

```go
		LibraryDir:         os.Getenv("LIBRARY_DIR"),
		PersonaDir:         os.Getenv("PERSONA_DIR"),
```

In `main.go`, default it next to the existing `LibraryDir` default:

```go
	if cfg.LibraryDir == "" {
		cfg.LibraryDir = filepath.Join(appDir, "library")
	}
	if cfg.PersonaDir == "" {
		cfg.PersonaDir = filepath.Join(appDir, "personas")
	}
```

- [ ] **Step 5: Write the failing appapi tests**

Create `internal/appapi/persona_test.go`:

```go
package appapi

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/tools/safemath"
	"github.com/cajundata/starshp_app/internal/tools/searchtextbook"
)

// allToolNames must list exactly the tools the app can register. If a tool is
// added and this list is not updated, every persona naming it is rejected —
// a silent, confusing failure. This test makes that impossible.
func TestAllToolNamesMatchesTheRegisterableTools(t *testing.T) {
	live := []string{
		safemath.New().Name(),
		searchtextbook.New(nil, nil, 4000).Name(),
	}
	sort.Strings(live)
	got := append([]string(nil), allToolNames...)
	sort.Strings(got)
	if len(got) != len(live) {
		t.Fatalf("allToolNames = %v, registerable tools = %v", got, live)
	}
	for i := range live {
		if got[i] != live[i] {
			t.Errorf("allToolNames = %v, registerable tools = %v", got, live)
			break
		}
	}
}

func newPersonaAPI(t *testing.T, files map[string]string) *API {
	t.Helper()
	dir := t.TempDir()
	pdir := filepath.Join(dir, "personas")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(pdir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{
		PersonaDir: pdir,
		LibraryDir: filepath.Join(dir, "library"),
		AppDBPath:  filepath.Join(dir, "app.db"),
	}
	reg := provider.Registry{Models: []provider.ModelInfo{
		{ID: "gpt-5", Display: "GPT-5", Provider: "openai"},
	}}
	st := testStore(t) // match the helper the other appapi tests use
	return NewAPI(cfg, st, reg, nil)
}

func TestPersonasBindingReturnsLoadedPersonas(t *testing.T) {
	a := newPersonaAPI(t, map[string]string{
		"scout.md": "---\nname: Scout\nmodel: gpt-5\ncolor: \"#4fb3ff\"\n---\nYou are Scout.\n",
	})
	ps := a.Personas()
	if len(ps) != 1 || ps[0].ID != "scout" {
		t.Fatalf("Personas() = %+v", ps)
	}
	if ps[0].Prompt != "" {
		t.Error("Persona.Prompt must not be exposed to the frontend")
	}
}

func TestStartupIssuesReportsRejectedPersonas(t *testing.T) {
	a := newPersonaAPI(t, map[string]string{
		"broken.md": "---\nname: Broken\nmodel: no-such-model\n---\nbody\n",
	})
	var found bool
	for _, s := range a.StartupIssues() {
		if strings.Contains(s, "broken.md") && strings.Contains(s, "unknown model") {
			found = true
		}
	}
	if !found {
		t.Errorf("StartupIssues() = %v, want a line naming broken.md and unknown model", a.StartupIssues())
	}
}

// An unknown persona ID is a hard error. Falling back to a default persona
// would attribute output to an assistant the operator did not choose — the
// exact failure this feature exists to prevent.
func TestSendMessageRejectsAnUnknownPersona(t *testing.T) {
	a := newPersonaAPI(t, map[string]string{
		"scout.md": "---\nname: Scout\nmodel: gpt-5\n---\nYou are Scout.\n",
	})
	c, err := a.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	err = a.SendMessage(c.ID, "hello", "ghost")
	if err == nil {
		t.Fatal("SendMessage with an unknown persona returned nil")
	}
	ae, ok := err.(provider.AppError)
	if !ok {
		t.Fatalf("error is %T, want provider.AppError", err)
	}
	if ae.Code != "config" {
		t.Errorf("Code = %q, want config", ae.Code)
	}
}

// A personas folder that exists but yields nothing valid must explain itself.
// The operator sees why the picker is empty instead of a bare "unknown
// assistant" for a persona they never got the chance to select.
func TestSendMessageWithNoValidPersonasNamesTheValidationFailures(t *testing.T) {
	a := newPersonaAPI(t, map[string]string{
		"broken.md": "---\nname: Broken\nmodel: no-such-model\n---\nbody\n",
	})
	c, err := a.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	err = a.SendMessage(c.ID, "hello", "")
	ae, ok := err.(provider.AppError)
	if !ok {
		t.Fatalf("error is %T, want provider.AppError", err)
	}
	if ae.Code != "config" {
		t.Errorf("Code = %q, want config", ae.Code)
	}
	if !strings.Contains(ae.UserMessage, "broken.md") {
		t.Errorf("UserMessage = %q, want it to name the file that failed", ae.UserMessage)
	}
}

func TestSeedsAnAssistantWhenThePersonaDirIsAbsent(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		PersonaDir: filepath.Join(dir, "personas"), // does not exist
		LibraryDir: filepath.Join(dir, "library"),
	}
	reg := provider.Registry{Models: []provider.ModelInfo{{ID: "gpt-5", Display: "GPT-5", Provider: "openai"}}}
	a := NewAPI(cfg, testStore(t), reg, nil)
	ps := a.Personas()
	if len(ps) != 1 || ps[0].ID != "assistant" {
		t.Fatalf("Personas() = %+v, want a seeded assistant", ps)
	}
	if ps[0].Model != "gpt-5" {
		t.Errorf("seeded model = %q, want the first model in the registry", ps[0].Model)
	}
}
```

Add to `internal/appapi/library_test.go`:

```go
// Persona body first (identity), then the persona's own library items, then the
// conversation's — and an item claimed by both appears once.
func TestAssembleSystemPromptOrdersPersonaThenLibrary(t *testing.T) {
	dir := t.TempDir()
	writeLib := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeLib("alpha.md", "# Alpha\n\nALPHA BODY\n")
	writeLib("zulu.md", "# Zulu\n\nZULU BODY\n")

	a := &API{cfg: config.Config{LibraryDir: dir}, lib: library.New(dir), st: testStore(t)}
	c, err := a.st.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.st.SetActiveItems(c.ID, []string{"zulu.md", "alpha.md"}); err != nil {
		t.Fatal(err)
	}

	p := persona.Persona{ID: "scout", Name: "Scout", Model: "gpt-5",
		Prompt:  "YOU ARE SCOUT",
		Library: []string{"alpha"}, // no extension: normalized to alpha.md
	}
	got, skipped, err := a.assembleSystemPrompt(c.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v", skipped)
	}
	want := "YOU ARE SCOUT\n\nALPHA BODY\n\nZULU BODY"
	if got != want {
		t.Errorf("prompt =\n%q\nwant\n%q", got, want)
	}
}
```

Match `testStore(t)` to whatever helper the existing appapi tests use; if there is none, open a `store.Open(filepath.Join(t.TempDir(), "app.db"))`.

- [ ] **Step 6: Run the tests to verify they fail**

Run: `go test ./internal/appapi/...`
Expected: FAIL — `undefined: allToolNames`, `a.Personas undefined`, `assembleSystemPrompt` takes 1 arg not 2.

- [ ] **Step 7: Wire the persona registry into the API**

In `internal/appapi/api.go`, add the import `"github.com/cajundata/starshp_app/internal/persona"`.

Add the field to `API`:

```go
	reg            provider.Registry
	personas       persona.Registry
```

Add the tool-name constant list above `NewAPI`:

```go
// allToolNames is every tool the app can register. Persona `tools:` lists are
// validated against this, not against the live registry: search_textbook is
// only registered when RAG is available, and a RAG outage must not silently
// disable every persona that names it. TestAllToolNamesMatchesTheRegisterableTools
// keeps this in step with the tools actually constructed in NewAPI.
var allToolNames = []string{"safe_math", "search_textbook"}
```

At the end of `NewAPI`, after the tool registry is built:

```go
	// Seed a starter persona only when the folder is absent, then load. Loading
	// never writes and never fails: a bad persona file is disabled and reported
	// through StartupIssues.
	if err := persona.Seed(cfg.PersonaDir, defaultModelID(reg)); err != nil {
		slog.Warn("persona: seed failed", "dir", cfg.PersonaDir, "err", err)
	}
	a.personas = persona.LoadRegistry(cfg.PersonaDir, modelIDs(reg), allToolNames)
	return a
}

// defaultModelID is the model a seeded persona points at: the first entry in
// models.yaml. Empty when no models are configured, which makes Seed a no-op.
func defaultModelID(reg provider.Registry) string {
	if len(reg.Models) > 0 {
		return reg.Models[0].ID
	}
	return ""
}

func modelIDs(reg provider.Registry) []string {
	out := make([]string, 0, len(reg.Models))
	for _, m := range reg.Models {
		out = append(out, m.ID)
	}
	return out
}
```

(Re-add the `"log/slog"` import if Task 1 removed it.)

- [ ] **Step 8: Add the Personas binding and surface rejections**

In `internal/appapi/api.go`:

```go
// Personas returns the loaded assistants for the picker. The system prompt is
// not included (Persona.Prompt is json:"-") — the frontend renders names,
// colors, and model chips, and has no use for it.
func (a *API) Personas() []persona.Persona { return a.personas.Personas }
```

and extend `StartupIssues`:

```go
func (a *API) StartupIssues() []string {
	issues := ValidateStartup(a.cfg, a.reg)
	for _, is := range a.personas.Issues {
		issues = append(issues, "persona "+is.File+": "+is.Reason)
	}
	return issues
}
```

- [ ] **Step 9: Resolve the persona in SendMessage**

Replace `SendMessage` in `internal/appapi/api.go`:

```go
// SendMessage runs the agentic loop for one user turn as the named persona.
// The persona supplies the model, the system prompt, and the tool subset.
// Assistant output is surfaced through the chat:* event taxonomy (the bubble
// renders from events), so this returns only a normalized error.
func (a *API) SendMessage(convID, userText, personaID string) error {
	p, ok := a.personas.ByID(personaID)
	if !ok {
		// No fallback to a default persona: a silent substitution would attribute
		// output to an assistant the operator did not pick, which is the exact
		// failure per-persona attribution exists to prevent.
		return provider.AppError{
			Code:        "config",
			UserMessage: a.noPersonaMessage(personaID),
			Retryable:   false,
		}
	}
	prov, err := provider.New(a.reg, p.Model, a.cfg.OpenAIAPIKey, a.cfg.AnthropicAPIKey)
	if err != nil {
		return provider.NormalizeError(err)
	}

	existing, _ := a.st.GetConversationDisplayEvents(convID)
	if len(existing) == 0 {
		_ = a.st.SetConversationTitle(convID, titleFromText(userText))
	}

	systemPrompt, skipped, err := a.assembleSystemPrompt(convID, p)
	if err != nil {
		return provider.NormalizeError(err)
	}
	if len(skipped) > 0 {
		a.emit("library:notice",
			"Skipped missing library items: "+strings.Join(skipped, ", "))
	}

	scopes, _ := a.st.GetConversationTextbooks(convID)
	var retr chat.Retriever
	if len(scopes) > 0 && a.ragAdpt != nil {
		retr = ragRetriever{a: a, scopes: scopes}
	}

	cctx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	a.cancelInFlight = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		a.cancelInFlight = nil
		a.mu.Unlock()
	}()

	_, err = a.chatSvc.Send(cctx, chat.SendParams{
		ConversationID: convID,
		UserText:       userText,
		SystemPrompt:   systemPrompt,
		Model:          p.Model,
		PersonaID:      p.ID,
		Provider:       prov,
		ProviderName:   providerNameFromModelID(a.reg, p.Model),
		Registry:       a.toolReg.Subset(p.Tools),
		Resolver:       chatStoreResolver{st: a.st},
		Retriever:      retr,
		RetrievalMode:  a.retrievalMode(convID),
		Sink:           wailsSink{a: a},
		RemapErr:       a.localRemapErr(p.Model),
	}, nil)
	return err
}

// noPersonaMessage explains why a persona could not be resolved. When the
// registry loaded nothing valid, the useful thing to say is *which files failed
// and why* — not "unknown assistant", which describes a choice the operator was
// never offered.
func (a *API) noPersonaMessage(personaID string) string {
	if len(a.personas.Personas) == 0 {
		msg := "No assistants are available."
		if len(a.personas.Issues) > 0 {
			var parts []string
			for _, is := range a.personas.Issues {
				parts = append(parts, is.File+" ("+is.Reason+")")
			}
			msg += " These persona files failed to load: " + strings.Join(parts, "; ") + "."
		} else {
			msg += " Add a persona to your personas folder."
		}
		return msg
	}
	return "Unknown assistant \"" + personaID + "\". Check your personas folder."
}

// SetConversationPersona pins the persona the operator last used here. The
// persona's model is written alongside it, so pinned_model stays meaningful.
func (a *API) SetConversationPersona(convID, personaID string) error {
	p, ok := a.personas.ByID(personaID)
	if !ok {
		return provider.AppError{
			Code:        "config",
			UserMessage: "Unknown assistant \"" + personaID + "\".",
			Retryable:   false,
		}
	}
	if err := a.st.SetConversationPinned(convID, p.Model, p.ID); err != nil {
		return provider.NormalizeError(err)
	}
	return nil
}
```

Delete the old `SetConversationMeta` method (line 174).

- [ ] **Step 10: Compose the system prompt from persona + library**

In `internal/appapi/library.go`, replace `assembleSystemPrompt`:

```go
// assembleSystemPrompt builds the system prompt for one turn: the persona's
// body (identity), then the library items the persona always carries, then the
// items attached to this conversation. An item claimed by both appears once, in
// the persona's position — a conversation attachment reads as an addition to
// the persona's standing context, not an interruption of it.
//
// Missing library files are skipped, not fatal, and returned in `skipped`.
func (a *API) assembleSystemPrompt(convID string, p persona.Persona) (prompt string, skipped []string, err error) {
	convNames, err := a.st.GetActiveItems(convID)
	if err != nil {
		return "", nil, err
	}
	personaNames := make([]string, 0, len(p.Library))
	claimed := map[string]bool{}
	for _, n := range p.Library {
		n = normalizeLibraryName(n)
		personaNames = append(personaNames, n)
		claimed[n] = true
	}
	var rest []string
	for _, n := range convNames {
		if !claimed[n] {
			rest = append(rest, n)
		}
	}

	personaPre, skippedA, err := a.assembleLibraryPreamble(personaNames)
	if err != nil {
		return "", nil, err
	}
	convPre, skippedB, err := a.assembleLibraryPreamble(rest)
	if err != nil {
		return "", nil, err
	}
	return joinNonEmpty(p.Prompt, personaPre, convPre),
		append(skippedA, skippedB...), nil
}

// normalizeLibraryName lets a persona write `library: [style-guide]` instead of
// `[style-guide.md]`. Library IDs are filenames; this supplies the extension.
func normalizeLibraryName(n string) string {
	if strings.HasSuffix(strings.ToLower(n), ".md") {
		return n
	}
	return n + ".md"
}

func joinNonEmpty(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, "\n\n")
}
```

Add the `persona` import to `library.go`.

- [ ] **Step 11: Add attribution to the EventDTO**

In `internal/appapi/api.go`, add the fields to `EventDTO` after `RunID`:

```go
	RunID         string          `json:"runId,omitempty"`
	PersonaID     string          `json:"personaId,omitempty"`
	ModelID       string          `json:"modelId,omitempty"`
```

and populate them in `GetConversationDisplayEvents`:

```go
		out = append(out, EventDTO{
			ID: r.ID, TurnID: r.TurnID, RunID: r.RunID,
			PersonaID: r.PersonaID, ModelID: r.Model,
			Kind: r.Kind,
			Text: r.Text, ToolCallID: r.ToolCallID, ToolName: r.ToolName,
			ToolInput: r.ToolInput, ToolMetadata: r.ToolMetadata,
			ToolLatencyMs: r.ToolLatencyMs, IsError: r.IsError,
		})
```

The DTO carries IDs, not names or colors: the frontend resolves those from `Personas()`, so editing a color in a markdown file recolors that persona's history on next launch with no data migration.

- [ ] **Step 12: Run the tests to verify they pass**

Run:
```bash
go build ./... && go test ./...
```
Expected: PASS. If `api_compat_test.go` asserts the bound-method surface, update it: `SetConversationMeta` is gone, `Personas` and `SetConversationPersona` are new, and `SendMessage`'s third parameter is now a persona ID.

- [ ] **Step 13: Commit**

```bash
git add -A
git commit -m "feat(appapi): resolve a persona into a model, prompt, and tool subset

SendMessage's third argument becomes a persona ID. The persona supplies the
model, its body becomes the base system prompt (library items follow as
reference material), and its tools: list subsets the registry. An unknown
persona is a hard config error — no silent fallback, because a substitution
would attribute output to the wrong assistant."
```

---

### Task 7: Frontend — persona picker, colored bubbles, model chip

**Files:**
- Modify: `frontend/index.html` — `#modelSel` becomes `#personaSel`
- Modify: `frontend/src/main.ts` — persona cache, picker, bubble attribution header, footer
- Modify: `frontend/src/style.css` — persona stripe, dot, name, model chip
- Regenerate: `frontend/wailsjs/`

**Interfaces:**
- Consumes: `App.Personas()`, `App.SendMessage(convID, text, personaID)`, `App.SetConversationPersona(convID, personaID)`, `EventDTO.personaId` / `.modelId`, and the `chat:run_started` payload's `personaID` / `modelID`.
- Produces: no downstream consumers — this is the last code task.

- [ ] **Step 1: Regenerate the bindings**

Run:
```bash
wails generate module
```
Verify `frontend/wailsjs/go/appapi/API.d.ts` now declares `Personas()`, `SetConversationPersona(arg1, arg2)`, and no `SetConversationMeta`.

- [ ] **Step 2: Rename the picker in the markup**

In `frontend/index.html`, change line 20:

```html
            <select id="modelSel"></select>
```
to:
```html
            <select id="personaSel"></select>
```

- [ ] **Step 3: Cache personas and populate the picker**

In `frontend/src/main.ts`, replace the `modelSel` handle (line 41):

```ts
const personaSel = $('personaSel') as HTMLSelectElement
```

Add a persona cache next to `cachedModels` (line 21):

```ts
type PersonaInfo = { id: string; name: string; model: string; color: string }
let cachedPersonas: PersonaInfo[] = []

const NEUTRAL_COLOR = '#8a8a90'

function personaById(id: string): PersonaInfo | undefined {
  return cachedPersonas.find(p => p.id === id)
}

// modelLabel is what the bubble's model chip shows: the display name the
// operator gave the model in models.yaml, falling back to the raw ID.
function modelLabel(modelID: string): string {
  const m = cachedModels.find(x => x.id === modelID)
  return m?.display || modelID
}
```

`cachedModels` is currently typed `{ id: string; maxContext?: number }[]`. Widen it so `modelLabel` can read the display name:

```ts
let cachedModels: { id: string; display?: string; maxContext?: number }[] = []
```

Replace `loadMeta` (line 327):

```ts
async function loadMeta() {
  cachedModels = (await App.Models()) || []
  cachedPersonas = (await App.Personas()) || []
  personaSel.innerHTML = cachedPersonas
    .map(p => `<option value="${p.id}">${p.name}</option>`)
    .join('')
}
```

- [ ] **Step 4: Give the run bubble an attribution header**

In `frontend/src/main.ts`, replace `ensureRunBubble` (line 95):

```ts
function ensureRunBubble(runId: string, personaId = '', modelId = ''): RunBubble {
  let b = runBubbles.get(runId)
  if (!b) {
    const el = document.createElement('div')
    el.className = 'msg assistant'
    thread.appendChild(el)
    thread.scrollTop = thread.scrollHeight
    b = { el, curText: null, tools: new Map() }
    runBubbles.set(runId, b)
  }
  applyAttribution(b, personaId, modelId)
  return b
}

// applyAttribution stamps the bubble with who spoke and on which model. Both
// the live path (chat:run_started) and the replay path (event.personaId /
// event.modelId) call it with the same two IDs, so a reopened conversation is
// colored identically to what the operator watched stream in.
//
// A run with no persona (recorded before personas existed) shows the model chip
// alone in a neutral color — honest about what is known, rather than inventing
// an assistant. A persona ID with no matching file (the operator deleted it)
// shows the literal ID, also neutral. Neither is an error.
function applyAttribution(b: RunBubble, personaId: string, modelId: string) {
  if (!personaId && !modelId) return
  if (b.el.querySelector('.msg-attrib')) return

  const p = personaId ? personaById(personaId) : undefined
  b.el.style.setProperty('--persona-color', p?.color || NEUTRAL_COLOR)
  if (personaId) b.el.dataset.persona = personaId

  const row = document.createElement('div')
  row.className = 'msg-attrib'

  if (personaId) {
    const dot = document.createElement('span')
    dot.className = 'persona-dot'
    const name = document.createElement('span')
    name.className = 'persona-name'
    name.textContent = p?.name || personaId
    row.append(dot, name)
  }
  if (modelId) {
    const chip = document.createElement('span')
    chip.className = 'model-chip'
    chip.textContent = modelLabel(modelId)
    row.appendChild(chip)
  }
  b.el.insertBefore(row, b.el.firstChild)
}
```

- [ ] **Step 5: Keep the grounding header below the attribution row**

`setRunGrounding` (line 165) inserts at `firstChild`, which would push the grounding line above the persona name. Change its insertion:

```ts
function setRunGrounding(runId: string, status: string, sourceCount: number) {
  if (status !== 'ready') return
  const b = ensureRunBubble(runId)
  if (b.el.querySelector('.grounding-header')) return
  const h = document.createElement('div')
  h.className = 'grounding-header'
  h.textContent = `↳ grounded · ${sourceCount || 0} sources`
  const attrib = b.el.querySelector('.msg-attrib')
  if (attrib) b.el.insertBefore(h, attrib.nextSibling)
  else b.el.insertBefore(h, b.el.firstChild)
}
```

- [ ] **Step 6: Attribute the live bubble from run_started**

In `frontend/src/main.ts`, change the `chat:run_started` handler (line 386):

```ts
EventsOn('chat:run_started', (p: any) => {
  if (p.convID !== activeConv) return
  ensureRunBubble(p.runID, p.personaID || '', p.modelID || '')
})
```

- [ ] **Step 7: Attribute replayed bubbles from the event log**

In `openConversation` (line 273), create the bubble with its attribution before dispatching the event. Insert this immediately after the `if (!ev.runId) continue` line:

```ts
    if (!ev.runId) continue
    // Create the bubble with its attribution before any content lands in it, so
    // a replayed run is colored exactly as the live one was.
    ensureRunBubble(ev.runId, (ev as any).personaId || '', (ev as any).modelId || '')
```

The subsequent `appendRunText` / `addRunToolCall` / `updateRunToolResult` calls find the bubble already built, and their own `ensureRunBubble(runId)` calls become no-ops.

Also update the comment above the loop — the footer note is still accurate, but add the attribution fact:

```ts
  // History is the canonical display timeline: the active completed run per
  // turn (or the latest terminal run, so cancelled/errored partial output the
  // user saw is preserved). Each assistant event carries the persona and model
  // that produced it (joined from runs), so replayed bubbles are colored the
  // same as live ones. Token usage is not carried on events, so the footer
  // stays empty until the next live turn emits chat:usage.
```

- [ ] **Step 8: Restore the pinned persona and send as it**

In `openConversation`, replace the pinned-model restore (lines 311–317):

```ts
  const convs = (await App.ListConversations()) || []
  const c = convs.find(x => x.id === id)
  if (c && c.pinnedPersona) {
    if (Array.from(personaSel.options).some(o => o.value === c.pinnedPersona)) {
      personaSel.value = c.pinnedPersona
    }
  }
```

In `send()` (line 357), replace the two calls:

```ts
    await App.SendMessage(activeConv!, text, personaSel.value)
    await App.SetConversationPersona(activeConv!, personaSel.value)
```

- [ ] **Step 9: Show the active persona in the footer**

In `updateFooter` (line 23), append the persona name:

```ts
  const persona = personaSel.selectedOptions[0]?.text || ''
  const who = persona ? ` · ${persona}` : ''
  el.textContent = `context ${prefix}${fmt(occ)}${denom} · this turn ${fmt(u.input)}→${fmt(u.output)} · cache ${fmt(u.cached)}${who}`
```

- [ ] **Step 10: Style the persona bubble**

In `frontend/src/style.css`, extend the `/* ---- Agentic run bubbles ---- */` block. Add these rules after line 63:

```css
/* Persona attribution. --persona-color is set inline per bubble from the
   persona registry; one rule set, parameterized — no generated stylesheet. */
.msg.assistant { --persona-color: #8a8a90; border-left: 3px solid var(--persona-color); padding-left: 9px; }
.msg-attrib { display: flex; align-items: center; gap: 6px; margin-bottom: 5px; font-size: 11px; }
.persona-dot { flex: none; width: 8px; height: 8px; border-radius: 50%; background: var(--persona-color); }
.persona-name { color: var(--persona-color); font-weight: 600; }
.model-chip { color: #6f6f76; background: #16161a; border: 1px solid #2b2b30; border-radius: 999px;
  padding: 1px 7px; font-family: ui-monospace, "Cascadia Code", Consolas, monospace; font-size: 10px;
  max-width: 190px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
```

The stripe sits on the `.msg.assistant` container, not on `.msg-text`, so one continuous line spans the whole run — text segments, tool blocks, and all — rather than repeating on each segment.

- [ ] **Step 11: Verify the frontend compiles**

Run:
```bash
cd frontend && npm run build
```
Expected: `tsc` clean, `vite build` writes `dist/`. A TS error on `c.pinnedPersona` means the bindings were not regenerated — rerun `wails generate module`.

- [ ] **Step 12: Drive the app and confirm the behavior**

This change has a runtime surface, and the failure mode it exists to prevent (live and replay disagreeing) is invisible to `tsc`. Run the app:

```bash
wails dev
```

Confirm, in order:
1. The picker lists your personas by name (seeded `Assistant` at minimum).
2. Send a message. The bubble shows a colored dot, the persona name in that color, a muted model chip, and a colored left stripe — all present the moment the bubble appears, before the first token lands.
3. Switch to a second persona on a second conversation and send. Different color, different name, correct chip.
4. **Close the conversation, reopen it. The bubbles come back the same colors they were live.** If they do not, the replay join or the `openConversation` attribution call is wrong — fix it before committing.

- [ ] **Step 13: Commit**

```bash
git add -A
git commit -m "feat(frontend): color-code assistant bubbles by persona with a model chip

The model dropdown becomes a persona dropdown. Each run bubble carries an
attribution header — a dot and the persona name in the persona's color, plus a
muted model chip — and a left stripe in that color. Live bubbles are attributed
from chat:run_started, replayed ones from the persona and model joined onto each
event, so history is colored identically to what the operator watched stream in."
```

---

### Task 8: Documentation

**Files:**
- Create: `personas.example/assistant.md`, `personas.example/scout.md`, `personas.example/skeptic.md`
- Modify: `docs/SMOKE.md`
- Modify: `README.md`
- Modify: `BACKLOG.md`

**Interfaces:**
- Consumes: everything above.
- Produces: nothing.

- [ ] **Step 1: Ship example personas**

Create `personas.example/assistant.md`:

```markdown
---
name: Assistant
model: claude-opus-4-8
---
You are a capable, direct assistant. Answer the question that was asked.
State your reasoning when it is load-bearing and skip it when it is not.
If you are uncertain, say so plainly rather than hedging.
```

Create `personas.example/scout.md`:

```markdown
---
name: Scout
model: claude-opus-4-8
color: "#4fb3ff"
---
You are Scout. You find the angle nobody else is looking at.

Given a problem or an idea, your job is to widen the option space before
anyone narrows it. Surface adjacent possibilities, prior art, and the
framing that has not been tried. Prefer three sharp, distinct directions
over ten shallow ones.

Do not evaluate or rank. That is someone else's job.
```

Create `personas.example/skeptic.md`:

```markdown
---
name: Skeptic
model: gpt-5
color: "#ff7b72"
tools: [safe_math]
---
You are Skeptic. You look for the reason this fails.

Attack the strongest version of the argument, not a weak restatement of it.
Name the specific assumption that has to hold, and say what would have to be
true of the world for it not to. Where a claim rests on numbers, check them.

If you cannot find a real problem, say so — a manufactured objection is worse
than none.
```

Copy the model IDs to match the operator's actual `models.yaml`; these are the defaults from `models.example.yaml`.

- [ ] **Step 2: Add the persona smoke steps**

In `docs/SMOKE.md`, remove any assignment-solving section (deleted in Tasks 1–2) and add:

```markdown
## Personas

1. **Picker.** The composer's dropdown lists every persona by name. A persona
   file with a typo (unknown model, unknown tool, bad color) is *absent* from the
   list, and its rejection appears in the startup banner naming the file and the
   reason.
2. **Attribution.** Send a message. The bubble carries a colored dot, the persona
   name in that color, a muted model chip, and a colored left stripe — all
   present before the first token arrives.
3. **Two personas.** Send as persona A in one conversation and persona B in
   another. Distinct colors, correct names, correct model chips.
4. **Replay parity (the important one).** Close a conversation and reopen it.
   Every bubble returns in the same color it was live, with the same name and
   chip. Live/replay divergence is the failure this design exists to prevent —
   if it happens, stop and fix it.
5. **Deleted persona.** Delete a persona's markdown file, relaunch, open a
   conversation it spoke in. Its bubbles render neutral gray with the literal
   persona ID as the name. No error, no blank thread.
6. **Recolor.** Change a persona's `color:` in its file and relaunch. That
   persona's *history* recolors, not just new messages.
7. **Legacy run.** Open a conversation from before personas existed. Its bubbles
   are neutral gray and carry only a model chip — no persona name.
8. **Unknown persona.** With the app running, delete the persona currently
   selected in the picker and send. The send fails with a config error naming the
   assistant — it does not silently fall back to another persona.
```

- [ ] **Step 3: Update the README**

In `README.md`, replace the accounting/assignments description with the persona model. Cover:
- Starshp is a personal team of assistants. One persona per conversation.
- Personas live in `<app-dir>/personas/` as markdown with YAML frontmatter (`name`, `model`, optional `color`, `tools`, `library`). Copy `personas.example/` to get started.
- The filename stem is the persona ID.
- `color` is optional — omit it and one is assigned deterministically from a contrast-checked palette.
- `library:` auto-attaches library items whenever that persona runs; the conversation's own library items are appended after.
- A persona file that fails validation is disabled and reported in the startup banner, never fatal.
- Remove the assignments/`_json` section entirely.

- [ ] **Step 4: Note Spec 2 in the backlog**

Add to `BACKLOG.md`:

```markdown
- **Multi-persona threads (Spec 2).** `@Persona` routing within one conversation,
  with baton-pass context: a persona receives the operator's messages plus the
  immediately preceding persona's output, not the full shared thread. Requires
  deciding how a mid-thread persona switch interacts with the `active_for_replay`
  run model. Spec 1 (one persona per conversation) shipped first deliberately, so
  personas could be lived with before this design risk is taken.
```

- [ ] **Step 5: Verify the whole thing one more time**

Run:
```bash
go build ./... && go test ./...
cd frontend && npm run build
```
Expected: PASS, clean build.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "docs: persona setup, smoke steps, and the Spec 2 backlog entry"
```
