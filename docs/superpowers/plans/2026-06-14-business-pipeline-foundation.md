# Business Pipeline Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the persistent idea-pipeline foundation — SQLite data model, domain logic, bound API, a Pipeline UI, and an on-launch Reviews Due sweep — so business ideas become tracked entities with kill criteria that resurface on schedule.

**Architecture:** A new `internal/pipeline` package holds pure domain logic (status-transition legality, reviews-due shaping). `internal/store` gains the schema and mechanical CRUD for the new tables, mirroring the existing `assignments` precedent. `internal/appapi` exposes Wails-bound methods that wire validation to storage and return the normalized `{code, userMessage, retryable}` error envelope. The frontend gains a full-screen Pipeline view (modeled on the existing Assignments view) and a Reviews Due panel populated at launch.

**Tech Stack:** Go 1.25, `modernc.org/sqlite`, Wails v2, vanilla TypeScript frontend (no framework), `github.com/google/uuid`.

**Module path:** `github.com/cajundata/starshp_app`

**Design doc:** `docs/superpowers/specs/2026-06-13-business-pipeline-foundation-design.md`

**Scope note:** The full schema (including `idea_reviews`, `idea_review_roles`, `send_backs`) ships in this plan, but only `ideas`, `idea_status_history`, and `kill_criteria` are exercised. The review tables are foundation for Spec 2 and are created empty. The `financial_flag` field is stored but not enforced (enforcement is Spec 2).

---

## File Structure

**Created:**
- `internal/store/ideas.go` — types + mechanical CRUD for ideas, status history, kill criteria, due-reviews query.
- `internal/store/ideas_test.go` — store round-trip tests.
- `internal/pipeline/pipeline.go` — pure domain logic: `ValidateTransition`, `ShapeDueReviews`.
- `internal/pipeline/pipeline_test.go` — domain logic tests.
- `internal/appapi/pipeline.go` — Wails-bound API methods.
- `internal/appapi/pipeline_test.go` — API boundary tests.
- `frontend/src/pipeline.ts` — Pipeline view + Reviews Due panel (imperative DOM, mirrors the Assignments view).

**Modified:**
- `internal/store/schema.go` — append the six tables + indexes to `schemaSQL`.
- `frontend/index.html` — sidebar button, `#pipelineView` markup, badge element.
- `frontend/src/main.ts` — import and init the pipeline module; run the launch sweep.
- `frontend/src/style.css` — Pipeline view + panel styling.
- `docs/SMOKE.md` — add pipeline smoke steps.

---

## Task 1: Schema — pipeline tables

**Files:**
- Modify: `internal/store/schema.go`
- Test: `internal/store/ideas_test.go` (new; schema-presence test only in this task)

- [ ] **Step 1: Write the failing test**

Create `internal/store/ideas_test.go`:

```go
package store

import "testing"

func TestPipelineSchemaTablesExist(t *testing.T) {
	st := openTestStore(t)
	tables := []string{
		"ideas", "idea_status_history", "idea_reviews",
		"idea_review_roles", "kill_criteria", "send_backs",
	}
	for _, name := range tables {
		var got string
		err := st.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
		if err != nil {
			t.Fatalf("table %q missing: %v", name, err)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestPipelineSchemaTablesExist -v`
Expected: FAIL — `table "ideas" missing: sql: no rows in result set`.

- [ ] **Step 3: Append the tables to the schema**

