# Local / OSS Models via OpenAI-Compatible Runtimes — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `openai_compat` as a third value of `models.yaml`'s `provider` field so Ollama (and any other OpenAI-compatible local runtime) appears as a normal entry in the per-message model picker on both Windows and macOS, without changing the existing OpenAI or Anthropic code paths.

**Architecture:** Reuse the existing `provider.openai` implementation against a custom `base_url`. Add `BaseURL` and `APIKeyEnv` fields to `provider.ModelInfo`. Extend `provider.LoadRegistry` to validate the new fields. Extend the factory with one new `case "openai_compat"`. Add a `local_unreachable` error code emitted at the `appapi` boundary when a network error originates from an `openai_compat` model. Make `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` startup warnings conditional on the registry's contents (and textbook configuration for OpenAI). Cross-platform documentation updates round out the cycle.

**Tech Stack:** Go 1.25, Wails v2, `openai-go/v3` (already a dependency — reused via custom `BaseURL`), `gopkg.in/yaml.v3` (already a dependency), the existing `httptest` test pattern.

**Spec:** `docs/superpowers/specs/2026-06-04-local-oss-models-design.md`

---

## Pre-flight (one-time, before Task 1)

- [ ] **From the repo root, confirm clean working tree before starting**

Run:
```bash
git status
```
Expected: `nothing to commit, working tree clean`. If not, stash or commit existing work before beginning. The plan creates one atomic commit per task and assumes a clean starting state.

- [ ] **Sanity-check the existing test suite passes**

Run:
```bash
go test ./...
```
Expected: all packages green. If any are red before we start, fix them first — the new tests assume a known-green baseline.

---

## Task 1: Registry Fields and Validation

Add `BaseURL` and `APIKeyEnv` to `provider.ModelInfo`. Make `LoadRegistry` reject (a) `openai_compat` entries with no `base_url`, and (b) `openai` / `anthropic` entries that carry a stray `base_url`.

**Files:**
- Modify: `internal/provider/registry.go`
- Modify: `internal/provider/registry_test.go`

- [ ] **Step 1.1: Add a failing test for parsing the new fields**

Open `internal/provider/registry_test.go` and append:

```go
func TestLoadRegistryParsesOpenAICompatFields(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "models.yaml")
	os.WriteFile(p, []byte(`models:
  - display: Llama 3.2 (local)
    id: llama3.2
    provider: openai_compat
    base_url: http://localhost:11434/v1
    max_context: 131072
  - display: LM Studio Qwen
    id: qwen2.5
    provider: openai_compat
    base_url: http://localhost:1234/v1
    api_key_env: LM_STUDIO_TOKEN
`), 0o600)
	reg, err := LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if len(reg.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(reg.Models))
	}
	llama, ok := reg.ByID("llama3.2")
	if !ok {
		t.Fatal("llama3.2 not in registry")
	}
	if llama.Provider != "openai_compat" {
		t.Errorf("llama.Provider = %q, want openai_compat", llama.Provider)
	}
	if llama.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("llama.BaseURL = %q, want http://localhost:11434/v1", llama.BaseURL)
	}
	if llama.APIKeyEnv != "" {
		t.Errorf("llama.APIKeyEnv = %q, want empty (omitted in yaml)", llama.APIKeyEnv)
	}
	qwen, _ := reg.ByID("qwen2.5")
	if qwen.APIKeyEnv != "LM_STUDIO_TOKEN" {
		t.Errorf("qwen.APIKeyEnv = %q, want LM_STUDIO_TOKEN", qwen.APIKeyEnv)
	}
}
```

- [ ] **Step 1.2: Run the test and confirm it fails**

Run:
```bash
go test ./internal/provider/ -run TestLoadRegistryParsesOpenAICompatFields -v
```
Expected: a compile error or failure complaining that `BaseURL` / `APIKeyEnv` are not fields of `ModelInfo`.

- [ ] **Step 1.3: Add the two fields to `ModelInfo`**

Open `internal/provider/registry.go`. Replace the `ModelInfo` struct (currently lines 9–14) with:

```go
type ModelInfo struct {
	Display    string `yaml:"display" json:"display"`
	ID         string `yaml:"id" json:"id"`
	Provider   string `yaml:"provider" json:"provider"` // "openai" | "anthropic" | "openai_compat"
	MaxContext int    `yaml:"max_context,omitempty" json:"maxContext,omitempty"`
	BaseURL    string `yaml:"base_url,omitempty" json:"baseURL,omitempty"`
	APIKeyEnv  string `yaml:"api_key_env,omitempty" json:"apiKeyEnv,omitempty"`
}
```

- [ ] **Step 1.4: Re-run the parsing test and confirm it passes**

Run:
```bash
go test ./internal/provider/ -run TestLoadRegistryParsesOpenAICompatFields -v
```
Expected: PASS.

- [ ] **Step 1.5: Add a failing test for missing-`base_url` rejection**

Append to `internal/provider/registry_test.go`:

```go
func TestLoadRegistryRejectsOpenAICompatMissingBaseURL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "models.yaml")
	os.WriteFile(p, []byte(`models:
  - display: Llama 3.2 (local)
    id: llama3.2
    provider: openai_compat
`), 0o600)
	_, err := LoadRegistry(p)
	if err == nil {
		t.Fatal("expected error for openai_compat entry missing base_url, got nil")
	}
	if !strings.Contains(err.Error(), "base_url") {
		t.Errorf("error %q does not mention base_url", err)
	}
	if !strings.Contains(err.Error(), "llama3.2") {
		t.Errorf("error %q does not mention the offending model id", err)
	}
}
```

