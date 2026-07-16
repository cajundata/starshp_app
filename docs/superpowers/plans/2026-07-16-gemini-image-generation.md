# Gemini Image Generation (Spec B) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An image-model persona (Nano Banana 2 / `gemini-3-pro-image`) whose replies render as images in the chat thread, stored as content-hash PNGs, refinable across turns, and visible to text personas via textual baton blocks.

**Architecture:** Images are a first-class `assistant_image` event kind flowing store → provider replay → chat engine → sink → frontend. A new `internal/imagestore` package owns content-addressed PNG files under `<app-dir>/images/` and serves them at `/appimages/<hash>.png` through the Wails asset-server handler. The gemini adapter gains an image mode (responseModalities TEXT+IMAGE, tools omitted); the chat engine persists interleaved text/image events, inflates the newest 6 own-persona images back into provider context, and textualizes foreign images in baton/pin blocks.

**Tech Stack:** Go (Wails v2.13), `google.golang.org/genai` v1.63.0, `modernc.org/sqlite`, vanilla-TS frontend (no framework, no test runner — frontend verification is `wails build` + smoke).

**Spec:** `docs/superpowers/specs/2026-07-16-gemini-image-generation-design.md`

## Global Constraints

- NEVER modify anything under `internal/rag/{chunker,embedding,ragindex}/` — verbatim copies of acctutor; their gofmt drift is permanent.
- After any `wails build`/generate that rewrites `frontend/wailsjs/go/*`: `chmod 644` those files before `git add` (the regenerator flips them to 755).
- Refinement cap is the package constant `maxInlineImages = 6` in `internal/chat` — not operator config.
- Exact placeholder text (adapter, for an image without bytes): `[earlier image omitted]`
- Exact baton line for a foreign image: `[image — generated from: "<triggering user prompt, truncated to 120 runes>"]`
- Image URL path: `/appimages/<hash>.png` where `<hash>` is exactly 64 lowercase hex chars. Content type `image/png`. PNG is assumed everywhere; mime is not persisted.
- New event kind string: `assistant_image`. New sink event name: `chat:image` (payload field `hash`).
- Every task ends with `go test ./...` green. Do not commit with failing tests.
- Commit messages follow repo style (`feat(scope): …`, `docs(scope): …`), ending with the Claude co-author trailer.

---

### Task 1: `internal/imagestore` — content-addressed PNG store + HTTP handler

**Files:**
- Create: `internal/imagestore/imagestore.go`
- Test: `internal/imagestore/imagestore_test.go`

**Interfaces:**
- Consumes: nothing (stdlib only).
- Produces: `New(dir string) (*Store, error)`, `(*Store) Put(data []byte) (hash string, err error)`, `(*Store) Read(hash string) ([]byte, error)` (missing file → error wrapping `fs.ErrNotExist`), `(*Store) Handler() http.Handler` serving `/appimages/<hash>.png`. Task 4 consumes Put/Read via an interface; Task 6 consumes New/Handler.

- [ ] **Step 1: Write the failing tests**

```go
package imagestore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "images"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPutReadRoundTrip(t *testing.T) {
	s := newStore(t)
	data := []byte("fake-png-bytes")
	hash, err := s.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if want := hex.EncodeToString(sum[:]); hash != want {
		t.Fatalf("hash = %q, want %q", hash, want)
	}
	got, err := s.Read(hash)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("Read = %q, want %q", got, data)
	}
}

func TestPutIsIdempotent(t *testing.T) {
	s := newStore(t)
	data := []byte("same-bytes")
	h1, err := s.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := s.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hashes differ: %q vs %q", h1, h2)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want 1", len(entries))
	}
}

func TestReadMissingHashIsNotExist(t *testing.T) {
	s := newStore(t)
	_, err := s.Read("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestReadRejectsInvalidHash(t *testing.T) {
	s := newStore(t)
	for _, bad := range []string{"", "abc", "../../etc/passwd", "ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"} {
		if _, err := s.Read(bad); err == nil {
			t.Fatalf("Read(%q) succeeded, want error", bad)
		}
	}
}

func TestHandlerServesStoredImage(t *testing.T) {
	s := newStore(t)
	hash, _ := s.Put([]byte("png-payload"))
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/appimages/" + hash + ".png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "png-payload" {
		t.Fatalf("body = %q", body)
	}
}

func TestHandler404s(t *testing.T) {
	s := newStore(t)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	for _, path := range []string{
		"/appimages/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.png", // unknown hash
		"/appimages/notahash.png",       // malformed hash
		"/appimages/../app.db",          // traversal shape
		"/somewhere/else.png",           // wrong prefix
		"/appimages/",                   // empty
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", path, resp.StatusCode)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/imagestore/`
Expected: FAIL — package does not exist / `New` undefined.

- [ ] **Step 3: Write the implementation**

```go
// Package imagestore persists model-generated images as content-addressed
// PNG files (<sha256-hex>.png) under one directory, and serves them to the
// frontend at /appimages/<hash>.png via the Wails asset-server handler.
package imagestore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Store struct{ dir string }

// New returns a store rooted at dir, creating the directory if absent.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("imagestore: %w", err)
	}
	return &Store{dir: dir}, nil
}

var hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Put writes data as <sha256-hex>.png and returns the hash. Identical content
// dedupes to the existing file. The write goes through a temp file + rename so
// a crash never leaves a torn file under a valid hash name.
func (s *Store) Put(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	path := s.path(hash)
	if _, err := os.Stat(path); err == nil {
		return hash, nil
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("imagestore: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("imagestore: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("imagestore: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("imagestore: %w", err)
	}
	return hash, nil
}

// Read returns the stored bytes for hash. A missing file surfaces as an error
// wrapping fs.ErrNotExist (deleted-image callers degrade to a placeholder).
func (s *Store) Read(hash string) ([]byte, error) {
	if !hashRE.MatchString(hash) {
		return nil, fmt.Errorf("imagestore: invalid hash %q", hash)
	}
	return os.ReadFile(s.path(hash))
}

func (s *Store) path(hash string) string { return filepath.Join(s.dir, hash+".png") }

// Handler serves stored images at /appimages/<hash>.png. The hash segment must
// be exactly 64 lowercase hex chars — traversal is rejected by construction.
// Everything else is 404.
func (s *Store) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutPrefix(r.URL.Path, "/appimages/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		hash, ok := strings.CutSuffix(name, ".png")
		if !ok || !hashRE.MatchString(hash) {
			http.NotFound(w, r)
			return
		}
		data, err := s.Read(hash)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(data)
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/imagestore/`
Expected: PASS (6 tests).

- [ ] **Step 5: Full suite + commit**

Run: `go test ./...`
Expected: PASS.

