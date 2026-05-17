# Discussion Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Grok-style desktop LLM chat client (Wails + Go) with per-message model selection (OpenAI + Anthropic), persistent history, system-prompt presets, and textbook-grounded RAG reused from acctutor.

**Architecture:** Wails v2 single binary. Go backend in `internal/` packages behind a Wails-bound API. acctutor's `embedding`/`chunker`/`ragindex` packages are copied verbatim into `internal/rag/` and used only through `internal/rag/adapter.go`. Two SQLite DBs (app data; RAG index) via pure-Go `modernc.org/sqlite`. Streaming chat surfaced to a vanilla-TS frontend via Wails runtime events.

**Tech Stack:** Go 1.25, Wails v2, `modernc.org/sqlite`, `github.com/openai/openai-go/v3`, `github.com/anthropics/anthropic-sdk-go`, `github.com/tiktoken-go/tokenizer`, `github.com/joho/godotenv`, `gopkg.in/yaml.v3`, vanilla TypeScript frontend.

**Reference spec:** `docs/superpowers/specs/2026-05-17-discussion-engine-llm-chat-client-design.md`

**Module path:** `github.com/cajundata/discussion_engine`

**Conventions for every task:** Run `gofmt -w` on changed Go files before committing. Test commands assume repo root. Commit messages use Conventional Commits.

---

### Task 1: Project scaffold (Wails + Go module)

**Files:**
- Create: `wails.json`, `main.go`, `app.go`, `go.mod`, `frontend/` (Wails vanilla-ts template), `.env.example`
- Verify: `.gitignore` already ignores `.superpowers/`, `.env`, `*.db`, `/build/`

- [ ] **Step 1: Install Wails CLI and scaffold**

Run:
```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
cd C:/Users/weldo/Projects && wails init -n discussion_engine -t vanilla-ts -d discussion_engine_tmp
```
Then move the scaffolded files into the existing `discussion_engine/` (which already has `docs/`, `.git/`, `.gitignore`):
```bash
robocopy C:/Users/weldo/Projects/discussion_engine_tmp C:/Users/weldo/Projects/discussion_engine /E /XD .git
rmdir /S /Q C:/Users/weldo/Projects/discussion_engine_tmp
```
Expected: `main.go`, `app.go`, `wails.json`, `frontend/` now exist in the repo.

- [ ] **Step 2: Set the module path**

Edit `go.mod` line 1 to:
```
module github.com/cajundata/discussion_engine
```
Run: `go mod tidy`
Expected: resolves with no errors.

- [ ] **Step 3: Create `.env.example`**

Create `.env.example`:
```
OPENAI_API_KEY=sk-your-key-here
ANTHROPIC_API_KEY=sk-ant-your-key-here
EMBEDDING_MODEL=text-embedding-3-small
APP_DB_PATH=
RAG_DB_PATH=
TEXTBOOKS_CONFIG=textbooks.yaml
MODELS_CONFIG=models.yaml
CONTEXT_TOKEN_BUDGET=2500
RAG_TOP_K=8
```

- [ ] **Step 4: Verify the app builds and runs**

