# Assignment Solver Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Point Starshp at a companion-exported question directory and concurrently solve every question — producing worked answers, structured answer JSON, and per-question confidence/flags — reviewable in-app.

**Architecture:** A new `internal/assignment` package (loader → renderer → orchestrator) reads the companion `manifest.json` + per-question JSON, renders each question into a prompt, and fans out bounded-concurrent `chat.Service.Send` calls — one run per question. Each solver must call a per-question `submit_answer` tool whose schema-validated input *is* the answer; the orchestrator recovers it from the persisted `assistant_tool_call` event. Results land in two new store tables, sibling `_answers/NNN.json` files, and an Assignments review view that reuses the existing run/event rendering.

**Tech Stack:** Go 1.25, Wails v2, modernc.org/sqlite, `github.com/xeipuuv/gojsonschema` (already a dep, used by the tool registry), vanilla TypeScript + Vite. No new Go dependencies.

**Spec:** [`docs/superpowers/specs/2026-06-03-assignment-solver-design.md`](../specs/2026-06-03-assignment-solver-design.md)

---

## File Map

**New package `internal/assignment`:**
- `internal/assignment/question.go` — `Question`, `Type` consts, `MultipleChoiceBody`, `WorksheetBody`, `Tab`/`Table`/`Row`/`Cell`, `Manifest`
- `internal/assignment/question_test.go`
- `internal/assignment/load.go` — `Load(dir) (*Loaded, error)` reads manifest + per-question JSON
- `internal/assignment/load_test.go`
- `internal/assignment/cells.go` — `AnswerableCells(q) []CellRef` (the answerable-cell rule)
- `internal/assignment/cells_test.go`
- `internal/assignment/render.go` — `RenderPrompt(q) (system, user string)` (MC + worksheet)
- `internal/assignment/render_test.go`
- `internal/assignment/answer.go` — `Answer` types, `Flag` vocabulary, `BuildSubmitAnswerSchema(q)`
- `internal/assignment/answer_test.go`
- `internal/assignment/submittool.go` — `SubmitAnswer` `tools.Tool` implementation
- `internal/assignment/submittool_test.go`
- `internal/assignment/grounding.go` — `GroundingSource` interface + `TextbookGrounding`
- `internal/assignment/grounding_test.go`
- `internal/assignment/orchestrator.go` — `Orchestrator`, `Options`, `ProviderFactory`, `Emitter`, `Run`
- `internal/assignment/orchestrator_test.go`
- `internal/assignment/output.go` — `writeAnswerFile(dir, item, answer)`
- `internal/assignment/output_test.go`
- `internal/assignment/testdata/mod04/_json/manifest.json` — copied real fixture
- `internal/assignment/testdata/mod04/_json/001.json` — copied real MC fixture
- `internal/assignment/testdata/mod04/_json/004.json` — copied real worksheet fixture

**Modified packages:**
- `internal/store/schema.go` — add `assignments` + `assignment_items` DDL + indexes
- `internal/store/migrate.go` — add nullable `conversations.assignment_id` column
- `internal/store/store.go` — add `busy_timeout` pragma to the DSN
- `internal/store/migrate_test.go` — assertions for the new tables/column
- `internal/store/assignments.go` — assignment/item CRUD + `SetConversationAssignment`
- `internal/store/assignments_test.go`
- `internal/store/replay.go` — add `GetSubmittedAnswer(runID)` (or a new `answers.go`)
- `internal/store/conversations.go` — `ListConversations` excludes assignment-tagged convos
- `internal/appapi/api.go` — construct `Orchestrator`; `SolveAssignment` / `CancelAssignment` / `ListAssignments` / `GetAssignment` / `ListAssignmentItems`
- `internal/appapi/api_test.go` — method tests with a fake provider
- `frontend/src/main.ts` — Assignments view: folder pick, progress, item list, drill-in
- `frontend/src/style.css` — assignment list / item / confidence-badge / flag styles
- `frontend/wailsjs/go/appapi/API.d.ts`, `API.js`, `models.ts` — regenerated
- `docs/SMOKE.md` — new manual smoke section

---

## Phase 1 — Input model

## Task 1: Question + manifest types and JSON loader

**Files:**
- Create: `internal/assignment/question.go`
- Create: `internal/assignment/load.go`
- Create: `internal/assignment/load_test.go`
- Create: `internal/assignment/testdata/mod04/_json/{manifest.json,001.json,004.json}`

- [ ] **Step 1: Copy the real fixtures into testdata**

Copy these three files verbatim from the sample export into the testdata dir:
- `C:\Users\weldo\OneDrive\School\LSUA_PBC_Accounting\acct4421_gov-nonprof-acct\mod04\hw07-08\_json\manifest.json` → `internal/assignment/testdata/mod04/_json/manifest.json`
- `…\hw07-08\_json\001.json` → `internal/assignment/testdata/mod04/_json/001.json`
- `…\hw07-08\_json\004.json` → `internal/assignment/testdata/mod04/_json/004.json`

(001.json is a `multipleChoice`; 004.json is a `worksheet`. The manifest lists 24 questions — that is fine; the loader only loads files present in testdata, see Step 5.)

- [ ] **Step 2: Write the failing test**

Create `internal/assignment/load_test.go`:
```go
package assignment

import (
	"path/filepath"
	"testing"
)

func testdataDir(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "mod04", "_json")
}

func TestLoad_ParsesManifestAndQuestions(t *testing.T) {
	loaded, err := Load(testdataDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Manifest.Count != 24 {
		t.Fatalf("manifest count want 24, got %d", loaded.Manifest.Count)
	}
	// Only the two fixtures we copied are loadable; the rest are listed in the
	// manifest but absent on disk and recorded as load errors, not fatal.
	byPath := map[string]Question{}
	for _, q := range loaded.Questions {
		byPath[q.Path] = q
	}
	mc, ok := byPath["001.html"]
	if !ok {
		t.Fatal("001.html not loaded")
	}
	if mc.Type != TypeMultipleChoice {
		t.Fatalf("001 type want multipleChoice, got %q", mc.Type)
	}
	if mc.MultipleChoice == nil || len(mc.MultipleChoice.Choices) != 4 {
		t.Fatalf("001 should have 4 choices, got %+v", mc.MultipleChoice)
	}
	if mc.MultipleChoice.Stem == "" {
		t.Fatal("001 stem should be non-empty")
	}

	ws, ok := byPath["004.html"]
	if !ok {
		t.Fatal("004.html not loaded")
	}
	if ws.Type != TypeWorksheet {
		t.Fatalf("004 type want worksheet, got %q", ws.Type)
	}
	if ws.Worksheet == nil || ws.Worksheet.Scenario == "" {
		t.Fatal("004 worksheet should have a scenario")
	}
	if len(ws.Worksheet.Tabs) == 0 {
		t.Fatal("004 should have tabs")
	}
	// Spot-check a known cell exists with its stable id.
	var found bool
	for _, tab := range ws.Worksheet.Tabs {
		for _, tbl := range tab.Tables {
			for _, row := range tbl.Rows {
				for _, c := range row.Cells {
					if c.ID == "0_table0_cell_c2_r0" {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("expected cell id 0_table0_cell_c2_r0 in 004 worksheet")
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/assignment/... -run TestLoad -v`
Expected: FAIL — package/types undefined.

- [ ] **Step 4: Implement the types**

Create `internal/assignment/question.go`:
```go
// Package assignment loads companion-exported question sets and solves them in
// a bounded-concurrent fan-out over the agentic chat loop.
package assignment

// Type is the companion question kind.
type Type string

const (
	TypeMultipleChoice Type = "multipleChoice"
	TypeWorksheet      Type = "worksheet"
	TypeUnsupported    Type = "unsupported" // any companion type we do not solve
)

// Manifest is the companion's _json/manifest.json.
type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	GeneratedFrom string            `json:"generatedFrom"`
	Count         int               `json:"count"`
	Questions     []ManifestEntry   `json:"questions"`
}

type ManifestEntry struct {
	Path  string `json:"path"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

// Question is one fully-loaded companion question (NNN.json).
type Question struct {
	Path           string
	Type           Type
	Title          string
	Warnings       []string
	MultipleChoice *MultipleChoiceBody // set when Type == TypeMultipleChoice
	Worksheet      *WorksheetBody      // set when Type == TypeWorksheet
}

type MultipleChoiceBody struct {
	Stem    string   `json:"stem"`
	Choices []Choice `json:"choices"`
}

type Choice struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

type WorksheetBody struct {
	Scenario string   `json:"scenario"`
	Required []string `json:"required"`
	Tabs     []Tab    `json:"tabs"`
}

type Tab struct {
	Label  string  `json:"label"`
	Tables []Table `json:"tables"`
}

type Table struct {
	Headers []string `json:"headers"`
	Rows    []Row    `json:"rows"`
}

type Row struct {
	Label string `json:"label"`
	Cells []Cell `json:"cells"`
}

// Cell mirrors a companion worksheet cell. Value/Formula are pointers so a JSON
// null (blank, answerable) is distinguishable from an empty string.
type Cell struct {
	ID       string   `json:"id"`
	CellType string   `json:"cellType"` // input | dropdown | readonly | formula
	AriaLabel string  `json:"ariaLabel"`
	Formula  *string  `json:"formula"`
	Value    *string  `json:"value"`
	Options  []string `json:"options"`
}
```

- [ ] **Step 5: Implement the loader**

Create `internal/assignment/load.go`:
```go
package assignment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Loaded is the result of reading a companion _json directory.
type Loaded struct {
	Dir         string
	Manifest    Manifest
	Questions   []Question         // successfully loaded, in manifest order
	LoadErrors  map[string]string  // path -> error, for files listed but unreadable
}

// rawQuestion is the on-disk per-question JSON envelope.
type rawQuestion struct {
	Type     string          `json:"type"`
	Title    string          `json:"title"`
	Warnings []string        `json:"warnings"`
	Body     json.RawMessage `json:"body"`
}

// Load reads manifest.json and every per-question NNN.json it references.
// Files listed in the manifest but missing/unreadable are recorded in
// LoadErrors rather than failing the whole load.
func Load(dir string) (*Loaded, error) {
	mb, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var man Manifest
	if err := json.Unmarshal(mb, &man); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	out := &Loaded{Dir: dir, Manifest: man, LoadErrors: map[string]string{}}
	for _, entry := range man.Questions {
		jsonName := jsonFileFor(entry.Path)
		q, err := loadQuestion(filepath.Join(dir, jsonName), entry)
		if err != nil {
			out.LoadErrors[entry.Path] = err.Error()
			continue
		}
		out.Questions = append(out.Questions, q)
	}
	return out, nil
}