```bash
git add internal/imagestore/
git commit -m "feat(imagestore): content-addressed PNG store with /appimages handler

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: Store — `assistant_image` kind, `image_hash` column, table-rebuild migration, replay

**Files:**
- Modify: `internal/store/schema.go` (conversation_events CHECK + column)
- Modify: `internal/store/events.go` (const, struct field, `AppendAssistantImage`)
- Modify: `internal/store/migrate.go` (table rebuild for legacy DBs)
- Modify: `internal/store/replay.go` (SELECT + scan `image_hash`)
- Test: `internal/store/events_test.go`, `internal/store/migrate_test.go` (append to existing files, following their patterns)

**Interfaces:**
- Consumes: nothing new.
- Produces: `store.EventKindAssistantImage = "assistant_image"`, `ConversationEvent.ImageHash string` (JSON `imageHash,omitempty`), `(*Store) AppendAssistantImage(convID, turnID, runID, imageHash string) (ConversationEvent, error)`. Both `GetProviderReplayEvents` and `GetConversationDisplayEvents` return the kind with `ImageHash` populated. Tasks 4 and 5 consume all of these.

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/events_test.go` (use that file's existing helper for opening a test store — it will have an `openStore(t)`-style helper; match it):

```go
func TestAppendAssistantImageAndReplay(t *testing.T) {
	s := openTestStore(t) // match the existing test helper name in this file
	conv, err := s.CreateConversation("c")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.AppendUserMessage(conv.ID, "draw a cat")
	if err != nil {
		t.Fatal(err)
	}
	runID := "run-img-1"
	if err := s.CreateRun(conv.ID, u.TurnID, runID, "gemini", "gemini-3-pro-image", "auto_grounded_default", "artist"); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("ab", 32) // 64 hex chars
	ev, err := s.AppendAssistantImage(conv.ID, u.TurnID, runID, hash)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != EventKindAssistantImage || ev.ImageHash != hash {
		t.Fatalf("event = %+v", ev)
	}
	if err := s.CompleteRun(runID, RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}

	disp, err := s.GetConversationDisplayEvents(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(disp) != 2 || disp[1].Kind != EventKindAssistantImage || disp[1].ImageHash != hash {
		t.Fatalf("display events = %+v", disp)
	}
	prov, err := s.GetProviderReplayEvents(conv.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(prov) != 2 || prov[1].Kind != EventKindAssistantImage || prov[1].ImageHash != hash {
		t.Fatalf("provider events = %+v", prov)
	}
}
```

Append to `internal/store/migrate_test.go`:

```go
// TestMigrateAddsImageHashToLegacyEventsTable simulates a database created
// before Spec B: conversation_events exists with the four-kind CHECK and no
// image_hash column. Open must rebuild the table, preserve rows, and accept
// the new kind afterward.
func TestMigrateAddsImageHashToLegacyEventsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(wal)&_pragma=foreign_keys(on)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	legacy := `
CREATE TABLE conversations (
  id TEXT PRIMARY KEY, title TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  pinned_model TEXT, pinned_persona TEXT,
  retrieval_mode TEXT NOT NULL DEFAULT 'auto_grounded_default'
);
CREATE TABLE conversation_events (
  id              TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  turn_id         TEXT NOT NULL,
  run_id          TEXT,
  sequence_index  INTEGER NOT NULL,
  kind            TEXT NOT NULL CHECK (kind IN (
                      'user_message','assistant_text',
                      'assistant_tool_call','tool_result')),
  text            TEXT,
  tool_call_id    TEXT,
  tool_name       TEXT,
  tool_input      TEXT,
  tool_metadata   TEXT,
  tool_result_hash TEXT,
  tool_latency_ms INTEGER,
  is_error        INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL
);`
	if _, err := db.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO conversations (id, title, created_at, updated_at) VALUES ('c1','t',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO conversation_events (id, conversation_id, turn_id, sequence_index, kind, text, created_at)
		 VALUES ('e1','c1','e1',0,'user_message','hello',1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on legacy db: %v", err)
	}
	defer s.Close()

	// The legacy row survived.
	evs, err := s.GetConversationDisplayEvents("c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Text != "hello" {
		t.Fatalf("legacy rows lost: %+v", evs)
	}
	// The new kind is accepted (the old CHECK would reject it).
	if err := s.CreateRun("c1", "e1", "r1", "gemini", "m", "auto_grounded_default", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendAssistantImage("c1", "e1", "r1", strings.Repeat("ab", 32)); err != nil {
		t.Fatalf("AppendAssistantImage on migrated db: %v", err)
	}
}
```

Add any missing imports (`database/sql`, `fmt`, `path/filepath`, `strings`) to the test files.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -run 'AssistantImage|ImageHash' -v`
Expected: FAIL — `EventKindAssistantImage` undefined / `AppendAssistantImage` undefined.

- [ ] **Step 3: Implement**

`schema.go` — widen the CHECK and add the column (this is what fresh databases get):

```sql
  kind            TEXT NOT NULL CHECK (kind IN (
                      'user_message','assistant_text',
                      'assistant_tool_call','tool_result',
                      'assistant_image')),
```

and after `tool_latency_ms INTEGER,` add:

```sql
  image_hash      TEXT,
```

`events.go` — add to the const block:

```go
	EventKindAssistantImage    = "assistant_image"
```

Add to `ConversationEvent` (after `ToolLatencyMs`):

```go
	ImageHash       string          `json:"imageHash,omitempty"`
```

Add the append function:

```go
// AppendAssistantImage persists one generated image the model emitted, by
// content hash. The bytes live in the imagestore (<app-dir>/images/); the
// event log carries only the reference, so a deleted file degrades to a
// placeholder rather than corrupting replay.
func (s *Store) AppendAssistantImage(convID, turnID, runID, imageHash string) (ConversationEvent, error) {
	id := uuid.NewString()
	seq, err := nextSequenceIndex(s, convID)
	if err != nil {
		return ConversationEvent{}, err
	}
	ev := ConversationEvent{
		ID: id, ConversationID: convID, TurnID: turnID, RunID: runID,
		SequenceIndex: seq, Kind: EventKindAssistantImage, ImageHash: imageHash,
		CreatedAt: time.Now().UnixMilli(),
	}
	_, err = s.db.Exec(
		`INSERT INTO conversation_events
            (id, conversation_id, turn_id, run_id, sequence_index, kind,
             image_hash, is_error, created_at)
         VALUES (?,?,?,?,?,?,?,0,?)`,
		ev.ID, convID, turnID, runID, ev.SequenceIndex, ev.Kind, imageHash, ev.CreatedAt)
	return ev, err
}
```

`migrate.go` — add the rebuild, called from `migrate()` immediately before `migrateMessagesToEvents(db)`:

```go
	if err := migrateEventsImageHash(db); err != nil {
		return err
	}
```

```go
// migrateEventsImageHash rebuilds conversation_events for databases created
// before assistant_image existed. SQLite cannot alter a CHECK constraint, so
// this follows the documented table-rebuild recipe: create-new, copy, drop,
// rename, re-index — in one transaction. image_hash doubles as the marker:
// when the column exists (fresh DBs get it from schemaSQL), nothing runs.
// No other table references conversation_events, so drop+rename is FK-safe.
func migrateEventsImageHash(db *sql.DB) error {
	has, err := columnExists(db, "conversation_events", "image_hash")
	if err != nil || has {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmts := []string{
		`CREATE TABLE conversation_events_new (
  id              TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  turn_id         TEXT NOT NULL,
  run_id          TEXT,
  sequence_index  INTEGER NOT NULL,
  kind            TEXT NOT NULL CHECK (kind IN (
                      'user_message','assistant_text',
                      'assistant_tool_call','tool_result',
                      'assistant_image')),
  text            TEXT,
  tool_call_id    TEXT,
  tool_name       TEXT,
  tool_input      TEXT,
  tool_metadata   TEXT,
  tool_result_hash TEXT,
  tool_latency_ms INTEGER,
  image_hash      TEXT,
  is_error        INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL
)`,
		`INSERT INTO conversation_events_new
            (id, conversation_id, turn_id, run_id, sequence_index, kind, text,
             tool_call_id, tool_name, tool_input, tool_metadata,
             tool_result_hash, tool_latency_ms, image_hash, is_error, created_at)
         SELECT id, conversation_id, turn_id, run_id, sequence_index, kind, text,
             tool_call_id, tool_name, tool_input, tool_metadata,
             tool_result_hash, tool_latency_ms, NULL, is_error, created_at
           FROM conversation_events`,
		`DROP TABLE conversation_events`,
		`ALTER TABLE conversation_events_new RENAME TO conversation_events`,
		`CREATE INDEX IF NOT EXISTS conversation_events_conv_seq
  ON conversation_events(conversation_id, sequence_index)`,
		`CREATE INDEX IF NOT EXISTS conversation_events_turn
  ON conversation_events(turn_id)`,
		`CREATE INDEX IF NOT EXISTS conversation_events_run
  ON conversation_events(run_id)`,
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
```

The rebuilt definition MUST stay byte-equivalent in meaning to the `schema.go` definition — if you touch one, touch both.

`replay.go` — in `eventsForRunsPlusUserMessages`, extend the SELECT column list: after `COALESCE(e.tool_result_hash,''),` add `COALESCE(e.image_hash,''),` and scan it: after `&ev.ToolResultHash,` add `&ev.ImageHash,` (order must match the SELECT).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS, including both new tests and all existing store tests.

- [ ] **Step 5: Full suite + commit**

Run: `go test ./...`
Expected: PASS.

```bash
git add internal/store/
git commit -m "feat(store): assistant_image event kind with image_hash column and legacy-table rebuild

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: Provider — `Delta.Image`, `Event` image fields, gemini image mode

**Files:**
- Modify: `internal/provider/provider.go` (ImageBlob, Delta.Image, Event.ImageHash/ImageData)
- Modify: `internal/provider/registry.go` (`OutputsImage`, comment refresh)
- Modify: `internal/provider/gemini.go` (imageOutput mode: responseModalities, tools omitted, InlineData case, contents mapping)
- Modify: `internal/provider/factory.go` (pass image mode to `NewGemini`)
- Test: `internal/provider/gemini_test.go`, `internal/provider/registry_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `provider.ImageBlob{MIME string; Data []byte}`, `Delta.Image *ImageBlob`, `Event.ImageHash string` + `Event.ImageData []byte`, `ModelInfo.OutputsImage() bool`, `NewGemini(apiKey, baseURL string, imageOutput bool) ChatProvider` (signature change — update ALL call sites). Task 4 consumes Delta.Image and the Event fields.

- [ ] **Step 1: Write the failing tests**

Append to `internal/provider/gemini_test.go`:

```go
func TestGeminiStreamImageDelta(t *testing.T) {
	// "aGVsbG8=" is base64 for "hello" — stands in for PNG bytes.
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Here you go:"}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"One cat."}]},"finishReason":"STOP"}]}`,
	}, nil)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL, true)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model:  "gemini-3-pro-image",
		Events: []Event{{Kind: "user_message", Text: "draw a cat"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var order []string
	var img *ImageBlob
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("delta err: %v", d.Err)
		}
		if d.Text != "" {
			order = append(order, "text")
		}
		if d.Image != nil {
			order = append(order, "image")
			img = d.Image
		}
	}
	if want := []string{"text", "image", "text"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("delta order = %v, want %v", order, want)
	}
	if img == nil || img.MIME != "image/png" || string(img.Data) != "hello" {
		t.Fatalf("image = %+v, want mime image/png data 'hello'", img)
	}
}

func TestGeminiImageModeSetsModalitiesAndOmitsTools(t *testing.T) {
	var body []byte
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`,
	}, &body)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL, true)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model:  "gemini-3-pro-image",
		Tools:  []ToolDef{{Name: "safe_math", Description: "evaluate", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Events: []Event{{Kind: "user_message", Text: "draw"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}
	s := string(body)
	if !strings.Contains(s, `"responseModalities":["TEXT","IMAGE"]`) {
		t.Fatalf("request lacks responseModalities: %s", s)
	}
	if strings.Contains(s, "functionDeclarations") {
		t.Fatalf("image mode must omit tools: %s", s)
	}
}

func TestGeminiTextModeOmitsModalities(t *testing.T) {
	var body []byte
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`,
	}, &body)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL, false)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model:  "gemini-3-pro",
		Events: []Event{{Kind: "user_message", Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}
	if strings.Contains(string(body), "responseModalities") {
		t.Fatalf("text mode must not set responseModalities: %s", body)
	}
}

