# Prompt / Context Library Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the SQLite system-prompt presets with a markdown-backed prompt/context library — reusable snippets stored as `.md` files, toggled per conversation, authored in an in-app editor.

**Architecture:** A new `internal/library/` package owns pure file I/O against a `library/` folder under the app data dir (one `.md` per item; filename is a frozen slug, the H1 is the display name). A new `conversation_library_items` SQLite table records which items are active per conversation. On send, the backend reads each active item, strips its H1, and concatenates the bodies into the system-prompt slot. The presets table, its CRUD, and the preset modal are removed entirely. The frontend gains a toolbar-toggled library panel and a full-surface in-app markdown editor.

**Tech Stack:** Go 1.25 (`modernc.org/sqlite`, Wails v2.12), TypeScript + Vite (no framework, no frontend test runner).

**Prerequisite (already satisfied):** The `discussion_engine` → `starshp_app` rename has landed (commits `8093625`, `a34f2a0`). The module path is `github.com/cajundata/starshp_app` and the data dir is `%APPDATA%\starshp_app\`.

**Conventions:**
- Go tests run with `go test ./...`. Frontend "build" verification is `npm --prefix frontend run build` (runs `tsc` typecheck + `vite build`); the frontend has no automated test runner — behaviour is verified via `docs/SMOKE.md`.
- The frontend `tsconfig.json` sets `noUnusedLocals` and `noUnusedParameters` — every declared symbol must be used or `tsc` fails.
- Conventional Commit prefixes (`feat:`, `refactor:`, `chore:`, `docs:`, `style:`) match the existing history.

---

## Task Order & Build-Green Guarantee

Tasks 1–4 are purely additive — `go test ./...` stays green after each. Task 5 is the one coordinated cross-package change (removing presets); it touches `store` and `appapi` together and ends green. Task 6 regenerates bindings. Tasks 7–10 are frontend + docs. Every task ends with a green build and a commit.

1. `internal/library` package
2. `LibraryDir` config + data-dir wiring
3. `conversation_library_items` table + active-item store methods
4. Library API methods + system-prompt assembly (additive)
5. Remove presets — store schema migration + appapi rewiring (atomic)
6. Regenerate Wails bindings
7. Frontend — remove preset UI, fix call signatures, new HTML structure
8. Frontend — library panel + markdown editor
9. Frontend — styling
10. `docs/SMOKE.md` — library smoke section

---

## Task 1: `internal/library` package

The library package is pure file I/O with no DB and no dependency on the rest of the app. It is fully test-driven.

**Files:**
- Create: `internal/library/library.go`
- Test: `internal/library/library_test.go`

- [ ] **Step 1: Write the failing test file**

Create `internal/library/library_test.go`:

```go
package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractH1(t *testing.T) {
	cases := []struct{ in, want string }{
		{"# Hello World\n\nbody", "Hello World"},
		{"\n\n# Leading blank\n", "Leading blank"},
		{"## Only an H2\n", ""},
		{"no heading at all", ""},
		{"# Trimmed   \n", "Trimmed"},
	}
	for _, c := range cases {
		if got := ExtractH1(c.in); got != c.want {
			t.Errorf("ExtractH1(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStripH1(t *testing.T) {
	if got := StripH1("# Title\n\nThe body."); got != "The body." {
		t.Errorf("StripH1 with body = %q", got)
	}
	if got := StripH1("# Title only"); got != "" {
		t.Errorf("StripH1 no body = %q, want empty", got)
	}
	if got := StripH1("no heading\njust text"); got != "no heading\njust text" {
		t.Errorf("StripH1 no H1 = %q", got)
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Discussion Post Tone", "discussion-post-tone"},
		{"  Trim Me!  ", "trim-me"},
		{"Mixed CASE 123", "mixed-case-123"},
		{"日本語", "item"},
	}
	for _, c := range cases {
		if got := slugify(c.in); got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCreateReadList(t *testing.T) {
	l := New(t.TempDir())
	item, err := l.Create("# Revenue Tone\n\nBe precise about ASC 606.")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.Filename != "revenue-tone.md" || item.Name != "Revenue Tone" {
		t.Fatalf("Create returned %+v", item)
	}
	content, err := l.Read(item.Filename)
	if err != nil || !strings.Contains(content, "ASC 606") {
		t.Fatalf("Read = %q, err=%v", content, err)
	}
	items, err := l.List()
	if err != nil || len(items) != 1 || items[0].Name != "Revenue Tone" {
		t.Fatalf("List = %+v, err=%v", items, err)
	}
}

func TestCreateRequiresH1(t *testing.T) {
	l := New(t.TempDir())
	if _, err := l.Create("just a body, no heading"); err != ErrNoH1 {
		t.Fatalf("Create without H1 = %v, want ErrNoH1", err)
	}
}

func TestCreateCollisionSuffix(t *testing.T) {
	l := New(t.TempDir())
	a, _ := l.Create("# Tone\n\nfirst")
	b, _ := l.Create("# Tone\n\nsecond")
	c, _ := l.Create("# Tone\n\nthird")
	if a.Filename != "tone.md" || b.Filename != "tone-2.md" || c.Filename != "tone-3.md" {
		t.Fatalf("collision filenames: %q %q %q", a.Filename, b.Filename, c.Filename)
	}
}

func TestSaveKeepsFilenameAcrossH1Change(t *testing.T) {
	l := New(t.TempDir())
	item, _ := l.Create("# Original Name\n\nbody")
	if err := l.Save(item.Filename, "# Renamed Display\n\nnew body"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	items, _ := l.List()
	if len(items) != 1 || items[0].Filename != "original-name.md" || items[0].Name != "Renamed Display" {
		t.Fatalf("after Save: %+v", items)
	}
}

func TestSaveRequiresH1(t *testing.T) {
	l := New(t.TempDir())
	item, _ := l.Create("# Name\n\nbody")
	if err := l.Save(item.Filename, "no heading now"); err != ErrNoH1 {
		t.Fatalf("Save without H1 = %v, want ErrNoH1", err)
	}
}

func TestDelete(t *testing.T) {
	l := New(t.TempDir())
	item, _ := l.Create("# Gone\n\nbody")
	if err := l.Delete(item.Filename); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	items, _ := l.List()
	if len(items) != 0 {
		t.Fatalf("expected empty after Delete, got %+v", items)
	}
}

func TestListEmptyAndMissingFolder(t *testing.T) {
	missing := New(filepath.Join(t.TempDir(), "does-not-exist"))
	if items, err := missing.List(); err != nil || len(items) != 0 {
		t.Fatalf("missing folder List = %+v, err=%v", items, err)
	}
	empty := New(t.TempDir())
	if items, err := empty.List(); err != nil || len(items) != 0 {
		t.Fatalf("empty folder List = %+v, err=%v", items, err)
	}
}

func TestListIgnoresNonMarkdownAndFallsBackToStem(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("# Not Markdown"), 0o644)
	os.WriteFile(filepath.Join(dir, "headless.md"), []byte("no heading here"), 0o644)
	items, err := New(dir).List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected only the .md file, got %+v", items)
	}
	if items[0].Filename != "headless.md" || items[0].Name != "headless" {
		t.Fatalf("stem fallback failed: %+v", items[0])
	}
}

func TestCreateUnicodeNameFallsBackToItemSlug(t *testing.T) {
	l := New(t.TempDir())
	item, err := l.Create("# 日本語\n\nbody")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.Filename != "item.md" || item.Name != "日本語" {
		t.Fatalf("unicode item = %+v", item)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/library/...`
Expected: FAIL — build error, `undefined: New` / `undefined: ExtractH1` etc. (`library.go` does not exist yet).

- [ ] **Step 3: Write the implementation**

Create `internal/library/library.go`:

```go
// Package library stores reusable prompt/context snippets as individual
// markdown files in a folder on disk. Each file's H1 is its display name;
// the filename is a frozen slug that serves as the stable item ID.
package library

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ErrNoH1 is returned by Create and Save when the content has no H1 heading.
var ErrNoH1 = errors.New(`item must start with an H1 heading (e.g. "# Title")`)

// ErrBadName is returned when a filename argument is not a bare file name.
var ErrBadName = errors.New("invalid item filename")

// Library is a folder of markdown snippet files.
type Library struct{ dir string }

// New returns a Library backed by dir. The folder is created lazily on the
// first write; a missing folder reads as an empty library.
func New(dir string) *Library { return &Library{dir: dir} }

// Item is one library entry as shown in the panel list.
type Item struct {
	Filename string `json:"filename"` // stable ID, e.g. "discussion-tone.md"
	Name     string `json:"name"`     // display name (the H1), or the stem if none
	Error    string `json:"error"`    // non-empty if the file could not be read
}

var h1Re = regexp.MustCompile(`(?m)^#[ \t]+(.+?)[ \t]*$`)

// ExtractH1 returns the text of the first H1 heading, or "" if there is none.
func ExtractH1(content string) string {
	m := h1Re.FindStringSubmatch(content)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// StripH1 removes the first H1 heading line from content and returns the rest,
// trimmed of surrounding blank lines.
func StripH1(content string) string {
	loc := h1Re.FindStringIndex(content)
	if loc == nil {
		return strings.TrimSpace(content)
	}
	return strings.TrimSpace(content[:loc[0]] + content[loc[1]:])
}

var slugDropRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify converts a display name into a lowercase, no-space filename stem.
// A name with no slug-able characters yields "item".
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugDropRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "item"
	}
	return s
}

// safeName rejects anything that is not a bare file name (no path separators,
// no ".."), so a caller cannot escape the library folder.
func safeName(filename string) (string, error) {
	if filename == "" || filename != filepath.Base(filename) || strings.Contains(filename, "..") {
		return "", ErrBadName
	}
	return filename, nil
}

// List scans the folder and returns one Item per ".md" file, sorted by display
// name. A missing folder is an empty library. An unreadable file still yields a
// row, with Error set.
func (l *Library) List() ([]Item, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Item{}, nil
		}
		return nil, err
	}
	items := []Item{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		raw, err := os.ReadFile(filepath.Join(l.dir, e.Name()))
		if err != nil {
			items = append(items, Item{Filename: e.Name(), Name: stem, Error: err.Error()})
			continue
		}
		name := ExtractH1(string(raw))
		if name == "" {
			name = stem
		}
		items = append(items, Item{Filename: e.Name(), Name: name})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return items, nil
}

// Read returns the raw markdown content of one item.
func (l *Library) Read(filename string) (string, error) {
	name, err := safeName(filename)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(l.dir, name))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// Create writes a new item. The filename is a unique slug derived from the
// content's H1; a numeric suffix breaks collisions. Returns ErrNoH1 if the
// content has no H1.
func (l *Library) Create(content string) (Item, error) {
	h1 := ExtractH1(content)
	if h1 == "" {
		return Item{}, ErrNoH1
	}
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return Item{}, err
	}
	stem := slugify(h1)
	filename := stem + ".md"
	for n := 2; ; n++ {
		if _, err := os.Stat(filepath.Join(l.dir, filename)); os.IsNotExist(err) {
			break
		}
		filename = stem + "-" + strconv.Itoa(n) + ".md"
	}
	if err := os.WriteFile(filepath.Join(l.dir, filename), []byte(content), 0o644); err != nil {
		return Item{}, err
	}
	return Item{Filename: filename, Name: h1}, nil
}

// Save overwrites an existing item's content. The filename never changes, even
// if the H1 (display name) does. Returns ErrNoH1 if the content has no H1.
func (l *Library) Save(filename, content string) error {
	name, err := safeName(filename)
	if err != nil {
		return err
	}
	if ExtractH1(content) == "" {
		return ErrNoH1
	}
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(l.dir, name), []byte(content), 0o644)
}

// Delete removes an item file.
func (l *Library) Delete(filename string) error {
	name, err := safeName(filename)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(l.dir, name))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/library/...`
Expected: PASS — `ok  github.com/cajundata/starshp_app/internal/library`.

- [ ] **Step 5: Commit**

```bash
git add internal/library/
git commit -m "feat: add library package for markdown prompt snippets"
```

---

## Task 2: `LibraryDir` config + data-dir wiring

Add a `LibraryDir` config field and default it to `%APPDATA%\starshp_app\library\`, mirroring how `AppDBPath` is handled.

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `main.go`

- [ ] **Step 1: Write the failing test**

Append this function to `internal/config/config_test.go`:

```go
func TestLoadLibraryDir(t *testing.T) {
	t.Setenv("LIBRARY_DIR", "/tmp/custom-library")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LibraryDir != "/tmp/custom-library" {
		t.Fatalf("LibraryDir = %q, want /tmp/custom-library", cfg.LibraryDir)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/... -run TestLoadLibraryDir`
Expected: FAIL — build error, `cfg.LibraryDir undefined`.

- [ ] **Step 3: Add the `LibraryDir` field**

In `internal/config/config.go`, add `LibraryDir` to the `Config` struct (after `AppDBPath`):

```go
type Config struct {
	OpenAIAPIKey       string
	AnthropicAPIKey    string
	EmbeddingModel     string
	AppDBPath          string
	RAGDBPath          string
	LibraryDir         string
	TextbooksConfig    string
	ModelsConfig       string
	ContextTokenBudget int
	RAGTopK            int
}
```

In the same file, inside `Load`, add the `LibraryDir` line to the `Config{...}` literal (after `RAGDBPath`):

```go
	c := Config{
		OpenAIAPIKey:       strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		AnthropicAPIKey:    strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")),
		EmbeddingModel:     envOr("EMBEDDING_MODEL", "text-embedding-3-small"),
		AppDBPath:          os.Getenv("APP_DB_PATH"),
		RAGDBPath:          os.Getenv("RAG_DB_PATH"),
		LibraryDir:         os.Getenv("LIBRARY_DIR"),
		TextbooksConfig:    envOr("TEXTBOOKS_CONFIG", "textbooks.yaml"),
		ModelsConfig:       envOr("MODELS_CONFIG", "models.yaml"),
		ContextTokenBudget: envInt("CONTEXT_TOKEN_BUDGET", 2500),
		RAGTopK:            envInt("RAG_TOP_K", 8),
	}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/config/...`
Expected: PASS.

- [ ] **Step 5: Default `LibraryDir` in `main.go`**

In `main.go`, in `main()`, add a default after the `RAGDBPath` block (uses `filepath`, already imported):

```go
	if cfg.RAGDBPath == "" {
		cfg.RAGDBPath = filepath.Join(dataDir(), "rag.db")
	}
	if cfg.LibraryDir == "" {
		cfg.LibraryDir = filepath.Join(dataDir(), "library")
	}
```

- [ ] **Step 6: Verify the whole project still builds and tests pass**

Run: `go build ./... && go test ./...`
Expected: PASS — the change is purely additive.

- [ ] **Step 7: Commit**

```bash
git add internal/config/ main.go
git commit -m "feat: add LibraryDir config and wire data directory"
```

---

## Task 3: `conversation_library_items` table + active-item store methods

Add the activation table and its accessors. This is additive — the `presets` table stays untouched until Task 5.

**Files:**
- Modify: `internal/store/schema.go`
- Create: `internal/store/library_items.go`
- Test: `internal/store/library_items_test.go`

- [ ] **Step 1: Add the table to the schema**

In `internal/store/schema.go`, add the `conversation_library_items` table to `schemaSQL`, immediately before the `CREATE INDEX` line:

```go
CREATE TABLE IF NOT EXISTS conversation_library_items (
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  item_name TEXT NOT NULL,
  PRIMARY KEY (conversation_id, item_name)
);
CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conversation_id);
```

- [ ] **Step 2: Write the failing test**

Create `internal/store/library_items_test.go`:

```go
package store

import "testing"

func TestActiveItemsReplaceAll(t *testing.T) {
	s := newTestStore(t)
	c, _ := s.CreateConversation("t")
	if err := s.SetActiveItems(c.ID, []string{"b.md", "a.md"}); err != nil {
		t.Fatalf("SetActiveItems: %v", err)
	}
	got, err := s.GetActiveItems(c.ID)
	if err != nil || len(got) != 2 || got[0] != "a.md" || got[1] != "b.md" {
		t.Fatalf("GetActiveItems = %v, err=%v", got, err)
	}
	// Replace-all: a new set drops the old one entirely.
	if err := s.SetActiveItems(c.ID, []string{"c.md"}); err != nil {
		t.Fatalf("SetActiveItems 2: %v", err)
	}
	got, _ = s.GetActiveItems(c.ID)
	if len(got) != 1 || got[0] != "c.md" {
		t.Fatalf("replace-all failed: %v", got)
	}
}

func TestActiveItemsCascadeDelete(t *testing.T) {
	s := newTestStore(t)
	c, _ := s.CreateConversation("t")
	if err := s.SetActiveItems(c.ID, []string{"a.md"}); err != nil {
		t.Fatalf("SetActiveItems: %v", err)
	}
	if err := s.DeleteConversation(c.ID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	got, _ := s.GetActiveItems(c.ID)
	if len(got) != 0 {
		t.Fatalf("cascade delete failed: %v", got)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/store/... -run TestActiveItems`
Expected: FAIL — build error, `s.SetActiveItems undefined` / `s.GetActiveItems undefined`.

- [ ] **Step 4: Write the store methods**

Create `internal/store/library_items.go`:

```go
package store

// GetActiveItems returns the library item filenames active for a conversation,
// ordered by filename for deterministic results.
func (s *Store) GetActiveItems(convID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT item_name FROM conversation_library_items WHERE conversation_id=? ORDER BY item_name`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// SetActiveItems replaces the full active set for a conversation in one
// transaction (replace-all semantics).
func (s *Store) SetActiveItems(convID string, names []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM conversation_library_items WHERE conversation_id=?`, convID); err != nil {
		return err
	}
	for _, n := range names {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO conversation_library_items(conversation_id,item_name) VALUES(?,?)`, convID, n); err != nil {
			return err
		}
	}
	return tx.Commit()
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/store/...`
Expected: PASS — all store tests, including the existing `TestOpenCreatesSchema` (it counts only its 4 named tables, so the new table does not disturb it).

- [ ] **Step 6: Commit**

```bash
git add internal/store/schema.go internal/store/library_items.go internal/store/library_items_test.go
git commit -m "feat: add conversation_library_items table and active-item store methods"
```

---

## Task 4: Library API methods + system-prompt assembly (additive)

Add the seven library API methods, the `assembleSystemPrompt` helper, and the startup writability check. Additive — the preset methods and the old `SendMessage` signature remain until Task 5, so `go test ./...` stays green.

**Files:**
- Modify: `internal/appapi/api.go` (add `lib` field, import, `NewAPI` wiring)
- Create: `internal/appapi/library.go`
- Test: `internal/appapi/library_test.go`
- Modify: `internal/appapi/validate.go`
- Modify: `internal/appapi/validate_test.go`

- [ ] **Step 1: Add the `lib` field to the `API` struct**

In `internal/appapi/api.go`, add the `library` import to the import block:

```go
import (
	"context"
	"strings"
	"sync"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/library"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/rag"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/cajundata/starshp_app/internal/textbooks"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)
```

Add the `lib` field to the `API` struct:

```go
type API struct {
	ctx            context.Context
	cfg            config.Config
	st             *store.Store
	reg            provider.Registry
	ragAdpt        *rag.Adapter
	lib            *library.Library
	chatSvc        *chat.Service
	mu             sync.Mutex
	cancelInFlight context.CancelFunc
}
```

Update `NewAPI` to construct the library:

```go
func NewAPI(cfg config.Config, st *store.Store, reg provider.Registry, ragAdpt *rag.Adapter) *API {
	return &API{cfg: cfg, st: st, reg: reg, ragAdpt: ragAdpt,
		lib: library.New(cfg.LibraryDir), chatSvc: chat.New(st)}
}
```

- [ ] **Step 2: Write the failing test file**

Create `internal/appapi/library_test.go`:

```go
package appapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

func TestAssembleSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	libDir := filepath.Join(dir, "library")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(libDir, "zebra.md"), []byte("# Zebra tone\n\nBe concise."), 0o644)
	os.WriteFile(filepath.Join(libDir, "alpha.md"), []byte("# Alpha role\n\nYou are a tutor."), 0o644)

	st, err := store.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	conv, _ := st.CreateConversation("t")
	if err := st.SetActiveItems(conv.ID, []string{"zebra.md", "alpha.md"}); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(config.Config{LibraryDir: libDir}, st, provider.Registry{}, nil)
	prompt, skipped, err := api.assembleSystemPrompt(conv.ID)
	if err != nil {
		t.Fatalf("assembleSystemPrompt: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("expected nothing skipped, got %v", skipped)
	}
	// Bodies concatenate in display-name order: "Alpha role" before "Zebra tone".
	want := "You are a tutor.\n\nBe concise."
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

func TestAssembleSkipsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	libDir := filepath.Join(dir, "library")
	os.MkdirAll(libDir, 0o755)
	os.WriteFile(filepath.Join(libDir, "real.md"), []byte("# Real\n\nKeep me."), 0o644)

	st, _ := store.Open(filepath.Join(dir, "app.db"))
	defer st.Close()
	conv, _ := st.CreateConversation("t")
	st.SetActiveItems(conv.ID, []string{"real.md", "ghost.md"})

	api := NewAPI(config.Config{LibraryDir: libDir}, st, provider.Registry{}, nil)
	prompt, skipped, err := api.assembleSystemPrompt(conv.ID)
	if err != nil {
		t.Fatalf("assembleSystemPrompt: %v", err)
	}
	if prompt != "Keep me." {
		t.Fatalf("prompt = %q, want %q", prompt, "Keep me.")
	}
	if len(skipped) != 1 || skipped[0] != "ghost.md" {
		t.Fatalf("skipped = %v, want [ghost.md]", skipped)
	}
}

func TestCreateLibraryItemRequiresH1(t *testing.T) {
	dir := t.TempDir()
	api := NewAPI(config.Config{LibraryDir: filepath.Join(dir, "library")}, nil, provider.Registry{}, nil)
	_, err := api.CreateLibraryItem("no heading here")
	if err == nil {
		t.Fatal("expected an error for content with no H1")
	}
	ae, ok := err.(provider.AppError)
	if !ok || ae.Code != "validation" {
		t.Fatalf("expected a validation AppError, got %#v", err)
	}
}

func TestGetActiveItemsPrunesOrphans(t *testing.T) {
	dir := t.TempDir()
	libDir := filepath.Join(dir, "library")
	os.MkdirAll(libDir, 0o755)
	os.WriteFile(filepath.Join(libDir, "real.md"), []byte("# Real\n\nbody"), 0o644)

	st, _ := store.Open(filepath.Join(dir, "app.db"))
	defer st.Close()
	conv, _ := st.CreateConversation("t")
	st.SetActiveItems(conv.ID, []string{"real.md", "ghost.md"})

	api := NewAPI(config.Config{LibraryDir: libDir}, st, provider.Registry{}, nil)
	live, err := api.GetActiveItems(conv.ID)
	if err != nil {
		t.Fatalf("GetActiveItems: %v", err)
	}
	if len(live) != 1 || live[0] != "real.md" {
		t.Fatalf("GetActiveItems = %v, want [real.md]", live)
	}
	// The orphan row must have been pruned from the store.
	persisted, _ := st.GetActiveItems(conv.ID)
	if len(persisted) != 1 || persisted[0] != "real.md" {
		t.Fatalf("orphan not pruned: %v", persisted)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/appapi/... -run "TestAssemble|TestCreateLibraryItem|TestGetActiveItems"`
Expected: FAIL — build error, `api.assembleSystemPrompt undefined` / `api.CreateLibraryItem undefined`.

- [ ] **Step 4: Write the library API file**

Create `internal/appapi/library.go`:

```go
package appapi

import (
	"sort"
	"strings"

	"github.com/cajundata/starshp_app/internal/library"
	"github.com/cajundata/starshp_app/internal/provider"
)

// ListLibraryItems returns every snippet in the library folder.
func (a *API) ListLibraryItems() ([]library.Item, error) {
	items, err := a.lib.List()
	if err != nil {
		return nil, provider.NormalizeError(err)
	}
	return items, nil
}

// ReadLibraryItem returns one item's raw markdown content.
func (a *API) ReadLibraryItem(filename string) (string, error) {
	content, err := a.lib.Read(filename)
	if err != nil {
		return "", provider.NormalizeError(err)
	}
	return content, nil
}

// CreateLibraryItem writes a new snippet and returns the created item.
func (a *API) CreateLibraryItem(content string) (library.Item, error) {
	item, err := a.lib.Create(content)
	if err != nil {
		return library.Item{}, libraryError(err)
	}
	return item, nil
}

// SaveLibraryItem overwrites an existing snippet's content.
func (a *API) SaveLibraryItem(filename, content string) error {
	if err := a.lib.Save(filename, content); err != nil {
		return libraryError(err)
	}
	return nil
}

// DeleteLibraryItem removes a snippet file.
func (a *API) DeleteLibraryItem(filename string) error {
	if err := a.lib.Delete(filename); err != nil {
		return provider.NormalizeError(err)
	}
	return nil
}

// GetActiveItems returns a conversation's active item filenames, pruning any
// whose files no longer exist on disk (self-healing on panel load).
func (a *API) GetActiveItems(convID string) ([]string, error) {
	names, err := a.st.GetActiveItems(convID)
	if err != nil {
		return nil, provider.NormalizeError(err)
	}
	items, err := a.lib.List()
	if err != nil {
		return nil, provider.NormalizeError(err)
	}
	valid := map[string]bool{}
	for _, it := range items {
		valid[it.Filename] = true
	}
	live := []string{}
	pruned := false
	for _, n := range names {
		if valid[n] {
			live = append(live, n)
		} else {
			pruned = true
		}
	}
	if pruned {
		_ = a.st.SetActiveItems(convID, live) // best-effort self-heal
	}
	return live, nil
}

// SetActiveItems replaces the active set for a conversation.
func (a *API) SetActiveItems(convID string, names []string) error {
	if err := a.st.SetActiveItems(convID, names); err != nil {
		return provider.NormalizeError(err)
	}
	return nil
}

// libraryError maps a library validation error to a friendly AppError and
// falls back to the generic normalizer for everything else.
func libraryError(err error) provider.AppError {
	switch err {
	case library.ErrNoH1:
		return provider.AppError{Code: "validation", UserMessage: `Add an H1 heading (e.g. "# Title") — it becomes the item's name.`, Retryable: false}
	case library.ErrBadName:
		return provider.AppError{Code: "validation", UserMessage: "That library item name is invalid.", Retryable: false}
	default:
		return provider.NormalizeError(err)
	}
}

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
	type entry struct{ display, body string }
	var entries []entry
	for _, name := range names {
		content, rerr := a.lib.Read(name)
		if rerr != nil {
			skipped = append(skipped, name)
			continue
		}
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

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/appapi/... -run "TestAssemble|TestCreateLibraryItem|TestGetActiveItems"`
Expected: PASS.

- [ ] **Step 6: Add the library writability check to `ValidateStartup`**

In `internal/appapi/validate.go`, add `path/filepath` to the imports:

```go
import (
	"os"
	"path/filepath"

	"github.com/cajundata/starshp_app/internal/config"
)
```

In the same file, add the library folder check inside `ValidateStartup`, before `return issues`:

```go
	if c.LibraryDir != "" {
		writable := true
		if err := os.MkdirAll(c.LibraryDir, 0o755); err != nil {
			writable = false
		} else {
			probe := filepath.Join(c.LibraryDir, ".write-probe")
			if f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o600); err != nil {
				writable = false
			} else {
				f.Close()
				os.Remove(probe)
			}
		}
		if !writable {
			issues = append(issues, "Library folder is not writable: "+c.LibraryDir)
		}
	}
	return issues
```

- [ ] **Step 7: Update the validate test for the new field**

In `internal/appapi/validate_test.go`, update `TestValidateStartup` so the `good` config has a writable `LibraryDir` (the issue count stays 1 — only `models.yaml` is missing):

```go
func TestValidateStartup(t *testing.T) {
	dir := t.TempDir()
	good := config.Config{OpenAIAPIKey: "k", AppDBPath: filepath.Join(dir, "a.db"),
		RAGDBPath: filepath.Join(dir, "r.db"), TextbooksConfig: filepath.Join(dir, "tb.yaml"),
		ModelsConfig: filepath.Join(dir, "m.yaml"), LibraryDir: filepath.Join(dir, "library")}
	if issues := ValidateStartup(good); len(issues) != 1 { // missing models.yaml only
		t.Fatalf("expected 1 issue (models.yaml), got %v", issues)
	}
	bad := config.Config{}
	if issues := ValidateStartup(bad); len(issues) == 0 {
		t.Fatal("expected issues for empty config")
	}
}
```

- [ ] **Step 8: Run the full Go suite**

Run: `go build ./... && go test ./...`
Expected: PASS — everything still green (preset methods and the old `SendMessage` signature are still in place).

- [ ] **Step 9: Commit**

```bash
git add internal/appapi/
git commit -m "feat: add library API methods and system-prompt assembly"
```

---

## Task 5: Remove presets — store schema migration + appapi rewiring (atomic)

This is the one coordinated cross-package change. It deletes the presets feature from `store` and `appapi` together, adds the schema migration, and switches `SendMessage` to assemble the system prompt from active library items. `go test ./...` must be green at the end.

**Files:**
- Modify: `internal/store/schema.go` (drop `presets` table + `preset_id` column)
- Modify: `internal/store/store.go` (run the migration in `Open`)
- Create: `internal/store/migrate.go`
- Create: `internal/store/migrate_test.go`
- Delete: `internal/store/presets.go`, `internal/store/presets_test.go`
- Modify: `internal/store/conversations.go` (`Conversation` struct, `ListConversations`, `SetConversationMeta`)
- Modify: `internal/store/conversations_test.go`, `internal/store/store_test.go`
- Modify: `internal/appapi/api.go` (remove preset methods, change `SendMessage` + `SetConversationMeta`)
- Modify: `internal/appapi/api_test.go`

- [ ] **Step 1: Write the migration test**

Create `internal/store/migrate_test.go`:

```go
package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigrateDropsLegacyPresets simulates a dev DB created before the library
// feature — it has a `presets` table and a `conversations.preset_id` column —
// and verifies store.Open migrates it: presets table gone, preset_id gone.
func TestMigrateDropsLegacyPresets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy := `
CREATE TABLE presets (id TEXT PRIMARY KEY, name TEXT NOT NULL, system_prompt TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE conversations (id TEXT PRIMARY KEY, title TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, preset_id TEXT, pinned_model TEXT);
`
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(legacy); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	db.Close()

	// Open through the real store — this must run the migration.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='presets'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("presets table should have been dropped")
	}
	has, err := columnExists(s.db, "conversations", "preset_id")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("conversations.preset_id should have been dropped")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/... -run TestMigrateDropsLegacyPresets`
Expected: FAIL — build error, `columnExists undefined`.

- [ ] **Step 3: Write the migration**

Create `internal/store/migrate.go`:

```go
package store

import "database/sql"

// migrate brings a pre-library dev database up to the current schema. It is
// idempotent: on a fresh database both operations are no-ops.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`DROP TABLE IF EXISTS presets`); err != nil {
		return err
	}
	has, err := columnExists(db, "conversations", "preset_id")
	if err != nil {
		return err
	}
	if has {
		if _, err := db.Exec(`ALTER TABLE conversations DROP COLUMN preset_id`); err != nil {
			return err
		}
	}
	return nil
}

// columnExists reports whether table has a column named col. The table name is
// an internal constant, never user input, so string interpolation is safe.
func columnExists(db *sql.DB, table, col string) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid            int
			name, ctype    string
			notnull, pk    int
			dflt           sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}
```

- [ ] **Step 4: Call the migration from `Open`**

In `internal/store/store.go`, update `Open` to run `migrate` after the schema is applied:

```go
func Open(dbPath string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(wal)&_pragma=foreign_keys(on)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return &Store{db: db}, nil
}
```

- [ ] **Step 5: Drop the `presets` table and `preset_id` column from the schema**

Replace the entire contents of `internal/store/schema.go` with:

```go
package store

const schemaSQL = `
CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY, title TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  pinned_model TEXT
);
CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  role TEXT NOT NULL, content TEXT NOT NULL, model TEXT,
  created_at INTEGER NOT NULL, rag_context TEXT, rag_sources TEXT
);
CREATE TABLE IF NOT EXISTS conversation_textbooks (
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  textbook_name TEXT NOT NULL, chapter_nums TEXT,
  PRIMARY KEY (conversation_id, textbook_name)
);
CREATE TABLE IF NOT EXISTS conversation_library_items (
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  item_name TEXT NOT NULL,
  PRIMARY KEY (conversation_id, item_name)
);
CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conversation_id);
`
```

- [ ] **Step 6: Run the migration test to verify it passes**

Run: `go test ./internal/store/... -run TestMigrateDropsLegacyPresets`
Expected: PASS.

- [ ] **Step 7: Remove `PresetID` from the `Conversation` struct and store methods**

In `internal/store/conversations.go`:

Update the `Conversation` struct (remove the `PresetID` field):

```go
type Conversation struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	PinnedModel string `json:"pinnedModel"`
}
```

Update `ListConversations` (drop `preset_id` from the SELECT and the `Scan`):

```go
func (s *Store) ListConversations() ([]Conversation, error) {
	rows, err := s.db.Query(`SELECT id,title,created_at,updated_at,COALESCE(pinned_model,'') FROM conversations ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt, &c.PinnedModel); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
```

Update `SetConversationMeta` (drop the `presetID` parameter and column):

```go
func (s *Store) SetConversationMeta(id, pinnedModel string) error {
	_, err := s.db.Exec(`UPDATE conversations SET pinned_model=?,updated_at=? WHERE id=?`,
		pinnedModel, time.Now().Unix(), id)
	return err
}
```

- [ ] **Step 8: Delete the presets store files**

```bash
git rm internal/store/presets.go internal/store/presets_test.go
```

- [ ] **Step 9: Fix the affected store tests**

In `internal/store/conversations_test.go`, in `TestConversationLifecycle`, change the `SetConversationMeta` call from three arguments to two:

Find:
```go
	if err := s.SetConversationMeta(c.ID, "preset-1", "claude-opus-4-7"); err != nil {
```
Replace with:
```go
	if err := s.SetConversationMeta(c.ID, "claude-opus-4-7"); err != nil {
```

In `internal/store/store_test.go`, replace `TestOpenCreatesSchema` with:

```go
func TestOpenCreatesSchema(t *testing.T) {
	s := newTestStore(t)
	var n int
	row := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('conversations','messages','conversation_textbooks','conversation_library_items')`)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n != 4 {
		t.Fatalf("expected 4 tables, got %d", n)
	}
	// A fresh database must not create the legacy presets table.
	var presets int
	s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='presets'`).Scan(&presets)
	if presets != 0 {
		t.Fatal("fresh DB should not have a presets table")
	}
}
```

- [ ] **Step 10: Run the store suite to verify it passes**

Run: `go test ./internal/store/...`
Expected: PASS. (`go build ./...` will still fail — `appapi` is fixed in the next steps.)

- [ ] **Step 11: Remove the preset methods from `appapi`**

In `internal/appapi/api.go`, delete these four lines (the preset methods, between `ListMessages` and `Models`):

```go
func (a *API) ListPresets() ([]store.Preset, error)            { return a.st.ListPresets() }
func (a *API) CreatePreset(name, prompt string) (store.Preset, error) {
	return a.st.CreatePreset(name, prompt)
}
func (a *API) UpdatePreset(id, name, prompt string) error { return a.st.UpdatePreset(id, name, prompt) }
func (a *API) DeletePreset(id string) error               { return a.st.DeletePreset(id) }
```

- [ ] **Step 12: Update `SetConversationMeta` in `appapi`**

In `internal/appapi/api.go`, replace:

```go
func (a *API) SetConversationMeta(convID, presetID, model string) error {
	return a.st.SetConversationMeta(convID, presetID, model)
}
```

with:

```go
func (a *API) SetConversationMeta(convID, model string) error {
	return a.st.SetConversationMeta(convID, model)
}
```

- [ ] **Step 13: Change `SendMessage` to assemble the system prompt**

In `internal/appapi/api.go`, replace the entire `SendMessage` method (its doc comment and body) with:

```go
// SendMessage streams the assistant reply to the frontend via the
// "chat:token" event and returns the full text (or a normalized error). The
// system prompt is assembled from the conversation's active library items.
func (a *API) SendMessage(convID, userText, modelID string) (string, error) {
	prov, err := provider.New(a.reg, modelID, a.cfg.OpenAIAPIKey, a.cfg.AnthropicAPIKey)
	if err != nil {
		return "", provider.NormalizeError(err)
	}
	// Auto-title: set title from first user message (best-effort, must not block send).
	existing, _ := a.st.ListMessages(convID)
	if len(existing) == 0 {
		_ = a.st.SetConversationTitle(convID, titleFromText(userText))
	}

	systemPrompt, skipped, err := a.assembleSystemPrompt(convID)
	if err != nil {
		return "", provider.NormalizeError(err)
	}
	if len(skipped) > 0 {
		// A missing snippet is not fatal — skip it, surface a soft notice.
		wruntime.EventsEmit(a.ctx, "library:notice",
			"Skipped missing library items: "+strings.Join(skipped, ", "))
	}

	scopes, _ := a.st.GetConversationTextbooks(convID) // failure → no RAG scope, not fatal
	var retr chat.Retriever
	if len(scopes) > 0 && a.ragAdpt != nil {
		retr = ragRetriever{a: a, scopes: scopes}
	}

	// Derive a per-request cancellable context so CancelMessage can abort this stream.
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

	return a.chatSvc.Send(cctx, chat.SendParams{
		ConversationID: convID, UserText: userText, SystemPrompt: systemPrompt,
		Model: modelID, Provider: prov, Retriever: retr,
	}, func(tok string) {
		wruntime.EventsEmit(a.ctx, "chat:token", tok) // use a.ctx: events always flow to UI
	})
}
```

- [ ] **Step 14: Fix the `SendMessage` call in `api_test.go`**

In `internal/appapi/api_test.go`, in `TestSendMessageNilRagAdapterNoPanic`, change:

```go
	_, err = api.SendMessage(conv.ID, "hello", "", "m1")
```

to:

```go
	_, err = api.SendMessage(conv.ID, "hello", "m1")
```

- [ ] **Step 15: Run the full Go suite to verify everything is green**

Run: `go build ./... && go test ./...`
Expected: PASS — all packages build, all tests pass. The presets feature is fully gone from the backend.

- [ ] **Step 16: Commit**

```bash
git add internal/store/ internal/appapi/
git commit -m "refactor: replace SQLite presets with the prompt library"
```

---

## Task 6: Regenerate Wails bindings

The Go API changed (methods added and removed, signatures changed). Regenerate the TypeScript/JS bindings so the frontend compiles against the new surface.

**Files:**
- Modify (generated): `frontend/wailsjs/go/appapi/API.d.ts`, `frontend/wailsjs/go/appapi/API.js`, `frontend/wailsjs/go/models.ts`

- [ ] **Step 1: Regenerate the bindings**

Run from the repository root: `wails generate module`
Expected: completes without error; it recompiles the Go bindings into `frontend/wailsjs/`.

- [ ] **Step 2: Verify the new API surface**

Run: `git diff --stat frontend/wailsjs/`
Expected: `API.d.ts`, `API.js`, and `models.ts` show changes.

Confirm the new methods are present and the preset methods are gone:

Run: `grep -E "LibraryItem|ActiveItems|CreatePreset" frontend/wailsjs/go/appapi/API.d.ts`
Expected: lines for `ListLibraryItems`, `ReadLibraryItem`, `CreateLibraryItem`, `SaveLibraryItem`, `DeleteLibraryItem`, `GetActiveItems`, `SetActiveItems` — and **no** `CreatePreset`. Also confirm `SendMessage` now takes three args and `SetConversationMeta` takes two.

- [ ] **Step 3: Commit**

```bash
git add frontend/wailsjs/
git commit -m "chore: regenerate Wails bindings for the library API"
```

---

## Task 7: Frontend — remove preset UI, fix call signatures, new HTML structure

Remove the preset dropdown and all preset code, update the `SendMessage` / `SetConversationMeta` calls to the new signatures, and add the HTML structure for the library panel and editor (wired up in Task 8). After this task the app builds and runs — the library button simply does nothing yet.

**Files:**
- Modify: `frontend/index.html`
- Modify: `frontend/src/main.ts`

- [ ] **Step 1: Replace `frontend/index.html`**

Replace the entire contents of `frontend/index.html` with:

```html
<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8" /><title>Starshp</title></head>
<body>
  <div id="app">
    <aside id="sidebar">
      <button id="newChat">+ New chat</button>
      <div class="cap">History</div>
      <div id="convList"></div>
    </aside>
    <main id="main">
      <div id="thread"></div>
      <div id="composer">
        <textarea id="input" placeholder="Message…" rows="3"></textarea>
        <div id="toolbar">
          <div class="left">
            <select id="modelSel"></select>
            <button id="libBtn">≡ Library</button>
            <button id="tbBtn">📚 Textbooks</button>
          </div>
          <button id="sendBtn">Send ▸</button>
        </div>
      </div>
    </main>
  </div>
  <div id="tbModal" class="hidden"><div id="tbModalInner"></div></div>
  <div id="libModal" class="hidden"><div id="libModalInner"></div></div>
  <div id="editorView" class="hidden">
    <div id="editorBar">
      <button id="editorBack">← Library</button>
      <span id="editorTitle">New item</span>
      <span id="editorMsg"></span>
      <span class="spacer"></span>
      <button id="editorDelete" class="hidden">Delete</button>
      <button id="editorSave">Save</button>
    </div>
    <textarea id="editorArea" spellcheck="false"
      placeholder="# Item name&#10;&#10;Write the prompt or context in markdown…"></textarea>
  </div>
  <script type="module" src="/src/main.ts"></script>
</body>
</html>
```

- [ ] **Step 2: Replace `frontend/src/main.ts`**

Replace the entire contents of `frontend/src/main.ts` with the version below — the preset dropdown const, `currentSystemPrompt()`, the preset branch of `loadMeta()`, and the preset line of `openConversation()` are removed, and the `send()` calls use the new signatures:

```ts
import './style.css'
import * as App from '../wailsjs/go/appapi/API'
import { EventsOn } from '../wailsjs/runtime/runtime'

let activeConv: string | null = null
let streaming = false

const $ = (id: string) => document.getElementById(id) as HTMLElement
const thread = $('thread')
const input = $('input') as HTMLTextAreaElement
const modelSel = $('modelSel') as HTMLSelectElement
const sendBtn = $('sendBtn') as HTMLButtonElement

let ragStatusEl: HTMLElement | null = null

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
  let revertTimer: ReturnType<typeof setTimeout> | null = null
  btn.onclick = async () => {
    try {
      await navigator.clipboard.writeText(msgText(msgEl).textContent || '')
      if (revertTimer !== null) clearTimeout(revertTimer)
      btn.classList.add('copied')
      btn.innerHTML = CHECK_ICON
      revertTimer = setTimeout(() => {
        btn.classList.remove('copied')
        btn.innerHTML = COPY_ICON
        revertTimer = null
      }, 1500)
    } catch {
      // clipboard unavailable — leave the icon unchanged, no crash
    }
  }
  row.appendChild(btn)
  msgEl.appendChild(row)
}

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

async function openConversation(id: string) {
  activeConv = id
  thread.innerHTML = ''
  const msgs = (await App.ListMessages(id)) || []
  for (const m of msgs) {
    const el = addMsg(m.role, m.content)
    if (m.role === 'assistant' && m.content.trim()) attachCopyButton(el)
  }
  const convs = (await App.ListConversations()) || []
  const c = convs.find(x => x.id === id)
  if (c && c.pinnedModel) {
    if (Array.from(modelSel.options).some(o => o.value === c.pinnedModel)) {
      modelSel.value = c.pinnedModel
    }
  }
  await loadConversations()
}

async function newChat() {
  const c = await App.CreateConversation('New conversation')
  await openConversation(c.id)
}

async function loadMeta() {
  const models = (await App.Models()) || []
  modelSel.innerHTML = models.map(m => `<option value="${m.id}">${m.display}</option>`).join('')
}

async function send() {
  if (streaming || !input.value.trim()) return
  if (!activeConv) await newChat()
  const idxStatus = addMsg('assistant', 'Preparing textbook context…')
  ragStatusEl = idxStatus
  try {
    await App.EnsureIndexed(activeConv!)
    idxStatus.remove()
  } catch (e: any) {
    msgText(idxStatus).textContent = `Cannot send: textbook indexing failed — ${e?.userMessage || e}`
    return
  } finally {
    ragStatusEl = null
  }
  const text = input.value.trim()
  input.value = ''
  addMsg('user', text)
  const asst = addMsg('assistant', '')
  streaming = true
  sendBtn.textContent = 'Stop ◼'
  sendBtn.classList.add('streaming')
  try {
    await App.SendMessage(activeConv!, text, modelSel.value)
    await App.SetConversationMeta(activeConv!, modelSel.value)
  } catch (e: any) {
    msgText(asst).textContent += `\n\n[${e?.code || 'error'}] ${e?.userMessage || e}`
  } finally {
    streaming = false
    sendBtn.textContent = 'Send ▸'
    sendBtn.classList.remove('streaming')
    if (msgText(asst).textContent?.trim()) attachCopyButton(asst)
    await loadConversations()
  }
}

EventsOn('chat:token', (tok: string) => {
  const last = thread.querySelector('.msg.assistant:last-child .msg-text')
  if (last) { last.textContent += tok; thread.scrollTop = thread.scrollHeight }
})

EventsOn('rag:index', (p: any) => {
  if (ragStatusEl) msgText(ragStatusEl).textContent = `Indexing ${p.book}… ${p.done}/${p.total} chapters`
})

async function showTextbooks() {
  if (!activeConv) await newChat()
  const books = (await App.ListBooks()) || []
  const current = (await App.GetConversationScope(activeConv!)) || []
  const inner = $('tbModalInner')
  inner.innerHTML = '<h3>Attach textbooks</h3>'
  for (const b of books) {
    const checked = current.some(s => s.name === b.name) ? 'checked' : ''
    inner.innerHTML += `<label><input type="checkbox" data-book="${b.name}" ${checked}/> ${b.name} (${b.chapters.length} ch)</label>`
  }
  const save = document.createElement('button')
  save.textContent = 'Save'
  save.onclick = async () => {
    const boxes = inner.querySelectorAll('input[type=checkbox]')
    const scopes: any[] = []
    boxes.forEach((b: any) => { if (b.checked) scopes.push({ name: b.dataset.book, chapters: null }) })
    await App.SetConversationScope(activeConv!, scopes)
    $('tbModal').classList.add('hidden')
    const banner = addMsg('assistant', 'Indexing textbooks…')
    ragStatusEl = banner
    try { await App.EnsureIndexed(activeConv!); msgText(banner).textContent = 'Textbooks ready.' }
    catch (e: any) { msgText(banner).textContent = `Indexing failed: ${e?.userMessage || e}` }
    finally { ragStatusEl = null }
  }
  inner.appendChild(save)
  $('tbModal').classList.remove('hidden')
}

$('newChat').onclick = newChat
sendBtn.onclick = () => { if (streaming) { App.CancelMessage() } else { void send() } }
$('tbBtn').onclick = showTextbooks
$('tbModal').onclick = (e) => { if (e.target === $('tbModal')) $('tbModal').classList.add('hidden') }
input.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) send()
})

;(async () => {
  const issues = (await App.StartupIssues()) || []
  if (issues.length) addMsg('assistant', '⚠ Setup:\n' + issues.join('\n'))
  await loadMeta()
  await loadConversations()
})()
```

- [ ] **Step 3: Verify the frontend builds**

Run: `npm --prefix frontend run build`
Expected: PASS — `tsc` typecheck succeeds (no unused locals) and `vite build` writes `frontend/dist/`.

- [ ] **Step 4: Commit**

```bash
git add frontend/index.html frontend/src/main.ts
git commit -m "refactor: remove preset UI from the frontend"
```

---

## Task 8: Frontend — library panel + markdown editor

Wire the library panel (list items, toggle active state) and the full-surface markdown editor (create / edit / save / delete). Panel and editor are tightly coupled — the panel opens the editor and the editor returns to the panel — so they land together.

**Files:**
- Modify: `frontend/src/main.ts`

- [ ] **Step 1: Add the `library:notice` listener**

In `frontend/src/main.ts`, immediately after the `EventsOn('rag:index', ...)` block, add:

```ts
EventsOn('library:notice', (msg: string) => {
  const note = document.createElement('div')
  note.className = 'notice'
  note.textContent = '⚠ ' + msg
  // Insert before the streaming assistant bubble so chat:token still targets it.
  const lastAsst = thread.querySelector('.msg.assistant:last-child')
  if (lastAsst) thread.insertBefore(note, lastAsst)
  else thread.appendChild(note)
  thread.scrollTop = thread.scrollHeight
})
```

- [ ] **Step 2: Add the panel + editor functions**

In `frontend/src/main.ts`, immediately after the `showTextbooks` function (before the `$('newChat').onclick = newChat` wiring block), add:

```ts
// ---- Prompt / context library ----------------------------------------------

const libModal = $('libModal')
const editorView = $('editorView')
const editorArea = $('editorArea') as HTMLTextAreaElement
const editorTitle = $('editorTitle')
const editorMsg = $('editorMsg')
const editorDelete = $('editorDelete') as HTMLButtonElement

let editingFile: string | null = null // null = creating a new item

async function openLibraryPanel() {
  if (!activeConv) await newChat()
  const items = (await App.ListLibraryItems()) || []
  const active = new Set((await App.GetActiveItems(activeConv!)) || [])
  const inner = $('libModalInner')
  inner.innerHTML = '<h3>Prompt / context library</h3>'
  if (items.length === 0) {
    inner.innerHTML += '<p class="lib-empty">No items yet. Create one to get started.</p>'
  }
  for (const it of items) {
    const row = document.createElement('div')
    row.className = 'lib-row'
    const label = document.createElement('label')
    const cb = document.createElement('input')
    cb.type = 'checkbox'
    cb.checked = active.has(it.filename)
    cb.disabled = !!it.error
    cb.dataset.file = it.filename
    cb.onchange = saveActive
    label.appendChild(cb)
    const span = document.createElement('span')
    span.textContent = it.error ? `${it.name} (unreadable)` : it.name
    label.appendChild(span)
    row.appendChild(label)
    const edit = document.createElement('button')
    edit.className = 'lib-edit'
    edit.textContent = 'Edit'
    edit.onclick = () => void openEditor(it.filename)
    row.appendChild(edit)
    inner.appendChild(row)
  }
  const add = document.createElement('button')
  add.className = 'lib-new'
  add.textContent = '+ New item'
  add.onclick = () => void openEditor(null)
  inner.appendChild(add)
  libModal.classList.remove('hidden')
}

async function saveActive() {
  const boxes = $('libModalInner').querySelectorAll('input[type=checkbox]')
  const names: string[] = []
  boxes.forEach((b) => {
    const i = b as HTMLInputElement
    if (i.checked && i.dataset.file) names.push(i.dataset.file)
  })
  await App.SetActiveItems(activeConv!, names)
}

async function openEditor(file: string | null) {
  editingFile = file
  editorMsg.textContent = ''
  if (file) {
    editorTitle.textContent = 'Edit item'
    editorDelete.classList.remove('hidden')
    try {
      editorArea.value = await App.ReadLibraryItem(file)
    } catch (e: any) {
      editorArea.value = ''
      editorMsg.textContent = e?.userMessage || String(e)
    }
  } else {
    editorTitle.textContent = 'New item'
    editorDelete.classList.add('hidden')
    editorArea.value = ''
  }
  libModal.classList.add('hidden')
  editorView.classList.remove('hidden')
  editorArea.focus()
}

function closeEditor() {
  editorView.classList.add('hidden')
}

async function saveEditor() {
  const content = editorArea.value
  try {
    if (editingFile) {
      await App.SaveLibraryItem(editingFile, content)
    } else {
      await App.CreateLibraryItem(content)
    }
  } catch (e: any) {
    editorMsg.textContent = e?.userMessage || String(e)
    return
  }
  closeEditor()
  await openLibraryPanel()
}

async function deleteEditorItem() {
  if (!editingFile) return
  try {
    await App.DeleteLibraryItem(editingFile)
  } catch (e: any) {
    editorMsg.textContent = e?.userMessage || String(e)
    return
  }
  closeEditor()
  await openLibraryPanel()
}
```

- [ ] **Step 3: Wire the library controls**

In `frontend/src/main.ts`, in the wiring block, add these lines after `$('tbModal').onclick = ...`:

```ts
$('libBtn').onclick = () => void openLibraryPanel()
libModal.onclick = (e) => { if (e.target === libModal) libModal.classList.add('hidden') }
$('editorBack').onclick = () => { closeEditor(); void openLibraryPanel() }
$('editorSave').onclick = () => void saveEditor()
editorDelete.onclick = () => void deleteEditorItem()
```

- [ ] **Step 4: Verify the frontend builds**

Run: `npm --prefix frontend run build`
Expected: PASS — `tsc` succeeds (every declared symbol is used) and `vite build` completes.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/main.ts
git commit -m "feat: add library panel and markdown editor"
```

---

## Task 9: Frontend — styling

Style the library button, panel, rows, the skipped-item notice, and the full-surface editor. Reuse the existing dark-theme tokens.

**Files:**
- Modify: `frontend/src/style.css`

- [ ] **Step 1: Add `#libBtn` to the toolbar control styling**

In `frontend/src/style.css`, find:

```css
#toolbar select, #tbBtn { background: #202024; color: #cfcfd3; border: 1px solid #34343a; border-radius: 999px; padding: 6px 11px; font-size: 12px; cursor: pointer; }
```

Replace it with:

```css
#toolbar select, #tbBtn, #libBtn { background: #202024; color: #cfcfd3; border: 1px solid #34343a; border-radius: 999px; padding: 6px 11px; font-size: 12px; cursor: pointer; }
```

- [ ] **Step 2: Extend the modal styling to cover `#libModal`**

In `frontend/src/style.css`, find:

```css
#tbModal { position: fixed; inset: 0; background: rgba(0,0,0,.6); display: flex; align-items: center; justify-content: center; }
#tbModalInner { background: #1b1b1e; border: 1px solid #34343a; border-radius: 10px; padding: 18px; min-width: 360px; max-height: 70vh; overflow: auto; }
#tbModalInner label { display: block; padding: 4px 0; color: #cfcfd3; }
```

Replace those three lines with:

```css
#tbModal, #libModal { position: fixed; inset: 0; background: rgba(0,0,0,.6); display: flex; align-items: center; justify-content: center; }
#tbModalInner, #libModalInner { background: #1b1b1e; border: 1px solid #34343a; border-radius: 10px; padding: 18px; min-width: 360px; max-height: 70vh; overflow: auto; }
#tbModalInner label { display: block; padding: 4px 0; color: #cfcfd3; }
#libModalInner h3 { color: #e7e7e8; margin-bottom: 10px; font-size: 14px; }
#libModalInner .lib-empty { color: #6f6f76; font-size: 13px; padding: 6px 0; }
.lib-row { display: flex; align-items: center; gap: 8px; padding: 5px 0; }
.lib-row label { display: flex; align-items: center; gap: 8px; color: #cfcfd3; flex: 1; cursor: pointer; }
.lib-edit { background: #202024; color: #a9a9ad; border: 1px solid #34343a; border-radius: 6px; padding: 3px 9px; font-size: 12px; cursor: pointer; }
.lib-edit:hover { color: #e7e7e8; }
.lib-new { margin-top: 12px; background: #2b2b30; color: #fff; border: 0; border-radius: 7px; padding: 8px 12px; font-weight: 600; cursor: pointer; }
.notice { align-self: center; color: #d7a64a; font-size: 12px; padding: 4px 10px; }
```

- [ ] **Step 3: Add the full-surface editor styling**

Append to the end of `frontend/src/style.css`:

```css
#editorView { position: fixed; inset: 0; z-index: 10; background: #0f0f10; display: flex; flex-direction: column; }
#editorBar { display: flex; align-items: center; gap: 10px; padding: 10px 14px; border-bottom: 1px solid #26262a; background: #141416; }
#editorBar .spacer { flex: 1; }
#editorBar #editorTitle { color: #e7e7e8; font-weight: 600; }
#editorBar #editorMsg { color: #e0654a; font-size: 12px; }
#editorBack { background: #202024; color: #cfcfd3; border: 1px solid #34343a; border-radius: 7px; padding: 6px 11px; font-size: 12px; cursor: pointer; }
#editorDelete { background: #2b2b30; color: #e0654a; border: 1px solid #34343a; border-radius: 7px; padding: 6px 12px; font-size: 12px; cursor: pointer; }
#editorSave { background: #ff5714; color: #fff; border: 0; border-radius: 7px; padding: 6px 14px; font-weight: 600; cursor: pointer; }
#editorArea { flex: 1; width: 100%; background: #0f0f10; color: #e7e7e8; border: 0; outline: none; resize: none; padding: 16px 18px; font-family: ui-monospace, "Cascadia Code", Consolas, monospace; font-size: 13px; line-height: 1.5; }
```

- [ ] **Step 4: Verify the frontend builds**

Run: `npm --prefix frontend run build`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/style.css
git commit -m "style: add library panel and editor styling"
```

---

## Task 10: `docs/SMOKE.md` — library smoke section

Replace the obsolete preset step and add a manual smoke section for the library, covering the spec's six frontend verification points.

**Files:**
- Modify: `docs/SMOKE.md`

- [ ] **Step 1: Replace `docs/SMOKE.md`**

Replace the entire contents of `docs/SMOKE.md` with:

```markdown
# Manual Smoke Checklist

Prereq: `.env` with OPENAI_API_KEY (+ ANTHROPIC_API_KEY to use Claude models),
`textbooks.yaml` pointing at a markdown textbook dir, `models.yaml` present.

Run: `wails dev`

## Core

1. [ ] App launches; if keys/configs missing, a setup notice lists the issues.
2. [ ] "+ New chat" creates a conversation; it appears in the sidebar.
3. [ ] Type a message, Send → assistant reply streams token-by-token.
4. [ ] Switch the model dropdown mid-conversation; next reply uses the new model.
5. [ ] Click 📚 Textbooks, attach a book, Save → "Indexing… N/total" then "ready".
6. [ ] Ask a question answerable from the textbook → reply is grounded.
7. [ ] Click Stop during streaming → stream is cancelled; the partial reply is persisted.
8. [ ] Close and relaunch → conversation history is intact; reopening restores messages.
9. [ ] Delete a conversation → it and its messages disappear (no orphan rows).

## Library

10. [ ] Click ≡ Library → the panel opens and lists existing items (empty on first run).
11. [ ] "+ New item" → the editor opens; saving content with no H1 is rejected with a clear message.
12. [ ] Add an H1 (`# My Item`) and a body, Save → the item appears in the panel list.
13. [ ] Edit an item and change its H1 → the display name updates; the `.md` file on disk keeps its original name.
14. [ ] Toggle items active with the checkboxes; the checked state reflects the selection.
15. [ ] Send a message → the active items' bodies reach the model in the system prompt, with the H1 stripped.
16. [ ] Switch conversations and relaunch the app → each conversation restores its own active set (sticky).
17. [ ] Delete an active item's `.md` file on disk → it drops from the panel and is skipped on send (a soft notice appears), no crash.
```

- [ ] **Step 2: Commit**

```bash
git add docs/SMOKE.md
git commit -m "docs: add library smoke checklist"
```

---

## Final Verification

After all ten tasks, confirm the whole project is green:

- [ ] Run `go build ./... && go test ./...` — expected: all packages build, all tests pass.
- [ ] Run `npm --prefix frontend run build` — expected: `tsc` typecheck passes, `vite build` succeeds.
- [ ] Run `wails dev` and walk the `docs/SMOKE.md` **Library** section (steps 10–17) by hand — this is the only coverage for frontend behaviour.