// jsonFileFor maps "001.html" -> "001.json".
func jsonFileFor(htmlPath string) string {
	ext := filepath.Ext(htmlPath)
	return htmlPath[:len(htmlPath)-len(ext)] + ".json"
}

func loadQuestion(path string, entry ManifestEntry) (Question, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Question{}, err
	}
	var raw rawQuestion
	if err := json.Unmarshal(b, &raw); err != nil {
		return Question{}, fmt.Errorf("parse %s: %w", path, err)
	}
	q := Question{Path: entry.Path, Title: raw.Title, Warnings: raw.Warnings}
	switch raw.Type {
	case string(TypeMultipleChoice):
		q.Type = TypeMultipleChoice
		var body MultipleChoiceBody
		if err := json.Unmarshal(raw.Body, &body); err != nil {
			return Question{}, fmt.Errorf("parse mc body %s: %w", path, err)
		}
		q.MultipleChoice = &body
	case string(TypeWorksheet):
		q.Type = TypeWorksheet
		var body WorksheetBody
		if err := json.Unmarshal(raw.Body, &body); err != nil {
			return Question{}, fmt.Errorf("parse worksheet body %s: %w", path, err)
		}
		q.Worksheet = &body
	default:
		q.Type = TypeUnsupported
	}
	return q, nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/assignment/... -run TestLoad -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```
git add internal/assignment/question.go internal/assignment/load.go internal/assignment/load_test.go internal/assignment/testdata
git commit -m "feat(assignment): companion question/manifest types + JSON loader"
```

---

## Phase 2 — Rendering

## Task 2: Answerable-cell extraction

**Files:**
- Create: `internal/assignment/cells.go`
- Create: `internal/assignment/cells_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/assignment/cells_test.go`:
```go
package assignment

import "testing"

func loadWorksheet(t *testing.T) Question {
	t.Helper()
	loaded, err := Load(testdataDir(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range loaded.Questions {
		if q.Path == "004.html" {
			return q
		}
	}
	t.Fatal("004.html not loaded")
	return Question{}
}

func TestAnswerableCells_ExcludesFormulaReadonlyPrefilled(t *testing.T) {
	q := loadWorksheet(t)
	refs := AnswerableCells(q)
	ids := map[string]bool{}
	for _, r := range refs {
		ids[r.ID] = true
	}
	// A blank input cell IS answerable.
	if !ids["0_table0_cell_c2_r0"] {
		t.Error("blank input cell c2_r0 should be answerable")
	}
	// A formula cell is NOT answerable (it is auto-computed).
	if ids["0_table0_cell_c2_r13"] { // Req B "Total Operating Expenses" formula
		t.Error("formula cell c2_r13 must not be answerable")
	}
	// A prefilled input cell (value != null, e.g. "WASHINGTON CITY") is NOT answerable.
	if ids["0_table0_cell_c0_r0"] {
		t.Error("prefilled cell c0_r0 must not be answerable")
	}
	if len(refs) == 0 {
		t.Fatal("expected some answerable cells")
	}
}

func TestAnswerableCells_MultipleChoiceIsNil(t *testing.T) {
	loaded, _ := Load(testdataDir(t))
	for _, q := range loaded.Questions {
		if q.Type == TypeMultipleChoice {
			if AnswerableCells(q) != nil {
				t.Fatal("MC question has no answerable cells")
			}
			return
		}
	}
	t.Fatal("no MC question loaded")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/assignment/... -run TestAnswerableCells -v`
Expected: FAIL — `AnswerableCells`/`CellRef` undefined.

- [ ] **Step 3: Implement**

Create `internal/assignment/cells.go`:
```go
package assignment

// CellRef identifies one answerable worksheet cell plus the context a model
// needs to fill it.
type CellRef struct {
	ID        string
	TabLabel  string
	RowLabel  string
	AriaLabel string
	IsDropdown bool
	Options   []string
}

// AnswerableCells returns the cells a solver must fill for a worksheet, in
// stable (tab, table, row, cell) order. A cell is answerable iff its cellType
// is input or dropdown AND its value is null (blank). readonly, formula, and
// prefilled cells are excluded — the latter two are computed or given.
// Returns nil for non-worksheet questions.
func AnswerableCells(q Question) []CellRef {
	if q.Type != TypeWorksheet || q.Worksheet == nil {
		return nil
	}
	var out []CellRef
	for _, tab := range q.Worksheet.Tabs {
		for _, tbl := range tab.Tables {
			for _, row := range tbl.Rows {
				for _, c := range row.Cells {
					if (c.CellType == "input" || c.CellType == "dropdown") && c.Value == nil {
						out = append(out, CellRef{
							ID:         c.ID,
							TabLabel:   tab.Label,
							RowLabel:   row.Label,
							AriaLabel:  c.AriaLabel,
							IsDropdown: c.CellType == "dropdown",
							Options:    c.Options,
						})
					}
				}
			}
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/assignment/... -run TestAnswerableCells -v`
Expected: PASS.

(If a referenced cell id assertion fails because the fixture differs, open `004.json`, pick a real blank input cell id, a real formula cell id, and a real prefilled cell id, and update the test literals — the rule under test is the categorization, not the specific ids.)

- [ ] **Step 5: Commit**

```
git add internal/assignment/cells.go internal/assignment/cells_test.go
git commit -m "feat(assignment): answerable-cell rule (input/dropdown AND blank)"
```

---

## Task 3: Prompt renderer (MC + worksheet)

**Files:**
- Create: `internal/assignment/render.go`
- Create: `internal/assignment/render_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/assignment/render_test.go`:
```go
package assignment

import (
	"strings"
	"testing"
)

func TestRenderPrompt_MultipleChoice(t *testing.T) {
	loaded, _ := Load(testdataDir(t))
	var mc Question
	for _, q := range loaded.Questions {
		if q.Type == TypeMultipleChoice {
			mc = q
		}
	}
	system, user := RenderPrompt(mc)
	if !strings.Contains(system, "submit_answer") {
		t.Error("system prompt must instruct calling submit_answer")
	}
	if !strings.Contains(user, mc.MultipleChoice.Stem) {
		t.Error("user prompt must contain the stem")
	}
	for i, ch := range mc.MultipleChoice.Choices {
		if !strings.Contains(user, ch.Text) {
			t.Errorf("user prompt missing choice %d text", i)
		}
	}
}

func TestRenderPrompt_WorksheetTagsAnswerableCells(t *testing.T) {
	q := loadWorksheet(t)
	system, user := RenderPrompt(q)
	if !strings.Contains(system, "safe_math") {
		t.Error("worksheet system prompt must require safe_math verification")
	}
	if !strings.Contains(user, q.Worksheet.Scenario) {
		t.Error("user prompt must contain the scenario")
	}
	// Every answerable cell id must appear tagged; no formula cell id should.
	for _, ref := range AnswerableCells(q) {
		if !strings.Contains(user, "⟦"+ref.ID+"⟧") {
			t.Errorf("answerable cell %s not tagged in prompt", ref.ID)
		}
	}
	if strings.Contains(user, "⟦0_table0_cell_c2_r13⟧") {
		t.Error("formula cell must not be tagged as answerable")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/assignment/... -run TestRenderPrompt -v`
Expected: FAIL — `RenderPrompt` undefined.

- [ ] **Step 3: Implement**

Create `internal/assignment/render.go`:
```go
package assignment

import (
	"fmt"
	"strings"
)

const mcSystem = `You are an expert accounting tutor solving a multiple-choice question.
Reason carefully, then call the submit_answer tool exactly once with your chosen
answerIndex, a one-line rationale in notes, your confidence, and any flags. If the
question appears to be missing information needed to answer, still pick your best
answer but add a flag with code "missing_information". After calling submit_answer, stop.`

const worksheetSystem = `You are an expert accounting tutor completing a worksheet exercise.
Work through the scenario and required items. Verify every numeric value with the
safe_math tool before reporting it. Fill the cells you are confident about by calling
submit_answer exactly once with a list of {id, value} entries — use only the cell ids
shown in ⟦ ⟧ tags. Omit any cell you cannot determine and explain why with a flag. Use
flag code "missing_information" when the prompt lacks needed data and
"uncaptured_dropdown_options" when a dropdown's options were not provided. After calling
submit_answer, stop.`

// RenderPrompt produces the (system, user) prompt pair for a question.
func RenderPrompt(q Question) (system, user string) {
	switch q.Type {
	case TypeMultipleChoice:
		return mcSystem, renderMC(q)
	case TypeWorksheet:
		return worksheetSystem, renderWorksheet(q)
	default:
		return "You are an accounting tutor.",
			fmt.Sprintf("Title: %s\n(Unsupported question type; answer from background knowledge.)", q.Title)
	}
}

func renderMC(q Question) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Title: %s\n\n%s\n\nChoices:\n", q.Title, q.MultipleChoice.Stem)
	for _, ch := range q.MultipleChoice.Choices {
		fmt.Fprintf(&b, "  [%d] %s\n", ch.Index, ch.Text)
	}
	return b.String()
}

func renderWorksheet(q Question) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Title: %s\n\nScenario:\n%s\n\n", q.Title, q.Worksheet.Scenario)
	if len(q.Worksheet.Required) > 0 {
		b.WriteString("Required / given information:\n")
		for i, r := range q.Worksheet.Required {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, r)
		}
		b.WriteString("\n")
	}
	for _, tab := range q.Worksheet.Tabs {
		fmt.Fprintf(&b, "== %s ==\n", tab.Label)
		for _, tbl := range tab.Tables {
			for _, row := range tbl.Rows {
				renderRow(&b, row)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("Fill the cells tagged ⟦id⟧. Do not fill auto-computed cells.\n")
	return b.String()
}

func renderRow(b *strings.Builder, row Row) {
	if row.Label != "" {
		fmt.Fprintf(b, "%s:", row.Label)
	}
	for _, c := range row.Cells {
		switch {
		case c.CellType == "formula" && c.Formula != nil:
			fmt.Fprintf(b, " (auto: %s)", *c.Formula)
		case c.CellType == "readonly" && c.Value != nil:
			fmt.Fprintf(b, " %s", *c.Value)
		case c.Value != nil: // prefilled input/given
			fmt.Fprintf(b, " %s", *c.Value)
		case c.CellType == "input" || c.CellType == "dropdown":
			ctx := c.AriaLabel
			if c.IsDropdownContext() && len(c.Options) > 0 {
				ctx = fmt.Sprintf("%s; options: %s", ctx, strings.Join(c.Options, ", "))
			}
			if ctx != "" {
				fmt.Fprintf(b, " ⟦%s⟧(%s)", c.ID, ctx)
			} else {
				fmt.Fprintf(b, " ⟦%s⟧", c.ID)
			}
		}
	}
	b.WriteString("\n")
}
```