func TestGeminiSafetyFinishReasonErrors(t *testing.T) {
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]},"finishReason":"IMAGE_SAFETY"}]}`,
	}, nil)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL, true)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model:  "gemini-3-pro-image",
		Events: []Event{{Kind: "user_message", Text: "draw"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var final Delta
	for d := range ch {
		if d.Done {
			final = d
		}
	}
	if final.StopReason != "error" || final.Err == nil ||
		!strings.Contains(final.Err.Error(), "IMAGE_SAFETY") {
		t.Fatalf("final = %+v, want error stop carrying IMAGE_SAFETY", final)
	}
}

func TestGeminiContentsAssistantImage(t *testing.T) {
	events := []Event{
		{Kind: "user_message", Text: "draw a cat"},
		{Kind: "assistant_image", ImageHash: strings.Repeat("a", 64), ImageData: []byte{1, 2, 3}},
		{Kind: "assistant_image", ImageHash: strings.Repeat("b", 64)}, // beyond cap / deleted: no bytes
		{Kind: "user_message", Text: "make the sky darker"},
	}
	got := geminiContentsFromEvents(events)
	// user / model(inlineData + placeholder text) / user
	if len(got) != 3 {
		t.Fatalf("len(contents) = %d, want 3: %+v", len(got), got)
	}
	if got[1].Role != genai.RoleModel || len(got[1].Parts) != 2 {
		t.Fatalf("contents[1] = %+v, want model with 2 parts", got[1])
	}
	blob := got[1].Parts[0].InlineData
	if blob == nil || blob.MIMEType != "image/png" || len(blob.Data) != 3 {
		t.Fatalf("inline part = %+v, want image/png with 3 bytes", got[1].Parts[0])
	}
	if got[1].Parts[1].Text != "[earlier image omitted]" {
		t.Fatalf("placeholder = %q, want '[earlier image omitted]'", got[1].Parts[1].Text)
	}
}
```

Add `"reflect"` to the test file's imports.

Append to `internal/provider/registry_test.go`:

```go
func TestOutputsImage(t *testing.T) {
	if (ModelInfo{OutputModalities: []string{"text"}}).OutputsImage() {
		t.Fatal("text-only model reports image output")
	}
	if !(ModelInfo{OutputModalities: []string{"text", "image"}}).OutputsImage() {
		t.Fatal("text+image model does not report image output")
	}
	if (ModelInfo{}).OutputsImage() {
		t.Fatal("empty modalities must not report image output")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/provider/ -run 'GeminiStreamImage|GeminiImageMode|GeminiTextMode|GeminiSafety|GeminiContentsAssistantImage|OutputsImage' -v`
Expected: FAIL — compile errors (`NewGemini` arity, `ImageBlob` undefined, `OutputsImage` undefined).

- [ ] **Step 3: Implement**

`provider.go` — extend `Event` (append fields + update the Kind comment):

```go
	Kind         string          // user_message | assistant_text | assistant_tool_call | tool_result | assistant_image
	...existing fields unchanged...
	ImageHash string // assistant_image: content hash of the stored PNG
	ImageData []byte // assistant_image: bytes inflated by the chat engine for provider replay; transient, never persisted
```