This test uses `strings.Contains`. The existing `registry_test.go` does not import `strings`. Add it to the import block at the top of the file.

- [ ] **Step 1.6: Add a failing test for stray-`base_url` rejection on cloud providers**

Append:

```go
func TestLoadRegistryRejectsCloudProvidersWithBaseURL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "models.yaml")
	os.WriteFile(p, []byte(`models:
  - display: Claude Opus 4.7
    id: claude-opus-4-7
    provider: anthropic
    base_url: http://example.com
`), 0o600)
	_, err := LoadRegistry(p)
	if err == nil {
		t.Fatal("expected error for anthropic entry with stray base_url, got nil")
	}
	if !strings.Contains(err.Error(), "base_url") {
		t.Errorf("error %q does not mention base_url", err)
	}

	// Same check for openai.
	os.WriteFile(p, []byte(`models:
  - display: GPT-5.4
    id: gpt-5.4-2026-03-05
    provider: openai
    base_url: http://example.com
`), 0o600)
	_, err = LoadRegistry(p)
	if err == nil {
		t.Fatal("expected error for openai entry with stray base_url, got nil")
	}
}
```

- [ ] **Step 1.7: Run both validation tests and confirm they fail**

Run:
```bash
go test ./internal/provider/ -run 'TestLoadRegistryRejects' -v
```
Expected: both FAIL (no validation yet).

- [ ] **Step 1.8: Implement registry validation**

Open `internal/provider/registry.go`. Replace the `LoadRegistry` function (currently lines 20–30) with:

```go
func LoadRegistry(path string) (Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, err
	}
	var r Registry
	if err := yaml.Unmarshal(raw, &r); err != nil {
		return Registry{}, err
	}
	for _, m := range r.Models {
		switch m.Provider {
		case "openai_compat":
			if m.BaseURL == "" {
				return Registry{}, fmt.Errorf("model %s: base_url is required for provider openai_compat", m.ID)
			}
		case "openai", "anthropic":
			if m.BaseURL != "" {
				return Registry{}, fmt.Errorf("model %s: base_url is not allowed for provider %s", m.ID, m.Provider)
			}
		}
	}
	return r, nil
}
```

Add `"fmt"` to the imports at the top of the file (the existing imports are `"os"` and `"gopkg.in/yaml.v3"`).

- [ ] **Step 1.9: Run the full provider test suite**

Run:
```bash
go test ./internal/provider/ -v
```
Expected: all tests PASS, including the three new ones and the existing `TestLoadRegistry` / `TestLoadRegistryWithMaxContext` (the existing yaml fixtures are valid — no stray `base_url`).

- [ ] **Step 1.10: Commit**

```bash
git add internal/provider/registry.go internal/provider/registry_test.go
git commit -m "feat(provider): add BaseURL and APIKeyEnv to ModelInfo, with LoadRegistry validation

Required when provider is openai_compat; rejected on openai/anthropic.
The two new fields prepare for the openai_compat factory branch landing
in the next task."
```

---

## Task 2: Factory `openai_compat` Branch

Extend `provider.New` with a third case that constructs an OpenAI-SDK client against the model's `BaseURL`. Resolve the API key from the env var named by `APIKeyEnv`, falling back to the literal string `"local"` (the OpenAI SDK requires non-empty; Ollama ignores the value).

**Files:**
- Modify: `internal/provider/factory.go`
- Create: `internal/provider/factory_test.go`

- [ ] **Step 2.1: Create the failing branch-wiring test**

Create `internal/provider/factory_test.go`:

```go
package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// helper: a registry containing a single openai_compat model pointing at the
// given URL, optionally requiring an env-var API key.
func compatReg(id, baseURL, apiKeyEnv string) Registry {
	return Registry{Models: []ModelInfo{{
		Display:   id,
		ID:        id,
		Provider:  "openai_compat",
		BaseURL:   baseURL,
		APIKeyEnv: apiKeyEnv,
	}}}
}

func TestFactoryOpenAICompatPointsAtBaseURL(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		flush := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"index\":0}]}\n\n")
		flush.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush.Flush()
	}))
	defer srv.Close()

	reg := compatReg("llama3.2", srv.URL, "")

	// Cloud keys are irrelevant for openai_compat; pass empty to prove they're ignored.
	p, err := New(reg, "llama3.2", "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "llama3.2", Messages: []Message{{Role: "user", Content: "hi"}},
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
	if sb.String() != "ok" {
		t.Fatalf("assembled = %q, want %q", sb.String(), "ok")
	}
	if !strings.HasPrefix(gotAuth, "Bearer local") {
		t.Errorf("Authorization = %q, want prefix \"Bearer local\" (dummy key)", gotAuth)
	}
}

func TestFactoryOpenAICompatHonorsAPIKeyEnv(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		flush := w.(http.Flusher)
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush.Flush()
	}))
	defer srv.Close()

	t.Setenv("LM_STUDIO_TOKEN", "lms-secret-42")
	reg := compatReg("qwen2.5", srv.URL, "LM_STUDIO_TOKEN")

	p, err := New(reg, "qwen2.5", "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "qwen2.5", Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}
	if gotAuth != "Bearer lms-secret-42" {
		t.Errorf("Authorization = %q, want \"Bearer lms-secret-42\"", gotAuth)
	}
}

func TestFactoryOpenAICompatFallsBackWhenEnvUnset(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		flush := w.(http.Flusher)
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush.Flush()
	}))
	defer srv.Close()

	// APIKeyEnv names an env var, but it is explicitly unset.
	os.Unsetenv("SOME_UNSET_TOKEN_VAR")
	reg := compatReg("custom", srv.URL, "SOME_UNSET_TOKEN_VAR")

	p, err := New(reg, "custom", "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, _ := p.Stream(context.Background(), ChatRequest{
		Model: "custom", Messages: []Message{{Role: "user", Content: "hi"}},
	})
	for range ch {
	}
	if !strings.HasPrefix(gotAuth, "Bearer local") {
		t.Errorf("Authorization = %q, want fallback prefix \"Bearer local\"", gotAuth)
	}
}
```