Add a tiny helper to `question.go`:
```go
// IsDropdownContext reports whether the cell is a dropdown (used by the renderer).
func (c Cell) IsDropdownContext() bool { return c.CellType == "dropdown" }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/assignment/... -run TestRenderPrompt -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/assignment/render.go internal/assignment/render_test.go internal/assignment/question.go
git commit -m "feat(assignment): MC + worksheet prompt renderer with cell-id tags"
```

---

## Phase 3 — The submit_answer tool

## Task 4: Answer types, flag vocabulary, and per-question schema builder

**Files:**
- Create: `internal/assignment/answer.go`
- Create: `internal/assignment/answer_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/assignment/answer_test.go`:
```go
package assignment

import (
	"encoding/json"
	"testing"

	"github.com/xeipuuv/gojsonschema"
)

func validate(t *testing.T, schema json.RawMessage, doc string) *gojsonschema.Result {
	t.Helper()
	s, err := gojsonschema.NewSchema(gojsonschema.NewBytesLoader(schema))
	if err != nil {
		t.Fatalf("schema invalid: %v", err)
	}
	res, err := s.Validate(gojsonschema.NewStringLoader(doc))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestBuildSubmitAnswerSchema_MC_BoundsIndex(t *testing.T) {
	loaded, _ := Load(testdataDir(t))
	var mc Question
	for _, q := range loaded.Questions {
		if q.Type == TypeMultipleChoice {
			mc = q
		}
	}
	schema := BuildSubmitAnswerSchema(mc) // 4 choices -> 0..3
	if !validate(t, schema, `{"confidence":"high","answerIndex":3}`).Valid() {
		t.Error("index 3 should be valid for a 4-choice question")
	}
	if validate(t, schema, `{"confidence":"high","answerIndex":4}`).Valid() {
		t.Error("index 4 should be rejected for a 4-choice question")
	}
	if validate(t, schema, `{"answerIndex":0}`).Valid() {
		t.Error("missing confidence should be rejected")
	}
}

func TestBuildSubmitAnswerSchema_Worksheet_EnumeratesCellIDs(t *testing.T) {
	q := loadWorksheet(t)
	schema := BuildSubmitAnswerSchema(q)
	good := `{"confidence":"medium","cells":[{"id":"0_table0_cell_c2_r0","value":"54800"}]}`
	if !validate(t, schema, good).Valid() {
		t.Error("a real answerable cell id should be accepted")
	}
	bad := `{"confidence":"medium","cells":[{"id":"not_a_real_cell","value":"1"}]}`
	if validate(t, schema, bad).Valid() {
		t.Error("an unknown cell id should be rejected")
	}
}

func TestBuildSubmitAnswerSchema_FlagVocabulary(t *testing.T) {
	loaded, _ := Load(testdataDir(t))
	var mc Question
	for _, q := range loaded.Questions {
		if q.Type == TypeMultipleChoice {
			mc = q
		}
	}
	schema := BuildSubmitAnswerSchema(mc)
	ok := `{"confidence":"low","answerIndex":0,"flags":[{"code":"missing_information","detail":"no rate given"}]}`
	if !validate(t, schema, ok).Valid() {
		t.Error("known flag code should be accepted")
	}
	bad := `{"confidence":"low","answerIndex":0,"flags":[{"code":"banana","detail":"x"}]}`
	if validate(t, schema, bad).Valid() {
		t.Error("unknown flag code should be rejected")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/assignment/... -run TestBuildSubmitAnswerSchema -v`
Expected: FAIL — `BuildSubmitAnswerSchema` undefined.

- [ ] **Step 3: Implement**

Create `internal/assignment/answer.go`:
```go
package assignment

import "encoding/json"

// FlagCodes is the closed vocabulary for submit_answer flags.
var FlagCodes = []string{
	"missing_information",
	"uncaptured_dropdown_options",
	"ambiguous_requirement",
	"out_of_scope",
	"low_confidence",
}

// Flag is one structured concern about a question/answer.
type Flag struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
	CellID string `json:"cellId,omitempty"`
}

// Answer is the parsed submit_answer payload (the tool input). For MC,
// AnswerIndex is set; for worksheets, Cells is set.
type Answer struct {
	Confidence  string      `json:"confidence"`
	AnswerIndex *int        `json:"answerIndex,omitempty"`
	AnswerText  string      `json:"answerText,omitempty"`
	Cells       []CellValue `json:"cells,omitempty"`
	Flags       []Flag      `json:"flags,omitempty"`
	Notes       string      `json:"notes,omitempty"`
}

type CellValue struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// BuildSubmitAnswerSchema builds a JSON Schema tailored to one question:
// MC bounds answerIndex to the choice count; worksheet enumerates answerable
// cell ids. Flags use the closed FlagCodes vocabulary.
func BuildSubmitAnswerSchema(q Question) json.RawMessage {
	flagSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"code":   map[string]any{"enum": toAny(FlagCodes)},
			"detail": map[string]any{"type": "string"},
			"cellId": map[string]any{"type": "string"},
		},
		"required":             []string{"code", "detail"},
		"additionalProperties": false,
	}
	props := map[string]any{
		"confidence": map[string]any{"enum": []any{"high", "medium", "low"}},
		"flags":      map[string]any{"type": "array", "items": flagSchema},
		"notes":      map[string]any{"type": "string"},
	}
	required := []string{"confidence"}

	switch q.Type {
	case TypeMultipleChoice:
		max := 0
		if q.MultipleChoice != nil && len(q.MultipleChoice.Choices) > 0 {
			max = len(q.MultipleChoice.Choices) - 1
		}
		props["answerIndex"] = map[string]any{"type": "integer", "minimum": 0, "maximum": max}
		props["answerText"] = map[string]any{"type": "string"}
		required = append(required, "answerIndex")
	case TypeWorksheet:
		var ids []string
		for _, ref := range AnswerableCells(q) {
			ids = append(ids, ref.ID)
		}
		props["cells"] = map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":    map[string]any{"enum": toAny(ids)},
					"value": map[string]any{"type": "string"},
				},
				"required":             []string{"id", "value"},
				"additionalProperties": false,
			},
		}
		required = append(required, "cells")
	}

	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/assignment/... -run TestBuildSubmitAnswerSchema -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/assignment/answer.go internal/assignment/answer_test.go
git commit -m "feat(assignment): Answer types + per-question submit_answer schema"
```

---

## Task 5: `submit_answer` tool implementation

**Files:**
- Create: `internal/assignment/submittool.go`
- Create: `internal/assignment/submittool_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/assignment/submittool_test.go`:
```go
package assignment

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cajundata/starshp_app/internal/tools"
)

func TestSubmitAnswerTool_RegistersAndValidates(t *testing.T) {
	loaded, _ := Load(testdataDir(t))
	var mc Question
	for _, q := range loaded.Questions {
		if q.Type == TypeMultipleChoice {
			mc = q
		}
	}
	reg := tools.NewRegistry(time.Second)
	if err := reg.Register(NewSubmitAnswer(mc)); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Valid input: not an error, output is a confirmation.
	_, isErr, _, err := reg.Execute(context.Background(), tools.ExecContext{},
		"submit_answer", json.RawMessage(`{"confidence":"high","answerIndex":1}`))
	if err != nil || isErr {
		t.Fatalf("valid answer should succeed: isErr=%v err=%v", isErr, err)
	}
	// Out-of-range index: registry schema validation marks it is_error.
	_, isErr, _, _ = reg.Execute(context.Background(), tools.ExecContext{},
		"submit_answer", json.RawMessage(`{"confidence":"high","answerIndex":9}`))
	if !isErr {
		t.Fatal("out-of-range answerIndex should be a tool-result error")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/assignment/... -run TestSubmitAnswerTool -v`
Expected: FAIL — `NewSubmitAnswer` undefined.

- [ ] **Step 3: Implement**

Create `internal/assignment/submittool.go`:
```go
package assignment

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cajundata/starshp_app/internal/tools"
)

// SubmitAnswer is a per-question tool whose input IS the structured answer.
// The orchestrator recovers the answer from the persisted assistant_tool_call
// event, so Execute only returns a confirmation that ends the model's turn.
type SubmitAnswer struct {
	schema json.RawMessage
}

const SubmitAnswerName = "submit_answer"

func NewSubmitAnswer(q Question) *SubmitAnswer {
	return &SubmitAnswer{schema: BuildSubmitAnswerSchema(q)}
}

func (s *SubmitAnswer) Name() string                 { return SubmitAnswerName }
func (s *SubmitAnswer) InputSchema() json.RawMessage { return s.schema }
func (s *SubmitAnswer) Timeout() time.Duration       { return 5 * time.Second }

func (s *SubmitAnswer) Description() string {
	return "Submit your final structured answer for this question. Call exactly once, then stop."
}

func (s *SubmitAnswer) Execute(_ context.Context, _ tools.ExecContext, _ json.RawMessage) (tools.ExecResult, error) {
	return tools.ExecResult{Output: `{"status":"answer_recorded"}`}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/assignment/... -run TestSubmitAnswerTool -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/assignment/submittool.go internal/assignment/submittool_test.go
git commit -m "feat(assignment): submit_answer tool (input is the answer)"
```

---

## Phase 4 — Persistence

## Task 6: Schema, migration, and busy_timeout

**Files:**
- Modify: `internal/store/schema.go`
- Modify: `internal/store/migrate.go`
- Modify: `internal/store/store.go`
- Modify: `internal/store/migrate_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/migrate_test.go` (reuse the existing `readTableColumns`/`indexExists`/`openTestDB` helpers from the tool-calling migration tests):
```go
func TestMigrate_CreatesAssignments(t *testing.T) {
	db := openTestDB(t)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	cols := readTableColumns(t, db, "assignments")
	for _, want := range []string{
		"id", "source_dir", "title", "manifest_hash", "model",
		"grounding_scope", "status", "total_items", "created_at", "updated_at",
	} {
		if _, ok := cols[want]; !ok {
			t.Errorf("assignments missing column %q", want)
		}
	}
}

func TestMigrate_CreatesAssignmentItems(t *testing.T) {
	db := openTestDB(t)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	cols := readTableColumns(t, db, "assignment_items")
	for _, want := range []string{
		"id", "assignment_id", "seq", "source_path", "type", "title",
		"run_id", "conversation_id", "status", "confidence", "answer_json",
		"flags_json", "answer_path", "error", "created_at", "updated_at",
	} {
		if _, ok := cols[want]; !ok {
			t.Errorf("assignment_items missing column %q", want)
		}
	}
}

func TestMigrate_AddsAssignmentIDToConversations(t *testing.T) {
	db := openTestDB(t)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	cols := readTableColumns(t, db, "conversations")
	if _, ok := cols["assignment_id"]; !ok {
		t.Fatal("conversations missing assignment_id column")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/... -run TestMigrate_CreatesAssignments -run TestMigrate_CreatesAssignmentItems -run TestMigrate_AddsAssignmentID -v`