Add above `Delta`:

```go
// ImageBlob is one generated image emitted mid-stream by an image-capable
// provider. Data is the raw (already base64-decoded) file bytes.
type ImageBlob struct {
	MIME string
	Data []byte
}
```

Extend `Delta`:

```go
type Delta struct {
	Text       string
	ToolCall   *ToolCall
	Image      *ImageBlob
	StopReason string
	Done       bool
	Err        error
	Usage      *Usage
}
```

`registry.go` — add after `ByID`, and update the `InputModalities`/`OutputModalities` doc comment (drop the "not yet wired into the app" clause; image output is now rendered via the gemini adapter):

```go
// OutputsImage reports whether the model declares "image" among its output
// modalities. Drives the gemini adapter's image mode and the appapi persona
// gate.
func (m ModelInfo) OutputsImage() bool {
	for _, v := range m.OutputModalities {
		if v == "image" {
			return true
		}
	}
	return false
}
```

`gemini.go` — struct, constructor, config, part switch, contents:

```go
type geminiProvider struct {
	apiKey      string
	baseURL     string
	imageOutput bool
}

// NewGemini builds a Gemini provider. baseURL may be empty for the default
// endpoint (tests pass an httptest URL). imageOutput selects image mode:
// responseModalities TEXT+IMAGE and no function tools (the API rejects tools
// alongside image output), for models whose registry entry outputs image.
func NewGemini(apiKey, baseURL string, imageOutput bool) ChatProvider {
	return &geminiProvider{apiKey: apiKey, baseURL: baseURL, imageOutput: imageOutput}
}
```

Replace the tools block in `Stream`:

```go
	if p.imageOutput {
		cfg.ResponseModalities = []string{"TEXT", "IMAGE"}
	} else if tools := buildGeminiTools(req.Tools); len(tools) > 0 {
		cfg.Tools = tools
	}
```

In the part switch (after the `part.FunctionCall != nil` case, before the text case):

```go
					case part.InlineData != nil:
						select {
						case out <- Delta{Image: &ImageBlob{MIME: part.InlineData.MIMEType, Data: part.InlineData.Data}}:
						case <-ctx.Done():
							return
						}
```

Carry non-STOP finish reasons into the terminal frame (today a `SAFETY`-class stop completes the run silently; the spec requires the reason in the message, with no new AppError code). In the goroutine's `var (...)` block add `finishErr error`; change the finish-reason switch's default case and the final frame:

```go
				default:
					stopReason = "error"
					// SAFETY / IMAGE_SAFETY / PROHIBITED_CONTENT etc. — carry the
					// reason so the run error says why generation stopped.
					finishErr = fmt.Errorf("gemini: generation stopped: %s", cand.FinishReason)
```

```go
		final := Delta{Done: true, StopReason: stopReason}
		if finishErr != nil {
			final.Err = finishErr
		}
		if haveUsage {
			u := usage
			final.Usage = &u
		}
		out <- final
```

In `geminiContentsFromEvents`, add a case (after `assistant_text`):

```go
		case "assistant_image":
			// Inflated bytes replay inline so refinement edits the actual image;
			// an event without bytes (beyond the cap, or file deleted) degrades
			// to a placeholder the model can still anchor ordering on.
			if len(e.ImageData) > 0 {
				appendPart(genai.RoleModel, genai.NewPartFromBytes(e.ImageData, "image/png"))
			} else {
				appendPart(genai.RoleModel, genai.NewPartFromText("[earlier image omitted]"))
			}
```

`factory.go` — the gemini case becomes:

```go
	case "gemini":
		if keys.Gemini == "" {
			return nil, AppError{"auth", "Gemini API key not set.", false}
		}
		return NewGemini(keys.Gemini, "", m.OutputsImage()), nil
```

Update every existing `NewGemini("test-key", srv.URL)` call in `gemini_test.go` to `NewGemini("test-key", srv.URL, false)`.

Note: the anthropic and openai event mappers need NO change — both structurally skip unknown kinds (`openaiMessagesFromEvents` has a `default: i++`; `anthropicMessagesFromEvents`'s switch has no matching case), and the chat engine only ever hands them textualized foreign images anyway.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/provider/ -v`
Expected: PASS — all new tests plus every pre-existing gemini/factory/registry test.

- [ ] **Step 5: Full suite + commit**

Run: `go test ./...`
Expected: PASS (chat/appapi don't call NewGemini directly; the factory covers them).

```bash
git add internal/provider/
git commit -m "feat(provider): gemini image mode — Delta.Image, inline-data replay, responseModalities

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: Chat engine — persist interleaved images, refinement inflation, baton textualization, `chat:image` sink kind

**Files:**
- Modify: `internal/chat/chat.go`
- Test: `internal/chat/chat_test.go`, `internal/chat/canonical_events_test.go`

**Interfaces:**
- Consumes: `store.EventKindAssistantImage`, `(*Store).AppendAssistantImage` (Task 2); `provider.Delta.Image`, `provider.Event.ImageHash/ImageData` (Task 3).
- Produces: `chat.ImageStore` interface (`Put([]byte) (string, error)`; `Read(string) ([]byte, error)`), `SendParams.Images ImageStore`, `SinkImage SinkEventKind = "image"` (payload `{"hash": <string>}`), `maxInlineImages = 6`, `inflateImages(events []provider.Event, images ImageStore, max int)`. Task 5 consumes SinkImage and SendParams.Images.

- [ ] **Step 1: Write the failing engine test**

Append to `internal/chat/chat_test.go`:

```go
// fakeImages is an in-memory ImageStore matching imagestore semantics.
type fakeImages struct{ files map[string][]byte }

func newFakeImages() *fakeImages { return &fakeImages{files: map[string][]byte{}} }

func (f *fakeImages) Put(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	f.files[h] = data
	return h, nil
}

func (f *fakeImages) Read(hash string) ([]byte, error) {
	b, ok := f.files[hash]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return b, nil
}

func TestSend_ImageDeltas_PersistInterleavedAndEmitSink(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	sink := &captureSink{}
	images := newFakeImages()

	prov := &scriptedProvider{iterations: [][]provider.Delta{{
		{Text: "Two options:"},
		{Image: &provider.ImageBlob{MIME: "image/png", Data: []byte("png-1")}},
		{Text: "and a variant:"},
		{Image: &provider.ImageBlob{MIME: "image/png", Data: []byte("png-2")}},
		{Done: true, StopReason: "end_turn", Usage: &provider.Usage{InputTokens: 5, OutputTokens: 9}},
	}}}

	res, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID,
		UserText:       "draw a cat",
		Model:          "gemini-3-pro-image",
		Provider:       prov,
		Registry:       tools.NewRegistry(time.Second),
		Resolver:       emptyResolver{},
		RetrievalMode:  RetrievalAutoGroundedDefault,
		Sink:           sink,
		Images:         images,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.TerminalReason != "end_turn" {
		t.Fatalf("terminal = %q", res.TerminalReason)
	}

	evs, _ := st.GetConversationDisplayEvents(conv.ID)
	kinds := make([]string, len(evs))
	for i, e := range evs {
		kinds[i] = e.Kind
	}
	want := []string{
		store.EventKindUserMessage,
		store.EventKindAssistantText,  // "Two options:"
		store.EventKindAssistantImage, // png-1
		store.EventKindAssistantText,  // "and a variant:"
		store.EventKindAssistantImage, // png-2
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("event kinds = %v, want %v", kinds, want)
	}
	if evs[2].ImageHash == "" || evs[4].ImageHash == "" || evs[2].ImageHash == evs[4].ImageHash {
		t.Fatalf("image hashes wrong: %q, %q", evs[2].ImageHash, evs[4].ImageHash)
	}
	if _, err := images.Read(evs[2].ImageHash); err != nil {
		t.Fatalf("first image not in store: %v", err)
	}

	var imageSinks []SinkEvent
	for _, e := range sink.events {
		if e.Kind == SinkImage {
			imageSinks = append(imageSinks, e)
		}
	}
	if len(imageSinks) != 2 {
		t.Fatalf("got %d image sink events, want 2", len(imageSinks))
	}
	if h, _ := imageSinks[0].Payload["hash"].(string); h != evs[2].ImageHash {
		t.Fatalf("sink hash = %q, want %q", h, evs[2].ImageHash)
	}
}

func TestSend_ImageDeltaWithoutStore_ErrorsRun(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	sink := &captureSink{}
	prov := &scriptedProvider{iterations: [][]provider.Delta{{
		{Image: &provider.ImageBlob{MIME: "image/png", Data: []byte("png")}},
		{Done: true, StopReason: "end_turn"},
	}}}
	res, _ := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID, UserText: "draw", Model: "m",
		Provider: prov, Registry: tools.NewRegistry(time.Second),
		Resolver: emptyResolver{}, RetrievalMode: RetrievalAutoGroundedDefault,
		Sink: sink, // Images deliberately nil
	}, nil)
	if res.TerminalReason != "provider_error" {
		t.Fatalf("terminal = %q, want provider_error", res.TerminalReason)
	}
}
```

Add missing imports to `chat_test.go`: `"crypto/sha256"`, `"encoding/hex"`, `"io/fs"`, `"reflect"`.

- [ ] **Step 2: Write the failing canonical-events / inflation tests**

Append to `internal/chat/canonical_events_test.go`:

```go
// imageTurn persists a completed turn whose run emitted images (with optional
// interleaved text before each image is not modeled here — hashes only).
func imageTurn(t *testing.T, st *store.Store, convID, userText, personaID, model string, hashes ...string) string {
	t.Helper()
	u, err := st.AppendUserMessage(convID, userText)
	if err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	if err := st.CreateRun(convID, u.TurnID, runID, "gemini", model, "auto_grounded_default", personaID); err != nil {
		t.Fatal(err)
	}
	for _, h := range hashes {
		if _, err := st.AppendAssistantImage(convID, u.TurnID, runID, h); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CompleteRun(runID, store.RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}
	return u.TurnID
}

func TestCanonicalEvents_OwnImageKeepsHash(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	imageTurn(t, st, conv.ID, "draw a cat", "artist", "gemini-3-pro-image", strings.Repeat("aa", 32))
	turnID, runID := currentTurn(t, st, conv.ID, "make the sky darker", "artist", "gemini-3-pro-image")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "artist", nil)
	// user / assistant_image / user — the artist keeps its own image event.
	if len(got) != 3 {
		t.Fatalf("events = %+v, want 3", got)
	}
	if got[1].Kind != store.EventKindAssistantImage || got[1].ImageHash != strings.Repeat("aa", 32) {
		t.Fatalf("own image event = %+v", got[1])
	}
}