In `internal/store/schema.go`, append the following inside the `schemaSQL` backtick string, immediately before the closing `` ` ``:

```sql
CREATE TABLE IF NOT EXISTS ideas (
  id              TEXT PRIMARY KEY,
  title           TEXT NOT NULL,
  summary         TEXT NOT NULL DEFAULT '',
  pathway         TEXT,
  status          TEXT NOT NULL CHECK (status IN (
                      'raw','triaged','in_review','validating',
                      'go','parked','killed')),
  kill_reason     TEXT,
  financial_flag  INTEGER NOT NULL DEFAULT 0,
  source          TEXT NOT NULL DEFAULT 'manual' CHECK (source IN (
                      'manual','scout','import')),
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS idea_status_history (
  id          TEXT PRIMARY KEY,
  idea_id     TEXT NOT NULL REFERENCES ideas(id) ON DELETE CASCADE,
  from_status TEXT,
  to_status   TEXT NOT NULL,
  reason      TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS idea_reviews (
  id              TEXT PRIMARY KEY,
  idea_id         TEXT NOT NULL REFERENCES ideas(id) ON DELETE CASCADE,
  conversation_id TEXT,
  pathway         TEXT NOT NULL,
  model           TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL CHECK (status IN (
                      'in_progress','completed','parked','cancelled','errored')),
  bluf_verdict    TEXT,
  bluf_json       TEXT,
  document_md     TEXT,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS idea_review_roles (
  id            TEXT PRIMARY KEY,
  review_id     TEXT NOT NULL REFERENCES idea_reviews(id) ON DELETE CASCADE,
  seq           INTEGER NOT NULL,
  role_key      TEXT NOT NULL,
  role_name     TEXT NOT NULL,
  status        TEXT NOT NULL CHECK (status IN (
                    'pending','running','done','errored','cancelled')),
  verdict       TEXT,
  findings_json TEXT,
  run_id        TEXT,
  error         TEXT,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS kill_criteria (
  id          TEXT PRIMARY KEY,
  idea_id     TEXT NOT NULL REFERENCES ideas(id) ON DELETE CASCADE,
  review_id   TEXT REFERENCES idea_reviews(id) ON DELETE SET NULL,
  metric      TEXT NOT NULL,
  threshold   TEXT NOT NULL,
  review_date INTEGER NOT NULL,
  on_miss     TEXT NOT NULL CHECK (on_miss IN ('kill','park','halt')),
  status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN (
                  'pending','met','missed','resolved')),
  notes       TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS send_backs (
  id          TEXT PRIMARY KEY,
  review_id   TEXT NOT NULL REFERENCES idea_reviews(id) ON DELETE CASCADE,
  from_role   TEXT NOT NULL,
  question    TEXT NOT NULL,
  answer      TEXT,
  effect      TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','answered')),
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idea_status_history_idea
  ON idea_status_history(idea_id, created_at);
CREATE INDEX IF NOT EXISTS idea_reviews_idea ON idea_reviews(idea_id);
CREATE INDEX IF NOT EXISTS idea_review_roles_review
  ON idea_review_roles(review_id, seq);
CREATE INDEX IF NOT EXISTS kill_criteria_idea ON kill_criteria(idea_id);
CREATE INDEX IF NOT EXISTS kill_criteria_due
  ON kill_criteria(review_date) WHERE status = 'pending';
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestPipelineSchemaTablesExist -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/schema.go internal/store/ideas_test.go
git commit -m "feat(store): add idea-pipeline schema tables"
```

---

## Task 2: Store — Idea CRUD

**Files:**
- Create: `internal/store/ideas.go`
- Test: `internal/store/ideas_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/ideas_test.go`:

```go
func TestIdeaCRUD(t *testing.T) {
	st := openTestStore(t)
	i := Idea{
		ID: "id1", Title: "HDPE cooler mounts", Summary: "marine mounts",
		Pathway: "small_project", Status: "raw", FinancialFlag: true,
		Source: "import",
	}
	if err := st.CreateIdea(i); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetIdea("id1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "HDPE cooler mounts" || got.Pathway != "small_project" {
		t.Fatalf("get mismatch: %+v", got)
	}
	if !got.FinancialFlag {
		t.Fatalf("financial flag not persisted: %+v", got)
	}
	got.Summary = "updated"
	got.FinancialFlag = false
	if err := st.UpdateIdea(got); err != nil {
		t.Fatal(err)
	}
	reread, _ := st.GetIdea("id1")
	if reread.Summary != "updated" || reread.FinancialFlag {
		t.Fatalf("update not persisted: %+v", reread)
	}
	list, err := st.ListIdeas()
	if err != nil || len(list) != 1 {
		t.Fatalf("list want 1, got %d err=%v", len(list), err)
	}
	if err := st.DeleteIdea("id1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetIdea("id1"); err == nil {
		t.Fatal("expected error getting deleted idea")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestIdeaCRUD -v`
Expected: FAIL — `undefined: Idea` / `st.CreateIdea undefined`.

- [ ] **Step 3: Create `internal/store/ideas.go` with types and idea CRUD**

```go
package store

import "time"

type Idea struct {
	ID            string
	Title         string
	Summary       string
	Pathway       string
	Status        string
	KillReason    string
	FinancialFlag bool
	Source        string
	CreatedAt     int64
	UpdatedAt     int64
}

type StatusChange struct {
	ID         string
	IdeaID     string
	FromStatus string
	ToStatus   string
	Reason     string
	CreatedAt  int64
}

type KillCriterion struct {
	ID         string
	IdeaID     string
	ReviewID   string
	Metric     string
	Threshold  string
	ReviewDate int64
	OnMiss     string
	Status     string
	Notes      string
	CreatedAt  int64
	UpdatedAt  int64
}

// DueReview is one overdue/due kill criterion joined to its parent idea, as
// returned by the launch sweep.
type DueReview struct {
	CriterionID string
	IdeaID      string
	IdeaTitle   string
	IdeaStatus  string
	Metric      string
	Threshold   string
	ReviewDate  int64
	OnMiss      string
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) CreateIdea(i Idea) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(
		`INSERT INTO ideas
		    (id, title, summary, pathway, status, kill_reason,
		     financial_flag, source, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		i.ID, i.Title, i.Summary, nullIfEmpty(i.Pathway), i.Status,
		nullIfEmpty(i.KillReason), boolToInt(i.FinancialFlag),
		defaultStr(i.Source, "manual"), now, now)
	return err
}

// UpdateIdea overwrites the editable fields (title, summary, pathway,
// financial_flag). Status is changed only through SetIdeaStatus.
func (s *Store) UpdateIdea(i Idea) error {
	_, err := s.db.Exec(
		`UPDATE ideas SET title=?, summary=?, pathway=?, financial_flag=?, updated_at=?
		  WHERE id=?`,
		i.Title, i.Summary, nullIfEmpty(i.Pathway), boolToInt(i.FinancialFlag),
		time.Now().UnixMilli(), i.ID)
	return err
}

func (s *Store) GetIdea(id string) (Idea, error) {
	return scanIdea(s.db.QueryRow(
		`SELECT id, title, summary, COALESCE(pathway,''), status,
		        COALESCE(kill_reason,''), financial_flag, source, created_at, updated_at
		   FROM ideas WHERE id=?`, id))
}

func (s *Store) ListIdeas() ([]Idea, error) {
	rows, err := s.db.Query(
		`SELECT id, title, summary, COALESCE(pathway,''), status,
		        COALESCE(kill_reason,''), financial_flag, source, created_at, updated_at
		   FROM ideas ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Idea
	for rows.Next() {
		i, err := scanIdea(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) DeleteIdea(id string) error {
	_, err := s.db.Exec(`DELETE FROM ideas WHERE id=?`, id)
	return err
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanIdea(r rowScanner) (Idea, error) {
	var i Idea
	var fin int
	err := r.Scan(&i.ID, &i.Title, &i.Summary, &i.Pathway, &i.Status,
		&i.KillReason, &fin, &i.Source, &i.CreatedAt, &i.UpdatedAt)
	i.FinancialFlag = fin != 0
	return i, err
}

// defaultStr returns def when s is empty.
func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
```

> This file imports only `"time"` in this task. `database/sql` is not needed (all DB access is through the embedded `s.db`, typed in `store.go`); `uuid` is added in Task 3 when `SetIdeaStatus` first uses it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestIdeaCRUD -v`
Expected: PASS. Then run `go vet ./internal/store/` — expect clean.

- [ ] **Step 5: Commit**

```bash
git add internal/store/ideas.go internal/store/ideas_test.go
git commit -m "feat(store): idea CRUD"
```

---

## Task 3: Store — SetIdeaStatus (transactional) + ListStatusHistory

**Files:**
- Modify: `internal/store/ideas.go`
- Test: `internal/store/ideas_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/ideas_test.go`:

```go
func TestSetIdeaStatusWritesHistoryAtomically(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateIdea(Idea{ID: "id1", Title: "t", Status: "raw", Source: "manual"})

	if err := st.SetIdeaStatus("id1", "triaged", "looks worth a look"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetIdea("id1")
	if got.Status != "triaged" {
		t.Fatalf("status want triaged, got %q", got.Status)
	}

	if err := st.SetIdeaStatus("id1", "killed", "no channel"); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetIdea("id1")
	if got.Status != "killed" || got.KillReason != "no channel" {
		t.Fatalf("kill not recorded: %+v", got)
	}

	hist, err := st.ListStatusHistory("id1")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("want 2 history rows, got %d", len(hist))
	}
	// Ordered oldest-first: raw->triaged, then triaged->killed.
	if hist[0].FromStatus != "raw" || hist[0].ToStatus != "triaged" {
		t.Fatalf("row0 wrong: %+v", hist[0])
	}
	if hist[1].ToStatus != "killed" || hist[1].Reason != "no channel" {
		t.Fatalf("row1 wrong: %+v", hist[1])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestSetIdeaStatusWritesHistoryAtomically -v`
Expected: FAIL — `st.SetIdeaStatus undefined`.

- [ ] **Step 3: Add `SetIdeaStatus` and `ListStatusHistory` to `ideas.go`**

First change the import line `import "time"` to an import block that adds uuid:

```go
import (
	"time"

	"github.com/google/uuid"
)
```

Then append the methods:

```go
// SetIdeaStatus updates the idea's status and appends a status-history row in a
// single transaction. For terminal statuses (killed, parked) the reason is also
// stored on the idea's kill_reason column. It is mechanical: legality of the
// transition is validated by the pipeline package before this is called.
func (s *Store) SetIdeaStatus(id, toStatus, reason string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var from string
	if err := tx.QueryRow(`SELECT status FROM ideas WHERE id=?`, id).Scan(&from); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if toStatus == "killed" || toStatus == "parked" {
		if _, err := tx.Exec(
			`UPDATE ideas SET status=?, kill_reason=?, updated_at=? WHERE id=?`,
			toStatus, reason, now, id); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(
			`UPDATE ideas SET status=?, updated_at=? WHERE id=?`,
			toStatus, now, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO idea_status_history (id, idea_id, from_status, to_status, reason, created_at)
		 VALUES (?,?,?,?,?,?)`,
		uuid.NewString(), id, from, toStatus, reason, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListStatusHistory(ideaID string) ([]StatusChange, error) {
	rows, err := s.db.Query(
		`SELECT id, idea_id, COALESCE(from_status,''), to_status, reason, created_at
		   FROM idea_status_history WHERE idea_id=? ORDER BY created_at ASC`, ideaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatusChange
	for rows.Next() {
		var c StatusChange
		if err := rows.Scan(&c.ID, &c.IdeaID, &c.FromStatus, &c.ToStatus,
			&c.Reason, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestSetIdeaStatusWritesHistoryAtomically -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/ideas.go internal/store/ideas_test.go
git commit -m "feat(store): transactional SetIdeaStatus with history"
```

---

## Task 4: Store — kill criteria CRUD + due-reviews query

**Files:**
- Modify: `internal/store/ideas.go`
- Test: `internal/store/ideas_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/ideas_test.go`:

```go
func TestKillCriteriaAndDueReviews(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateIdea(Idea{ID: "id1", Title: "Home automation", Status: "validating", Source: "import"})

	overdue := KillCriterion{
		ID: "k1", IdeaID: "id1", Metric: "Paid installs", Threshold: ">=2 in 30d",
		ReviewDate: 1000, OnMiss: "kill", Status: "pending",
	}
	future := KillCriterion{
		ID: "k2", IdeaID: "id1", Metric: "Churn", Threshold: "<10%/mo",
		ReviewDate: 9_000_000_000_000, OnMiss: "park", Status: "pending",
	}
	resolved := KillCriterion{
		ID: "k3", IdeaID: "id1", Metric: "Capital", Threshold: "<=1500",
		ReviewDate: 500, OnMiss: "halt", Status: "resolved",
	}
	for _, k := range []KillCriterion{overdue, future, resolved} {
		if err := st.AddKillCriterion(k); err != nil {
			t.Fatal(err)
		}
	}

	all, err := st.ListKillCriteria("id1")
	if err != nil || len(all) != 3 {
		t.Fatalf("list want 3, got %d err=%v", len(all), err)
	}

	// asOf = 2000: k1 (date 1000, pending) is due; k2 future excluded;
	// k3 resolved excluded even though its date is past.
	due, err := st.ListDueKillCriteria(2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].CriterionID != "k1" {
		t.Fatalf("due want [k1], got %+v", due)
	}
	if due[0].IdeaTitle != "Home automation" || due[0].OnMiss != "kill" {
		t.Fatalf("due row not joined to idea: %+v", due[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestKillCriteriaAndDueReviews -v`
Expected: FAIL — `st.AddKillCriterion undefined`.

- [ ] **Step 3: Add kill-criteria methods to `ideas.go`**

```go
func (s *Store) AddKillCriterion(k KillCriterion) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(
		`INSERT INTO kill_criteria
		    (id, idea_id, review_id, metric, threshold, review_date,
		     on_miss, status, notes, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		k.ID, k.IdeaID, nullIfEmpty(k.ReviewID), k.Metric, k.Threshold, k.ReviewDate,
		k.OnMiss, defaultStr(k.Status, "pending"), k.Notes, now, now)
	return err
}

func (s *Store) UpdateKillCriterion(k KillCriterion) error {
	_, err := s.db.Exec(
		`UPDATE kill_criteria
		    SET metric=?, threshold=?, review_date=?, on_miss=?, status=?, notes=?, updated_at=?
		  WHERE id=?`,
		k.Metric, k.Threshold, k.ReviewDate, k.OnMiss, k.Status, k.Notes,
		time.Now().UnixMilli(), k.ID)
	return err
}

func (s *Store) DeleteKillCriterion(id string) error {
	_, err := s.db.Exec(`DELETE FROM kill_criteria WHERE id=?`, id)
	return err
}

func (s *Store) ListKillCriteria(ideaID string) ([]KillCriterion, error) {
	rows, err := s.db.Query(
		`SELECT id, idea_id, COALESCE(review_id,''), metric, threshold, review_date,
		        on_miss, status, notes, created_at, updated_at
		   FROM kill_criteria WHERE idea_id=? ORDER BY review_date ASC`, ideaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KillCriterion
	for rows.Next() {
		var k KillCriterion
		if err := rows.Scan(&k.ID, &k.IdeaID, &k.ReviewID, &k.Metric, &k.Threshold,
			&k.ReviewDate, &k.OnMiss, &k.Status, &k.Notes, &k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ListDueKillCriteria returns pending kill criteria whose review_date is at or
// before asOf, joined to the parent idea, oldest review date first.
func (s *Store) ListDueKillCriteria(asOf int64) ([]DueReview, error) {
	rows, err := s.db.Query(
		`SELECT k.id, k.idea_id, i.title, i.status, k.metric, k.threshold,
		        k.review_date, k.on_miss
		   FROM kill_criteria k JOIN ideas i ON i.id = k.idea_id
		  WHERE k.status='pending' AND k.review_date <= ?
		  ORDER BY k.review_date ASC`, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueReview
	for rows.Next() {
		var d DueReview
		if err := rows.Scan(&d.CriterionID, &d.IdeaID, &d.IdeaTitle, &d.IdeaStatus,
			&d.Metric, &d.Threshold, &d.ReviewDate, &d.OnMiss); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestKillCriteriaAndDueReviews -v`
Expected: PASS. Run the full store package: `go test ./internal/store/...` — all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/ideas.go internal/store/ideas_test.go
git commit -m "feat(store): kill criteria CRUD and due-reviews query"
```

---

## Task 5: Pipeline — status-transition validation

**Files:**
- Create: `internal/pipeline/pipeline.go`
- Test: `internal/pipeline/pipeline_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/pipeline/pipeline_test.go`:

```go
package pipeline

import (
	"errors"
	"testing"
)

func TestValidateTransition(t *testing.T) {
	cases := []struct {
		name           string
		from, to, reas string
		wantErr        bool
		wantCode       string
	}{
		{"raw to triaged", "raw", "triaged", "", false, ""},
		{"in_review to go", "in_review", "go", "", false, ""},
		{"kill needs reason ok", "in_review", "killed", "no demand", false, ""},
		{"kill missing reason", "in_review", "killed", "", true, "reason_required"},
		{"park missing reason", "validating", "parked", "", true, "reason_required"},
		{"illegal raw to go", "raw", "go", "", true, "invalid_transition"},
		{"killed is terminal", "killed", "triaged", "", true, "invalid_transition"},
		{"no-op rejected", "raw", "raw", "", true, "invalid_transition"},
		{"unknown target", "raw", "bogus", "", true, "invalid_transition"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateTransition(c.from, c.to, c.reas)
			if c.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v got err=%v", c.wantErr, err)
			}
			if c.wantErr {
				var te *TransitionError
				if !errors.As(err, &te) {
					t.Fatalf("want *TransitionError, got %T", err)
				}
				if te.Code != c.wantCode {
					t.Fatalf("code want %q got %q", c.wantCode, te.Code)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pipeline/ -run TestValidateTransition -v`
Expected: FAIL — package does not compile (`ValidateTransition` undefined).

- [ ] **Step 3: Create `internal/pipeline/pipeline.go`**

```go
// Package pipeline holds pure domain logic for the idea pipeline: which status
// transitions are legal, and how to shape the launch-time reviews-due sweep.
// It has no storage or I/O dependencies so it is trivially testable.
package pipeline

import "fmt"

// allowedTransitions maps a current status to the statuses it may move to.
// killed is terminal in this milestone (no outbound transitions).
var allowedTransitions = map[string][]string{
	"raw":        {"triaged", "killed", "parked"},
	"triaged":    {"in_review", "killed", "parked"},
	"in_review":  {"validating", "go", "parked", "killed"},
	"validating": {"go", "parked", "killed"},
	"go":         {"validating", "parked", "killed"},
	"parked":     {"raw", "triaged", "in_review", "killed"},
	"killed":     {},
}

// TransitionError is a validation failure with a stable code the API boundary
// maps to a user-facing message.
type TransitionError struct {
	Code    string // "invalid_transition" | "reason_required"
	Message string
}

func (e *TransitionError) Error() string { return e.Message }

// ValidateTransition reports whether moving from -> to is legal and, for
// terminal statuses (killed, parked), that a non-empty reason was supplied.
func ValidateTransition(from, to, reason string) error {
	allowed, ok := allowedTransitions[from]
	if !ok {
		return &TransitionError{Code: "invalid_transition",
			Message: fmt.Sprintf("Unknown current status %q.", from)}
	}
	legal := false
	for _, s := range allowed {
		if s == to {
			legal = true
			break
		}
	}
	if !legal {
		return &TransitionError{Code: "invalid_transition",
			Message: fmt.Sprintf("Cannot move an idea from %q to %q.", from, to)}
	}
	if (to == "killed" || to == "parked") && reason == "" {
		return &TransitionError{Code: "reason_required",
			Message: fmt.Sprintf("Moving to %q requires a reason.", to)}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/pipeline/ -run TestValidateTransition -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pipeline/pipeline.go internal/pipeline/pipeline_test.go
git commit -m "feat(pipeline): status-transition validation"
```

---

## Task 6: Pipeline — reviews-due shaping

**Files:**
- Modify: `internal/pipeline/pipeline.go`
- Test: `internal/pipeline/pipeline_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/pipeline/pipeline_test.go`:

```go
import_store "github.com/cajundata/starshp_app/internal/store"
```

> Add that import to the existing import block at the top of the file (do not create a second import block). Then append:

```go
func TestShapeDueReviews(t *testing.T) {
	const day = int64(86_400_000)
	rows := []import_store.DueReview{
		{CriterionID: "k1", IdeaTitle: "A", Metric: "m", ReviewDate: 0},
		{CriterionID: "k2", IdeaTitle: "B", Metric: "n", ReviewDate: 5 * day},
	}
	out := ShapeDueReviews(rows, 5*day) // asOf = 5 days
	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
	if out[0].DaysOverdue != 5 {
		t.Fatalf("k1 overdue want 5, got %d", out[0].DaysOverdue)
	}
	if out[1].DaysOverdue != 0 {
		t.Fatalf("k2 due-today overdue want 0, got %d", out[1].DaysOverdue)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pipeline/ -run TestShapeDueReviews -v`
Expected: FAIL — `ShapeDueReviews undefined` / `DueReviewView` undefined.

- [ ] **Step 3: Add shaping to `pipeline.go`**

Add `"github.com/cajundata/starshp_app/internal/store"` to the import block, then append:

```go
// DueReviewView is a due kill criterion enriched with how overdue it is, for
// display in the Reviews Due panel. No JSON tags: like the store structs, the
// Wails binding generates PascalCase TS fields (IdeaTitle, DaysOverdue, …),
// which is the convention the frontend already follows.
type DueReviewView struct {
	CriterionID string
	IdeaID      string
	IdeaTitle   string
	IdeaStatus  string
	Metric      string
	Threshold   string
	ReviewDate  int64
	OnMiss      string
	DaysOverdue int
}

// ShapeDueReviews computes days-overdue (0 when due today) for each row,
// relative to asOf. Both are UnixMilli.
func ShapeDueReviews(rows []store.DueReview, asOf int64) []DueReviewView {
	const day = int64(86_400_000)
	out := make([]DueReviewView, 0, len(rows))
	for _, r := range rows {
		overdue := 0
		if asOf > r.ReviewDate {
			overdue = int((asOf - r.ReviewDate) / day)
		}
		out = append(out, DueReviewView{
			CriterionID: r.CriterionID, IdeaID: r.IdeaID, IdeaTitle: r.IdeaTitle,
			IdeaStatus: r.IdeaStatus, Metric: r.Metric, Threshold: r.Threshold,
			ReviewDate: r.ReviewDate, OnMiss: r.OnMiss, DaysOverdue: overdue,
		})
	}
	return out
}
```

> The test's `import_store` alias and this file's `store` import refer to the same package; the alias in the test only avoids shadowing the test's own identifiers. Both compile.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/pipeline/ -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/pipeline/pipeline.go internal/pipeline/pipeline_test.go
git commit -m "feat(pipeline): reviews-due shaping"
```

---

## Task 7: API — bound pipeline methods

**Files:**
- Create: `internal/appapi/pipeline.go`
- Test: `internal/appapi/pipeline_test.go`

> Check the existing `internal/appapi` test files first to reuse their `*API` test constructor. If a helper like `newTestAPI(t)` exists, use it. If not, construct via `appapi.NewAPI(config.Config{}, st, provider.Registry{}, nil)` with an in-memory/temp store opened by `store.Open(filepath.Join(t.TempDir(), "api.db"))`. Confirm the exact constructor signature from `internal/appapi/api.go:NewAPI` (already: `NewAPI(cfg config.Config, st *store.Store, reg provider.Registry, ragAdpt *rag.Adapter) *API`).

- [ ] **Step 1: Write the failing test**

Create `internal/appapi/pipeline_test.go`:

```go
package appapi

import (
	"path/filepath"
	"testing"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

func newPipelineTestAPI(t *testing.T) *API {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewAPI(config.Config{}, st, provider.Registry{}, nil)
}

func TestCreateAndListIdeas(t *testing.T) {
	a := newPipelineTestAPI(t)
	idea, err := a.CreateIdea("Home automation", "aging-in-place", "side_business", true)
	if err != nil {
		t.Fatal(err)
	}
	if idea.ID == "" || idea.Status != "raw" {
		t.Fatalf("new idea wrong: %+v", idea)
	}
	list, err := a.ListIdeas()
	if err != nil || len(list) != 1 {
		t.Fatalf("list want 1, got %d err=%v", len(list), err)
	}
}

func TestSetIdeaStatusRejectsIllegalTransition(t *testing.T) {
	a := newPipelineTestAPI(t)
	idea, _ := a.CreateIdea("X", "", "small_project", false)
	err := a.SetIdeaStatus(idea.ID, "go", "") // raw -> go is illegal
	if err == nil {
		t.Fatal("expected error")
	}
	ae, ok := err.(provider.AppError)
	if !ok || ae.Code != "invalid_transition" {
		t.Fatalf("want invalid_transition AppError, got %#v", err)
	}
}

func TestSetIdeaStatusRequiresKillReason(t *testing.T) {
	a := newPipelineTestAPI(t)
	idea, _ := a.CreateIdea("X", "", "small_project", false)
	_ = a.SetIdeaStatus(idea.ID, "triaged", "")
	err := a.SetIdeaStatus(idea.ID, "killed", "")
	ae, ok := err.(provider.AppError)
	if !ok || ae.Code != "reason_required" {
		t.Fatalf("want reason_required AppError, got %#v", err)
	}
}

func TestReviewsDueSweep(t *testing.T) {
	a := newPipelineTestAPI(t)
	idea, _ := a.CreateIdea("Home automation", "", "side_business", false)
	if _, err := a.AddKillCriterion(idea.ID, "Paid installs", ">=2", 1000, "kill"); err != nil {
		t.Fatal(err)
	}
	due, err := a.ListReviewsDue() // asOf = now, far in the future of 1000
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].Metric != "Paid installs" {
		t.Fatalf("due want 1 [Paid installs], got %+v", due)
	}
	if due[0].DaysOverdue <= 0 {
		t.Fatalf("expected positive days overdue, got %d", due[0].DaysOverdue)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/appapi/ -run 'TestCreateAndListIdeas|TestSetIdeaStatus|TestReviewsDue' -v`
Expected: FAIL — `a.CreateIdea undefined`.

- [ ] **Step 3: Create `internal/appapi/pipeline.go`**

```go
package appapi

import (
	"errors"
	"time"

	"github.com/cajundata/starshp_app/internal/pipeline"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/google/uuid"
)

// CreateIdea creates a new idea in the 'raw' status and returns it.
func (a *API) CreateIdea(title, summary, pathway string, financialFlag bool) (store.Idea, error) {
	if title == "" {
		return store.Idea{}, provider.AppError{
			Code: "invalid_input", UserMessage: "An idea needs a title.", Retryable: false}
	}
	idea := store.Idea{
		ID: uuid.NewString(), Title: title, Summary: summary, Pathway: pathway,
		Status: "raw", FinancialFlag: financialFlag, Source: "manual",
	}
	if err := a.st.CreateIdea(idea); err != nil {
		return store.Idea{}, err
	}
	return a.st.GetIdea(idea.ID)
}

func (a *API) UpdateIdea(i store.Idea) error    { return a.st.UpdateIdea(i) }
func (a *API) ListIdeas() ([]store.Idea, error) { return a.st.ListIdeas() }
func (a *API) GetIdea(id string) (store.Idea, error) {
	return a.st.GetIdea(id)
}
func (a *API) DeleteIdea(id string) error { return a.st.DeleteIdea(id) }

// SetIdeaStatus validates the transition (legality + reason-required for
// terminal statuses) before persisting it.
func (a *API) SetIdeaStatus(id, toStatus, reason string) error {
	cur, err := a.st.GetIdea(id)
	if err != nil {
		return provider.AppError{Code: "not_found",
			UserMessage: "That idea no longer exists.", Retryable: false}
	}
	if verr := pipeline.ValidateTransition(cur.Status, toStatus, reason); verr != nil {
		var te *pipeline.TransitionError
		if errors.As(verr, &te) {
			return provider.AppError{Code: te.Code, UserMessage: te.Message, Retryable: false}
		}
		return verr
	}
	return a.st.SetIdeaStatus(id, toStatus, reason)
}

func (a *API) ListStatusHistory(ideaID string) ([]store.StatusChange, error) {
	return a.st.ListStatusHistory(ideaID)
}

// AddKillCriterion stores a new kill criterion (status 'pending') and returns it.
func (a *API) AddKillCriterion(ideaID, metric, threshold string, reviewDate int64, onMiss string) (store.KillCriterion, error) {
	if metric == "" || threshold == "" {
		return store.KillCriterion{}, provider.AppError{Code: "invalid_input",
			UserMessage: "A kill criterion needs a metric and a threshold.", Retryable: false}
	}
	switch onMiss {
	case "kill", "park", "halt":
	default:
		return store.KillCriterion{}, provider.AppError{Code: "invalid_input",
			UserMessage: "On-miss must be kill, park, or halt.", Retryable: false}
	}
	k := store.KillCriterion{
		ID: uuid.NewString(), IdeaID: ideaID, Metric: metric, Threshold: threshold,
		ReviewDate: reviewDate, OnMiss: onMiss, Status: "pending",
	}
	if err := a.st.AddKillCriterion(k); err != nil {
		return store.KillCriterion{}, err
	}
	return k, nil
}

func (a *API) UpdateKillCriterion(k store.KillCriterion) error { return a.st.UpdateKillCriterion(k) }
func (a *API) DeleteKillCriterion(id string) error            { return a.st.DeleteKillCriterion(id) }
func (a *API) ListKillCriteria(ideaID string) ([]store.KillCriterion, error) {
	return a.st.ListKillCriteria(ideaID)
}

// ListReviewsDue runs the on-launch sweep: pending kill criteria due at or
// before now, shaped with days-overdue for the Reviews Due panel.
func (a *API) ListReviewsDue() ([]pipeline.DueReviewView, error) {
	rows, err := a.st.ListDueKillCriteria(time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	return pipeline.ShapeDueReviews(rows, time.Now().UnixMilli()), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/appapi/ -run 'TestCreateAndListIdeas|TestSetIdeaStatus|TestReviewsDue' -v`
Expected: PASS. Then run `go test ./...` — all packages PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/appapi/pipeline.go internal/appapi/pipeline_test.go
git commit -m "feat(appapi): bound idea-pipeline methods"
```

---

## Task 8: Regenerate Wails bindings

**Files:**
- Modify (generated): `frontend/wailsjs/go/appapi/API.js`, `frontend/wailsjs/go/appapi/API.d.ts`, `frontend/wailsjs/go/models.ts`

- [ ] **Step 1: Regenerate bindings**

Run: `wails generate module`
Expected: regenerates the `wailsjs/go` bindings from the Go API. (If `wails generate module` is unavailable in this environment, `wails dev` / `wails build` regenerate bindings as a side effect; a no-op build is sufficient.)

- [ ] **Step 2: Verify the new methods appear**

Run: `grep -l "CreateIdea" frontend/wailsjs/go/appapi/API.d.ts`
Expected: the file path prints (the binding exists). Confirm `ListReviewsDue`, `SetIdeaStatus`, `AddKillCriterion`, and the `store.Idea` / `store.KillCriterion` / `pipeline.DueReviewView` types are present in `API.d.ts` and `models.ts`.

- [ ] **Step 3: Commit**

```bash
git add frontend/wailsjs/go/appapi/API.js frontend/wailsjs/go/appapi/API.d.ts frontend/wailsjs/go/models.ts
git commit -m "chore(bindings): regenerate Wails bindings for pipeline API"
```

---

## Task 9: Frontend — Pipeline view

**Files:**
- Modify: `frontend/index.html`
- Create: `frontend/src/pipeline.ts`
- Modify: `frontend/src/main.ts` (import + init)
- Modify: `frontend/src/style.css`

> No frontend unit-test harness exists in this project (verification is manual smoke testing per `docs/SMOKE.md`). These steps are build-and-smoke, not TDD.

- [ ] **Step 1: Add the sidebar button and view markup to `index.html`**

In `#sidebar`, add a button after `#asgBtn`:

```html
      <button id="pipelineBtn">🎯 Pipeline <span id="reviewsDueBadge" class="badge hidden">0</span></button>
```

After the `#asgView` block (before `#tbModal`), add:

```html
  <div id="pipelineView" class="hidden">
    <div id="pipelineBar">
      <button id="pipelineBack">← Chat</button>
      <span id="pipelineBarTitle">Pipeline</span>
      <span class="spacer"></span>
      <button id="pipelineNewBtn">+ New idea</button>
    </div>
    <div id="reviewsDuePanel" class="hidden"></div>
    <div id="pipelineBody">
      <div id="ideaListPane"><div id="ideaList"></div></div>
      <div id="ideaDetail"></div>
    </div>
  </div>
```

- [ ] **Step 2: Create `frontend/src/pipeline.ts`**

```ts
import * as App from '../wailsjs/go/appapi/API'
import { store, pipeline } from '../wailsjs/go/models'

const $ = (id: string) => document.getElementById(id) as HTMLElement

const PATHWAYS = [
  { key: '', label: '— unrouted —' },
  { key: 'rapid_brainstorm', label: 'Rapid Brainstorm' },
  { key: 'side_business', label: 'Side Business' },
  { key: 'small_project', label: 'Small Project' },
  { key: 'tech_product', label: 'Technology / Product-Led' },
  { key: 'full_startup', label: 'Full Startup' },
]

const STATUSES = ['raw', 'triaged', 'in_review', 'validating', 'go', 'parked', 'killed']

let ideas: store.Idea[] = []
let selectedId: string | null = null

function fmtDate(ms: number): string {
  if (!ms) return ''
  const d = new Date(ms)
  return d.toISOString().slice(0, 10)
}

function pathwayLabel(key: string): string {
  return PATHWAYS.find(p => p.key === key)?.label ?? key
}

async function loadIdeas() {
  try {
    ideas = (await App.ListIdeas()) || []
  } catch (e: any) {
    $('ideaList').innerHTML = `<p class="pl-error">Could not load ideas: ${e?.userMessage || e}</p>`
    return
  }
  renderIdeaList()
}

function renderIdeaList() {
  const host = $('ideaList')
  host.innerHTML = ''
  if (ideas.length === 0) {
    host.innerHTML = '<p class="pl-empty">No ideas yet. Add one to start the pipeline.</p>'
    $('ideaDetail').innerHTML = ''
    return
  }
  for (const idea of ideas) {
    const row = document.createElement('div')
    row.className = 'idea-row' + (idea.ID === selectedId ? ' selected' : '')
    row.innerHTML =
      `<span class="idea-title">${idea.Title}</span>` +
      `<span class="status-chip status-${idea.Status}">${idea.Status}</span>` +
      `<span class="idea-pathway">${pathwayLabel(idea.Pathway)}</span>` +
      (idea.FinancialFlag ? '<span class="fin-flag" title="Touches financial data">$</span>' : '')
    row.onclick = () => { selectedId = idea.ID; renderIdeaList(); void renderDetail(idea.ID) }
    host.appendChild(row)
  }
}

async function renderDetail(id: string) {
  const idea = ideas.find(i => i.ID === id)
  if (!idea) return
  const detail = $('ideaDetail')

  let crits: store.KillCriterion[] = []
  try { crits = (await App.ListKillCriteria(id)) || [] } catch { /* show empty */ }

  const statusOptions = STATUSES.filter(s => s !== idea.Status)
    .map(s => `<option value="${s}">${s}</option>`).join('')

  detail.innerHTML = `
    <h2>${idea.Title}</h2>
    <p class="idea-summary">${idea.Summary || '<em>No summary.</em>'}</p>
    <div class="detail-row"><label>Pathway</label> ${pathwayLabel(idea.Pathway)}</div>
    <div class="detail-row"><label>Status</label>
      <span class="status-chip status-${idea.Status}">${idea.Status}</span>
      <select id="statusSel"><option value="">Move to…</option>${statusOptions}</select>
    </div>
    <h3>Kill criteria</h3>
    <table class="kc-table"><thead><tr>
      <th>Metric</th><th>Threshold</th><th>Review date</th><th>On miss</th><th></th>
    </tr></thead><tbody id="kcBody">
      ${crits.map(k => `<tr>
        <td>${k.Metric}</td><td>${k.Threshold}</td>
        <td>${fmtDate(k.ReviewDate)}</td><td>${k.OnMiss}</td>
        <td><button class="kc-del" data-id="${k.ID}">✕</button></td></tr>`).join('')}
    </tbody></table>
    <button id="addKcBtn">+ Add kill criterion</button>
  `

  ;($('statusSel') as HTMLSelectElement).onchange = (e) => {
    const to = (e.target as HTMLSelectElement).value
    if (to) void moveStatus(idea.ID, to)
  }
  $('addKcBtn').onclick = () => void addCriterion(idea.ID)
  detail.querySelectorAll('.kc-del').forEach(btn => {
    ;(btn as HTMLElement).onclick = async () => {
      await App.DeleteKillCriterion((btn as HTMLElement).dataset.id!)
      void renderDetail(idea.ID)
    }
  })
}

async function moveStatus(id: string, to: string) {
  let reason = ''
  if (to === 'killed' || to === 'parked') {
    reason = prompt(`Reason for moving to ${to}:`) || ''
    if (!reason) return
  }
  try {
    await App.SetIdeaStatus(id, to, reason)
  } catch (e: any) {
    alert(e?.userMessage || `Could not change status: ${e}`)
    return
  }
  await loadIdeas()
  void renderDetail(id)
}

async function addCriterion(ideaID: string) {
  const metric = prompt('Metric (e.g. "Paid installs"):')
  if (!metric) return
  const threshold = prompt('Threshold (e.g. ">=2 in 30 days"):')
  if (!threshold) return
  const dateStr = prompt('Review date (YYYY-MM-DD):')
  if (!dateStr) return
  const reviewDate = Date.parse(dateStr + 'T00:00:00Z')
  if (isNaN(reviewDate)) { alert('Invalid date.'); return }
  const onMiss = (prompt('On miss — kill, park, or halt:', 'kill') || '').trim()
  try {
    await App.AddKillCriterion(ideaID, metric, threshold, reviewDate, onMiss)
  } catch (e: any) {
    alert(e?.userMessage || `Could not add criterion: ${e}`)
    return
  }
  void renderDetail(ideaID)
}

async function newIdea() {
  const title = prompt('Idea title:')
  if (!title) return
  const summary = prompt('One-line summary (optional):') || ''
  const pathway = (prompt('Pathway key (side_business, small_project, full_startup, …) or blank:') || '').trim()
  const financial = confirm('Does this idea touch financial data? OK = yes.')
  try {
    const created = await App.CreateIdea(title, summary, pathway, financial)
    selectedId = created.ID
  } catch (e: any) {
    alert(e?.userMessage || `Could not create idea: ${e}`)
    return
  }
  await loadIdeas()
  if (selectedId) void renderDetail(selectedId)
}

export function openPipeline() {
  $('pipelineView').classList.remove('hidden')
  void loadIdeas()
  void refreshReviewsDue()
}

function closePipeline() {
  $('pipelineView').classList.add('hidden')
}

// refreshReviewsDue runs the sweep and updates the sidebar badge + panel.
export async function refreshReviewsDue() {
  let due: pipeline.DueReviewView[] = []
  try { due = (await App.ListReviewsDue()) || [] } catch { return }
  const badge = $('reviewsDueBadge')
  if (due.length > 0) {
    badge.textContent = String(due.length)
    badge.classList.remove('hidden')
  } else {
    badge.classList.add('hidden')
  }
  const panel = $('reviewsDuePanel')
  if (due.length === 0) {
    panel.classList.add('hidden')
    panel.innerHTML = ''
    return
  }
  panel.classList.remove('hidden')
  panel.innerHTML =
    `<div class="rd-title">⏰ ${due.length} review${due.length > 1 ? 's' : ''} due</div>` +
    due.map(d => `<div class="rd-row">
      <strong>${d.IdeaTitle}</strong> — ${d.Metric} (${d.Threshold}),
      due ${fmtDate(d.ReviewDate)}${d.DaysOverdue > 0 ? `, ${d.DaysOverdue}d overdue` : ''}
      → on miss: ${d.OnMiss}</div>`).join('')
}

export function initPipeline() {
  $('pipelineBtn').onclick = () => openPipeline()
  $('pipelineBack').onclick = () => closePipeline()
  $('pipelineNewBtn').onclick = () => void newIdea()
}
```

> If the generated `models.ts` namespaces the view type differently (e.g. `pipeline.DueReviewView` vs a flat type), adjust the import in this file to match what Task 8 produced. Confirm by opening `frontend/wailsjs/go/models.ts`.

- [ ] **Step 3: Wire the module into `main.ts`**

Add near the top imports of `frontend/src/main.ts`:

```ts
import { initPipeline, refreshReviewsDue } from './pipeline'
```

At the end of `main.ts` (where other one-time wiring runs — alongside the `$('asgBtn').onclick` lines near line 1278), add:

```ts
initPipeline()
void refreshReviewsDue() // launch sweep: badge appears if anything is due
```

- [ ] **Step 4: Add styling to `style.css`**

Append to `frontend/src/style.css`:

```css
#pipelineView { position: fixed; inset: 0; background: var(--bg, #1b1b1f); z-index: 20;
  display: flex; flex-direction: column; }
#pipelineBar { display: flex; align-items: center; gap: 8px; padding: 8px 12px;
  border-bottom: 1px solid #333; }
#pipelineBar .spacer { flex: 1; }
#pipelineBody { flex: 1; display: flex; min-height: 0; }
#ideaListPane { width: 340px; border-right: 1px solid #333; overflow-y: auto; }
#ideaDetail { flex: 1; padding: 16px; overflow-y: auto; }
.idea-row { display: flex; align-items: center; gap: 8px; padding: 8px 12px;
  cursor: pointer; border-bottom: 1px solid #2a2a2e; }
.idea-row.selected, .idea-row:hover { background: #26262b; }
.idea-title { flex: 1; }
.idea-pathway { font-size: 11px; opacity: 0.6; }
.fin-flag { color: #e0b341; font-weight: bold; }
.status-chip { font-size: 11px; padding: 1px 6px; border-radius: 10px; background: #333; }
.status-go { background: #2e6b34; } .status-killed { background: #7a2b2b; }
.status-parked { background: #5a4a1f; } .status-validating { background: #2b5a7a; }
.badge { background: #c0392b; color: #fff; border-radius: 10px; padding: 0 6px;
  font-size: 11px; margin-left: 4px; }
#reviewsDuePanel { padding: 8px 12px; background: #3a2f1a; border-bottom: 1px solid #5a4a1f; }
.rd-title { font-weight: bold; margin-bottom: 4px; }
.rd-row { font-size: 13px; padding: 2px 0; }
.kc-table { width: 100%; border-collapse: collapse; margin: 8px 0; font-size: 13px; }
.kc-table th, .kc-table td { text-align: left; padding: 4px 6px; border-bottom: 1px solid #2a2a2e; }
.detail-row { margin: 6px 0; } .detail-row label { opacity: 0.6; margin-right: 8px; }
.pl-empty, .pl-error { opacity: 0.6; padding: 12px; }
```

> These colors assume the existing dark theme. If `style.css` uses CSS variables for surfaces, prefer those over the hardcoded hexes to match the app.

- [ ] **Step 5: Build and smoke-test**

Run: `wails dev`
Verify: the sidebar shows a "🎯 Pipeline" button; clicking it opens the full-screen Pipeline view; "+ New idea" creates an idea that appears in the list; selecting it shows the detail pane; the status dropdown changes status (and prompts for a reason on kill/park); "+ Add kill criterion" adds a row. "← Chat" returns to the chat view.

- [ ] **Step 6: Commit**

```bash
git add frontend/index.html frontend/src/pipeline.ts frontend/src/main.ts frontend/src/style.css
git commit -m "feat(frontend): pipeline view and reviews-due panel"
```

---

## Task 10: Reviews Due — verify launch sweep end-to-end

**Files:**
- Modify: `docs/SMOKE.md`

> The badge/panel code and the launch-sweep call were added in Task 9. This task verifies the full path with real data and documents the smoke steps. No new code unless a defect is found.

- [ ] **Step 1: Seed an overdue criterion and verify the badge on a cold start**

With `wails dev` running: create an idea, add a kill criterion with a review date in the past (e.g. yesterday). Stop and restart `wails dev`. On launch the sidebar "🎯 Pipeline" button must show a red badge with count ≥ 1, and opening the Pipeline view must show the Reviews Due panel listing that criterion with "Nd overdue".

- [ ] **Step 2: Verify a future date does not surface**

Add a second criterion dated in the future. Restart. The badge count must not include it.

- [ ] **Step 3: Document the smoke steps in `docs/SMOKE.md`**

Add a "## Business pipeline" section with the two checks above (create idea → add overdue kill criterion → restart → badge appears; future-dated criterion excluded), written in the same style as the existing smoke entries.

- [ ] **Step 4: Commit**

```bash
git add docs/SMOKE.md
git commit -m "docs(smoke): pipeline reviews-due launch sweep steps"
```

---

## Task 11: Seed the three portfolio ideas

**Files:** none (data entry through the running app)

> Guided manual entry, per the design — three heterogeneous vault notes do not justify a parser. Read each source note, then enter through the Pipeline UI.

- [ ] **Step 1: Read the source notes**

Read these for titles, summaries, and (for home automation) the kill criteria:
- `C:\Obsidian\Weldon_OpsBrain\20_active\biznass_brainstorming\sample-review.md` (home-automation kill criteria + thresholds)
- The operator's notes for HDPE cooler mounts and Bayou Wildlife Systems (ask the operator for paths if not obvious in the vault).

- [ ] **Step 2: Enter the three ideas**

Via "+ New idea":
- **HDPE cooler mounts** — pathway `small_project`, financial flag as appropriate. Set status `raw → triaged` (reason: "open design decision, scoped").
- **Aging-in-place home automation** — pathway `side_business`, financial flag **yes**. Set status `raw → triaged → in_review → validating` (reasons drawn from the sample's pre-pilot framing).
- **Bayou Wildlife Systems** — pathway `full_startup`. Set status `raw → triaged` (reason: "customer discovery pending").

- [ ] **Step 3: Enter the home-automation kill criteria**

From the sample review's Kill Criteria table, add each with its metric, threshold, on-miss, and a real review date (use the pilot day-30 and day-120 dates relative to a chosen pilot start). At least one should be dated in the past so the Reviews Due sweep demonstrably surfaces it.

- [ ] **Step 4: Verify**

Restart the app. Confirm all three ideas list with correct statuses/pathways, the home-automation idea shows its kill criteria, and the Reviews Due badge reflects any past-dated criteria.

> No commit — this is local data in the app's SQLite database, not source.

---

## Task 12: Final verification

**Files:** none

- [ ] **Step 1: Full Go test suite**

Run: `go test ./...`
Expected: all packages PASS.

- [ ] **Step 2: Vet and build**

Run: `go vet ./...` (expect clean) then `wails build` (expect a successful build producing the binary).

- [ ] **Step 3: Confirm no regressions in frozen features**

Smoke-test that the existing Chat and Assignments views still open and function (a chat send, an assignment list load). The pipeline work touches only additive schema and new files plus two wiring lines in `main.ts`; nothing in the chat/assignment paths should change.

- [ ] **Step 4: Final commit (if any uncommitted changes remain)**

```bash
git add -A
git commit -m "chore: business pipeline foundation complete"
```

---

## Self-Review Notes

- **Spec coverage:** data model (Task 1) ✓; store CRUD incl. transactional status + history (Tasks 2–4) ✓; `internal/pipeline` transition validation + sweep shaping (Tasks 5–6) ✓; bound API with normalized errors (Task 7) ✓; regenerated bindings (Task 8) ✓; Pipeline UI + kill-criteria editor (Task 9) ✓; on-launch Reviews Due sweep + panel (Tasks 9–10) ✓; `financial_flag` stored unenforced ✓; portfolio seeding (Task 11) ✓. Review tables created but unexercised, as scoped ✓.
- **Deferred, by design:** review driver, role prompts, `submit_role_verdict`, BLUF/renderer, RAG, confidentiality enforcement, Scout, vault import of framework/knowledge — all out of scope for Spec 1.
- **Refinement vs spec prose:** validation (transition legality + reason-required) lives in `internal/pipeline` and is enforced at the appapi boundary, not inside `store.SetIdeaStatus` (kept mechanical). Functionally equivalent to the spec's intent, cleaner separation.
- **Type consistency:** `store.Idea`, `store.KillCriterion`, `store.DueReview`, `store.StatusChange`, and `pipeline.DueReviewView` are used consistently across store → pipeline → appapi → frontend. Method names (`CreateIdea`, `SetIdeaStatus`, `AddKillCriterion`, `ListReviewsDue`, `ShapeDueReviews`, `ValidateTransition`) match across tasks.
```