Expected: FAIL — tables/column do not exist.

- [ ] **Step 3: Add DDL to `schema.go`**

In `internal/store/schema.go`, append these tables to the `schemaSQL` string (before the closing backtick):
```sql
CREATE TABLE IF NOT EXISTS assignments (
  id              TEXT PRIMARY KEY,
  source_dir      TEXT NOT NULL,
  title           TEXT NOT NULL,
  manifest_hash   TEXT NOT NULL,
  model           TEXT NOT NULL,
  grounding_scope TEXT,
  status          TEXT NOT NULL CHECK (status IN (
                      'in_progress','completed','cancelled','errored')),
  total_items     INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS assignment_items (
  id              TEXT PRIMARY KEY,
  assignment_id   TEXT NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
  seq             INTEGER NOT NULL,
  source_path     TEXT NOT NULL,
  type            TEXT NOT NULL,
  title           TEXT,
  run_id          TEXT,
  conversation_id TEXT,
  status          TEXT NOT NULL CHECK (status IN (
                      'pending','solving','answered','no_answer',
                      'errored','cancelled','unsupported')),
  confidence      TEXT,
  answer_json     TEXT,
  flags_json      TEXT,
  answer_path     TEXT,
  error           TEXT,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS assignment_items_assignment
  ON assignment_items(assignment_id, seq);
CREATE INDEX IF NOT EXISTS assignment_items_run ON assignment_items(run_id);
```

- [ ] **Step 4: Add the `conversations.assignment_id` column in `migrate.go`**

In `internal/store/migrate.go`, inside `migrate`, after the `retrieval_mode` block and before `migrateMessagesToEvents`, add:
```go
	has, err = columnExists(db, "conversations", "assignment_id")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE conversations ADD COLUMN assignment_id TEXT`); err != nil {
			return err
		}
	}
```

- [ ] **Step 5: Add `busy_timeout` to the DSN in `store.go`**

In `internal/store/store.go`, change the `dsn` line in `Open`:
```go
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(wal)&_pragma=foreign_keys(on)&_pragma=busy_timeout(5000)", dbPath)
```
WAL is already enabled; `busy_timeout(5000)` makes concurrent writers wait up to 5s for the write lock instead of returning `SQLITE_BUSY` during the fan-out.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/store/... -run TestMigrate -v`
Expected: PASS.

- [ ] **Step 7: Full regression**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 8: Commit**

```
git add internal/store/schema.go internal/store/migrate.go internal/store/store.go internal/store/migrate_test.go
git commit -m "feat(store): assignments + assignment_items tables, conversations.assignment_id, busy_timeout"
```

---

## Task 7: Assignment store CRUD

**Files:**
- Create: `internal/store/assignments.go`
- Create: `internal/store/assignments_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/assignments_test.go`:
```go
package store

import "testing"

func TestAssignmentLifecycle(t *testing.T) {
	st := openTestStore(t)
	a := Assignment{
		ID: "a1", SourceDir: "/d", Title: "mod04", ManifestHash: "h",
		Model: "m", Status: "in_progress", TotalItems: 2,
	}
	if err := st.CreateAssignment(a); err != nil {
		t.Fatal(err)
	}
	it := AssignmentItem{
		ID: "i1", AssignmentID: "a1", Seq: 0, SourcePath: "001.html",
		Type: "multipleChoice", Title: "Item 1", Status: "pending",
	}
	if err := st.CreateAssignmentItem(it); err != nil {
		t.Fatal(err)
	}
	it.Status = "answered"
	it.Confidence = "high"
	it.AnswerJSON = `{"answerIndex":1}`
	it.RunID = "r1"
	if err := st.UpdateAssignmentItem(it); err != nil {
		t.Fatal(err)
	}
	items, err := st.ListAssignmentItems("a1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != "answered" || items[0].Confidence != "high" {
		t.Fatalf("item not updated: %+v", items)
	}
	got, err := st.GetAssignment("a1")
	if err != nil || got.Title != "mod04" {
		t.Fatalf("get assignment: %+v err=%v", got, err)
	}
	list, _ := st.ListAssignments()
	if len(list) != 1 {
		t.Fatalf("want 1 assignment, got %d", len(list))
	}
}

func TestSetConversationAssignment_HidesFromList(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateAssignment(Assignment{ID: "a1", SourceDir: "/d", Title: "t",
		ManifestHash: "h", Model: "m", Status: "in_progress"})
	normal, _ := st.CreateConversation("normal")
	item, _ := st.CreateConversation("item")
	if err := st.SetConversationAssignment(item.ID, "a1"); err != nil {
		t.Fatal(err)
	}
	convs, _ := st.ListConversations()
	for _, c := range convs {
		if c.ID == item.ID {
			t.Fatal("assignment-tagged conversation must be hidden from ListConversations")
		}
	}
	var sawNormal bool
	for _, c := range convs {
		if c.ID == normal.ID {
			sawNormal = true
		}
	}
	if !sawNormal {
		t.Fatal("normal conversation should still be listed")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/... -run TestAssignmentLifecycle -run TestSetConversationAssignment -v`
Expected: FAIL — types/methods undefined.

- [ ] **Step 3: Implement the CRUD**

Create `internal/store/assignments.go`:
```go
package store

import "time"

type Assignment struct {
	ID             string
	SourceDir      string
	Title          string
	ManifestHash   string
	Model          string
	GroundingScope string
	Status         string
	TotalItems     int
	CreatedAt      int64
	UpdatedAt      int64
}

type AssignmentItem struct {
	ID             string
	AssignmentID   string
	Seq            int
	SourcePath     string
	Type           string
	Title          string
	RunID          string
	ConversationID string
	Status         string
	Confidence     string
	AnswerJSON     string
	FlagsJSON      string
	AnswerPath     string
	Error          string
	CreatedAt      int64
	UpdatedAt      int64
}

func (s *Store) CreateAssignment(a Assignment) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(
		`INSERT INTO assignments
            (id, source_dir, title, manifest_hash, model, grounding_scope,
             status, total_items, created_at, updated_at)
         VALUES (?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.SourceDir, a.Title, a.ManifestHash, a.Model, nullIfEmpty(a.GroundingScope),
		a.Status, a.TotalItems, now, now)
	return err
}

func (s *Store) UpdateAssignmentStatus(id, status string) error {
	_, err := s.db.Exec(`UPDATE assignments SET status=?, updated_at=? WHERE id=?`,
		status, time.Now().UnixMilli(), id)
	return err
}