func TestCanonicalEvents_ForeignImageTextualizesInBaton(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	prompt := "draw a cat sitting on " + strings.Repeat("a very long fence ", 20) // > 120 runes
	imageTurn(t, st, conv.ID, prompt, "artist", "gemini-3-pro-image", strings.Repeat("aa", 32))
	turnID, runID := currentTurn(t, st, conv.ID, "what do you think of it?", "copywriter", "claude-x")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "copywriter", nil)
	// user(prompt) / attributed baton block / user(current) — no image event survives.
	for _, e := range got {
		if e.Kind == store.EventKindAssistantImage {
			t.Fatalf("foreign image leaked as event: %+v", e)
		}
	}
	var baton string
	for _, e := range got {
		if strings.HasPrefix(e.Text, "From artist (gemini-3-pro-image):") {
			baton = e.Text
		}
	}
	if baton == "" {
		t.Fatalf("no attributed baton block in %+v", got)
	}
	if !strings.Contains(baton, `[image — generated from: "`) {
		t.Fatalf("baton lacks image line: %q", baton)
	}
	if !strings.Contains(baton, "…") {
		t.Fatalf("prompt not truncated at 120 runes: %q", baton)
	}
	if strings.Contains(baton, strings.Repeat("aa", 32)) {
		t.Fatalf("baton leaks raw hash: %q", baton)
	}
}