- [ ] **Step 2.2: Run the new tests and confirm they fail**

Run:
```bash
go test ./internal/provider/ -run TestFactoryOpenAICompat -v
```
Expected: FAIL — `unsupported provider: openai_compat` from the existing factory's default branch.

- [ ] **Step 2.3: Implement the factory case**

Open `internal/provider/factory.go`. Replace the entire file with:

```go
package provider

import (
	"fmt"
	"os"
)

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
	case "openai_compat":
		// LoadRegistry already rejects an empty BaseURL; keep the guard for
		// programmatically-built registries (e.g., tests).
		if m.BaseURL == "" {
			return nil, fmt.Errorf("model %s: base_url required for openai_compat", m.ID)
		}
		return NewOpenAI(resolveCompatKey(m), m.BaseURL), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", m.Provider)
	}
}

// resolveCompatKey returns the bearer token to use for an openai_compat model.
// If APIKeyEnv names a set env var, that value wins; otherwise the dummy
// string "local" is used — Ollama ignores it and the OpenAI SDK only requires
// the value be non-empty.
func resolveCompatKey(m ModelInfo) string {
	if m.APIKeyEnv != "" {
		if v := os.Getenv(m.APIKeyEnv); v != "" {
			return v
		}
	}
	return "local"
}
```

- [ ] **Step 2.4: Re-run the factory tests and confirm they pass**

Run:
```bash
go test ./internal/provider/ -run TestFactoryOpenAICompat -v
```
Expected: all three PASS.

- [ ] **Step 2.5: Re-run the full provider suite to confirm no regressions**

Run:
```bash
go test ./internal/provider/ -v
```
Expected: all previously-passing tests still PASS.

- [ ] **Step 2.6: Commit**

```bash
git add internal/provider/factory.go internal/provider/factory_test.go
git commit -m "feat(provider): factory branch for openai_compat with env-var key resolution

The new case constructs an OpenAI-SDK client against the model's
BaseURL. APIKeyEnv, when set and present in the environment, supplies
the bearer token; otherwise a dummy 'local' string is sent (Ollama
ignores it, the SDK only requires non-empty)."
```

---

## Task 3: `local_unreachable` Error Code

Introduce a new normalized error code surfaced when an `openai_compat` model fails with a connection error. The mapping lives in `provider/errors.go`; the call site in `appapi/api.go` (`SendMessage`) applies it once `chat.Send` has already produced a generic `network` AppError.

**Files:**
- Modify: `internal/provider/errors.go`
- Modify: `internal/provider/errors_test.go`
- Modify: `internal/appapi/api.go`
- Create: `internal/appapi/api_compat_test.go`

- [ ] **Step 3.1: Add a failing test for `MaybeRemapLocal`**

Open `internal/provider/errors_test.go`. Append:

```go
func TestMaybeRemapLocalRemapsNetworkForCompatModel(t *testing.T) {
	m := ModelInfo{ID: "llama3.2", Provider: "openai_compat", BaseURL: "http://localhost:11434/v1"}
	in := AppError{Code: "network", UserMessage: "Network error reaching the provider. Check your connection.", Retryable: true}
	out := MaybeRemapLocal(in, m)
	if out.Code != "local_unreachable" {
		t.Errorf("Code = %q, want local_unreachable", out.Code)
	}
	if !strings.Contains(out.UserMessage, "http://localhost:11434/v1") {
		t.Errorf("UserMessage %q does not interpolate the base URL", out.UserMessage)
	}
	if !strings.Contains(out.UserMessage, "Ollama") {
		t.Errorf("UserMessage %q does not mention Ollama in the suggestion", out.UserMessage)
	}
	if !out.Retryable {
		t.Errorf("Retryable should remain true after remap")
	}
}

func TestMaybeRemapLocalIgnoresCloudModels(t *testing.T) {
	for _, p := range []string{"openai", "anthropic"} {
		m := ModelInfo{ID: "x", Provider: p}
		in := AppError{Code: "network", UserMessage: "Network error reaching the provider.", Retryable: true}
		out := MaybeRemapLocal(in, m)
		if out.Code != "network" {
			t.Errorf("provider=%s: Code = %q, want network (unchanged)", p, out.Code)
		}
	}
}

func TestMaybeRemapLocalIgnoresNonNetworkCodes(t *testing.T) {
	m := ModelInfo{ID: "llama3.2", Provider: "openai_compat", BaseURL: "http://localhost:11434/v1"}
	for _, code := range []string{"auth", "rate_limit", "context_length", "unknown"} {
		in := AppError{Code: code, UserMessage: "x"}
		out := MaybeRemapLocal(in, m)
		if out.Code != code {
			t.Errorf("Code=%q: got %q, want unchanged", code, out.Code)
		}
	}
}
```