func (s *Store) CreateAssignmentItem(it AssignmentItem) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(
		`INSERT INTO assignment_items
            (id, assignment_id, seq, source_path, type, title, run_id,
             conversation_id, status, confidence, answer_json, flags_json,
             answer_path, error, created_at, updated_at)
         VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		it.ID, it.AssignmentID, it.Seq, it.SourcePath, it.Type, it.Title,
		nullIfEmpty(it.RunID), nullIfEmpty(it.ConversationID), it.Status,
		nullIfEmpty(it.Confidence), nullIfEmpty(it.AnswerJSON), nullIfEmpty(it.FlagsJSON),
		nullIfEmpty(it.AnswerPath), nullIfEmpty(it.Error), now, now)
	return err
}

func (s *Store) UpdateAssignmentItem(it AssignmentItem) error {
	_, err := s.db.Exec(
		`UPDATE assignment_items
            SET status=?, confidence=?, answer_json=?, flags_json=?,
                answer_path=?, error=?, run_id=?, conversation_id=?, updated_at=?
          WHERE id=?`,
		it.Status, nullIfEmpty(it.Confidence), nullIfEmpty(it.AnswerJSON),
		nullIfEmpty(it.FlagsJSON), nullIfEmpty(it.AnswerPath), nullIfEmpty(it.Error),
		nullIfEmpty(it.RunID), nullIfEmpty(it.ConversationID),
		time.Now().UnixMilli(), it.ID)
	return err
}

func (s *Store) GetAssignment(id string) (Assignment, error) {
	var a Assignment
	err := s.db.QueryRow(
		`SELECT id, source_dir, title, manifest_hash, model,
                COALESCE(grounding_scope,''), status, total_items, created_at, updated_at
           FROM assignments WHERE id=?`, id).Scan(
		&a.ID, &a.SourceDir, &a.Title, &a.ManifestHash, &a.Model,
		&a.GroundingScope, &a.Status, &a.TotalItems, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func (s *Store) ListAssignments() ([]Assignment, error) {
	rows, err := s.db.Query(
		`SELECT id, source_dir, title, manifest_hash, model,
                COALESCE(grounding_scope,''), status, total_items, created_at, updated_at
           FROM assignments ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Assignment
	for rows.Next() {
		var a Assignment
		if err := rows.Scan(&a.ID, &a.SourceDir, &a.Title, &a.ManifestHash, &a.Model,
			&a.GroundingScope, &a.Status, &a.TotalItems, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ListAssignmentItems(assignmentID string) ([]AssignmentItem, error) {
	rows, err := s.db.Query(
		`SELECT id, assignment_id, seq, source_path, type, COALESCE(title,''),
                COALESCE(run_id,''), COALESCE(conversation_id,''), status,
                COALESCE(confidence,''), COALESCE(answer_json,''), COALESCE(flags_json,''),
                COALESCE(answer_path,''), COALESCE(error,''), created_at, updated_at
           FROM assignment_items WHERE assignment_id=? ORDER BY seq`, assignmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssignmentItem
	for rows.Next() {
		var it AssignmentItem
		if err := rows.Scan(&it.ID, &it.AssignmentID, &it.Seq, &it.SourcePath, &it.Type,
			&it.Title, &it.RunID, &it.ConversationID, &it.Status, &it.Confidence,
			&it.AnswerJSON, &it.FlagsJSON, &it.AnswerPath, &it.Error,
			&it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) SetConversationAssignment(convID, assignmentID string) error {
	_, err := s.db.Exec(`UPDATE conversations SET assignment_id=? WHERE id=?`,
		assignmentID, convID)
	return err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
```

- [ ] **Step 4: Make `ListConversations` exclude assignment-tagged rows**

In `internal/store/conversations.go`, find `ListConversations` and add `WHERE assignment_id IS NULL` to its `SELECT` (preserve the existing column list and `ORDER BY`). For example, if the query is `SELECT … FROM conversations ORDER BY updated_at DESC`, change it to `SELECT … FROM conversations WHERE assignment_id IS NULL ORDER BY updated_at DESC`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/store/... -run TestAssignment -run TestSetConversationAssignment -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```
git add internal/store/assignments.go internal/store/assignments_test.go internal/store/conversations.go
git commit -m "feat(store): assignment/item CRUD + conversation assignment tagging"
```

---

## Task 8: `GetSubmittedAnswer(runID)`

**Files:**
- Create: `internal/store/answers.go`
- Create: `internal/store/answers_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/answers_test.go`:
```go
package store

import (
	"encoding/json"
	"testing"
)

func TestGetSubmittedAnswer_ReturnsLatestSubmitAnswerInput(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	user, _ := st.AppendUserMessage(conv.ID, "q")
	_ = st.CreateRun(conv.ID, user.TurnID, "r1", "openai", "m", "auto_grounded_default")
	want := json.RawMessage(`{"confidence":"high","answerIndex":2}`)
	if _, err := st.AppendAssistantToolCall(conv.ID, user.TurnID, "r1",
		"call_1", "submit_answer", want); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetSubmittedAnswer("r1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("want %s, got %s", want, got)
	}
}

func TestGetSubmittedAnswer_EmptyWhenNoneSubmitted(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	user, _ := st.AppendUserMessage(conv.ID, "q")
	_ = st.CreateRun(conv.ID, user.TurnID, "r1", "openai", "m", "auto_grounded_default")
	_, _ = st.AppendAssistantText(conv.ID, user.TurnID, "r1", "no tool call here")
	got, err := st.GetSubmittedAnswer("r1")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %s", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/... -run TestGetSubmittedAnswer -v`
Expected: FAIL — `GetSubmittedAnswer` undefined.

- [ ] **Step 3: Implement**

Create `internal/store/answers.go`:
```go
package store

import (
	"database/sql"
	"encoding/json"
)

// GetSubmittedAnswer returns the input JSON of the latest submit_answer
// assistant_tool_call event for a run, or nil if the run never submitted one.
func (s *Store) GetSubmittedAnswer(runID string) (json.RawMessage, error) {
	var input string
	err := s.db.QueryRow(
		`SELECT COALESCE(tool_input,'')
           FROM conversation_events
          WHERE run_id = ? AND kind = 'assistant_tool_call' AND tool_name = 'submit_answer'
          ORDER BY sequence_index DESC
          LIMIT 1`, runID).Scan(&input)
	if err == sql.ErrNoRows || input == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(input), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/store/... -run TestGetSubmittedAnswer -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/store/answers.go internal/store/answers_test.go
git commit -m "feat(store): GetSubmittedAnswer recovers the answer from the event log"
```

---

## Phase 5 — Grounding

## Task 9: `GroundingSource` interface + textbook grounding

**Files:**
- Create: `internal/assignment/grounding.go`
- Create: `internal/assignment/grounding_test.go`

This is the pluggable seam from the spec. v1 wires only textbook grounding. A
grounding source supplies the per-item `chat.Retriever` (or nil) and ensures any
backing index exists before the batch runs.

- [ ] **Step 1: Write the failing test**

Create `internal/assignment/grounding_test.go`:
```go
package assignment

import (
	"context"
	"testing"

	"github.com/cajundata/starshp_app/internal/chat"
)

func TestNoGrounding_ReturnsNilRetriever(t *testing.T) {
	g := NoGrounding{}
	if err := g.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g.Retriever() != nil {
		t.Fatal("NoGrounding must provide a nil retriever")
	}
}

// fakeRetriever proves a GroundingSource can supply a chat.Retriever.
type fakeRetriever struct{}

func (fakeRetriever) Retrieve(_ context.Context, _ string) (string, string, []chat.RetrievedSource, error) {
	return "ctx", "[]", nil, nil
}

func TestStaticGrounding_SuppliesRetriever(t *testing.T) {
	g := StaticGrounding{R: fakeRetriever{}}
	if err := g.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g.Retriever() == nil {
		t.Fatal("StaticGrounding must provide its retriever")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/assignment/... -run Grounding -v`
Expected: FAIL — types undefined.

- [ ] **Step 3: Implement**

Create `internal/assignment/grounding.go`:
```go
package assignment

import (
	"context"

	"github.com/cajundata/starshp_app/internal/chat"
)

// GroundingSource is the pluggable grounding seam. Ensure prepares any backing
// index (idempotent); Retriever returns the chat.Retriever used for pre-turn
// grounding, or nil for no grounding. v1 ships NoGrounding and StaticGrounding;
// a future lesson/content source implements the same interface.
type GroundingSource interface {
	Ensure(ctx context.Context) error
	Retriever() chat.Retriever
}

// NoGrounding disables pre-turn retrieval (model knowledge + any model-called
// tools only).
type NoGrounding struct{}

func (NoGrounding) Ensure(context.Context) error { return nil }
func (NoGrounding) Retriever() chat.Retriever    { return nil }

// StaticGrounding wraps an already-prepared chat.Retriever (e.g. the appapi
// textbook retriever for attached books, whose index EnsureIndexed already
// built). Ensure is a no-op because indexing happens in appapi.
type StaticGrounding struct{ R chat.Retriever }

func (StaticGrounding) Ensure(context.Context) error { return nil }
func (g StaticGrounding) Retriever() chat.Retriever  { return g.R }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/assignment/... -run Grounding -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/assignment/grounding.go internal/assignment/grounding_test.go
git commit -m "feat(assignment): pluggable GroundingSource (NoGrounding + StaticGrounding)"
```

---

## Phase 6 — Orchestrator

## Task 10: Disk output writer

**Files:**
- Create: `internal/assignment/output.go`
- Create: `internal/assignment/output_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/assignment/output_test.go`:
```go
package assignment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAnswerFile_MirrorsSchema(t *testing.T) {
	dir := t.TempDir()
	ans := Answer{Confidence: "medium", Cells: []CellValue{{ID: "x", Value: "1"}}}
	rawAns, _ := json.Marshal(ans)
	path, err := writeAnswerFile(dir, "004.html", "worksheet", "Ex 7-4", "r1", rawAns)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "004.json" {
		t.Fatalf("want 004.json, got %s", filepath.Base(path))
	}
	b, _ := os.ReadFile(path)
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["source"] != "004.html" || got["type"] != "worksheet" || got["runId"] != "r1" {
		t.Fatalf("envelope mismatch: %v", got)
	}
	if _, ok := got["answer"]; !ok {
		t.Fatal("answer field missing")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/assignment/... -run TestWriteAnswerFile -v`
Expected: FAIL — `writeAnswerFile` undefined.

- [ ] **Step 3: Implement**

Create `internal/assignment/output.go`:
```go
package assignment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// writeAnswerFile writes <dir>/_answers/NNN.json mirroring the companion's
// per-question convention. answerRaw is the verbatim submit_answer input.
func writeAnswerFile(dir, sourcePath, qType, title, runID string, answerRaw json.RawMessage) (string, error) {
	outDir := filepath.Join(dir, "_answers")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	name := jsonFileFor(sourcePath)
	envelope := map[string]any{
		"schemaVersion": 1,
		"source":        sourcePath,
		"type":          qType,
		"title":         title,
		"answer":        answerRaw,
		"runId":         runID,
		"solvedAt":      time.Now().UnixMilli(),
	}
	b, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(outDir, name)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/assignment/... -run TestWriteAnswerFile -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/assignment/output.go internal/assignment/output_test.go
git commit -m "feat(assignment): _answers/NNN.json output writer"
```

---

## Task 11: Orchestrator — single-item solve

**Files:**
- Create: `internal/assignment/orchestrator.go`
- Create: `internal/assignment/orchestrator_test.go`

- [ ] **Step 1: Write the failing test (uses the existing fakeprovider)**

Create `internal/assignment/orchestrator_test.go`:
```go
package assignment

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/eval/fakeprovider"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "asg.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// scriptedFactory returns a provider that, for every question, emits a single
// submit_answer tool call then ends — simulating a solver.
func scriptedFactory(answer string) ProviderFactory {
	return func(string) (provider.ChatProvider, string, error) {
		return &fakeprovider.Scripted{Iterations: [][]provider.Delta{
			{
				{ToolCall: &provider.ToolCall{ID: "c1", Name: "submit_answer",
					Input: json.RawMessage(answer)}},
				{Done: true, StopReason: "tool_use"},
			},
			{{Text: "done"}, {Done: true, StopReason: "end_turn"}},
		}}, "openai", nil
	}
}

func newTestOrchestrator(t *testing.T, st *store.Store, pf ProviderFactory) *Orchestrator {
	t.Helper()
	return New(st, chat.New(st), pf, Options{
		Model:       "m",
		Concurrency: 1,
		Grounding:   NoGrounding{},
		Emit:        func(string, any) {},
	})
}

func TestOrchestrator_SolvesOneItem(t *testing.T) {
	st := openStore(t)
	pf := scriptedFactory(`{"confidence":"high","answerIndex":1}`)
	orc := newTestOrchestrator(t, st, pf)

	asgID, err := orc.Run(context.Background(), testdataDir(t))
	if err != nil {
		t.Fatal(err)
	}
	items, _ := st.ListAssignmentItems(asgID)
	var mc *store.AssignmentItem
	for i := range items {
		if items[i].SourcePath == "001.html" {
			mc = &items[i]
		}
	}
	if mc == nil {
		t.Fatal("001.html item not created")
	}
	if mc.Status != "answered" {
		t.Fatalf("MC item status want answered, got %q (err=%q)", mc.Status, mc.Error)
	}
	if mc.Confidence != "high" || mc.RunID == "" {
		t.Fatalf("item not populated: %+v", mc)
	}
	if mc.AnswerPath == "" {
		t.Fatal("answer file path not recorded")
	}
}
```

Allow a generous per-test timeout when running (the worksheet item runs through
the loop too): `-timeout 60s`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/assignment/... -run TestOrchestrator_SolvesOneItem -v`
Expected: FAIL — `Orchestrator`/`New`/`Options`/`ProviderFactory` undefined.

- [ ] **Step 3: Implement the orchestrator core**

Create `internal/assignment/orchestrator.go`:
```go
package assignment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/cajundata/starshp_app/internal/tools"
	"github.com/google/uuid"
)

// ProviderFactory builds a provider for a model id, returning the provider and
// its provider name ("openai"|"anthropic"). Injected so tests use a fake.
type ProviderFactory func(modelID string) (provider.ChatProvider, string, error)

// Options configures a batch run.
type Options struct {
	Model       string
	Concurrency int
	Grounding   GroundingSource
	Emit        func(name string, payload any) // batch progress events; never nil in prod
	// SafeMath and SearchTool, when non-nil, are registered into each item's
	// registry so the solver can verify arithmetic / search textbooks. nil
	// disables that tool. appapi (Task 14) sets SafeMath = safemath.New() and
	// SearchTool = the search_textbook tool; unit tests leave both nil because
	// the scripted provider never calls them.
	SafeMath   tools.Tool
	SearchTool tools.Tool
}

type Orchestrator struct {
	st   *store.Store
	chat *chat.Service
	pf   ProviderFactory
	opts Options
}

func New(st *store.Store, chatSvc *chat.Service, pf ProviderFactory, opts Options) *Orchestrator {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.Emit == nil {
		opts.Emit = func(string, any) {}
	}
	if opts.Grounding == nil {
		opts.Grounding = NoGrounding{}
	}
	return &Orchestrator{st: st, chat: chatSvc, pf: pf, opts: opts}
}

// Run loads the companion directory, creates the assignment + item rows, then
// solves each question. (Concurrency + cancellation are added in Task 12; this
// task establishes the per-item solve, run sequentially.)
func (o *Orchestrator) Run(ctx context.Context, dir string) (string, error) {
	loaded, err := Load(dir)
	if err != nil {
		return "", err
	}
	if err := o.opts.Grounding.Ensure(ctx); err != nil {
		return "", fmt.Errorf("grounding: %w", err)
	}
	asgID := uuid.NewString()
	asg := store.Assignment{
		ID: asgID, SourceDir: dir, Title: titleFor(dir, loaded),
		ManifestHash: hashManifest(loaded), Model: o.opts.Model,
		Status: "in_progress", TotalItems: len(loaded.Questions),
	}
	if err := o.st.CreateAssignment(asg); err != nil {
		return "", err
	}
	o.opts.Emit("assignment:started", map[string]any{
		"assignmentId": asgID, "total": len(loaded.Questions), "title": asg.Title})

	for i, q := range loaded.Questions {
		itemID := uuid.NewString()
		_ = o.st.CreateAssignmentItem(store.AssignmentItem{
			ID: itemID, AssignmentID: asgID, Seq: i, SourcePath: q.Path,
			Type: string(q.Type), Title: q.Title, Status: "pending",
		})
		o.solveItem(ctx, dir, asgID, itemID, q)
	}

	o.st.UpdateAssignmentStatus(asgID, "completed")
	o.opts.Emit("assignment:completed", map[string]any{"assignmentId": asgID})
	return asgID, nil
}

// solveItem runs one question through the agentic loop and persists the result.
func (o *Orchestrator) solveItem(ctx context.Context, dir, asgID, itemID string, q Question) {
	item := store.AssignmentItem{ID: itemID, AssignmentID: asgID, Seq: 0,
		SourcePath: q.Path, Type: string(q.Type), Title: q.Title}

	if q.Type == TypeUnsupported {
		item.Status = "unsupported"
		_ = o.st.UpdateAssignmentItem(item)
		return
	}

	prov, provName, err := o.pf(o.opts.Model)
	if err != nil {
		item.Status = "errored"
		item.Error = err.Error()
		_ = o.st.UpdateAssignmentItem(item)
		return
	}

	conv, err := o.st.CreateConversation(q.Title)
	if err != nil {
		item.Status = "errored"
		item.Error = err.Error()
		_ = o.st.UpdateAssignmentItem(item)
		return
	}
	_ = o.st.SetConversationAssignment(conv.ID, asgID)
	item.ConversationID = conv.ID
	item.Status = "solving"
	_ = o.st.UpdateAssignmentItem(item)
	o.opts.Emit("assignment:item_started",
		map[string]any{"assignmentId": asgID, "seq": item.Seq, "title": q.Title, "type": q.Type})

	reg := o.buildRegistry(q)
	system, user := RenderPrompt(q)
	mode := chat.RetrievalNoRetrieval
	if o.opts.Grounding.Retriever() != nil {
		mode = chat.RetrievalAutoGroundedDefault
	}
	res, sendErr := o.chat.Send(ctx, chat.SendParams{
		ConversationID: conv.ID, UserText: user, SystemPrompt: system,
		Model: o.opts.Model, Provider: prov, ProviderName: provName,
		Registry: reg, Resolver: nil, Retriever: o.opts.Grounding.Retriever(),
		RetrievalMode: mode,
	}, nil)
	item.RunID = res.RunID
	if sendErr != nil {
		item.Status = "errored"
		item.Error = sendErr.Error()
		_ = o.st.UpdateAssignmentItem(item)
		o.emitItemDone(asgID, item)
		return
	}

	raw, _ := o.st.GetSubmittedAnswer(res.RunID)
	if len(raw) == 0 {
		item.Status = "no_answer"
		_ = o.st.UpdateAssignmentItem(item)
		o.emitItemDone(asgID, item)
		return
	}
	var ans Answer
	if err := json.Unmarshal(raw, &ans); err != nil {
		item.Status = "errored"
		item.Error = "unparseable submit_answer: " + err.Error()
		_ = o.st.UpdateAssignmentItem(item)
		o.emitItemDone(asgID, item)
		return
	}
	flagsJSON, _ := json.Marshal(ans.Flags)
	path, _ := writeAnswerFile(dir, q.Path, string(q.Type), q.Title, res.RunID, raw)
	item.Status = "answered"
	item.Confidence = ans.Confidence
	item.AnswerJSON = string(raw)
	item.FlagsJSON = string(flagsJSON)
	item.AnswerPath = path
	_ = o.st.UpdateAssignmentItem(item)
	o.emitItemDone(asgID, item)
}

func (o *Orchestrator) buildRegistry(q Question) *tools.Registry {
	reg := tools.NewRegistry(30 * time.Second)
	_ = reg.Register(NewSubmitAnswer(q))
	if o.opts.SafeMath != nil {
		_ = reg.Register(o.opts.SafeMath)
	}
	if o.opts.SearchTool != nil {
		_ = reg.Register(o.opts.SearchTool)
	}
	return reg
}

func (o *Orchestrator) emitItemDone(asgID string, item store.AssignmentItem) {
	flagCount := 0
	if item.FlagsJSON != "" {
		var fl []Flag
		_ = json.Unmarshal([]byte(item.FlagsJSON), &fl)
		flagCount = len(fl)
	}
	o.opts.Emit("assignment:item_done", map[string]any{
		"assignmentId": asgID, "seq": item.Seq, "status": item.Status,
		"confidence": item.Confidence, "flagCount": flagCount})
}

func hashManifest(l *Loaded) string {
	b, _ := json.Marshal(l.Manifest)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func titleFor(dir string, l *Loaded) string {
	if l.Manifest.GeneratedFrom != "" {
		return l.Manifest.GeneratedFrom
	}
	return dir
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/assignment/... -run TestOrchestrator_SolvesOneItem -v -timeout 60s`
Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/assignment/orchestrator.go internal/assignment/orchestrator_test.go
git commit -m "feat(assignment): orchestrator per-item solve over the agentic loop"
```

---

## Task 12: Orchestrator — bounded concurrency, cancellation, error isolation

**Files:**
- Modify: `internal/assignment/orchestrator.go`
- Modify: `internal/assignment/orchestrator_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/assignment/orchestrator_test.go`:
```go
func TestOrchestrator_AllItemsSolvedConcurrently(t *testing.T) {
	st := openStore(t)
	pf := scriptedFactory(`{"confidence":"high","answerIndex":0}`)
	orc := New(st, chat.New(st), pf, Options{Model: "m", Concurrency: 4,
		Grounding: NoGrounding{}, Emit: func(string, any) {}})
	asgID, err := orc.Run(context.Background(), testdataDir(t))
	if err != nil {
		t.Fatal(err)
	}
	items, _ := st.ListAssignmentItems(asgID)
	if len(items) == 0 {
		t.Fatal("no items")
	}
	for _, it := range items {
		// Worksheet answers use a different shape; the scripted MC answer fails
		// schema validation for the worksheet item, so it lands no_answer — but
		// it must never be left pending/solving.
		if it.Status == "pending" || it.Status == "solving" {
			t.Fatalf("item %s left unfinished: %s", it.SourcePath, it.Status)
		}
	}
}

func TestOrchestrator_CancelStopsBatch(t *testing.T) {
	st := openStore(t)
	pf := scriptedFactory(`{"confidence":"high","answerIndex":0}`)
	orc := New(st, chat.New(st), pf, Options{Model: "m", Concurrency: 1,
		Grounding: NoGrounding{}, Emit: func(string, any) {}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before running
	asgID, _ := orc.Run(ctx, testdataDir(t))
	a, _ := st.GetAssignment(asgID)
	if a.Status != "cancelled" {
		t.Fatalf("assignment status want cancelled, got %q", a.Status)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/assignment/... -run TestOrchestrator_All -run TestOrchestrator_Cancel -v -timeout 60s`
Expected: FAIL — items run sequentially / no cancellation handling yet.

- [ ] **Step 3: Rewrite `Run`'s item loop with a worker pool + cancellation**

Replace the `for i, q := range loaded.Questions { … }` loop and the trailing status update in `Run` with:
```go
	sem := make(chan struct{}, o.opts.Concurrency)
	var wg sync.WaitGroup
	cancelled := false
	for i, q := range loaded.Questions {
		if ctx.Err() != nil {
			cancelled = true
			break
		}
		itemID := uuid.NewString()
		_ = o.st.CreateAssignmentItem(store.AssignmentItem{
			ID: itemID, AssignmentID: asgID, Seq: i, SourcePath: q.Path,
			Type: string(q.Type), Title: q.Title, Status: "pending",
		})
		wg.Add(1)
		sem <- struct{}{}
		go func(itemID string, seq int, q Question) {
			defer wg.Done()
			defer func() { <-sem }()
			o.solveItem(ctx, dir, asgID, itemID, seq, q)
		}(itemID, i, q)
	}
	wg.Wait()

	status := "completed"
	if cancelled || ctx.Err() != nil {
		status = "cancelled"
		o.markUnfinishedCancelled(asgID)
	}
	o.st.UpdateAssignmentStatus(asgID, status)
	o.opts.Emit("assignment:"+statusEvent(status), map[string]any{"assignmentId": asgID})
	return asgID, nil
```

Add `"sync"` to the imports. Update `solveItem`'s signature to accept `seq int`
(set `item.Seq = seq` at construction) and have each early-return path set
`item.Seq`. Add helpers:
```go
func statusEvent(status string) string {
	if status == "cancelled" {
		return "cancelled"
	}
	return "completed"
}

// markUnfinishedCancelled flips any pending/solving items to cancelled after a
// batch is stopped.
func (o *Orchestrator) markUnfinishedCancelled(asgID string) {
	items, _ := o.st.ListAssignmentItems(asgID)
	for _, it := range items {
		if it.Status == "pending" || it.Status == "solving" {
			it.Status = "cancelled"
			_ = o.st.UpdateAssignmentItem(it)
		}
	}
}
```

Per-item error isolation already holds: every `solveItem` failure path sets a
terminal item status and returns; one item's failure never aborts the others.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/assignment/... -run TestOrchestrator -v -timeout 90s`
Expected: PASS (all orchestrator tests, including Task 11's).

- [ ] **Step 5: Commit**

```
git add internal/assignment/orchestrator.go internal/assignment/orchestrator_test.go
git commit -m "feat(assignment): bounded-concurrent fan-out with cancellation + error isolation"
```

---

## Task 13: Re-run / resume (skip already-answered items)

**Files:**
- Modify: `internal/assignment/orchestrator.go`
- Modify: `internal/store/assignments.go`
- Modify: `internal/assignment/orchestrator_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/assignment/orchestrator_test.go`:
```go
func TestOrchestrator_ResumeSkipsAnswered(t *testing.T) {
	st := openStore(t)
	pf := scriptedFactory(`{"confidence":"high","answerIndex":0}`)
	orc := New(st, chat.New(st), pf, Options{Model: "m", Concurrency: 2,
		Grounding: NoGrounding{}, Emit: func(string, any) {}})
	asgID, _ := orc.Run(context.Background(), testdataDir(t))

	// Count runs created so far.
	before := countRuns(t, st)
	// Re-run the same directory: answered items must be skipped (no new runs
	// for them). Resume reuses the existing assignment by manifest hash.
	asgID2, _ := orc.Run(context.Background(), testdataDir(t))
	if asgID2 != asgID {
		t.Fatalf("resume should reuse the assignment id; got %s vs %s", asgID2, asgID)
	}
	after := countRuns(t, st)
	// Only previously-unanswered items (e.g. the worksheet that landed no_answer
	// under the MC-shaped scripted answer) should re-run.
	if after-before > 2 {
		t.Fatalf("resume re-ran too many items: delta=%d", after-before)
	}
}

func countRuns(t *testing.T, st *store.Store) int {
	items := func() int {
		n := 0
		list, _ := st.ListAssignments()
		for _, a := range list {
			its, _ := st.ListAssignmentItems(a.ID)
			for _, it := range its {
				if it.RunID != "" {
					n++
				}
			}
		}
		return n
	}()
	return items
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/assignment/... -run TestOrchestrator_Resume -v -timeout 90s`
Expected: FAIL — `Run` always creates a new assignment.

- [ ] **Step 3: Add a manifest-hash lookup to the store**

Append to `internal/store/assignments.go`:
```go
// FindAssignmentByManifest returns the most recent assignment for a source dir
// + manifest hash, or ok=false if none exists.
func (s *Store) FindAssignmentByManifest(sourceDir, manifestHash string) (Assignment, bool, error) {
	var a Assignment
	err := s.db.QueryRow(
		`SELECT id, source_dir, title, manifest_hash, model,
                COALESCE(grounding_scope,''), status, total_items, created_at, updated_at
           FROM assignments WHERE source_dir=? AND manifest_hash=?
          ORDER BY created_at DESC LIMIT 1`, sourceDir, manifestHash).Scan(
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
Add `"database/sql"` to the imports of `assignments.go` if not present.

- [ ] **Step 4: Make `Run` resume**

At the top of `Run`, after loading and hashing, look for an existing assignment
and, when found, skip items already `answered`:
```go
	manifestHash := hashManifest(loaded)
	asgID := uuid.NewString()
	resume := map[string]bool{} // source_path already answered
	if existing, ok, _ := o.st.FindAssignmentByManifest(dir, manifestHash); ok {
		asgID = existing.ID
		o.st.UpdateAssignmentStatus(asgID, "in_progress")
		prev, _ := o.st.ListAssignmentItems(asgID)
		for _, it := range prev {
			if it.Status == "answered" {
				resume[it.SourcePath] = true
			}
		}
	} else {
		if err := o.st.CreateAssignment(store.Assignment{
			ID: asgID, SourceDir: dir, Title: titleFor(dir, loaded),
			ManifestHash: manifestHash, Model: o.opts.Model,
			Status: "in_progress", TotalItems: len(loaded.Questions),
		}); err != nil {
			return "", err
		}
	}
```
Then in the fan-out loop, skip answered items and avoid creating duplicate item
rows on resume:
```go
		if resume[q.Path] {
			continue
		}
```
(Use `INSERT OR IGNORE`-style safety by giving each item a deterministic id on
resume, or only create the item row when no prior answered row exists — simplest
is to skip answered `q.Path`s entirely, as above. Remove the old unconditional
`CreateAssignment` call near the top, now handled in the resume branch.)

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/assignment/... -run TestOrchestrator -v -timeout 120s`
Expected: PASS.

- [ ] **Step 6: Commit**

```
git add internal/assignment/orchestrator.go internal/store/assignments.go internal/assignment/orchestrator_test.go
git commit -m "feat(assignment): resume by manifest hash, skip answered items"
```

---

## Phase 7 — appapi integration & Wails bindings

## Task 14: appapi wiring, methods, and bindings

**Files:**
- Modify: `internal/appapi/api.go`
- Modify: `internal/appapi/api_test.go`
- Regenerate: `frontend/wailsjs/go/appapi/*`

- [ ] **Step 1: Write the failing test**

Append to `internal/appapi/api_test.go` (mirror the existing api_test fake-provider patterns; if the test file builds an `*API` via a helper, reuse it). Add:
```go
func TestSolveAssignment_RunsAndLists(t *testing.T) {
	a := newTestAPI(t) // existing helper; constructs API with a temp store + stub provider registry
	// Point the assignment provider factory at a scripted fake by overriding
	// a.assignmentFactory (see Step 2). The MC-shaped answer solves 001.html.
	a.assignmentFactory = func(string) (provider.ChatProvider, string, error) {
		return scriptedSubmit(`{"confidence":"high","answerIndex":1}`), "openai", nil
	}
	dir := copyTestdata(t) // copies internal/assignment/testdata/mod04/_json into a temp dir
	id, err := a.SolveAssignment(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.GetAssignment(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == "" {
		t.Fatal("assignment not retrievable")
	}
	items, _ := a.ListAssignmentItems(id)
	if len(items) == 0 {
		t.Fatal("no items returned")
	}
}
```
(Provide local test helpers `scriptedSubmit` — a `*fakeprovider.Scripted` emitting
a `submit_answer` call then end_turn — and `copyTestdata` — copies the three
fixture files into a temp dir. Keep them in `api_test.go`.)

- [ ] **Step 2: Add orchestrator wiring + methods to the API**

In `internal/appapi/api.go`:

Add fields to the `API` struct:
```go
	asgOrc            *assignment.Orchestrator
	assignmentFactory assignment.ProviderFactory // overridable in tests
	asgCancel         context.CancelFunc
```

In `NewAPI`, after the tool registry is built, construct the factory and
orchestrator:
```go
	a.assignmentFactory = func(modelID string) (provider.ChatProvider, string, error) {
		p, err := provider.New(a.reg, modelID, a.cfg.OpenAIAPIKey, a.cfg.AnthropicAPIKey)
		if err != nil {
			return nil, "", err
		}
		return p, providerNameFromModelID(a.reg, modelID), nil
	}
```
(The orchestrator is constructed per-run in `SolveAssignment` because options —
model, grounding — depend on the request.)

Add the methods:
```go
// SolveAssignment loads a companion _json directory and solves every question
// concurrently in the background. Returns the assignment id immediately.
func (a *API) SolveAssignment(dir string) (string, error) {
	model := a.defaultModelID() // first model in registry; or a stored preference
	var search tools.Tool
	if a.ragAdpt != nil {
		search = searchtextbook.New(ragRetrieverShim{a: a}, chatStoreResolver{st: a.st}, 4000)
	}
	opts := assignment.Options{
		Model:       model,
		Concurrency: assignmentConcurrency(),
		Grounding:   assignment.NoGrounding{}, // v1: textbooks via search_textbook tool, no pre-turn grounding
		SafeMath:    safemath.New(),
		SearchTool:  search,
		Emit: func(name string, payload any) {
			wruntime.EventsEmit(a.ctx, name, payload)
		},
	}
	orc := assignment.New(a.st, a.chatSvc, a.assignmentFactory, opts)

	cctx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	a.asgCancel = cancel
	a.mu.Unlock()

	// Create the assignment row synchronously so we can return its id; the solve
	// runs in the background.
	id, err := orc.Prepare(cctx, dir) // see note below
	if err != nil {
		cancel()
		return "", provider.NormalizeError(err)
	}
	go func() {
		defer cancel()
		_ = orc.RunPrepared(cctx, dir, id)
	}()
	return id, nil
}

func (a *API) CancelAssignment(_ string) {
	a.mu.Lock()
	c := a.asgCancel
	a.mu.Unlock()
	if c != nil {
		c()
	}
}

func (a *API) ListAssignments() ([]store.Assignment, error)      { return a.st.ListAssignments() }
func (a *API) GetAssignment(id string) (store.Assignment, error) { return a.st.GetAssignment(id) }
func (a *API) ListAssignmentItems(id string) ([]store.AssignmentItem, error) {
	return a.st.ListAssignmentItems(id)
}

func assignmentConcurrency() int {
	if v := os.Getenv("STARSHP_ASSIGNMENT_CONCURRENCY"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return 4
}
```

Refactor `Orchestrator.Run` into `Prepare` (load, hash, create/resume the
assignment row, return id) + `RunPrepared` (the fan-out, given the id). For the
synchronous unit test, `SolveAssignment` can instead call `orc.Run` directly and
return its id (background goroutine not required for tests). Choose the split
that keeps `SolveAssignment` returning an id before the batch finishes; the
test waits on completion by polling `GetAssignment(id).Status` if needed, or by
running `orc.Run` synchronously in the test build. Keep it simple: expose
`Run(ctx, dir) (id, error)` (already built) and have `SolveAssignment` call it
inside the goroutine, obtaining the id up-front via a new
`PrepareAssignment(ctx, dir) (id string, err error)` that only creates/resumes
the row.

Add the necessary imports to `api.go`: `"os"`,
`"github.com/cajundata/starshp_app/internal/assignment"`,
`"github.com/cajundata/starshp_app/internal/tools/safemath"`. (`searchtextbook`,
`tools`, `provider`, `store`, `wruntime` are already imported.)

Add a small `defaultModelID()` helper returning `a.reg.Models[0].ID` (guard
empty registry by returning "").

- [ ] **Step 3: Run the test to verify it passes**

Run: `go test ./internal/appapi/... -run TestSolveAssignment -v -timeout 90s`
Expected: PASS.

- [ ] **Step 4: Regenerate Wails bindings**

Run: `wails generate module`
Expected: `frontend/wailsjs/go/appapi/{API.d.ts,API.js,models.ts}` now include
`SolveAssignment`, `CancelAssignment`, `ListAssignments`, `GetAssignment`,
`ListAssignmentItems`, and the `Assignment`/`AssignmentItem` models.

- [ ] **Step 5: Full regression**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```
git add internal/appapi/api.go internal/appapi/api_test.go frontend/wailsjs
git commit -m "feat(appapi): SolveAssignment orchestration + assignment query methods + bindings"
```

---

## Phase 8 — Frontend

## Task 15: Assignments review view

**Files:**
- Modify: `frontend/src/main.ts`
- Modify: `frontend/src/style.css`
- Modify: `frontend/index.html` (add an "Assignments" entry point if the app uses a nav)

This phase is UI; verification is the manual smoke pass (Task 17). Keep the view
minimal and reuse the existing event-rendering helpers.

- [ ] **Step 1: Add an Assignments panel and a "Solve a folder…" action**

In `frontend/src/main.ts`, add a panel (mirroring the existing Library/Textbooks
panel pattern) with a button that calls the Wails-bound `SolveAssignment`. Use
the runtime directory dialog if available, else a text input for the path:
```ts
import { SolveAssignment, ListAssignments, GetAssignment, ListAssignmentItems, CancelAssignment }
  from '../wailsjs/go/appapi/API'

async function startAssignment(dir: string) {
  const id = await SolveAssignment(dir)
  currentAssignmentId = id
  await refreshAssignment(id)
}
```

- [ ] **Step 2: Subscribe to batch progress events**

```ts
EventsOn('assignment:started', (p: any) => renderAssignmentHeader(p))
EventsOn('assignment:item_started', (p: any) => markItem(p.seq, 'solving'))
EventsOn('assignment:item_done', (p: any) =>
  markItem(p.seq, p.status, p.confidence, p.flagCount))
EventsOn('assignment:completed', () => refreshAssignment(currentAssignmentId))
EventsOn('assignment:cancelled', () => refreshAssignment(currentAssignmentId))
```
`renderAssignmentHeader` shows a progress bar (`done/total`); `markItem` updates
a row's status pill, confidence badge, and flag indicator.

- [ ] **Step 3: Render the item list and drill-in**

`refreshAssignment(id)` calls `ListAssignmentItems(id)` and renders a row per
item (seq, title, type, status pill, confidence badge, flag count). Clicking a
row whose `conversationId`/`runId` is set loads its run via the existing
`GetConversationDisplayEvents(conversationId)` and renders it with the same
event-bubble code the chat view already uses — so the worked reasoning, tool
calls, and the `submit_answer` payload appear inline. Low-confidence/flagged
rows get a highlight class.

- [ ] **Step 4: Styles**

In `frontend/src/style.css`, add classes: `.assignment-list`, `.assignment-item`,
`.confidence-high|medium|low`, `.item-flagged`, `.assignment-progress`.

- [ ] **Step 5: Build the frontend**

Run: `cd frontend && npm run build && cd ..`
Expected: builds without TypeScript errors; new hashed bundle emitted.

- [ ] **Step 6: Commit (source + rebuilt bundle)**

```
git add frontend/src frontend/index.html frontend/dist
git commit -m "feat(frontend): Assignments view — solve a folder, progress, item review"
```

---

## Phase 9 — Eval & smoke

## Task 16: API-gated quality fixtures

**Files:**
- Create: `internal/assignment/quality_test.go`

- [ ] **Step 1: Write the API-gated end-to-end test**

Create `internal/assignment/quality_test.go`:
```go
package assignment

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/tools/safemath"
)

// TestQuality_SolvesRealQuestions runs the orchestrator against a real provider
// over the bundled fixtures. Skipped without API keys so keyless CI stays green.
func TestQuality_SolvesRealQuestions(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("quality eval requires OPENAI_API_KEY or ANTHROPIC_API_KEY")
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	model := os.Getenv("STARSHP_EVAL_MODEL")
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	pname := "anthropic"
	if model[:7] != "claude-" {
		pname = "openai"
	}
	pf := func(string) (provider.ChatProvider, string, error) {
		reg := provider.Registry{Models: []provider.ModelInfo{{ID: model, Provider: pname}}}
		p, err := provider.New(reg, model, cfg.OpenAIAPIKey, cfg.AnthropicAPIKey)
		return p, pname, err
	}
	st := openStore(t)
	orc := New(st, chat.New(st), pf, Options{Model: model, Concurrency: 2,
		Grounding: NoGrounding{}, SafeMath: safemath.New(), Emit: func(string, any) {}})

	dir := filepath.Join("testdata", "mod04", "_json")
	asgID, err := orc.Run(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := st.ListAssignmentItems(asgID)
	var answered int
	for _, it := range items {
		if it.Status == "answered" {
			answered++
		}
	}
	if answered == 0 {
		t.Fatal("expected at least one answered item end-to-end")
	}
	t.Logf("answered %d/%d items", answered, len(items))
}
```

- [ ] **Step 2: Run it (skips without keys)**

Run: `OPENAI_API_KEY= ANTHROPIC_API_KEY= go test ./internal/assignment/... -run TestQuality -v`
Expected: SKIP.

- [ ] **Step 3: Commit**

```
git add internal/assignment/quality_test.go
git commit -m "test(assignment): API-gated end-to-end quality fixture"
```

---

## Task 17: Smoke checklist + final regression

**Files:**
- Modify: `docs/SMOKE.md`

- [ ] **Step 1: Append a smoke section to `docs/SMOKE.md`**

Add (continue the file's numbered convention):
```markdown
## Assignment solver

34. [ ] **Solve a folder.** In the Assignments view, choose a companion `_json`
    directory and start. A progress bar advances `done/total`; items flip from
    solving → answered/no_answer/errored as the batch runs.
35. [ ] **Review an item.** Click an answered item → its run opens with the
    worked reasoning, tool calls (safe_math / search_textbook), and the
    submit_answer payload (MC choice or worksheet cell table).
36. [ ] **Confidence & flags.** Low-confidence and flagged items are
    highlighted; a worksheet with uncaptured dropdown options shows an
    `uncaptured_dropdown_options` flag; a question missing data shows
    `missing_information`.
37. [ ] **Answer files written.** A sibling `_answers/NNN.json` exists for each
    answered question, mirroring the input file names, with the answer payload
    and runId.
38. [ ] **Stop mid-batch.** Start a large folder, click Stop. In-flight items
    finish or cancel; pending items become `cancelled`; answered items persist.
39. [ ] **Resume.** Re-run the same folder. Already-answered items are skipped
    (no new runs); only pending/errored/no_answer items re-solve.
40. [ ] **Sidebar isolation.** Item conversations do not appear in the normal
    conversation sidebar; they are reachable only via the assignment view.
41. [ ] **Concurrency env.** Set `STARSHP_ASSIGNMENT_CONCURRENCY=2`, re-run, and
    confirm no SQLITE_BUSY errors in logs (busy_timeout + WAL cover contention).
```

- [ ] **Step 2: Final regression pass**

Run:
```
go test ./...
cd frontend && npm run build && cd ..
```
Expected: all green; frontend builds.

- [ ] **Step 3: Commit**

```
git add docs/SMOKE.md
git commit -m "docs(smoke): assignment solver manual checklist"
```

- [ ] **Step 4: Manual smoke run**

Walk the new checklist against the real app with a companion `_json` directory
and API keys. File any regression as a follow-up commit before merging.

---

## Self-Review

### Spec coverage

| Spec section / requirement | Task(s) |
| --- | --- |
| Companion JSON input contract (MC + worksheet + manifest) | 1 |
| Answerable-cell rule (input/dropdown AND blank; exclude formula/readonly/prefilled) | 2 |
| Worksheet + MC prompt rendering with stable cell-id tags | 3 |
| `submit_answer` tool; per-question schema (MC bounds, worksheet id enum); flag vocabulary | 4, 5 |
| Answer recovered from the event log | 8, 11 |
| `assignments` + `assignment_items` tables; `conversations.assignment_id`; busy_timeout | 6 |
| Assignment/item CRUD; sidebar isolation | 7 |
| Pluggable `GroundingSource`; textbooks-only v1 | 9, 14 |
| Structured answer JSON to `_answers/NNN.json` | 10, 11 |
| Parallel fan-out; bounded concurrency; cancellation; error isolation | 12 |
| Batch events (`assignment:*`); not the per-token stream | 11, 12, 14 |
| Re-run/resume by manifest hash | 13 |
| appapi surface + Wails bindings | 14 |
| In-app review surface reusing display events | 15 |
| API-gated quality fixtures | 16 |
| Smoke docs + regression | 17 |

No spec requirement is left without a task. (Independent verifier agent,
lesson-page grounding, round-trip auto-fill, and grade mode are explicit
spec Non-goals / Future work and are intentionally absent.)

### Placeholder scan

No "TBD"/"TODO"/"implement later" steps. Every code step contains real code and
every command step a concrete command + expected result. Task 14 contains an
explicit `Prepare`/`RunPrepared` refactor note (a real decision the implementer
makes), not a placeholder — the fallback (call `Run` synchronously) is spelled
out.

### Type consistency

- `Question`, `Type` consts, `CellRef`, `Answer`, `Flag`, `CellValue` are
  defined in Tasks 1/2/4 and reused unchanged in 3/5/10/11.
- `tools.Tool` interface (Name/Description/InputSchema/Execute/Timeout) matches
  the verified registry signature; `SubmitAnswer` implements exactly it (Task 5).
- `chat.SendParams` fields (`ConversationID, UserText, SystemPrompt, Model,
  Provider, ProviderName, Registry, Resolver, Retriever, RetrievalMode, Sink`)
  match the verified `chat.go` definition (Task 11).
- `store.Assignment` / `store.AssignmentItem` field names are identical across
  Tasks 6/7/11/13/14.
- `ProviderFactory`, `Options`, `Orchestrator`, `New` signatures match between
  Tasks 11–14.
- `GroundingSource` (`Ensure`, `Retriever`) matches between Tasks 9, 11, 14.

### Known refactor flagged for the implementer

Task 14 splits `Orchestrator.Run` into a synchronous prepare (create/resume the
assignment row, return id) plus a background fan-out, so `SolveAssignment` can
return the id before the batch finishes. The single-method `Run(ctx, dir)`
built in Tasks 11–13 stays valid for unit tests; only appapi needs the split.