func TestInflateImages_CapNewestAndSkipMissing(t *testing.T) {
	images := newFakeImages()
	var events []provider.Event
	var hashes []string
	for i := 0; i < 8; i++ {
		h, _ := images.Put([]byte{byte(i)})
		hashes = append(hashes, h)
		events = append(events, provider.Event{Kind: store.EventKindAssistantImage, ImageHash: h})
	}
	// Delete the newest image's file: it must not consume a cap slot.
	delete(images.files, hashes[7])

	inflateImages(events, images, 6)

	if len(events[7].ImageData) != 0 {
		t.Fatal("deleted image inflated")
	}
	// Newest 6 readable images (indexes 6,5,4,3,2,1) carry bytes; 0 does not.
	for _, i := range []int{6, 5, 4, 3, 2, 1} {
		if len(events[i].ImageData) == 0 {
			t.Fatalf("events[%d] not inflated", i)
		}
	}
	if len(events[0].ImageData) != 0 {
		t.Fatal("event beyond cap inflated")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/chat/ -run 'ImageDelta|OwnImage|ForeignImage|InflateImages' -v`
Expected: FAIL — `SendParams.Images` undefined, `SinkImage` undefined, `inflateImages` undefined.

- [ ] **Step 4: Implement in `chat.go`**

Add the sink kind to the const block:

```go
	SinkImage          SinkEventKind = "image"
```

Add below the `Retriever` interface:

```go
// ImageStore persists generated images by content hash; implemented by
// imagestore.Store. Nil is legal — an image delta then errors the run with a
// clear message instead of panicking.
type ImageStore interface {
	Put(data []byte) (string, error)
	Read(hash string) ([]byte, error)
}

// maxInlineImages caps how many of the persona's own prior images ride back
// into provider context as inline bytes (newest first). Gemini's inline
// request payload tops out around 20 MB and each PNG runs 1–2 MB; older
// images degrade to a textual placeholder instead of hard-failing the call.
const maxInlineImages = 6
```

Add to `SendParams` (after `Retriever`):

```go
	Images ImageStore // image persistence for image-output models; may be nil
```

In `runLoop`, replace the stream-drain block (the `var (text strings.Builder …)` declaration through the post-drain `AppendAssistantText`) with a segment-flushing version. The full replacement for that region:

```go
		var (
			text       strings.Builder
			toolCalls  []*provider.ToolCall
			stopReason string
			streamErr  error
			persistErr error
			persistCode string
		)
		// flushText persists the accumulated text segment, if any. Called when
		// an image lands (so the event log preserves text/image interleaving)
		// and once after the stream drains.
		flushText := func() {
			if persistErr != nil {
				return
			}
			t := strings.TrimSpace(text.String())
			text.Reset()
			if t == "" {
				return
			}
			if _, err := s.st.AppendAssistantText(p.ConversationID, turnID, runID, t); err != nil {
				persistErr, persistCode = err, "persist_assistant_text"
			}
		}
		for d := range ch {
			if d.Err != nil {
				streamErr = d.Err
				continue
			}
			if d.Text != "" {
				text.WriteString(d.Text)
				emit(p.Sink, SinkToken, p.ConversationID, runID, turnID,
					map[string]any{"text": d.Text})
			}
			if d.Image != nil && persistErr == nil {
				flushText()
				if persistErr == nil {
					hash, err := putImage(p.Images, d.Image.Data)
					if err != nil {
						persistErr, persistCode = err, "persist_image"
					} else if _, err := s.st.AppendAssistantImage(p.ConversationID, turnID, runID, hash); err != nil {
						persistErr, persistCode = err, "persist_image"
					} else {
						emit(p.Sink, SinkImage, p.ConversationID, runID, turnID,
							map[string]any{"hash": hash})
					}
				}
			}
			if d.ToolCall != nil {
				toolCalls = append(toolCalls, d.ToolCall)
			}
			if d.Usage != nil {
				totalUsage.InputTokens += d.Usage.InputTokens
				totalUsage.OutputTokens += d.Usage.OutputTokens
				totalUsage.CachedInputTokens += d.Usage.CachedInputTokens
				lastCall = *d.Usage
			}
			if d.Done && d.StopReason != "" {
				stopReason = d.StopReason
			}
		}
		flushText()
		if persistErr != nil {
			return s.errorOut(p, runID, turnID, "provider_error", persistCode, persistErr.Error()),
				persistErr
		}
```

(The lines from `if streamErr != nil {` onward are unchanged.)

Add the helper near `errorCodeFromMetadata`:

```go
// putImage stores one generated image, guarding the nil-store case (an image
// model was invoked through a path that never wired an ImageStore).
func putImage(images ImageStore, data []byte) (string, error) {
	if images == nil {
		return "", errors.New("image store unavailable: cannot persist generated image")
	}
	return images.Put(data)
}
```

Wire inflation at both provider-call sites. In `runLoop`:

```go
		evs := canonicalEvents(events, turnID, p.PersonaID, p.Namer)
		inflateImages(evs, p.Images, maxInlineImages)
		req := provider.ChatRequest{
			Model:           p.Model,
			System:          p.SystemPrompt,
			Grounding:       grounding,
			Tools:           catalog,
			Events:          evs,
			ReasoningEffort: p.ReasoningEffort,
		}
```

And identically in `finalizeWithoutTools` (replace its `Events: canonicalEvents(...)` the same way — image models never reach it, since they get no tools, but the code must not diverge).

Add `inflateImages` after `canonicalEvents`:

```go
// inflateImages loads the newest max assistant_image events' bytes so the
// provider replays them inline (iterative refinement edits the actual image).
// Walking newest-first, an unreadable file (deleted) is skipped without
// consuming a cap slot; anything not inflated keeps empty ImageData and the
// adapter renders "[earlier image omitted]" instead. Only the current
// persona's own images reach this point — canonicalEvents already textualized
// foreign ones.
func inflateImages(events []provider.Event, images ImageStore, max int) {
	if images == nil {
		return
	}
	inflated := 0
	for i := len(events) - 1; i >= 0 && inflated < max; i-- {
		if events[i].Kind != store.EventKindAssistantImage || events[i].ImageHash == "" {
			continue
		}
		data, err := images.Read(events[i].ImageHash)
		if err != nil {
			continue
		}
		events[i].ImageData = data
		inflated++
	}
}
```

In `canonicalEvents`, three changes:

(a) Build a turn → prompt map at the top (after `predecessor := …`):

```go
	// turnPrompts lets a foreign image textualize as the prompt that produced
	// it: the image turn's own user_message.
	turnPrompts := map[string]string{}
	for _, r := range rows {
		if r.Kind == store.EventKindUserMessage {
			turnPrompts[r.TurnID] = r.Text
		}
	}
```

(b) The own-persona whitelist case carries the hash — extend the `provider.Event` literal in the `case r.Kind == store.EventKindUserMessage || !foreign:` branch:

```go
			ev := provider.Event{
				Kind: r.Kind, Text: r.Text,
				ToolCallID: r.ToolCallID, ToolName: r.ToolName,
				ToolInput: r.ToolInput, IsError: r.IsError,
				ImageHash: r.ImageHash,
			}
```

(c) Baton and pin cases accept images. Replace the two case lines and their bodies:

```go
		case r.TurnID == predecessor &&
			(r.Kind == store.EventKindAssistantText || r.Kind == store.EventKindAssistantImage):
			if len(batonTexts) == 0 {
				batonPersona, batonModel = r.PersonaID, r.Model
			}
			batonTexts = append(batonTexts, batonLine(r, turnPrompts))
		case r.ContextOverride == store.OverrideAlways &&
			(r.Kind == store.EventKindAssistantText || r.Kind == store.EventKindAssistantImage):
			if len(pinTexts) == 0 {
				pinTurn, pinPersona, pinModel = r.TurnID, r.PersonaID, r.Model
			}
			pinTexts = append(pinTexts, batonLine(r, turnPrompts))
```

Add the helpers after `predecessorTurnID`:

```go
// batonLine renders one foreign event inside an attributed block: text rides
// verbatim; an image becomes a textual description carrying the prompt that
// produced it. Raw image bytes never cross a persona boundary.
func batonLine(r store.ConversationEvent, turnPrompts map[string]string) string {
	if r.Kind != store.EventKindAssistantImage {
		return r.Text
	}
	return `[image — generated from: "` + truncateRunes(turnPrompts[r.TurnID], 120) + `"]`
}

// truncateRunes shortens s to at most n runes with an ellipsis, never
// splitting a multibyte character (summarize is byte-based; prompts are
// operator text).
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/chat/ -v`
Expected: PASS — the four new tests AND every existing test (especially `attribution_leak_test.go` and the `legacyCanonical` byte-identical checks, which must not regress: `ImageHash` is empty on all four legacy kinds, so the whitelist extension is invisible to them).

If `legacyCanonical`-based tests fail comparing structs, update `legacyCanonical` in the test file to include `ImageHash: r.ImageHash` — it remains byte-identical for legacy rows (always empty).

- [ ] **Step 6: Full suite + commit**

Run: `go test ./...`
Expected: PASS.

```bash
git add internal/chat/
git commit -m "feat(chat): interleaved image persistence, last-6 refinement inflation, image baton lines

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: appapi — sink mapping, EventDTO, persona gate relax, imagestore wiring

**Files:**
- Modify: `internal/appapi/api.go`
- Modify: `internal/config/config.go` (add `ImagesDir`)
- Test: `internal/appapi/api_test.go` (or wherever `disableNonTextOutputPersonas` / `sinkEventName` tests live — find with `grep -rn "disableNonTextOutputPersonas\|sinkEventName" internal/appapi/*_test.go`)

**Interfaces:**
- Consumes: `chat.SinkImage`, `SendParams.Images` (Task 4); `imagestore.New` (Task 1); `store.ConversationEvent.ImageHash` (Task 2); `ModelInfo.OutputsImage` (Task 3).
- Produces: `sinkEventName(chat.SinkImage) == "chat:image"`, `EventDTO.ImageHash` (JSON `imageHash,omitempty`), `disableUnrenderablePersonas` (renamed gate), `config.Config.ImagesDir`. Task 7 consumes the event name and DTO field; Task 6 consumes `ImagesDir`.

- [ ] **Step 1: Write the failing tests**

Locate the existing gate test (`grep -rn "disableNonTextOutputPersonas" internal/appapi/`) and replace it (renaming to match) with:

```go
func TestDisableUnrenderablePersonas(t *testing.T) {
	models := provider.Registry{Models: []provider.ModelInfo{
		{ID: "text-model", Provider: "openai", OutputModalities: []string{"text"}},
		{ID: "nano-banana", Provider: "gemini", OutputModalities: []string{"text", "image"}},
		{ID: "gemini-image-only", Provider: "gemini", OutputModalities: []string{"image"}},
		{ID: "openai-image-only", Provider: "openai", OutputModalities: []string{"image"}},
		{ID: "no-modalities", Provider: "anthropic"},
	}}
	reg := persona.Registry{Personas: []persona.Persona{
		{ID: "writer", Model: "text-model"},
		{ID: "artist", Model: "nano-banana"},
		{ID: "pure-artist", Model: "gemini-image-only"},
		{ID: "broken", Model: "openai-image-only"},
		{ID: "vintage", Model: "no-modalities"},
	}}

	got := disableUnrenderablePersonas(reg, models)

	kept := map[string]bool{}
	for _, p := range got.Personas {
		kept[p.ID] = true
	}
	for _, want := range []string{"writer", "artist", "pure-artist", "vintage"} {
		if !kept[want] {
			t.Errorf("persona %s should be kept; issues: %+v", want, got.Issues)
		}
	}
	if kept["broken"] {
		t.Error("persona pinned to image-only non-gemini model must be disabled")
	}
	if len(got.Issues) != 1 || got.Issues[0].File != "broken.md" {
		t.Fatalf("issues = %+v, want exactly broken.md", got.Issues)
	}
}
```

Locate the sink-name test (`grep -rn "sinkEventName" internal/appapi/*_test.go`) and add the row/case:

```go
	if got := sinkEventName(chat.SinkImage); got != "chat:image" {
		t.Errorf("sinkEventName(SinkImage) = %q, want chat:image", got)
	}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/appapi/ -run 'Unrenderable|SinkEventName|sink' -v`
Expected: FAIL — `disableUnrenderablePersonas` undefined; sink case missing.

- [ ] **Step 3: Implement**

`internal/config/config.go` — add to the `Config` struct after `PersonaDir`:

```go
	ImagesDir          string
```

and in `Load`, after the `PersonaDir:` line:

```go
		ImagesDir:          os.Getenv("IMAGES_DIR"),
```

`api.go` — struct gains the store (after `toolReg`):

```go
	images         *imagestore.Store
```

Import `"github.com/cajundata/starshp_app/internal/imagestore"`.

In `NewAPI`, after the tool-registry block:

```go
	// Image persistence for image-output models. A failure leaves images nil;
	// an image-generating run then errors with a clear message instead of the
	// app failing to start.
	if cfg.ImagesDir != "" {
		img, err := imagestore.New(cfg.ImagesDir)
		if err != nil {
			slog.Warn("imagestore: init failed", "dir", cfg.ImagesDir, "err", err)
		} else {
			a.images = img
		}
	}
```

Rename and relax the gate — replace `disableNonTextOutputPersonas` entirely:

```go
// disableUnrenderablePersonas drops any persona pinned to a model whose
// output the app cannot render. Text output renders everywhere; image output
// renders only through the gemini adapter (the only image-capable provider),
// so an image-only model on any other provider disables its personas. It is
// disabled the same way any other invalid persona is — moved out of Personas
// and reported as an Issue for the startup banner.
//
// A model with no OutputModalities at all (registries built programmatically,
// e.g. in tests, bypass LoadRegistry's normalization) is treated as
// text-capable: only an explicit non-renderable list disables a persona.
func disableUnrenderablePersonas(reg persona.Registry, models provider.Registry) persona.Registry {
	kept := make([]persona.Persona, 0, len(reg.Personas))
	issues := append([]persona.Issue(nil), reg.Issues...)
	for _, p := range reg.Personas {
		m, ok := models.ByID(p.Model)
		if !ok || len(m.OutputModalities) == 0 {
			kept = append(kept, p)
			continue
		}
		renderable := modalitiesInclude(m.OutputModalities, "text") ||
			(m.OutputsImage() && m.Provider == "gemini")
		if !renderable {
			issues = append(issues, persona.Issue{
				File: p.ID + ".md",
				Reason: fmt.Sprintf(
					"model %s output (%v) cannot be rendered: text output or gemini image output required",
					p.Model, m.OutputModalities),
			})
			continue
		}
		kept = append(kept, p)
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].File < issues[j].File })
	return persona.Registry{Personas: kept, Issues: issues}
}
```

Update the call in `NewAPI`: `a.personas = disableUnrenderablePersonas(a.personas, reg)`.

`sinkEventName` gains:

```go
	case chat.SinkImage:
		return "chat:image"
```

`EventDTO` gains (after `ToolLatencyMs`):

```go
	ImageHash     string          `json:"imageHash,omitempty"`
```

and the `GetConversationDisplayEvents` mapping literal gains `ImageHash: r.ImageHash,`.

`SendMessage` wires the store — in the `chat.SendParams{...}` literal add nothing, but immediately before `a.chatSvc.Send`, build params in a variable so the nil-interface trap is avoided (a nil `*imagestore.Store` assigned directly would make a non-nil interface):

```go
	params := chat.SendParams{
		ConversationID:  convID,
		UserText:        userText,
		SystemPrompt:    systemPrompt,
		Model:           p.Model,
		PersonaID:       p.ID,
		Namer:           a.personas,
		Provider:        prov,
		ProviderName:    providerNameFromModelID(a.reg, p.Model),
		ReasoningEffort: reasoningEffort,
		Registry:        a.toolReg.Subset(p.Tools),
		Resolver:        chatStoreResolver{st: a.st},
		Retriever:       retr,
		RetrievalMode:   a.retrievalMode(convID),
		Sink:            wailsSink{a: a},
		RemapErr:        a.localRemapErr(p.Model),
	}
	// Assign only when non-nil: a typed-nil *imagestore.Store in the interface
	// would defeat the engine's nil check.
	if a.images != nil {
		params.Images = a.images
	}
	_, err = a.chatSvc.Send(cctx, params, nil)
	return err
```

(Preserve the existing comment about RemapErr above the field.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/appapi/ -v`
Expected: PASS — new tests and all existing ones (some existing tests reference the old gate name; the rename in Step 3 covers the one call site — fix any test still using the old name to the new one, expectations unchanged except: `[image]`-only on gemini is now KEPT).

- [ ] **Step 5: Full suite + commit**

Run: `go test ./...`
Expected: PASS.

```bash
git add internal/appapi/ internal/config/
git commit -m "feat(appapi): chat:image sink event, EventDTO imageHash, renderable-persona gate, imagestore wiring

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 6: main.go — images dir default + asset handler

**Files:**
- Modify: `main.go`

**Interfaces:**
- Consumes: `imagestore.New`/`Handler` (Task 1), `cfg.ImagesDir` (Task 5).
- Produces: the running app serves `/appimages/<hash>.png`; `<app-dir>/images/` exists on startup.

- [ ] **Step 1: Implement**

In `main.go`, after the `PersonaDir` default:

```go
	if cfg.ImagesDir == "" {
		cfg.ImagesDir = filepath.Join(appDir, "images")
	}
```

After `api := appapi.NewAPI(...)`:

```go
	// Generated images are served to the webview from the app dir; the handler
	// receives every request the embedded bundle can't satisfy.
	img, err := imagestore.New(cfg.ImagesDir)
	if err != nil {
		log.Printf("warning: image store: %v", err)
	}
```

Change the `AssetServer` option:

```go
		AssetServer: &assetserver.Options{Assets: assets, Handler: imageHandler(img)},
```

Add at the bottom of the file:

```go
// imageHandler serves /appimages/<hash>.png from the image store; a store
// that failed to initialize degrades to 404s (broken images render as the
// frontend's placeholder), never a startup crash.
func imageHandler(img *imagestore.Store) http.Handler {
	if img == nil {
		return http.NotFoundHandler()
	}
	return img.Handler()
}
```

Add imports: `"net/http"`, `"github.com/cajundata/starshp_app/internal/imagestore"`.

- [ ] **Step 2: Verify it builds and tests pass**

Run: `go build ./... && go test ./...`
Expected: both succeed. (`wails build` comes in Task 7 with the frontend change.)

- [ ] **Step 3: Commit**

```bash
git add main.go
git commit -m "feat(app): serve /appimages/<hash>.png via asset handler; create images dir

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 7: Frontend — render images live and on replay

**Files:**
- Modify: `frontend/src/main.ts`
- Modify: `frontend/src/style.css`
- Modify (regenerated): `frontend/wailsjs/go/**` (via `wails build`; `chmod 644` before staging)

**Interfaces:**
- Consumes: `chat:image` event `{convID, runID, turnID, hash}` (Task 5), `EventDTO.imageHash` (Task 5), `/appimages/<hash>.png` (Task 6).
- Produces: `appendRunImage(runId, hash)` — image segments in the run bubble, placeholder on missing file.

- [ ] **Step 1: Add `appendRunImage`**

In `main.ts`, directly after `appendRunText`:

```ts
// appendRunImage adds a generated-image segment to the run bubble, parallel
// to appendRunText. A missing file (the operator deleted the PNG from the
// app dir) swaps in a placeholder via onerror — identical for live + replay.
function appendRunImage(runId: string, hash: string) {
  if (!hash) return
  const b = ensureRunBubble(runId)
  b.curText = null // text after an image starts a new segment
  const wrap = document.createElement('div')
  wrap.className = 'msg-image'
  const img = document.createElement('img')
  img.src = `/appimages/${hash}.png`
  img.alt = 'Generated image'
  img.onerror = () => {
    wrap.classList.add('unavailable')
    wrap.textContent = 'image unavailable'
  }
  wrap.appendChild(img)
  b.el.appendChild(wrap)
  thread.scrollTop = thread.scrollHeight
}
```

- [ ] **Step 2: Wire the live event**

After the `EventsOn('chat:token_v2', …)` block:

```ts
EventsOn('chat:image', (p: any) => {
  if (p.convID !== activeConv) return
  appendRunImage(p.runID, p.hash || '')
})
```

- [ ] **Step 3: Wire the replay branch**

In `openConversation`'s event loop, after the `assistant_text` branch:

```ts
    } else if (ev.kind === 'assistant_image') {
      appendRunImage(ev.runId, (ev as any).imageHash || '')
```

- [ ] **Step 4: Styles**

Append to `frontend/src/style.css`:

```css
.msg-image { margin: 6px 0; }
.msg-image img {
  max-width: min(480px, 100%);
  border-radius: 8px;
  display: block;
}
.msg-image.unavailable {
  color: #8a8a90;
  font-style: italic;
  font-size: 12px;
  padding: 10px 12px;
  border: 1px dashed #8a8a90;
  border-radius: 8px;
  width: fit-content;
}
```

- [ ] **Step 5: Build (regenerates bindings) and verify**

Run: `wails build`
Expected: build succeeds; `frontend/wailsjs/go/appapi/` models now carry `imageHash` on the event DTO.

Run: `chmod 644 frontend/wailsjs/go/appapi/* frontend/wailsjs/go/models.ts 2>/dev/null; git status --short`
Expected: modified `main.ts`, `style.css`, and regenerated binding files, all mode 644.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/main.ts frontend/src/style.css frontend/wailsjs/
git commit -m "feat(ui): render generated images in run bubbles, live and on replay

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 8: Registry entry, persona, docs, smoke

**Files:**
- Modify: `models.example.yaml`
- Add: `personas.example/visual-designer.md` (already written, currently untracked)
- Modify: `README.md`
- Modify: `SMOKE.md`
- Modify: `BACKLOG.md` (repo root, Someday section)

**Interfaces:**
- Consumes: everything above.
- Produces: operator-facing config + verification script.

- [ ] **Step 1: models.example.yaml**

Add after the existing `gemini-3-pro` entry:

```yaml
  - display: Nano Banana 2
    id: gemini-3-pro-image
    provider: gemini
    max_context: 32768
    input_modalities: [text, image]
    output_modalities: [text, image]
```

Match the file's existing indentation exactly. Update the header comment (the lines naming Nano Banana 2 as "not yet wired") to say image output now renders in-thread for gemini models.

- [ ] **Step 2: Persona**

`personas.example/visual-designer.md` already exists untracked and pins `gemini-3-pro-image` — stage it as-is.

- [ ] **Step 3: README**

In the models/personas section: replace the sentence saying a persona pinned to a model without text output is disabled "until image support ships (Spec B)" with one sentence stating image-output gemini models (e.g. Nano Banana 2) now render generated images in the chat thread, stored under `<app-dir>/images/`; personas pinned to image-only models on other providers remain disabled.

- [ ] **Step 4: SMOKE.md**

Append a "Spec B — image generation" section continuing the existing step numbering (check the file's last number first):

```markdown
## Spec B — image generation (Nano Banana 2)

- [ ] N+1. Add the Nano Banana 2 entry to models.yaml and the visual-designer
  persona; restart. Persona picker shows Visual Designer with no startup banner.
- [ ] N+2. As Visual Designer: "draw a small cartoon cat". Interleaved
  text + image render in one attributed bubble; a <sha256>.png appears under
  <app-dir>/images/.
- [ ] N+3. Follow up: "make the sky darker". The reply edits the prior image
  (same subject, darker sky) — refinement context works.
- [ ] N+4. @-mention a text persona: "@<text-persona> critique the image".
  Its reply shows it received the attributed block (it references the image
  textually); no error.
- [ ] N+5. Delete the newest PNG from <app-dir>/images/, reopen the
  conversation. The bubble shows the "image unavailable" placeholder; other
  images still render.
- [ ] N+6. Restart the app, reopen the conversation. All images replay in
  position; footer shows sane token counts after the next image turn.
```

- [ ] **Step 5: BACKLOG Someday**

Add three lines to the Someday section:

```markdown
- Operator image upload (attach a sketch/logo for the Visual Designer to refine) — Spec B deferred.
- Image viewer polish: click-to-enlarge, save-as, copy image — Spec B deferred.
- Configurable refinement image cap (constant 6 in v1) — Spec B deferred.
```

- [ ] **Step 6: Verify + commit**

Run: `go test ./... && wails build`
Expected: PASS / build succeeds.

```bash
git add models.example.yaml personas.example/visual-designer.md README.md SMOKE.md BACKLOG.md
git commit -m "docs(spec-b): nano banana 2 registry entry, visual-designer persona, smoke steps, backlog deferrals

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

- [ ] **Step 7: Operator smoke gate (not automatable)**

The merge gate per spec: run the SMOKE.md Spec B steps against the real API on **both macOS and Windows** with a real `GEMINI_API_KEY`. Flag to the operator that this is pending before any merge/push.