Run: `wails build`
Expected: build succeeds, binary produced in `build/bin/`.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: scaffold Wails vanilla-ts project"
```

---

### Task 2: Copy acctutor RAG packages verbatim

acctutor's `ragindex` imports `chunker`. Copying changes the module path, so cross-package imports must be rewritten from `github.com/cajundata/acctutor/internal/chunker` to `github.com/cajundata/discussion_engine/internal/rag/chunker`. Files are copied **unmodified except for these import-path rewrites** (the boundary rule from the spec).

**Files:**
- Create: `internal/rag/embedding/*` (copy of `acctutor/internal/embedding/`)
- Create: `internal/rag/chunker/*` (copy of `acctutor/internal/chunker/`)
- Create: `internal/rag/ragindex/*` (copy of `acctutor/internal/ragindex/`)

- [ ] **Step 1: Copy the three packages with their tests**

```bash
mkdir -p internal/rag
robocopy C:/Users/weldo/Projects/acctutor/internal/embedding C:/Users/weldo/Projects/discussion_engine/internal/rag/embedding /E
robocopy C:/Users/weldo/Projects/acctutor/internal/chunker   C:/Users/weldo/Projects/discussion_engine/internal/rag/chunker   /E
robocopy C:/Users/weldo/Projects/acctutor/internal/ragindex  C:/Users/weldo/Projects/discussion_engine/internal/rag/ragindex  /E
```

- [ ] **Step 2: Rewrite cross-package import paths**

In every `.go` file under `internal/rag/`, replace:
- `github.com/cajundata/acctutor/internal/chunker` → `github.com/cajundata/discussion_engine/internal/rag/chunker`

Use Grep to find occurrences first:
Run: `grep -rl "cajundata/acctutor" internal/rag`
Then edit each listed file, replacing the old path with the new one. Re-run the grep; expect zero matches.

- [ ] **Step 3: Add dependencies**

Run: `go mod tidy`
Expected: pulls `github.com/openai/openai-go/v3`, `github.com/tiktoken-go/tokenizer`, `modernc.org/sqlite`, etc., matching acctutor's versions.

- [ ] **Step 4: Run the copied packages' own tests (proves the copy works)**

Run: `go test ./internal/rag/...`
Expected: PASS for `embedding`, `chunker`, `ragindex` (these shipped with acctutor's test files).

- [ ] **Step 5: Record the reusable surface for later tasks**

Create `internal/rag/REUSED.md`:
```markdown
# Reused from acctutor (verbatim — do not modify these files)

- chunker.ChunkFile(content string, meta chunker.ChapterMeta, maxTokens int) ([]chunker.Chunk, error)
- chunker.ChapterMeta{TextbookTitle, Edition string; ChapterNum int; ChapterTitle, SourceFile string}
- chunker.Chunk{ID, TextbookTitle, ChapterTitle string; ChapterNum int; SectionHeading, Subheading, Content string; TokenCount, ChunkOrder int; SourceFile, ChunkType, ParentSectionID string}
- embedding.NewEmbedder(apiKey, model string) embedding.Embedder
- embedding.NewEmbedderWithBaseURL(apiKey, model, baseURL string) embedding.Embedder
- embedding.Embedder.EmbedBatch(ctx, []string) ([][]float64, error)
- embedding.Embedder.EmbedSingle(ctx, string) ([]float64, error)
- ragindex.NewStore(dbPath string) (ragindex.Store, error)
- ragindex.Store.InsertChunks([]chunker.Chunk) error
- ragindex.Store.InsertEmbeddings(chunkIDs []string, vectors [][]float64) error
- ragindex.Store.QueryTopK(ctx, queryVec []float64, k int) ([]ragindex.ScoredChunk, error)
- ragindex.Store.GetMeta(key)/SetMeta(key,value)/Close()
- ragindex.ScoredChunk{ chunker.Chunk (embedded); Score float64 }

Any scope-aware query MUST be a NEW file in our copy, never a modification of an upstream file.
```

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat: copy acctutor embedding/chunker/ragindex behind rag boundary"
```

---

### Task 3: Config loader

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

`internal/config/config_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ok")
	t.Setenv("ANTHROPIC_API_KEY", "")
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.EmbeddingModel != "text-embedding-3-small" {
		t.Errorf("EmbeddingModel = %q", c.EmbeddingModel)
	}
	if c.ContextTokenBudget != 2500 {
		t.Errorf("ContextTokenBudget = %d", c.ContextTokenBudget)
	}
	if c.RAGTopK != 8 {
		t.Errorf("RAGTopK = %d", c.RAGTopK)
	}
	if c.OpenAIAPIKey != "ok" {
		t.Errorf("OpenAIAPIKey = %q", c.OpenAIAPIKey)
	}
}

func TestLoadReadsEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("OPENAI_API_KEY=fromfile\nRAG_TOP_K=3\n"), 0o600)
	c, err := Load(envPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.OpenAIAPIKey != "fromfile" {
		t.Errorf("OpenAIAPIKey = %q", c.OpenAIAPIKey)
	}
	if c.RAGTopK != 3 {
		t.Errorf("RAGTopK = %d", c.RAGTopK)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoad -v`
Expected: FAIL (package/function not defined).

- [ ] **Step 3: Write minimal implementation**

`internal/config/config.go`:
```go
// Package config loads runtime configuration from environment / .env.
package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	OpenAIAPIKey       string
	AnthropicAPIKey    string
	EmbeddingModel     string
	AppDBPath          string
	RAGDBPath          string
	TextbooksConfig    string
	ModelsConfig       string
	ContextTokenBudget int
	RAGTopK            int
}

// Load reads .env at envPath (if non-empty and present), then resolves config
// from the environment with defaults.
func Load(envPath string) (Config, error) {
	if envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			if err := godotenv.Load(envPath); err != nil {
				return Config{}, err
			}
		}
	}
	c := Config{
		OpenAIAPIKey:       strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		AnthropicAPIKey:    strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")),
		EmbeddingModel:     envOr("EMBEDDING_MODEL", "text-embedding-3-small"),
		AppDBPath:          os.Getenv("APP_DB_PATH"),
		RAGDBPath:          os.Getenv("RAG_DB_PATH"),
		TextbooksConfig:    envOr("TEXTBOOKS_CONFIG", "textbooks.yaml"),
		ModelsConfig:       envOr("MODELS_CONFIG", "models.yaml"),
		ContextTokenBudget: envInt("CONTEXT_TOKEN_BUDGET", 2500),
		RAGTopK:            envInt("RAG_TOP_K", 8),
	}
	return c, nil
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestLoad -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: config loader with .env support"
```

---

### Task 4: App store — open + schema

**Files:**
- Create: `internal/store/store.go`, `internal/store/schema.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

`internal/store/store_test.go`:
```go
package store

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesSchema(t *testing.T) {
	s := newTestStore(t)
	var n int
	row := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('conversations','messages','presets','conversation_textbooks')`)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n != 4 {
		t.Fatalf("expected 4 tables, got %d", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestOpenCreatesSchema -v`
Expected: FAIL (undefined `Open`).

- [ ] **Step 3: Write minimal implementation**

`internal/store/schema.go`:
```go
package store

const schemaSQL = `
CREATE TABLE IF NOT EXISTS presets (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, system_prompt TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY, title TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  preset_id TEXT, pinned_model TEXT
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
CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conversation_id);
`
```

`internal/store/store.go`:
```go
// Package store persists conversations, messages, and presets in SQLite.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

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
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestOpenCreatesSchema -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat: app store open + schema"
```

---

### Task 5: Store — presets CRUD

**Files:**
- Create: `internal/store/presets.go`
- Test: `internal/store/presets_test.go`

- [ ] **Step 1: Write the failing test**

`internal/store/presets_test.go`:
```go
package store

import "testing"

func TestPresetsCRUD(t *testing.T) {
	s := newTestStore(t)
	p, err := s.CreatePreset("Acct discussion post", "You are an accounting tutor.")
	if err != nil {
		t.Fatalf("CreatePreset: %v", err)
	}
	if p.ID == "" {
		t.Fatal("expected generated ID")
	}
	got, err := s.ListPresets()
	if err != nil || len(got) != 1 || got[0].Name != "Acct discussion post" {
		t.Fatalf("ListPresets = %+v, err=%v", got, err)
	}
	if err := s.UpdatePreset(p.ID, "Renamed", "New prompt"); err != nil {
		t.Fatalf("UpdatePreset: %v", err)
	}
	if err := s.DeletePreset(p.ID); err != nil {
		t.Fatalf("DeletePreset: %v", err)
	}
	got, _ = s.ListPresets()
	if len(got) != 0 {
		t.Fatalf("expected 0 presets, got %d", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestPresetsCRUD -v`
Expected: FAIL (undefined methods).

- [ ] **Step 3: Write minimal implementation**

`internal/store/presets.go`:
```go
package store

import (
	"time"

	"github.com/google/uuid"
)

type Preset struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	SystemPrompt string `json:"systemPrompt"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
}

func (s *Store) CreatePreset(name, systemPrompt string) (Preset, error) {
	now := time.Now().Unix()
	p := Preset{ID: uuid.NewString(), Name: name, SystemPrompt: systemPrompt, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.Exec(`INSERT INTO presets(id,name,system_prompt,created_at,updated_at) VALUES(?,?,?,?,?)`,
		p.ID, p.Name, p.SystemPrompt, p.CreatedAt, p.UpdatedAt)
	return p, err
}

func (s *Store) ListPresets() ([]Preset, error) {
	rows, err := s.db.Query(`SELECT id,name,system_prompt,created_at,updated_at FROM presets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Preset
	for rows.Next() {
		var p Preset
		if err := rows.Scan(&p.ID, &p.Name, &p.SystemPrompt, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdatePreset(id, name, systemPrompt string) error {
	_, err := s.db.Exec(`UPDATE presets SET name=?,system_prompt=?,updated_at=? WHERE id=?`,
		name, systemPrompt, time.Now().Unix(), id)
	return err
}

func (s *Store) DeletePreset(id string) error {
	_, err := s.db.Exec(`DELETE FROM presets WHERE id=?`, id)
	return err
}
```

Run: `go mod tidy` (adds `github.com/google/uuid`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestPresetsCRUD -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/ go.mod go.sum
git commit -m "feat: store presets CRUD"
```

---

### Task 6: Store — conversations, messages (cascade), textbook scope

**Files:**
- Create: `internal/store/conversations.go`
- Test: `internal/store/conversations_test.go`

- [ ] **Step 1: Write the failing test**

`internal/store/conversations_test.go`:
```go
package store

import "testing"

func TestConversationLifecycle(t *testing.T) {
	s := newTestStore(t)
	c, err := s.CreateConversation("Revenue recognition post")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if _, err := s.AddMessage(c.ID, "user", "Draft a post", "", "", ""); err != nil {
		t.Fatalf("AddMessage user: %v", err)
	}
	if _, err := s.AddMessage(c.ID, "assistant", "Here is a draft", "claude-opus-4-7", "ctx", `["ch18"]`); err != nil {
		t.Fatalf("AddMessage assistant: %v", err)
	}
	msgs, err := s.ListMessages(c.ID)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("ListMessages = %d msgs, err=%v", len(msgs), err)
	}
	if msgs[1].Model != "claude-opus-4-7" || msgs[1].RAGContext != "ctx" {
		t.Fatalf("assistant msg not persisted correctly: %+v", msgs[1])
	}

	if err := s.SetConversationTextbooks(c.ID, []TextbookScope{{Name: "intermediate-accounting", Chapters: []int{18}}}); err != nil {
		t.Fatalf("SetConversationTextbooks: %v", err)
	}
	scope, _ := s.GetConversationTextbooks(c.ID)
	if len(scope) != 1 || scope[0].Name != "intermediate-accounting" || scope[0].Chapters[0] != 18 {
		t.Fatalf("scope mismatch: %+v", scope)
	}

	if err := s.SetConversationMeta(c.ID, "preset-1", "claude-opus-4-7"); err != nil {
		t.Fatalf("SetConversationMeta: %v", err)
	}

	if err := s.DeleteConversation(c.ID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	msgs, _ = s.ListMessages(c.ID)
	if len(msgs) != 0 {
		t.Fatalf("cascade delete failed: %d messages remain", len(msgs))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestConversationLifecycle -v`
Expected: FAIL (undefined methods/types).

- [ ] **Step 3: Write minimal implementation**

`internal/store/conversations.go`:
```go
package store

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Conversation struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	PresetID    string `json:"presetId"`
	PinnedModel string `json:"pinnedModel"`
}

type Message struct {
	ID         string `json:"id"`
	ConvID     string `json:"conversationId"`
	Role       string `json:"role"`
	Content    string `json:"content"`
	Model      string `json:"model"`
	CreatedAt  int64  `json:"createdAt"`
	RAGContext string `json:"ragContext"`
	RAGSources string `json:"ragSources"`
}

type TextbookScope struct {
	Name     string `json:"name"`
	Chapters []int  `json:"chapters"`
}

func (s *Store) CreateConversation(title string) (Conversation, error) {
	now := time.Now().Unix()
	c := Conversation{ID: uuid.NewString(), Title: title, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.Exec(`INSERT INTO conversations(id,title,created_at,updated_at) VALUES(?,?,?,?)`,
		c.ID, c.Title, c.CreatedAt, c.UpdatedAt)
	return c, err
}

func (s *Store) ListConversations() ([]Conversation, error) {
	rows, err := s.db.Query(`SELECT id,title,created_at,updated_at,COALESCE(preset_id,''),COALESCE(pinned_model,'') FROM conversations ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt, &c.PresetID, &c.PinnedModel); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteConversation(id string) error {
	_, err := s.db.Exec(`DELETE FROM conversations WHERE id=?`, id)
	return err
}

func (s *Store) SetConversationMeta(id, presetID, pinnedModel string) error {
	_, err := s.db.Exec(`UPDATE conversations SET preset_id=?,pinned_model=?,updated_at=? WHERE id=?`,
		presetID, pinnedModel, time.Now().Unix(), id)
	return err
}

func (s *Store) AddMessage(convID, role, content, model, ragContext, ragSources string) (Message, error) {
	m := Message{ID: uuid.NewString(), ConvID: convID, Role: role, Content: content,
		Model: model, CreatedAt: time.Now().Unix(), RAGContext: ragContext, RAGSources: ragSources}
	_, err := s.db.Exec(`INSERT INTO messages(id,conversation_id,role,content,model,created_at,rag_context,rag_sources) VALUES(?,?,?,?,?,?,?,?)`,
		m.ID, m.ConvID, m.Role, m.Content, m.Model, m.CreatedAt, m.RAGContext, m.RAGSources)
	if err == nil {
		s.db.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, m.CreatedAt, convID)
	}
	return m, err
}

func (s *Store) ListMessages(convID string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT id,conversation_id,role,content,COALESCE(model,''),created_at,COALESCE(rag_context,''),COALESCE(rag_sources,'') FROM messages WHERE conversation_id=? ORDER BY created_at`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConvID, &m.Role, &m.Content, &m.Model, &m.CreatedAt, &m.RAGContext, &m.RAGSources); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) SetConversationTextbooks(convID string, scopes []TextbookScope) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM conversation_textbooks WHERE conversation_id=?`, convID); err != nil {
		return err
	}
	for _, sc := range scopes {
		var chJSON string
		if sc.Chapters != nil {
			b, _ := json.Marshal(sc.Chapters)
			chJSON = string(b)
		}
		if _, err := tx.Exec(`INSERT INTO conversation_textbooks(conversation_id,textbook_name,chapter_nums) VALUES(?,?,?)`,
			convID, sc.Name, chJSON); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetConversationTextbooks(convID string) ([]TextbookScope, error) {
	rows, err := s.db.Query(`SELECT textbook_name,COALESCE(chapter_nums,'') FROM conversation_textbooks WHERE conversation_id=?`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TextbookScope
	for rows.Next() {
		var sc TextbookScope
		var chJSON string
		if err := rows.Scan(&sc.Name, &chJSON); err != nil {
			return nil, err
		}
		if chJSON != "" {
			json.Unmarshal([]byte(chJSON), &sc.Chapters)
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestConversationLifecycle -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat: store conversations, messages cascade, textbook scope"
```

---

### Task 7: Textbooks scanner

Reads `textbooks.yaml` (acctutor-compatible) and lists books + chapter files. This is our own minimal code (not copied — keeps the RAG boundary clean).

**Files:**
- Create: `internal/textbooks/textbooks.go`
- Test: `internal/textbooks/textbooks_test.go`

- [ ] **Step 1: Write the failing test**

`internal/textbooks/textbooks_test.go`:
```go
package textbooks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScan(t *testing.T) {
	root := t.TempDir()
	bookDir := filepath.Join(root, "intermediate-accounting")
	os.MkdirAll(bookDir, 0o755)
	os.WriteFile(filepath.Join(bookDir, "chapter-01.md"), []byte("# C1\n## S\nbody"), 0o600)
	os.WriteFile(filepath.Join(bookDir, "chapter-18.md"), []byte("# C18\n## S\nbody"), 0o600)

	cfgPath := filepath.Join(root, "textbooks.yaml")
	os.WriteFile(cfgPath, []byte("textbooks:\n  - name: intermediate-accounting\n    chapter_dir: ./intermediate-accounting\n"), 0o600)

	books, err := Scan(cfgPath)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(books) != 1 || books[0].Name != "intermediate-accounting" {
		t.Fatalf("books = %+v", books)
	}
	if len(books[0].Chapters) != 2 || books[0].Chapters[0].Num != 1 || books[0].Chapters[1].Num != 18 {
		t.Fatalf("chapters = %+v", books[0].Chapters)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/textbooks/ -run TestScan -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Write minimal implementation**

`internal/textbooks/textbooks.go`:
```go
// Package textbooks scans the configured markdown textbook directory.
package textbooks

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Chapter struct {
	Num  int    `json:"num"`
	Path string `json:"path"`
}

type Book struct {
	Name     string    `json:"name"`
	Chapters []Chapter `json:"chapters"`
}

type yamlConfig struct {
	Textbooks []struct {
		Name       string `yaml:"name"`
		ChapterDir string `yaml:"chapter_dir"`
	} `yaml:"textbooks"`
}

var chapterRe = regexp.MustCompile(`chapter-0*([0-9]+)\.md$`)

// Scan loads the YAML config and lists each book's chapter files, sorted by
// chapter number. Returns an empty slice (not error) when no books configured.
func Scan(cfgPath string) ([]Book, error) {
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Book{}, nil
		}
		return nil, err
	}
	var cfg yamlConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	base := filepath.Dir(cfgPath)
	var books []Book
	for _, b := range cfg.Textbooks {
		dir := b.ChapterDir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(base, dir)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		var chs []Chapter
		for _, e := range entries {
			m := chapterRe.FindStringSubmatch(e.Name())
			if m == nil {
				continue
			}
			n, _ := strconv.Atoi(m[1])
			chs = append(chs, Chapter{Num: n, Path: filepath.Join(dir, e.Name())})
		}
		sort.Slice(chs, func(i, j int) bool { return chs[i].Num < chs[j].Num })
		books = append(books, Book{Name: b.Name, Chapters: chs})
	}
	return books, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/textbooks/ -run TestScan -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/textbooks/
git commit -m "feat: textbooks directory scanner"
```

---

### Task 8: RAG adapter — indexing

Indexes a book into the RAG SQLite DB: chunk each chapter (`chunker.ChunkFile`), batch-embed (`embedding.Embedder`), store (`ragindex.Store`). Idempotent via a per-book content hash in `index_meta`.

**Files:**
- Create: `internal/rag/adapter.go`
- Test: `internal/rag/adapter_index_test.go`

- [ ] **Step 1: Write the failing test (uses httptest to fake OpenAI embeddings)**

`internal/rag/adapter_index_test.go`:
```go
package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cajundata/discussion_engine/internal/textbooks"
)

// fakeEmbeddings returns a deterministic 3-dim vector per input.
func fakeEmbeddingServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Input []string `json:"input"` }
		json.NewDecoder(r.Body).Decode(&req)
		var data []map[string]any
		for i := range req.Input {
			data = append(data, map[string]any{"embedding": []float64{float64(i + 1), 0.5, 0.25}, "index": i})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data, "model": "x", "object": "list"})
	}))
}

func TestIndexBook(t *testing.T) {
	srv := fakeEmbeddingServer(t)
	defer srv.Close()

	root := t.TempDir()
	bookDir := filepath.Join(root, "ia")
	os.MkdirAll(bookDir, 0o755)
	os.WriteFile(filepath.Join(bookDir, "chapter-01.md"),
		[]byte("# Chapter 1\n## Revenue\nRevenue is recognized when earned.\n"), 0o600)

	a, err := NewAdapter(Options{
		RAGDBPath:      filepath.Join(root, "rag.db"),
		EmbeddingModel: "text-embedding-3-small",
		OpenAIKey:      "test",
		OpenAIBaseURL:  srv.URL,
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	defer a.Close()

	book := textbooks.Book{Name: "ia", Chapters: []textbooks.Chapter{{Num: 1, Path: filepath.Join(bookDir, "chapter-01.md")}}}
	res, err := a.IndexBook(context.Background(), book, nil)
	if err != nil {
		t.Fatalf("IndexBook: %v", err)
	}
	if res.ChunksIndexed == 0 {
		t.Fatal("expected chunks indexed")
	}
	// Second call must be a no-op (already indexed, unchanged).
	res2, err := a.IndexBook(context.Background(), book, nil)
	if err != nil {
		t.Fatalf("IndexBook 2: %v", err)
	}
	if !res2.SkippedUpToDate {
		t.Fatal("expected second index to skip (up to date)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/rag/ -run TestIndexBook -v`
Expected: FAIL (undefined `NewAdapter`).

- [ ] **Step 3: Write minimal implementation**

`internal/rag/adapter.go`:
```go
// Package rag is the ONLY entry point app code uses for retrieval. It wraps
// the verbatim-copied acctutor packages (embedding, chunker, ragindex).
package rag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/cajundata/discussion_engine/internal/rag/chunker"
	"github.com/cajundata/discussion_engine/internal/rag/embedding"
	"github.com/cajundata/discussion_engine/internal/rag/ragindex"
	"github.com/cajundata/discussion_engine/internal/textbooks"
)

const maxChunkTokens = 800

type Options struct {
	RAGDBPath      string
	EmbeddingModel string
	OpenAIKey      string
	OpenAIBaseURL  string // empty = default OpenAI endpoint
}

type Adapter struct {
	store    ragindex.Store
	embedder embedding.Embedder
}

type IndexResult struct {
	ChunksIndexed   int  `json:"chunksIndexed"`
	SkippedUpToDate bool `json:"skippedUpToDate"`
}

func NewAdapter(o Options) (*Adapter, error) {
	st, err := ragindex.NewStore(o.RAGDBPath)
	if err != nil {
		return nil, fmt.Errorf("open rag store: %w", err)
	}
	var emb embedding.Embedder
	if o.OpenAIBaseURL != "" {
		emb = embedding.NewEmbedderWithBaseURL(o.OpenAIKey, o.EmbeddingModel, o.OpenAIBaseURL)
	} else {
		emb = embedding.NewEmbedder(o.OpenAIKey, o.EmbeddingModel)
	}
	return &Adapter{store: st, embedder: emb}, nil
}

func (a *Adapter) Close() error { return a.store.Close() }

func bookHash(b textbooks.Book) (string, error) {
	h := sha256.New()
	for _, ch := range b.Chapters {
		data, err := os.ReadFile(ch.Path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%d:%x\n", ch.Num, sha256.Sum256(data))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// IndexBook chunks+embeds+stores all chapters of book unless the content hash
// is unchanged since last index. progress (may be nil) is called per chapter.
func (a *Adapter) IndexBook(ctx context.Context, book textbooks.Book, progress func(done, total int)) (IndexResult, error) {
	hash, err := bookHash(book)
	if err != nil {
		return IndexResult{}, err
	}
	metaKey := "book_hash:" + book.Name
	if prev, _ := a.store.GetMeta(metaKey); prev == hash {
		return IndexResult{SkippedUpToDate: true}, nil
	}
	total := len(book.Chapters)
	indexed := 0
	for i, ch := range book.Chapters {
		content, err := os.ReadFile(ch.Path)
		if err != nil {
			return IndexResult{}, err
		}
		chunks, err := chunker.ChunkFile(string(content), chunker.ChapterMeta{
			TextbookTitle: book.Name,
			ChapterNum:    ch.Num,
			ChapterTitle:  fmt.Sprintf("Chapter %d", ch.Num),
			SourceFile:    ch.Path,
		}, maxChunkTokens)
		if err != nil {
			return IndexResult{}, fmt.Errorf("chunk ch%d: %w", ch.Num, err)
		}
		if len(chunks) == 0 {
			continue
		}
		texts := make([]string, len(chunks))
		ids := make([]string, len(chunks))
		for j, c := range chunks {
			texts[j] = c.Content
			ids[j] = c.ID
		}
		vecs, err := a.embedder.EmbedBatch(ctx, texts)
		if err != nil {
			return IndexResult{}, fmt.Errorf("embed ch%d: %w", ch.Num, err)
		}
		if err := a.store.InsertChunks(chunks); err != nil {
			return IndexResult{}, err
		}
		if err := a.store.InsertEmbeddings(ids, vecs); err != nil {
			return IndexResult{}, err
		}
		indexed += len(chunks)
		if progress != nil {
			progress(i+1, total)
		}
	}
	if err := a.store.SetMeta(metaKey, hash); err != nil {
		return IndexResult{}, err
	}
	return IndexResult{ChunksIndexed: indexed}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/rag/ -run TestIndexBook -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/rag/adapter.go internal/rag/adapter_index_test.go
git commit -m "feat: rag adapter indexing with content-hash skip"
```

---

### Task 9: RAG adapter — scoped retrieval + context formatting

Embed the query, `QueryTopK` with over-fetch, filter to attached book/chapter scope, trim to a token budget, format a context block. Scope filtering lives **here in the adapter** (copied `ragindex` stays verbatim).

**Files:**
- Modify: `internal/rag/adapter.go` (append `Retrieve`)
- Test: `internal/rag/adapter_retrieve_test.go`

- [ ] **Step 1: Write the failing test**

`internal/rag/adapter_retrieve_test.go`:
```go
package rag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajundata/discussion_engine/internal/textbooks"
)

func TestRetrieveScopedAndBudgeted(t *testing.T) {
	srv := fakeEmbeddingServer(t)
	defer srv.Close()
	root := t.TempDir()
	bookDir := filepath.Join(root, "ia")
	os.MkdirAll(bookDir, 0o755)
	os.WriteFile(filepath.Join(bookDir, "chapter-01.md"),
		[]byte("# Chapter 1\n## Revenue\nRevenue recognized when earned.\n"), 0o600)
	os.WriteFile(filepath.Join(bookDir, "chapter-02.md"),
		[]byte("# Chapter 2\n## Leases\nLease classification rules.\n"), 0o600)

	a, _ := NewAdapter(Options{RAGDBPath: filepath.Join(root, "rag.db"),
		EmbeddingModel: "m", OpenAIKey: "k", OpenAIBaseURL: srv.URL})
	defer a.Close()
	book := textbooks.Book{Name: "ia", Chapters: []textbooks.Chapter{
		{Num: 1, Path: filepath.Join(bookDir, "chapter-01.md")},
		{Num: 2, Path: filepath.Join(bookDir, "chapter-02.md")},
	}}
	a.IndexBook(context.Background(), book, nil)

	// Scope to book "ia", chapter 1 only.
	res, err := a.Retrieve(context.Background(), "revenue", []ScopeFilter{{Book: "ia", Chapters: []int{1}}}, 10, 100000)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if res.Context == "" {
		t.Fatal("expected non-empty context")
	}
	if strings.Contains(res.Context, "Lease classification") {
		t.Fatal("chapter 2 leaked past the chapter-1 scope filter")
	}
	if len(res.Sources) == 0 {
		t.Fatal("expected sources recorded")
	}

	// Tiny budget => context is trimmed (shorter than the unbudgeted version).
	small, _ := a.Retrieve(context.Background(), "revenue", []ScopeFilter{{Book: "ia"}}, 10, 5)
	if len(small.Context) >= len(res.Context) {
		t.Fatal("expected budgeted context to be shorter")
	}

	// No scope => RAG skipped, empty context, no error.
	none, err := a.Retrieve(context.Background(), "revenue", nil, 10, 1000)
	if err != nil || none.Context != "" {
		t.Fatalf("expected empty context with no scope, got %q err=%v", none.Context, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/rag/ -run TestRetrieveScopedAndBudgeted -v`
Expected: FAIL (undefined `Retrieve`, `ScopeFilter`).

- [ ] **Step 3: Append implementation to `internal/rag/adapter.go`**

```go
import (
	// add to the existing import block:
	"sort"
	"strings"
)

const overfetchFactor = 6

type ScopeFilter struct {
	Book     string `json:"book"`
	Chapters []int  `json:"chapters"` // nil/empty = whole book
}

type Source struct {
	Book    string `json:"book"`
	Chapter int    `json:"chapter"`
	ChunkID string `json:"chunkId"`
}

type RetrieveResult struct {
	Context string   `json:"context"`
	Sources []Source `json:"sources"`
}

func inScope(book string, chapter int, filters []ScopeFilter) bool {
	for _, f := range filters {
		if f.Book != book {
			continue
		}
		if len(f.Chapters) == 0 {
			return true
		}
		for _, c := range f.Chapters {
			if c == chapter {
				return true
			}
		}
	}
	return false
}

// Retrieve embeds query, fetches topK*overfetch candidates, filters to the
// given scope, trims to budgetTokens, and formats a context block. With no
// scope filters it returns an empty result (RAG skipped) and no error.
func (a *Adapter) Retrieve(ctx context.Context, query string, filters []ScopeFilter, topK, budgetTokens int) (RetrieveResult, error) {
	if len(filters) == 0 {
		return RetrieveResult{}, nil
	}
	qv, err := a.embedder.EmbedSingle(ctx, query)
	if err != nil {
		return RetrieveResult{}, fmt.Errorf("embed query: %w", err)
	}
	cands, err := a.store.QueryTopK(ctx, qv, topK*overfetchFactor)
	if err != nil {
		return RetrieveResult{}, fmt.Errorf("query topk: %w", err)
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Score > cands[j].Score })

	var b strings.Builder
	var sources []Source
	used, tokens := 0, 0
	for _, sc := range cands {
		if used >= topK {
			break
		}
		if !inScope(sc.TextbookTitle, sc.ChapterNum, filters) {
			continue
		}
		if tokens+sc.TokenCount > budgetTokens && used > 0 {
			break
		}
		fmt.Fprintf(&b, "## %s — Chapter %d\n%s\n\n", sc.TextbookTitle, sc.ChapterNum, sc.Content)
		sources = append(sources, Source{Book: sc.TextbookTitle, Chapter: sc.ChapterNum, ChunkID: sc.ID})
		tokens += sc.TokenCount
		used++
	}
	out := strings.TrimSpace(b.String())
	if out != "" && tokens > budgetTokens && len(out) > budgetTokens*4 {
		out = out[:budgetTokens*4] // hard char cap as a budget backstop
	}
	return RetrieveResult{Context: out, Sources: sources}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/rag/ -v`
Expected: PASS (all rag tests, including copied-package tests).

- [ ] **Step 5: Commit**

```bash
git add internal/rag/
git commit -m "feat: rag adapter scoped+budgeted retrieval"
```

---

### Task 10: Provider interface + model registry

**Files:**
- Create: `internal/provider/provider.go`, `internal/provider/registry.go`
- Test: `internal/provider/registry_test.go`
- Create: `models.yaml` (seed)

- [ ] **Step 1: Write the failing test**

`internal/provider/registry_test.go`:
```go
package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRegistry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "models.yaml")
	os.WriteFile(p, []byte(`models:
  - display: Claude Opus 4.7
    id: claude-opus-4-7
    provider: anthropic
  - display: GPT-5.4
    id: gpt-5.4-2026-03-05
    provider: openai
`), 0o600)
	reg, err := LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if len(reg.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(reg.Models))
	}
	m, ok := reg.ByID("claude-opus-4-7")
	if !ok || m.Provider != "anthropic" {
		t.Fatalf("ByID lookup failed: %+v ok=%v", m, ok)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/ -run TestLoadRegistry -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Write implementations**

`internal/provider/provider.go`:
```go
// Package provider defines the generic streaming chat abstraction and its
// OpenAI and Anthropic implementations.
package provider

import "context"

type Message struct {
	Role    string // "user" | "assistant"
	Content string
}

type ChatRequest struct {
	Model        string
	CachedPrefix string // system prompt + textbook context (cacheable)
	Messages     []Message
}

type Delta struct {
	Text string
	Done bool
	Err  error
}

type ChatProvider interface {
	Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error)
}
```

`internal/provider/registry.go`:
```go
package provider

import (
	"os"

	"gopkg.in/yaml.v3"
)

type ModelInfo struct {
	Display  string `yaml:"display" json:"display"`
	ID       string `yaml:"id" json:"id"`
	Provider string `yaml:"provider" json:"provider"` // "openai" | "anthropic"
}

type Registry struct {
	Models []ModelInfo `yaml:"models" json:"models"`
}

func LoadRegistry(path string) (Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, err
	}
	var r Registry
	if err := yaml.Unmarshal(raw, &r); err != nil {
		return Registry{}, err
	}
	return r, nil
}

func (r Registry) ByID(id string) (ModelInfo, bool) {
	for _, m := range r.Models {
		if m.ID == id {
			return m, true
		}
	}
	return ModelInfo{}, false
}
```

Create `models.yaml`:
```yaml
models:
  - display: Claude Opus 4.7
    id: claude-opus-4-7
    provider: anthropic
  - display: Claude Sonnet 4.6
    id: claude-sonnet-4-6
    provider: anthropic
  - display: Claude Haiku 4.5
    id: claude-haiku-4-5-20251001
    provider: anthropic
  - display: GPT-5.4
    id: gpt-5.4-2026-03-05
    provider: openai
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/provider/ -run TestLoadRegistry -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/ models.yaml
git commit -m "feat: provider interface + model registry"
```

---

### Task 11: OpenAI streaming provider

**Files:**
- Create: `internal/provider/openai.go`
- Test: `internal/provider/openai_test.go`

- [ ] **Step 1: Write the failing test (mock OpenAI SSE via httptest + base URL)**

`internal/provider/openai_test.go`:
```go
package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flush := w.(http.Flusher)
		chunks := []string{"Hel", "lo"}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"index\":0}]}\n\n", c)
			flush.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush.Flush()
	}))
	defer srv.Close()

	p := NewOpenAI("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "gpt-5.4-2026-03-05", CachedPrefix: "You are helpful.",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var sb strings.Builder
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("delta err: %v", d.Err)
		}
		sb.WriteString(d.Text)
	}
	if sb.String() != "Hello" {
		t.Fatalf("assembled = %q, want %q", sb.String(), "Hello")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/ -run TestOpenAIStream -v`
Expected: FAIL (undefined `NewOpenAI`).

- [ ] **Step 3: Write minimal implementation**

`internal/provider/openai.go`:
```go
package provider

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type openAIProvider struct {
	client openai.Client
}

// NewOpenAI builds an OpenAI provider. baseURL may be empty for the default
// endpoint (tests pass an httptest URL).
func NewOpenAI(apiKey, baseURL string) ChatProvider {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &openAIProvider{client: openai.NewClient(opts...)}
}

func (p *openAIProvider) Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error) {
	msgs := []openai.ChatCompletionMessageParamUnion{}
	if req.CachedPrefix != "" {
		msgs = append(msgs, openai.SystemMessage(req.CachedPrefix))
	}
	for _, m := range req.Messages {
		if m.Role == "assistant" {
			msgs = append(msgs, openai.AssistantMessage(m.Content))
		} else {
			msgs = append(msgs, openai.UserMessage(m.Content))
		}
	}
	stream := p.client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: msgs,
	})
	out := make(chan Delta)
	go func() {
		defer close(out)
		for stream.Next() {
			chunk := stream.Current()
			if len(chunk.Choices) > 0 {
				if txt := chunk.Choices[0].Delta.Content; txt != "" {
					select {
					case out <- Delta{Text: txt}:
					case <-ctx.Done():
						out <- Delta{Err: ctx.Err(), Done: true}
						return
					}
				}
			}
		}
		if err := stream.Err(); err != nil {
			out <- Delta{Err: err, Done: true}
			return
		}
		out <- Delta{Done: true}
	}()
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/provider/ -run TestOpenAIStream -v`
Expected: PASS. (If the SDK's `Delta.Content` accessor differs in the pinned `openai-go/v3` version, consult `go doc github.com/openai/openai-go/v3` and adjust the accessor — the test asserts the externally observable behavior.)

- [ ] **Step 5: Commit**

```bash
git add internal/provider/openai.go internal/provider/openai_test.go
git commit -m "feat: openai streaming provider"
```

---

### Task 12: Anthropic streaming provider

**Files:**
- Create: `internal/provider/anthropic.go`
- Test: `internal/provider/anthropic_test.go`

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/anthropics/anthropic-sdk-go@latest && go mod tidy`
Expected: dependency added.

- [ ] **Step 2: Write the failing test (mock Anthropic SSE)**

`internal/provider/anthropic_test.go`:
```go
package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		writeSSE := func(event, data string) {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			fl.Flush()
		}
		writeSSE("message_start", `{"type":"message_start","message":{"id":"m","role":"assistant","content":[],"model":"x","usage":{"input_tokens":1,"output_tokens":0}}}`)
		writeSSE("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		writeSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`)
		writeSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`)
		writeSSE("content_block_stop", `{"type":"content_block_stop","index":0}`)
		writeSSE("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`)
		writeSSE("message_stop", `{"type":"message_stop"}`)
	}))
	defer srv.Close()

	p := NewAnthropic("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "claude-opus-4-7", CachedPrefix: "You are helpful.",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var sb strings.Builder
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("delta err: %v", d.Err)
		}
		sb.WriteString(d.Text)
	}
	if sb.String() != "Hello" {
		t.Fatalf("assembled = %q, want %q", sb.String(), "Hello")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/provider/ -run TestAnthropicStream -v`
Expected: FAIL (undefined `NewAnthropic`).

- [ ] **Step 4: Write minimal implementation**

`internal/provider/anthropic.go`:
```go
package provider

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type anthropicProvider struct {
	client anthropic.Client
}

func NewAnthropic(apiKey, baseURL string) ChatProvider {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &anthropicProvider{client: anthropic.NewClient(opts...)}
}

func (p *anthropicProvider) Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error) {
	msgs := make([]anthropic.MessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		block := anthropic.NewTextBlock(m.Content)
		if m.Role == "assistant" {
			msgs = append(msgs, anthropic.NewAssistantMessage(block))
		} else {
			msgs = append(msgs, anthropic.NewUserMessage(block))
		}
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: 4096,
		Messages:  msgs,
	}
	if req.CachedPrefix != "" {
		// Cache the system block (the prompt-caching cost lever from the spec).
		params.System = []anthropic.TextBlockParam{{
			Text:         req.CachedPrefix,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}}
	}
	stream := p.client.Messages.NewStreaming(ctx, params)
	out := make(chan Delta)
	go func() {
		defer close(out)
		for stream.Next() {
			event := stream.Current()
			if d, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
				if td, ok := d.Delta.AsAny().(anthropic.TextDelta); ok && td.Text != "" {
					select {
					case out <- Delta{Text: td.Text}:
					case <-ctx.Done():
						out <- Delta{Err: ctx.Err(), Done: true}
						return
					}
				}
			}
		}
		if err := stream.Err(); err != nil {
			out <- Delta{Err: err, Done: true}
			return
		}
		out <- Delta{Done: true}
	}()
	return out, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/provider/ -run TestAnthropicStream -v`
Expected: PASS. (The pinned `anthropic-sdk-go` event-accessor names may differ slightly; if so, run `go doc github.com/anthropics/anthropic-sdk-go` for the streaming event types and adjust the type assertions. The test asserts observable assembled text, so behavior is the contract.)

- [ ] **Step 6: Commit**

```bash
git add internal/provider/anthropic.go internal/provider/anthropic_test.go go.mod go.sum
git commit -m "feat: anthropic streaming provider with prompt caching"
```

---

### Task 13: Provider factory + error normalization

**Files:**
- Create: `internal/provider/factory.go`, `internal/provider/errors.go`
- Test: `internal/provider/errors_test.go`

- [ ] **Step 1: Write the failing test**

`internal/provider/errors_test.go`:
```go
package provider

import (
	"errors"
	"testing"
)

func TestNormalizeError(t *testing.T) {
	cases := []struct {
		in   error
		code string
	}{
		{errors.New("401 Unauthorized: invalid api key"), "auth"},
		{errors.New("429 Too Many Requests rate limit"), "rate_limit"},
		{errors.New("400 context length exceeded maximum"), "context_length"},
		{errors.New("dial tcp: connection refused"), "network"},
		{errors.New("weird"), "unknown"},
	}
	for _, c := range cases {
		got := NormalizeError(c.in)
		if got.Code != c.code {
			t.Errorf("NormalizeError(%q).Code = %q, want %q", c.in, got.Code, c.code)
		}
		if got.UserMessage == "" {
			t.Errorf("empty UserMessage for %q", c.in)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/ -run TestNormalizeError -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Write implementations**

`internal/provider/errors.go`:
```go
package provider

import "strings"

type AppError struct {
	Code        string `json:"code"`
	UserMessage string `json:"userMessage"`
	Retryable   bool   `json:"retryable"`
}

func (e AppError) Error() string { return e.Code + ": " + e.UserMessage }

func NormalizeError(err error) AppError {
	if err == nil {
		return AppError{}
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "401") || strings.Contains(s, "unauthorized") || strings.Contains(s, "invalid api key"):
		return AppError{"auth", "Invalid or missing API key. Check your .env file.", false}
	case strings.Contains(s, "429") || strings.Contains(s, "rate limit"):
		return AppError{"rate_limit", "Rate limited by the provider. Wait a moment and retry.", true}
	case strings.Contains(s, "context length") || strings.Contains(s, "maximum context"):
		return AppError{"context_length", "Too much context. Trim the attached textbook scope and retry.", false}
	case strings.Contains(s, "connection refused") || strings.Contains(s, "dial tcp") || strings.Contains(s, "timeout"):
		return AppError{"network", "Network error reaching the provider. Check your connection.", true}
	default:
		return AppError{"unknown", "Unexpected error: " + err.Error(), false}
	}
}
```

`internal/provider/factory.go`:
```go
package provider

import "fmt"

// New builds the right provider for a model ID using the registry.
func New(reg Registry, modelID, openAIKey, anthropicKey string) (ChatProvider, error) {
	m, ok := reg.ByID(modelID)
	if !ok {
		return nil, fmt.Errorf("unknown model: %s", modelID)
	}
	switch m.Provider {
	case "openai":
		if openAIKey == "" {
			return nil, AppError{"auth", "OpenAI API key not set.", false}
		}
		return NewOpenAI(openAIKey, ""), nil
	case "anthropic":
		if anthropicKey == "" {
			return nil, AppError{"auth", "Anthropic API key not set.", false}
		}
		return NewAnthropic(anthropicKey, ""), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", m.Provider)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/provider/ -v`
Expected: PASS (all provider tests).

- [ ] **Step 5: Commit**

```bash
git add internal/provider/
git commit -m "feat: provider factory + error normalization"
```

---

### Task 14: Chat orchestration

Assembles the cached prefix (`system preset + textbook context`), runs retrieval, streams from the provider, and persists the user message + assistant message (with `rag_context`/`rag_sources`).

**Files:**
- Create: `internal/chat/chat.go`
- Test: `internal/chat/chat_test.go`

- [ ] **Step 1: Write the failing test (fake provider + fake retriever)**

`internal/chat/chat_test.go`:
```go
package chat

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/cajundata/discussion_engine/internal/provider"
	"github.com/cajundata/discussion_engine/internal/store"
)

type fakeProvider struct{ gotPrefix string }

func (f *fakeProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.Delta, error) {
	f.gotPrefix = req.CachedPrefix
	ch := make(chan provider.Delta, 2)
	ch <- provider.Delta{Text: "Drafted post"}
	ch <- provider.Delta{Done: true}
	close(ch)
	return ch, nil
}

type fakeRetriever struct{}

func (fakeRetriever) Retrieve(ctx context.Context, q string) (string, string, error) {
	return "CTX: revenue rules", `[{"book":"ia","chapter":18}]`, nil
}

func TestSendPersistsAndAssemblesPrefix(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "app.db"))
	defer st.Close()
	conv, _ := st.CreateConversation("t")

	fp := &fakeProvider{}
	svc := New(st)

	var streamed string
	final, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID,
		UserText:       "Draft a post on ASC 606",
		SystemPrompt:   "You are an accounting tutor.",
		Model:          "claude-opus-4-7",
		Provider:       fp,
		Retriever:      fakeRetriever{},
	}, func(tok string) { streamed += tok })
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if streamed != "Drafted post" || final != "Drafted post" {
		t.Fatalf("stream=%q final=%q", streamed, final)
	}
	// Prefix = system prompt THEN textbook context.
	if fp.gotPrefix != "You are an accounting tutor.\n\nCTX: revenue rules" {
		t.Fatalf("prefix assembly wrong: %q", fp.gotPrefix)
	}
	msgs, _ := st.ListMessages(conv.ID)
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("messages = %+v", msgs)
	}
	if msgs[1].Model != "claude-opus-4-7" || msgs[1].RAGContext != "CTX: revenue rules" {
		t.Fatalf("assistant msg missing model/rag: %+v", msgs[1])
	}
	var srcs []map[string]any
	if json.Unmarshal([]byte(msgs[1].RAGSources), &srcs); len(srcs) != 1 {
		t.Fatalf("rag sources not persisted: %q", msgs[1].RAGSources)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chat/ -run TestSendPersistsAndAssemblesPrefix -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Write minimal implementation**

`internal/chat/chat.go`:
```go
// Package chat orchestrates retrieval + provider streaming + persistence.
package chat

import (
	"context"
	"strings"

	"github.com/cajundata/discussion_engine/internal/provider"
	"github.com/cajundata/discussion_engine/internal/store"
)

type Retriever interface {
	// Retrieve returns (contextBlock, sourcesJSON, error).
	Retrieve(ctx context.Context, query string) (string, string, error)
}

type Service struct{ st *store.Store }

func New(st *store.Store) *Service { return &Service{st: st} }

type SendParams struct {
	ConversationID string
	UserText       string
	SystemPrompt   string
	Model          string
	Provider       provider.ChatProvider
	Retriever      Retriever // may be nil (no textbook scope)
}

// Send persists the user message, retrieves context, streams the assistant
// response (token callback per chunk), persists the assistant message, and
// returns the full assistant text. A mid-stream error still persists the
// partial text marked incomplete.
func (s *Service) Send(ctx context.Context, p SendParams, onToken func(string)) (string, error) {
	if _, err := s.st.AddMessage(p.ConversationID, "user", p.UserText, "", "", ""); err != nil {
		return "", err
	}

	var ragCtx, ragSrc string
	if p.Retriever != nil {
		c, src, err := p.Retriever.Retrieve(ctx, p.UserText)
		if err != nil {
			return "", err // RAG failure is explicit, never silent (spec).
		}
		ragCtx, ragSrc = c, src
	}

	prefix := p.SystemPrompt
	if ragCtx != "" {
		if prefix != "" {
			prefix += "\n\n"
		}
		prefix += ragCtx
	}

	history, err := s.st.ListMessages(p.ConversationID)
	if err != nil {
		return "", err
	}
	var msgs []provider.Message
	for _, m := range history {
		if m.Role == "user" || m.Role == "assistant" {
			msgs = append(msgs, provider.Message{Role: m.Role, Content: m.Content})
		}
	}

	ch, err := p.Provider.Stream(ctx, provider.ChatRequest{
		Model: p.Model, CachedPrefix: prefix, Messages: msgs,
	})
	if err != nil {
		return "", provider.NormalizeError(err)
	}

	var sb strings.Builder
	var streamErr error
	for d := range ch {
		if d.Err != nil {
			streamErr = d.Err
			break
		}
		if d.Text != "" {
			sb.WriteString(d.Text)
			if onToken != nil {
				onToken(d.Text)
			}
		}
	}

	content := sb.String()
	if streamErr != nil {
		content += "\n\n⚠ response interrupted"
	}
	if _, err := s.st.AddMessage(p.ConversationID, "assistant", content, p.Model, ragCtx, ragSrc); err != nil {
		return content, err
	}
	if streamErr != nil {
		return content, provider.NormalizeError(streamErr)
	}
	return content, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chat/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chat/
git commit -m "feat: chat orchestration with prefix assembly + persistence"
```

---

### Task 15: App API layer (Wails bindings) + startup validation

Single struct bound to Wails. Wraps store/rag/provider/chat; normalizes errors at this boundary; streams tokens to the frontend via Wails runtime events.

**Files:**
- Create: `internal/appapi/api.go`, `internal/appapi/validate.go`
- Test: `internal/appapi/validate_test.go`

- [ ] **Step 1: Write the failing test (pure validation logic, no Wails runtime)**

`internal/appapi/validate_test.go`:
```go
package appapi

import (
	"path/filepath"
	"testing"

	"github.com/cajundata/discussion_engine/internal/config"
)

func TestValidateStartup(t *testing.T) {
	dir := t.TempDir()
	good := config.Config{OpenAIAPIKey: "k", AppDBPath: filepath.Join(dir, "a.db"),
		RAGDBPath: filepath.Join(dir, "r.db"), TextbooksConfig: filepath.Join(dir, "tb.yaml"),
		ModelsConfig: filepath.Join(dir, "m.yaml")}
	if issues := ValidateStartup(good); len(issues) != 1 { // missing models.yaml only
		t.Fatalf("expected 1 issue (models.yaml), got %v", issues)
	}
	bad := config.Config{}
	if issues := ValidateStartup(bad); len(issues) == 0 {
		t.Fatal("expected issues for empty config")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/appapi/ -run TestValidateStartup -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Write implementations**

`internal/appapi/validate.go`:
```go
package appapi

import (
	"os"

	"github.com/cajundata/discussion_engine/internal/config"
)

// ValidateStartup returns human-readable setup problems (empty = OK).
func ValidateStartup(c config.Config) []string {
	var issues []string
	if c.OpenAIAPIKey == "" {
		issues = append(issues, "OPENAI_API_KEY is not set (required for textbook embeddings).")
	}
	if _, err := os.Stat(c.ModelsConfig); err != nil {
		issues = append(issues, "models.yaml not found at "+c.ModelsConfig+".")
	}
	if c.AppDBPath != "" {
		if f, err := os.OpenFile(c.AppDBPath, os.O_CREATE|os.O_WRONLY, 0o600); err != nil {
			issues = append(issues, "App database path not writable: "+c.AppDBPath)
		} else {
			f.Close()
		}
	}
	return issues
}
```

`internal/appapi/api.go`:
```go
// Package appapi exposes the Wails-bound API. It is the error-normalization
// boundary: every returned error is a provider.AppError.
package appapi

import (
	"context"

	"github.com/cajundata/discussion_engine/internal/chat"
	"github.com/cajundata/discussion_engine/internal/config"
	"github.com/cajundata/discussion_engine/internal/provider"
	"github.com/cajundata/discussion_engine/internal/rag"
	"github.com/cajundata/discussion_engine/internal/store"
	"github.com/cajundata/discussion_engine/internal/textbooks"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type API struct {
	ctx      context.Context
	cfg      config.Config
	st       *store.Store
	reg      provider.Registry
	ragAdpt  *rag.Adapter
	chatSvc  *chat.Service
}

func NewAPI(cfg config.Config, st *store.Store, reg provider.Registry, ragAdpt *rag.Adapter) *API {
	return &API{cfg: cfg, st: st, reg: reg, ragAdpt: ragAdpt, chatSvc: chat.New(st)}
}

// Startup is called by Wails with the app context.
func (a *API) Startup(ctx context.Context) { a.ctx = ctx }

func (a *API) StartupIssues() []string { return ValidateStartup(a.cfg) }

func (a *API) ListConversations() ([]store.Conversation, error) { return a.st.ListConversations() }
func (a *API) CreateConversation(title string) (store.Conversation, error) {
	return a.st.CreateConversation(title)
}
func (a *API) DeleteConversation(id string) error { return a.st.DeleteConversation(id) }
func (a *API) ListMessages(id string) ([]store.Message, error) { return a.st.ListMessages(id) }
func (a *API) ListPresets() ([]store.Preset, error) { return a.st.ListPresets() }
func (a *API) CreatePreset(name, prompt string) (store.Preset, error) {
	return a.st.CreatePreset(name, prompt)
}
func (a *API) UpdatePreset(id, name, prompt string) error { return a.st.UpdatePreset(id, name, prompt) }
func (a *API) DeletePreset(id string) error              { return a.st.DeletePreset(id) }
func (a *API) Models() []provider.ModelInfo               { return a.reg.Models }
func (a *API) ListBooks() ([]textbooks.Book, error) {
	return textbooks.Scan(a.cfg.TextbooksConfig)
}
func (a *API) SetConversationScope(convID string, scopes []store.TextbookScope) error {
	return a.st.SetConversationTextbooks(convID, scopes)
}
func (a *API) GetConversationScope(convID string) ([]store.TextbookScope, error) {
	return a.st.GetConversationTextbooks(convID)
}
func (a *API) SetConversationMeta(convID, presetID, model string) error {
	return a.st.SetConversationMeta(convID, presetID, model)
}

// ragRetriever adapts rag.Adapter to chat.Retriever for one scoped request.
type ragRetriever struct {
	a       *API
	scopes  []store.TextbookScope
}

func (r ragRetriever) Retrieve(ctx context.Context, q string) (string, string, error) {
	var filters []rag.ScopeFilter
	for _, s := range r.scopes {
		filters = append(filters, rag.ScopeFilter{Book: s.Name, Chapters: s.Chapters})
	}
	res, err := r.a.ragAdpt.Retrieve(ctx, q, filters, r.a.cfg.RAGTopK, r.a.cfg.ContextTokenBudget)
	if err != nil {
		return "", "", err
	}
	srcJSON, _ := jsonMarshal(res.Sources)
	return res.Context, srcJSON, nil
}

// SendMessage streams the assistant reply to the frontend via the
// "chat:token" event and returns the full text (or a normalized error).
func (a *API) SendMessage(convID, userText, systemPrompt, modelID string) (string, error) {
	prov, err := provider.New(a.reg, modelID, a.cfg.OpenAIAPIKey, a.cfg.AnthropicAPIKey)
	if err != nil {
		return "", provider.NormalizeError(err)
	}
	scopes, _ := a.st.GetConversationTextbooks(convID)
	var retr chat.Retriever
	if len(scopes) > 0 {
		retr = ragRetriever{a: a, scopes: scopes}
	}
	return a.chatSvc.Send(a.ctx, chat.SendParams{
		ConversationID: convID, UserText: userText, SystemPrompt: systemPrompt,
		Model: modelID, Provider: prov, Retriever: retr,
	}, func(tok string) {
		wruntime.EventsEmit(a.ctx, "chat:token", tok)
	})
}
```

`internal/appapi/json.go`:
```go
package appapi

import "encoding/json"

func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/appapi/ -v`
Expected: PASS (validation test; the API methods compile but aren't unit-tested here — they're exercised via the manual smoke checklist).

- [ ] **Step 5: Commit**

```bash
git add internal/appapi/
git commit -m "feat: Wails-bound API + startup validation"
```

---

### Task 16: Wire main.go + app bootstrap

**Files:**
- Modify: `main.go`, `app.go`

- [ ] **Step 1: Replace `main.go` with wiring**

`main.go`:
```go
package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/cajundata/discussion_engine/internal/appapi"
	"github.com/cajundata/discussion_engine/internal/config"
	"github.com/cajundata/discussion_engine/internal/provider"
	"github.com/cajundata/discussion_engine/internal/rag"
	"github.com/cajundata/discussion_engine/internal/store"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func dataDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d, _ = os.Getwd()
	}
	p := filepath.Join(d, "discussion_engine")
	os.MkdirAll(p, 0o755)
	return p
}

func main() {
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.AppDBPath == "" {
		cfg.AppDBPath = filepath.Join(dataDir(), "app.db")
	}
	if cfg.RAGDBPath == "" {
		cfg.RAGDBPath = filepath.Join(dataDir(), "rag.db")
	}

	st, err := store.Open(cfg.AppDBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	reg, err := provider.LoadRegistry(cfg.ModelsConfig)
	if err != nil {
		log.Printf("warning: models registry: %v", err)
	}
	ragAdpt, err := rag.NewAdapter(rag.Options{
		RAGDBPath: cfg.RAGDBPath, EmbeddingModel: cfg.EmbeddingModel,
		OpenAIKey: cfg.OpenAIAPIKey,
	})
	if err != nil {
		log.Printf("warning: rag adapter: %v", err)
	}

	api := appapi.NewAPI(cfg, st, reg, ragAdpt)

	if err := wails.Run(&options.App{
		Title:  "Discussion Engine",
		Width:  1100,
		Height: 760,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup:  api.Startup,
		Bind:       []any{api},
	}); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 2: Remove the template `app.go` if it conflicts**

If `app.go` defines a sample `App` bound struct referenced nowhere, delete it:
```bash
rm app.go
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: compiles. (Frontend `dist` may not exist yet — create a placeholder so `//go:embed` works: `mkdir -p frontend/dist && echo "placeholder" > frontend/dist/.keep`.)

- [ ] **Step 4: Run the backend test suite**

Run: `go test ./...`
Expected: PASS (all packages; frontend not yet built).

- [ ] **Step 5: Commit**

```bash
git add main.go go.mod go.sum
git rm --cached app.go 2>/dev/null; true
git commit -m "feat: wire Wails bootstrap (config, store, rag, provider, api)"
```

---

### Task 17: Frontend — Grok-style Variant B shell

Vanilla TS. Layout matches approved Variant B: dark theme, left history sidebar, centered thread, composer with toolbar (model/preset/textbook left, Send right) **below** the input. Colors are intentionally minimal (visual polish is a deferred phase per spec).

**Files:**
- Replace: `frontend/src/main.ts`, `frontend/src/style.css`, `frontend/index.html`

- [ ] **Step 1: Write `frontend/index.html`**

```html
<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8" /><title>Discussion Engine</title></head>
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
            <select id="presetSel"></select>
            <button id="tbBtn">📚 Textbooks</button>
          </div>
          <button id="sendBtn">Send ▸</button>
        </div>
      </div>
    </main>
  </div>
  <div id="tbModal" class="hidden"><div id="tbModalInner"></div></div>
  <script type="module" src="/src/main.ts"></script>
</body>
</html>
```

- [ ] **Step 2: Write `frontend/src/style.css`**

```css
* { box-sizing: border-box; margin: 0; }
body { font-family: system-ui, sans-serif; }
#app { display: flex; height: 100vh; background: #0f0f10; color: #e7e7e8; }
#sidebar { width: 230px; background: #161618; border-right: 1px solid #26262a; padding: 12px; display: flex; flex-direction: column; gap: 6px; }
#newChat { background: #2b2b30; border: 0; color: #fff; border-radius: 7px; padding: 9px; font-weight: 600; cursor: pointer; }
.cap { font-size: 10px; letter-spacing: .06em; text-transform: uppercase; color: #6f6f76; margin: 10px 4px 4px; }
#convList .conv { padding: 7px 9px; border-radius: 6px; color: #a9a9ad; cursor: pointer; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
#convList .conv.active { background: #202024; color: #e7e7e8; }
#main { flex: 1; display: flex; flex-direction: column; }
#thread { flex: 1; overflow-y: auto; padding: 20px; display: flex; flex-direction: column; gap: 12px; }
.msg { max-width: 78%; padding: 10px 13px; border-radius: 12px; line-height: 1.45; white-space: pre-wrap; }
.msg.user { align-self: flex-end; background: #2f6df0; color: #fff; }
.msg.assistant { align-self: flex-start; background: #1d1d20; border: 1px solid #2b2b30; }
#composer { border-top: 1px solid #26262a; padding: 12px 16px; background: #141416; }
#input { width: 100%; background: #1b1b1e; border: 1px solid #34343a; border-radius: 10px; color: #e7e7e8; padding: 10px 12px; resize: vertical; font: inherit; }
#toolbar { display: flex; justify-content: space-between; align-items: center; margin-top: 9px; }
#toolbar .left { display: flex; gap: 8px; }
#toolbar select, #tbBtn { background: #202024; color: #cfcfd3; border: 1px solid #34343a; border-radius: 999px; padding: 6px 11px; font-size: 12px; cursor: pointer; }
#sendBtn { background: #2f6df0; color: #fff; border: 0; border-radius: 8px; padding: 8px 16px; font-weight: 600; cursor: pointer; }
#sendBtn.streaming { background: #b23b3b; }
.hidden { display: none; }
#tbModal { position: fixed; inset: 0; background: rgba(0,0,0,.6); display: flex; align-items: center; justify-content: center; }
#tbModalInner { background: #1b1b1e; border: 1px solid #34343a; border-radius: 10px; padding: 18px; min-width: 360px; max-height: 70vh; overflow: auto; }
#tbModalInner label { display: block; padding: 4px 0; color: #cfcfd3; }
```

- [ ] **Step 3: Commit the static shell**

```bash
git add frontend/index.html frontend/src/style.css
git commit -m "feat: frontend Grok-style shell (Variant B)"
```

---

### Task 18: Frontend — wire bindings, streaming, controls

**Files:**
- Replace: `frontend/src/main.ts`

- [ ] **Step 1: Write `frontend/src/main.ts`**

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
const presetSel = $('presetSel') as HTMLSelectElement
const sendBtn = $('sendBtn') as HTMLButtonElement

function addMsg(role: string, text: string): HTMLElement {
  const el = document.createElement('div')
  el.className = `msg ${role}`
  el.textContent = text
  thread.appendChild(el)
  thread.scrollTop = thread.scrollHeight
  return el
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
  for (const m of msgs) addMsg(m.role, m.content)
  await loadConversations()
}

async function newChat() {
  const c = await App.CreateConversation('New conversation')
  await openConversation(c.id)
}

async function loadMeta() {
  const models = (await App.Models()) || []
  modelSel.innerHTML = models.map(m => `<option value="${m.id}">${m.display}</option>`).join('')
  const presets = (await App.ListPresets()) || []
  presetSel.innerHTML = `<option value="">No preset</option>` +
    presets.map(p => `<option value="${p.id}">${p.name}</option>`).join('')
  ;(presetSel as any)._presets = presets
}

function currentSystemPrompt(): string {
  const presets = (presetSel as any)._presets || []
  const p = presets.find((x: any) => x.id === presetSel.value)
  return p ? p.systemPrompt : ''
}

async function send() {
  if (streaming || !input.value.trim()) return
  if (!activeConv) await newChat()
  const text = input.value.trim()
  input.value = ''
  addMsg('user', text)
  const asst = addMsg('assistant', '')
  streaming = true
  sendBtn.textContent = 'Stop ◼'
  sendBtn.classList.add('streaming')
  try {
    await App.SendMessage(activeConv!, text, currentSystemPrompt(), modelSel.value)
  } catch (e: any) {
    asst.textContent += `\n\n[${e?.code || 'error'}] ${e?.userMessage || e}`
  } finally {
    streaming = false
    sendBtn.textContent = 'Send ▸'
    sendBtn.classList.remove('streaming')
    await loadConversations()
  }
  function append(tok: string) { asst.textContent += tok; thread.scrollTop = thread.scrollHeight }
  ;(window as any).__append = append
}

EventsOn('chat:token', (tok: string) => {
  const last = thread.querySelector('.msg.assistant:last-child')
  if (last) { last.textContent += tok; thread.scrollTop = thread.scrollHeight }
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
  }
  inner.appendChild(save)
  $('tbModal').classList.remove('hidden')
}

$('newChat').onclick = newChat
sendBtn.onclick = send
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

Note: the duplicate token-append path inside `send()` (`__append`) is dead code from drafting — remove the `function append` block and `(window as any).__append` line; the `EventsOn('chat:token', …)` handler is the single source of truth for streaming.

- [ ] **Step 2: Remove the dead append block**

Delete these lines from `send()`:
```ts
  function append(tok: string) { asst.textContent += tok; thread.scrollTop = thread.scrollHeight }
  ;(window as any).__append = append
```

- [ ] **Step 3: Generate Wails bindings + build**

Run: `wails build`
Expected: `wailsjs/` bindings generated, `frontend/dist` built, binary produced with no TypeScript errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/
git commit -m "feat: frontend bindings, streaming, model/preset/textbook controls"
```

---

### Task 19: Indexing-on-attach + progress event

When a textbook is attached to a conversation, ensure it is indexed (idempotent) before the first send, emitting progress.

**Files:**
- Modify: `internal/appapi/api.go` (add `EnsureIndexed`)
- Modify: `frontend/src/main.ts` (call `EnsureIndexed` on scope save; show progress)
- Test: `internal/appapi/ensure_test.go`

- [ ] **Step 1: Write the failing test**

`internal/appapi/ensure_test.go`:
```go
package appapi

import "testing"

// indexBookNames is the pure helper EnsureIndexed delegates to: given the
// configured books and requested scope names, returns the books to index.
func TestBooksToIndex(t *testing.T) {
	all := []string{"ia", "blaw", "audit"}
	got := booksToIndex(all, []string{"blaw", "ia", "missing"})
	if len(got) != 2 || got[0] != "ia" || got[1] != "blaw" {
		t.Fatalf("booksToIndex = %v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/appapi/ -run TestBooksToIndex -v`
Expected: FAIL (undefined `booksToIndex`).

- [ ] **Step 3: Add implementation to `internal/appapi/api.go`**

```go
// booksToIndex returns configured book names that are in the requested set,
// preserving configured order.
func booksToIndex(configured, requested []string) []string {
	want := map[string]bool{}
	for _, r := range requested {
		want[r] = true
	}
	var out []string
	for _, c := range configured {
		if want[c] {
			out = append(out, c)
		}
	}
	return out
}

// EnsureIndexed indexes (idempotently) every attached book for a conversation,
// emitting "rag:index" progress events. Safe to call before each send.
func (a *API) EnsureIndexed(convID string) error {
	scopes, err := a.st.GetConversationTextbooks(convID)
	if err != nil || len(scopes) == 0 {
		return err
	}
	books, err := textbooks.Scan(a.cfg.TextbooksConfig)
	if err != nil {
		return err
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

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/appapi/ -v`
Expected: PASS.

- [ ] **Step 5: Wire frontend: index on scope save + progress banner**

In `frontend/src/main.ts`, in `showTextbooks()` `save.onclick`, after `await App.SetConversationScope(...)` add:
```ts
    $('tbModal').classList.add('hidden')
    const banner = addMsg('assistant', 'Indexing textbooks…')
    try { await App.EnsureIndexed(activeConv!) ; banner.textContent = 'Textbooks ready.' }
    catch (e: any) { banner.textContent = `Indexing failed: ${e?.userMessage || e}` }
```
And add near the other `EventsOn`:
```ts
EventsOn('rag:index', (p: any) => {
  const last = thread.querySelector('.msg.assistant:last-child')
  if (last) last.textContent = `Indexing ${p.book}… ${p.done}/${p.total} chapters`
})
```
Also call `await App.EnsureIndexed(activeConv!)` at the start of `send()` (before streaming) so an attached-but-unindexed book is built lazily; wrap in try/catch surfacing the normalized error into the assistant bubble.

- [ ] **Step 6: Build + commit**

Run: `wails build`
Expected: builds clean.
```bash
git add internal/appapi/ frontend/
git commit -m "feat: index-on-attach with progress events"
```

---

### Task 20: Manual smoke checklist + README

**Files:**
- Create: `docs/SMOKE.md`, `README.md`

- [ ] **Step 1: Write `docs/SMOKE.md`**

```markdown
# Manual Smoke Checklist (MVP)

Prereq: `.env` with OPENAI_API_KEY (+ ANTHROPIC_API_KEY to use Claude models),
`textbooks.yaml` pointing at a markdown textbook dir, `models.yaml` present.

Run: `wails dev`

1. [ ] App launches; if keys/configs missing, a setup notice lists the issues.
2. [ ] "+ New chat" creates a conversation; it appears in the sidebar.
3. [ ] Type a message, Send → assistant reply streams token-by-token.
4. [ ] Switch the model dropdown mid-conversation; next reply uses the new model.
5. [ ] Create a preset (via DB or a future settings UI); select it; reply reflects the system prompt.
6. [ ] Click 📚 Textbooks, attach a book, Save → "Indexing… N/total" then "ready".
7. [ ] Ask a question answerable from the textbook → reply is grounded.
8. [ ] Stop during streaming → partial reply persists, marked "⚠ response interrupted".
9. [ ] Close and relaunch → conversation history is intact; reopening restores messages.
10. [ ] Delete a conversation → it and its messages disappear (no orphan rows).
```

- [ ] **Step 2: Write `README.md`**

```markdown
# Discussion Engine

Grok-style desktop LLM chat client (Wails + Go) for drafting accounting
discussion posts, with per-message model choice (OpenAI/Anthropic), persistent
history, system-prompt presets, and textbook-grounded RAG (reused from acctutor).

## Setup
1. `cp .env.example .env` and fill in API keys.
2. Copy/point `textbooks.yaml` at your markdown textbook directory
   (`<book>/chapter-NN.md` layout).
3. `wails dev` (development) or `wails build` (release binary).

## Architecture
See `docs/superpowers/specs/2026-05-17-discussion-engine-llm-chat-client-design.md`
and `docs/superpowers/plans/2026-05-17-discussion-engine.md`.

RAG packages under `internal/rag/{embedding,chunker,ragindex}` are copied
verbatim from acctutor and used only via `internal/rag/adapter.go`. Do not
modify the copied files; add scope logic as new files in our copy.
```

- [ ] **Step 3: Final full test run**

Run: `go test ./...`
Expected: PASS across all packages.

- [ ] **Step 4: Commit**

```bash
git add docs/SMOKE.md README.md
git commit -m "docs: smoke checklist + README"
```

---

## Self-Review

**Spec coverage:**
- Wails + single binary → Tasks 1, 16. ✓
- Copy RAG behind adapter, copied code untouched → Tasks 2, 8, 9 (scope filtering in adapter, REUSED.md boundary note). ✓
- Grok-style Variant B UI → Tasks 17, 18. ✓
- Per-message model picker, OpenAI + Anthropic → Tasks 10–13, 18. ✓
- Persistent history, sticky model/preset/scope → Tasks 6, 15, 18 (`SetConversationMeta`, restore on open). ✓
- System-prompt presets → Tasks 5, 15, 18. ✓
- Textbook config dir, none/one/many, chapters → Tasks 7, 9, 19. ✓
- Streaming + Stop → Tasks 11, 12, 14 (interrupt → partial persisted), 18. ✓
- Cost controls (prompt caching, context budget, top-K) → Task 12 (`CacheControl`), Task 9 (budget), Task 3/9 (top-K). ✓
- .env keys → Task 3. ✓
- Error handling normalized at boundary, RAG never silent → Tasks 13, 14, 15. ✓
- Startup validation → Task 15. ✓
- Two separate SQLite DBs → Tasks 4 (app), 8 (rag), 16 (distinct paths). ✓
- Testing strategy (copied tests retained, adapter/provider/store/chat tests, TDD, manual frontend smoke) → Tasks 2, 8, 9, 11–14, 20. ✓
- Deferred items (visual polish, cross-platform, shared module, local embeddings, rename UI, UI automation) → correctly NOT implemented. ✓

**Placeholder scan:** One intentional dead-code block in Task 18 is explicitly called out with an immediate removal step (Step 2) — not a latent placeholder. SDK accessor caveats in Tasks 11/12 point to `go doc` with behavior-asserting tests as the contract. No "TBD"/"implement later".

**Type consistency:** `provider.AppError`, `provider.NormalizeError`, `ChatRequest.CachedPrefix`, `rag.ScopeFilter`, `rag.Adapter.Retrieve`, `store.TextbookScope`, `chat.Retriever` signatures are consistent across Tasks 9–19. `ragRetriever` adapts `rag.Adapter` → `chat.Retriever` correctly. `booksToIndex` defined (Task 19) before use.