`errors_test.go` already imports `"testing"` but not `"strings"`. Add `"strings"` to the import block at the top of the file (or expand the bare-import line to a parenthesised block if it isn't already).

- [ ] **Step 3.2: Add a failing test for the extended network-string detection**

Connection errors from Go's net package may not contain the substring `"connection refused"` — DNS failures use `"no such host"`, and context cancellation surfaces as `"context deadline exceeded"`. Append to `errors_test.go`:

```go
func TestNormalizeErrorClassifiesConnectionVariants(t *testing.T) {
	cases := []string{
		"dial tcp 127.0.0.1:11434: connect: connection refused",
		"Get \"http://localhost:11434/v1/chat/completions\": dial tcp: lookup wat.invalid: no such host",
		"Post \"http://localhost:11434/v1/chat/completions\": context deadline exceeded",
	}
	for _, msg := range cases {
		got := NormalizeError(errors.New(msg))
		if got.Code != "network" {
			t.Errorf("NormalizeError(%q).Code = %q, want network", msg, got.Code)
		}
	}
}
```

- [ ] **Step 3.3: Run the new error tests and confirm they fail**

Run:
```bash
go test ./internal/provider/ -run 'TestMaybeRemapLocal|TestNormalizeErrorClassifiesConnectionVariants' -v
```
Expected: all four FAIL — `MaybeRemapLocal` does not exist; the `no such host` / `context deadline exceeded` cases fall through to `unknown`.

- [ ] **Step 3.4: Extend `NormalizeError` to catch the additional connection substrings, and add `MaybeRemapLocal`**

Open `internal/provider/errors.go`. Replace the entire file with:

```go
package provider

import (
	"fmt"
	"strings"
)

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
	case strings.Contains(s, "connection refused") ||
		strings.Contains(s, "dial tcp") ||
		strings.Contains(s, "no such host") ||
		strings.Contains(s, "timeout") ||
		strings.Contains(s, "deadline exceeded"):
		return AppError{"network", "Network error reaching the provider. Check your connection.", true}
	default:
		return AppError{"unknown", "Unexpected error: " + err.Error(), false}
	}
}

// MaybeRemapLocal upgrades a generic `network` AppError into a more specific
// `local_unreachable` error when the failing model uses the openai_compat
// provider. The base URL is interpolated into the user message so the user
// knows exactly which endpoint Starshp was calling. Returns the input
// unchanged in all other cases.
func MaybeRemapLocal(e AppError, m ModelInfo) AppError {
	if m.Provider != "openai_compat" || e.Code != "network" {
		return e
	}
	return AppError{
		Code: "local_unreachable",
		UserMessage: fmt.Sprintf(
			"Local model server unreachable at %s. Is Ollama running? (Run `ollama serve` or start the Ollama app.)",
			m.BaseURL,
		),
		Retryable: true,
	}
}
```

- [ ] **Step 3.5: Re-run the error tests and confirm they pass**

Run:
```bash
go test ./internal/provider/ -v
```
Expected: all PASS, including the four new tests.

- [ ] **Step 3.6: Wire the remap into `appapi.SendMessage`**

Open `internal/appapi/api.go`. Locate `SendMessage` (begins at line 115). The error return at the end of `SendMessage` (currently `return text, err` on roughly line 163) must consult the model registry and upgrade `network` errors for `openai_compat` models.

Replace the block from line 154 onward (the `chatSvc.Send` call through `return text, err`) with:

```go
	text, usage, err := a.chatSvc.Send(cctx, chat.SendParams{
		ConversationID: convID, UserText: userText, SystemPrompt: systemPrompt,
		Model: modelID, Provider: prov, Retriever: retr,
	}, func(tok string) {
		wruntime.EventsEmit(a.ctx, "chat:token", tok) // use a.ctx: events always flow to UI
	})
	if payload := buildChatUsageEvent(convID, modelID, usage); payload != nil {
		wruntime.EventsEmit(a.ctx, "chat:usage", payload)
	}
	if err != nil {
		if ae, ok := err.(provider.AppError); ok {
			if m, found := a.reg.ByID(modelID); found {
				err = provider.MaybeRemapLocal(ae, m)
			}
		}
	}
	return text, err
}
```

Verify the closing `}` count matches the original function shape.

- [ ] **Step 3.7: Write a test for the end-to-end remap**

Create `internal/appapi/api_compat_test.go`:

```go
package appapi

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

// TestSendMessageRemapsLocalUnreachable boots an API wired to a registry
// containing a single openai_compat model whose base_url points at a TCP
// address with no listener. SendMessage must return an AppError with
// code local_unreachable and the base URL interpolated into the message.
func TestSendMessageRemapsLocalUnreachable(t *testing.T) {
	// Reserve an OS-assigned port, then close it so dialling fails immediately.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	baseURL := "http://" + addr + "/v1"

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	conv, err := st.CreateConversation("")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	reg := provider.Registry{Models: []provider.ModelInfo{{
		Display: "Llama 3.2 (local)", ID: "llama3.2",
		Provider: "openai_compat", BaseURL: baseURL,
	}}}
	cfg := config.Config{
		AppDBPath:    filepath.Join(dir, "app.db"),
		LibraryDir:   filepath.Join(dir, "library"),
		ModelsConfig: filepath.Join(dir, "m.yaml"),
	}
	api := NewAPI(cfg, st, reg, nil)
	api.Startup(context.Background())

	_, err = api.SendMessage(conv.ID, "hi", "llama3.2")
	if err == nil {
		t.Fatal("expected error from SendMessage against a closed local port, got nil")
	}
	ae, ok := err.(provider.AppError)
	if !ok {
		t.Fatalf("error is %T, want provider.AppError: %v", err, err)
	}
	if ae.Code != "local_unreachable" {
		t.Errorf("Code = %q, want local_unreachable (raw err: %v)", ae.Code, err)
	}
	if !strings.Contains(ae.UserMessage, baseURL) {
		t.Errorf("UserMessage %q does not interpolate base URL %q", ae.UserMessage, baseURL)
	}
	if !ae.Retryable {
		t.Errorf("Retryable should be true on a network/transient error")
	}
}
```

- [ ] **Step 3.8: Run the appapi test and confirm it passes**

Run:
```bash
go test ./internal/appapi/ -run TestSendMessageRemapsLocalUnreachable -v
```
Expected: PASS. If FAIL because the dial error wording is unrecognised by `NormalizeError`, inspect the raw error string in the test log and extend the substring set in `errors.go` (Step 3.4) accordingly.

- [ ] **Step 3.9: Run the full test suite**

Run:
```bash
go test ./...
```
Expected: green across all packages.

- [ ] **Step 3.10: Commit**

```bash
git add internal/provider/errors.go internal/provider/errors_test.go internal/appapi/api.go internal/appapi/api_compat_test.go
git commit -m "feat(provider,appapi): local_unreachable error code for openai_compat models

NormalizeError now recognises additional connection-error substrings
(no such host, deadline exceeded). MaybeRemapLocal converts a generic
network AppError into local_unreachable with the model's base URL
interpolated into the user message. SendMessage applies the remap once
the registry lookup confirms the failing model is openai_compat."
```

---

## Task 4: Conditional Startup Validation

Make the `OPENAI_API_KEY` warning fire only when needed: at least one model with `provider: openai` is registered, or `textbooks.yaml` lists at least one book. Add an analogous `ANTHROPIC_API_KEY` warning gated on a registered `provider: anthropic` model.

**Files:**
- Modify: `internal/appapi/validate.go`
- Modify: `internal/appapi/validate_test.go`
- Modify: `internal/appapi/api.go`

- [ ] **Step 4.1: Write failing tests for the conditional logic**

Open `internal/appapi/validate_test.go`. Replace the entire file with:

```go
package appapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/provider"
)

// writeTextbooksYAML writes a textbooks.yaml that lists one book pointing at
// a chapter-dir we create inside dir. Returns the path to the yaml file.
func writeTextbooksYAML(t *testing.T, dir string) string {
	t.Helper()
	bookDir := filepath.Join(dir, "books", "intermediate-accounting")
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		t.Fatalf("mkdir bookDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bookDir, "chapter-1.md"), []byte("# ch1"), 0o600); err != nil {
		t.Fatalf("write chapter: %v", err)
	}
	p := filepath.Join(dir, "tb.yaml")
	body := "textbooks:\n  - name: ia\n    chapter_dir: " + bookDir + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write tb.yaml: %v", err)
	}
	return p
}

func goodConfig(t *testing.T) (config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	mp := filepath.Join(dir, "m.yaml")
	if err := os.WriteFile(mp, []byte("models: []\n"), 0o600); err != nil {
		t.Fatalf("write m.yaml: %v", err)
	}
	return config.Config{
		AppDBPath: filepath.Join(dir, "a.db"), RAGDBPath: filepath.Join(dir, "r.db"),
		TextbooksConfig: filepath.Join(dir, "tb.yaml"), ModelsConfig: mp,
		LibraryDir: filepath.Join(dir, "library"),
	}, dir
}

func hasIssueMentioning(issues []string, fragment string) bool {
	for _, i := range issues {
		if strings.Contains(i, fragment) {
			return true
		}
	}
	return false
}

func TestValidateStartupSkipsOpenAIWhenOnlyCompatAndAnthropicModels(t *testing.T) {
	c, _ := goodConfig(t)
	c.AnthropicAPIKey = "k"
	reg := provider.Registry{Models: []provider.ModelInfo{
		{ID: "claude-opus-4-7", Provider: "anthropic"},
		{ID: "llama3.2", Provider: "openai_compat", BaseURL: "http://localhost:11434/v1"},
	}}
	issues := ValidateStartup(c, reg)
	if hasIssueMentioning(issues, "OPENAI_API_KEY") {
		t.Errorf("did not expect OPENAI_API_KEY warning; got %v", issues)
	}
}

func TestValidateStartupRequiresOpenAIWhenRealOpenAIModelRegistered(t *testing.T) {
	c, _ := goodConfig(t)
	reg := provider.Registry{Models: []provider.ModelInfo{
		{ID: "gpt-5.4-2026-03-05", Provider: "openai"},
	}}
	issues := ValidateStartup(c, reg)
	if !hasIssueMentioning(issues, "OPENAI_API_KEY") {
		t.Errorf("expected OPENAI_API_KEY warning when openai model registered; got %v", issues)
	}
}

func TestValidateStartupRequiresOpenAIWhenTextbooksConfigured(t *testing.T) {
	c, dir := goodConfig(t)
	c.TextbooksConfig = writeTextbooksYAML(t, dir)
	reg := provider.Registry{Models: []provider.ModelInfo{
		{ID: "claude-opus-4-7", Provider: "anthropic"},
	}}
	issues := ValidateStartup(c, reg)
	if !hasIssueMentioning(issues, "OPENAI_API_KEY") {
		t.Errorf("expected OPENAI_API_KEY warning when textbooks configured; got %v", issues)
	}
}

func TestValidateStartupRequiresAnthropicKeyWhenAnthropicModelRegistered(t *testing.T) {
	c, _ := goodConfig(t)
	c.OpenAIAPIKey = "k" // not under test
	reg := provider.Registry{Models: []provider.ModelInfo{
		{ID: "claude-opus-4-7", Provider: "anthropic"},
	}}
	issues := ValidateStartup(c, reg)
	if !hasIssueMentioning(issues, "ANTHROPIC_API_KEY") {
		t.Errorf("expected ANTHROPIC_API_KEY warning when anthropic model registered; got %v", issues)
	}
}

func TestValidateStartupSkipsAnthropicWarningWhenNoAnthropicModel(t *testing.T) {
	c, _ := goodConfig(t)
	reg := provider.Registry{Models: []provider.ModelInfo{
		{ID: "llama3.2", Provider: "openai_compat", BaseURL: "http://localhost:11434/v1"},
	}}
	issues := ValidateStartup(c, reg)
	if hasIssueMentioning(issues, "ANTHROPIC_API_KEY") {
		t.Errorf("did not expect ANTHROPIC_API_KEY warning; got %v", issues)
	}
}

// Regression: the existing un-key-related checks still surface (missing
// models.yaml, unwritable paths) so we do not silently lose them.
func TestValidateStartupStillReportsMissingModelsYAML(t *testing.T) {
	c, dir := goodConfig(t)
	c.ModelsConfig = filepath.Join(dir, "missing.yaml")
	issues := ValidateStartup(c, provider.Registry{})
	if !hasIssueMentioning(issues, "missing.yaml") {
		t.Errorf("expected missing-models.yaml warning; got %v", issues)
	}
}
```

- [ ] **Step 4.2: Run the new tests and confirm they fail**

Run:
```bash
go test ./internal/appapi/ -run 'TestValidateStartup' -v
```
Expected: compile error — `ValidateStartup` takes one arg, not two.

- [ ] **Step 4.3: Rewrite `ValidateStartup` to be conditional**

Open `internal/appapi/validate.go`. Replace the entire file with:

```go
package appapi

import (
	"os"
	"path/filepath"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/textbooks"
)

// ValidateStartup returns human-readable setup problems (empty = OK). The
// registry is consulted so that key warnings only fire when at least one
// model needs that key, or — in the OpenAI case — when textbooks are
// configured (RAG embeddings need an OpenAI key regardless of chat model).
func ValidateStartup(c config.Config, reg provider.Registry) []string {
	var issues []string

	if needsOpenAIKey(c, reg) && c.OpenAIAPIKey == "" {
		issues = append(issues, "OPENAI_API_KEY is not set (required for the registered OpenAI model or textbook embeddings).")
	}
	if needsAnthropicKey(reg) && c.AnthropicAPIKey == "" {
		issues = append(issues, "ANTHROPIC_API_KEY is not set (required for the registered Anthropic model).")
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
}

func needsOpenAIKey(c config.Config, reg provider.Registry) bool {
	for _, m := range reg.Models {
		if m.Provider == "openai" {
			return true
		}
	}
	// RAG embeddings are OpenAI-only; if any books are configured, the key is required.
	if books, err := textbooks.Scan(c.TextbooksConfig); err == nil && len(books) > 0 {
		return true
	}
	return false
}

func needsAnthropicKey(reg provider.Registry) bool {
	for _, m := range reg.Models {
		if m.Provider == "anthropic" {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4.4: Update the one caller in `api.go`**

Open `internal/appapi/api.go`. Replace the line:

```go
func (a *API) StartupIssues() []string { return ValidateStartup(a.cfg) }
```

with:

```go
func (a *API) StartupIssues() []string { return ValidateStartup(a.cfg, a.reg) }
```

- [ ] **Step 4.5: Run the validate tests and confirm they pass**

Run:
```bash
go test ./internal/appapi/ -run 'TestValidateStartup' -v
```
Expected: all six new tests PASS.

- [ ] **Step 4.6: Run the full appapi suite to catch regressions**

Run:
```bash
go test ./internal/appapi/ -v
```
Expected: everything green.

- [ ] **Step 4.7: Run the full repo test suite**

Run:
```bash
go test ./...
```
Expected: green.

- [ ] **Step 4.8: Commit**

```bash
git add internal/appapi/validate.go internal/appapi/validate_test.go internal/appapi/api.go
git commit -m "feat(appapi): conditional OPENAI_API_KEY and ANTHROPIC_API_KEY startup checks

ValidateStartup now takes the model registry. OPENAI_API_KEY is only
required when a real openai model is registered or textbooks are
configured (RAG embeddings stay OpenAI-only). ANTHROPIC_API_KEY warning
is new — it only fires when an anthropic model is registered. A user
running Anthropic + Ollama only is no longer nagged about OpenAI."
```

---

## Task 5: Wails Binding Regeneration and Smoke Verify

`ModelInfo` gained two JSON-serialised fields. The Wails-generated TypeScript binding must be refreshed so the frontend dropdown continues to see the same shape (display, id, provider, maxContext) plus the new optional fields it can safely ignore.

**Files:**
- Regenerated: `frontend/wailsjs/go/provider/*.ts` (and any `*.d.ts`)
- No source edits.

- [ ] **Step 5.1: Run the Wails binding generator**

Run:
```bash
wails generate module
```
Expected: command succeeds; `frontend/wailsjs/` is updated. If `wails` is not on PATH, install per the README prerequisites: `go install github.com/wailsapp/wails/v2/cmd/wails@latest`.

If `wails generate module` is unavailable in this version of Wails, fall back to:
```bash
wails build -skipbindings=false -clean
```
…which regenerates the bindings as part of the build.

- [ ] **Step 5.2: Inspect the regenerated binding**

Run:
```bash
git diff -- frontend/wailsjs/
```
Expected: the `ModelInfo` (or `provider.ModelInfo`) TypeScript type gains `baseURL?: string` and `apiKeyEnv?: string`. No other shape changes.

- [ ] **Step 5.3: Type-check the frontend**

Run:
```bash
cd frontend && npm install --silent && npx tsc --noEmit && cd ..
```
Expected: tsc reports no errors. The frontend never reads `baseURL` or `apiKeyEnv` (model picker shows only `display`), so the additions are purely additive at the type level.

- [ ] **Step 5.4: Commit**

```bash
git add frontend/wailsjs/
git commit -m "chore(wails): regenerate bindings for ModelInfo.BaseURL and APIKeyEnv

Additive optional fields; frontend code does not read them yet."
```

---

## Task 6: `models.example.yaml` Starter Entry

Append a commented-out Ollama entry so a user copying the template into their app directory has a working starting point.

**Files:**
- Modify: `models.example.yaml`

- [ ] **Step 6.1: Append the Ollama starter block**

Open `models.example.yaml`. Append (preserving any existing trailing newline):

```yaml

  # ---------------------------------------------------------------------------
  # Local model via Ollama (https://ollama.com).
  # Install Ollama on your OS, run `ollama pull llama3.2`, then uncomment:
  # ---------------------------------------------------------------------------
  # - display: Llama 3.2 (local)
  #   id: llama3.2
  #   provider: openai_compat
  #   base_url: http://localhost:11434/v1
  #   max_context: 131072
```

- [ ] **Step 6.2: Confirm the file still parses as valid YAML**

The appended block is entirely commented out, but a stray indentation slip is easy to miss. Verify the file parses cleanly. From the repo root run:

```bash
go run -mod=mod - <<'EOF'
package main

import (
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

func main() {
	b, err := os.ReadFile("models.example.yaml")
	if err != nil {
		log.Fatal(err)
	}
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		log.Fatal(err)
	}
	log.Println("models.example.yaml parses cleanly")
}
EOF
```
Expected: `models.example.yaml parses cleanly`. The existing four uncommented entries are still the only models present (the new entry is fully commented).

- [ ] **Step 6.3: Commit**

```bash
git add models.example.yaml
git commit -m "docs(models): commented-out Ollama starter entry in models.example.yaml

OS-agnostic (http://localhost:11434/v1 is identical on Windows and macOS).
Lives below the existing entries so a fresh copy keeps Claude + GPT
defaults plus a clearly-marked local option."
```

---

## Task 7: README Updates

Four small, distinct edits to `README.md`. Keep them in one commit since they share the same theme.

**Files:**
- Modify: `README.md`

- [ ] **Step 7.1: Add a new "Local models via Ollama" section**

In `README.md`, insert a new section directly above the existing `## Testing` heading (around line 188 in the current file). Use this exact content:

```markdown
## Local models via Ollama

Starshp talks to any OpenAI-compatible local server. Ollama is the
reference runtime — same simple install on Windows and macOS.

### Why

Zero per-token cost and zero network round-trip for chat. RAG textbook
embeddings still use OpenAI (see the project's intentional "out of
scope: local embeddings" line); only chat traffic moves local.

### Install

| OS | Command |
| --- | --- |
| macOS | `brew install ollama && brew services start ollama`, or installer from ollama.com |
| Windows | `winget install Ollama.Ollama`, or installer from ollama.com (auto-starts as a service) |

### Pull a model

```bash
ollama pull llama3.2
# or: ollama pull qwen2.5:7b
# or: ollama pull mistral
```

The exact model name passed to `ollama pull` is the value that goes in
the `id:` field of the `models.yaml` entry.

### Register it in `models.yaml`

```yaml
- display: Llama 3.2 (local)
  id: llama3.2
  provider: openai_compat
  base_url: http://localhost:11434/v1
  max_context: 131072
```

`provider: openai_compat` is the new third provider type, covering any
OpenAI-Chat-Completions-compatible endpoint (Ollama, LM Studio, vLLM,
llama.cpp server). `base_url` is required. `api_key_env` (not shown) is
an optional name of an env var holding a bearer token, for shims that
require one.

Restart Starshp. The model appears in the per-message picker. Pick it
on the next turn — done.

### Hardware sizing

Match the model size to the available RAM/VRAM. On Apple Silicon,
usable RAM ≈ unified memory minus ~8 GB reserved for the OS and the
app; on Windows the bound is GPU VRAM. A more detailed tier-by-tier
recommendation is queued in `BACKLOG.md` Someday.

### Troubleshooting

| Symptom | Fix |
| --- | --- |
| "Local model server unreachable at …" in the UI | Start Ollama (`ollama serve` or the Ollama menu-bar/system-tray app). |
| Context-footer HUD denominator looks wrong | `max_context` in `models.yaml` must match the model's actual `num_ctx`. Ollama's default is small (2048 or 4096 depending on model). Override with `OLLAMA_NUM_CTX` env var or a custom modelfile. |
| Slow first token after a model has been idle | Ollama is loading the model into memory. Subsequent turns within `keep_alive` (default 5 minutes) are fast. |
| Cached tokens in the footer always show 0 | Ollama's OpenAI-compat shim does not surface cache-hit stats. Not a bug. |

```

- [ ] **Step 7.2: Append a macOS note to the Prerequisites section**

Find the existing "Prerequisites" section (currently at around line 42). After the existing `wails doctor` bullet, append:

```markdown
- On macOS, `wails doctor` verifies Xcode Command Line Tools
  (`xcode-select --install` if missing) and WebKit. Apple Silicon and
  Intel both work without extra setup.
```

- [ ] **Step 7.3: Reword the `wails build` artifact line**

Find the "Running" section (currently around line 157). Replace:

```bash
wails build    # release binary at build/bin/starshp_app.exe
```

with:

```bash
wails build    # release binary: build/bin/starshp_app.exe (Windows),
               #                  build/bin/starshp_app.app (macOS),
               #                  build/bin/starshp_app    (Linux)
```

- [ ] **Step 7.4: Reword the "Out of scope" cross-platform bullet**

Find the "Out of scope (deferred)" bullet list near the end of the README. Replace:

```markdown
- Cross-platform packaging — Windows-first; macOS/Linux later.
```

with:

```markdown
- Linux packaging — Windows and macOS supported; Linux builds work but are not smoke-tested.
```

- [ ] **Step 7.5: Spot-check the README renders cleanly**

Run:
```bash
grep -n 'Local models via Ollama\|wails doctor\|starshp_app.app\|Linux packaging' README.md
```
Expected: each search hits exactly the lines you intended (the new section heading appears once; the macOS bullet appears under Prerequisites; the build line shows the three artifact paths; the out-of-scope reword replaced the original).

- [ ] **Step 7.6: Commit**

```bash
git add README.md
git commit -m "docs(README): cross-platform setup for local models via Ollama

Adds a 'Local models via Ollama' section covering install on Windows
and macOS, pulling a model, registering it in models.yaml, hardware
sizing, and troubleshooting. Refines macOS prerequisites
(xcode-select). Rewords the wails build artifact line and the
Out-of-scope cross-platform bullet to reflect active dev on both
Windows and macOS."
```

---

## Task 8: Smoke Step in `docs/SMOKE.md`

Append one numbered step covering the local-model end-to-end path. Keep it unchecked — Task 9 is when the box gets ticked.

**Files:**
- Modify: `docs/SMOKE.md`

- [ ] **Step 8.1: Append the local-model smoke step**

Open `docs/SMOKE.md`. After the final step in the "Context tracking footer" section (currently step 24, ending at line 42), append:

```markdown

## Local models (Ollama)

25. [ ] **Local model end-to-end.** With Ollama installed and `ollama pull
    llama3.2` complete, register the Llama 3.2 entry from
    `models.example.yaml` in your `models.yaml`, restart Starshp, pick
    "Llama 3.2 (local)" in a new conversation, send a short prompt.
    Confirm streaming, the Stop button, the context-footer HUD
    (input/output tokens populate, cached shows 0), and that stopping
    Ollama mid-session yields the `local_unreachable` error with the
    base URL interpolated into the message.
```

- [ ] **Step 8.2: Commit**

```bash
git add docs/SMOKE.md
git commit -m "docs(smoke): step 25 — local model end-to-end via Ollama

Covers streaming, Stop, context footer (cached=0), and the
local_unreachable error path."
```

---

## Task 9: macOS Manual Smoke Pass

This task is manual; no code changes. Execute the smoke checklist on macOS now that local-model support is in place, then tick the box.

- [ ] **Step 9.1: Install Ollama on macOS if not already present**

Run:
```bash
brew install ollama
brew services start ollama
ollama pull llama3.2
```

- [ ] **Step 9.2: Copy `models.example.yaml` and enable the Ollama entry**

Copy `models.example.yaml` into your app directory (`~/Library/Application Support/starshp_app/models.yaml`) if you have not already, then uncomment the Llama 3.2 entry.

- [ ] **Step 9.3: Build and launch Starshp**

Run:
```bash
wails dev
```
Expected: app launches without a setup notice if your app-directory keys / configs are correct. Verify the per-message picker now lists "Llama 3.2 (local)" alongside the cloud entries.

- [ ] **Step 9.4: Execute smoke step 25**

Follow the step verbatim from `docs/SMOKE.md` step 25. Confirm:
- Streaming works token-by-token.
- The Stop button cancels and persists the partial reply.
- The context-footer HUD shows non-zero input/output tokens and zero cached.
- Quitting Ollama (`brew services stop ollama`) and sending another turn yields the `local_unreachable` error message with `http://localhost:11434/v1` interpolated.
- Restarting Ollama (`brew services start ollama`) and retrying succeeds.

- [ ] **Step 9.5: Tick the smoke step and commit**

Open `docs/SMOKE.md` and change `25. [ ]` to `25. [x]`.

```bash
git add docs/SMOKE.md
git commit -m "docs(smoke): tick local-model end-to-end after macOS manual verification"
```

---

## Post-Plan Verification

- [ ] **Final full test run**

```bash
go test ./...
```
Expected: green.

- [ ] **Confirm working tree is clean**

```bash
git status
```
Expected: `nothing to commit, working tree clean`.

- [ ] **Inspect the commit graph**

```bash
git log --oneline -12
```
Expected: nine new commits (one per task, plus the smoke-tick), each with a clear `feat(...)`, `docs(...)`, or `chore(...)` prefix matching the staged scope.

---

## Reference index

- Spec: `docs/superpowers/specs/2026-06-04-local-oss-models-design.md`
- Reused streaming/usage path: `internal/provider/openai.go`
- Provider factory: `internal/provider/factory.go`
- Error normalization: `internal/provider/errors.go`
- Startup validation: `internal/appapi/validate.go`
- Chat orchestration error flow: `internal/chat/chat.go:71`, `internal/chat/chat.go:101`
- API send-path error remap site: `internal/appapi/api.go:115` (`SendMessage`)
- Backlog items queued for follow-up: `BACKLOG.md` Someday (auto-detect, test-connection button, curated starter models)
