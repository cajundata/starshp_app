# Gemini Text Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `provider: gemini` becomes the fourth first-class provider type — a persona pins a Gemini model exactly like any other model, with streaming, tool calls, usage/cache reporting, and normalized errors.

**Architecture:** A new `internal/provider/gemini.go` adapter implements the existing `ChatProvider.Stream` seam using Google's official pure-Go SDK (`google.golang.org/genai` v1.63.0, verified against module source). Everything above the seam (personas, mentions, baton-pass, overrides, streaming UI, Stop) is untouched. `provider.New` gains a `Keys` struct so the signature stops growing per provider family.

**Tech Stack:** Go 1.25, `google.golang.org/genai` (pure Go, no CGO), httptest SSE fakes for all tests.

**Spec:** `docs/superpowers/specs/2026-07-15-gemini-text-provider-design.md`

## Global Constraints

- Pure Go only — no CGO anywhere (cross-platform Windows + macOS requirement).
- Never modify anything under `internal/rag/{embedding,chunker,ragindex}/` (verbatim acctutor copies).
- `gofmt` all new/changed Go files before committing.
- Work directly on `master` (repo convention: fast-forward merges, no PR needed for solo cycles unless requested).
- All tests offline: fakes via `httptest`, never real API calls.
- Env var name is exactly `GEMINI_API_KEY`. Provider string is exactly `gemini`.
- No frontend or Wails-binding changes in this plan — no new bound methods exist, so `frontend/wailsjs/` must not change.
- Verified SDK facts (from `~/go/pkg/mod/google.golang.org/genai@v1.63.0` source): `NewClient(ctx, *ClientConfig)`; `ClientConfig{APIKey, Backend: genai.BackendGeminiAPI, HTTPOptions: genai.HTTPOptions{BaseURL}}`; `client.Models.GenerateContentStream(ctx, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error]`; `genai.Part{Text string, Thought bool, FunctionCall *genai.FunctionCall, FunctionResponse *genai.FunctionResponse}`; `genai.FunctionCall{ID, Name string, Args map[string]any}`; `genai.FunctionResponse{ID, Name string, Response map[string]any}`; `genai.FunctionDeclaration{Name, Description string, ParametersJsonSchema any}`; `genai.Tool{FunctionDeclarations []*genai.FunctionDeclaration}`; `genai.GenerateContentConfig{SystemInstruction *genai.Content, Tools []*genai.Tool}`; usage on `resp.UsageMetadata.{PromptTokenCount, CandidatesTokenCount, CachedContentTokenCount}` (int32); finish reasons `genai.FinishReasonStop` = `"STOP"`, `genai.FinishReasonMaxTokens` = `"MAX_TOKENS"`; `genai.APIError{Code int, Message, Status string}` with a **value receiver** `Error()`; roles `genai.RoleUser` = `"user"`, `genai.RoleModel` = `"model"`; the SDK POSTs to `{base}/v1beta/models/{model}:streamGenerateContent?alt=sse` with the key in the `x-goog-api-key` header, and parses `data: {json}` SSE frames.

---

### Task 1: Registry accepts `gemini`, rejects `base_url`/`api_key_env` on it

**Files:**
- Modify: `internal/provider/registry.go`
- Test: `internal/provider/registry_test.go`

**Interfaces:**
- Consumes: existing `LoadRegistry(path string) (Registry, error)`.
- Produces: `LoadRegistry` accepts `provider: gemini` entries and returns an error if such an entry sets `base_url` or `api_key_env`. Later tasks rely on `ModelInfo.Provider == "gemini"` being a valid registry state.

- [ ] **Step 1: Write the failing tests**

Append to `internal/provider/registry_test.go` (match the file's existing test style — read it first; it writes a temp YAML and calls `LoadRegistry`):

```go
func TestLoadRegistryAcceptsGemini(t *testing.T) {
	p := writeRegistry(t, `models:
  - display: Gemini 3 Pro
    id: gemini-3-pro
    provider: gemini
    max_context: 1000000
`)
	r, err := LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	m, ok := r.ByID("gemini-3-pro")
	if !ok || m.Provider != "gemini" || m.MaxContext != 1000000 {
		t.Fatalf("ByID = %+v, %v; want gemini model with max_context 1000000", m, ok)
	}
}

func TestLoadRegistryRejectsBaseURLOnGemini(t *testing.T) {
	p := writeRegistry(t, `models:
  - display: Gemini 3 Pro
    id: gemini-3-pro
    provider: gemini
    base_url: http://localhost:1234/v1
`)
	if _, err := LoadRegistry(p); err == nil {
		t.Fatal("LoadRegistry accepted base_url on a gemini model; want error")
	}
}

func TestLoadRegistryRejectsAPIKeyEnvOnGemini(t *testing.T) {
	p := writeRegistry(t, `models:
  - display: Gemini 3 Pro
    id: gemini-3-pro
    provider: gemini
    api_key_env: MY_KEY
`)
	if _, err := LoadRegistry(p); err == nil {
		t.Fatal("LoadRegistry accepted api_key_env on a gemini model; want error")
	}
}
```

If `registry_test.go` has no `writeRegistry` helper, add one (temp dir + `os.WriteFile`, returning the path) following the file's existing conventions:

```go
func writeRegistry(t *testing.T, yaml string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "models.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write models.yaml: %v", err)
	}
	return p
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/provider/ -run TestLoadRegistry -v`
Expected: `TestLoadRegistryRejectsBaseURLOnGemini` and `TestLoadRegistryRejectsAPIKeyEnvOnGemini` FAIL (no validation exists yet; `gemini` falls through the switch untouched). `TestLoadRegistryAcceptsGemini` may already pass — that's fine.

- [ ] **Step 3: Implement the validation**

In `internal/provider/registry.go`, extend the switch inside `LoadRegistry`:

```go
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
		case "gemini":
			if m.BaseURL != "" {
				return Registry{}, fmt.Errorf("model %s: base_url is not allowed for provider gemini", m.ID)
			}
			if m.APIKeyEnv != "" {
				return Registry{}, fmt.Errorf("model %s: api_key_env is not allowed for provider gemini (set GEMINI_API_KEY)", m.ID)
			}
		}
	}
```

Also update the `ModelInfo.Provider` field comment in the same file:

```go
	Provider   string `yaml:"provider" json:"provider"` // "openai" | "anthropic" | "openai_compat" | "gemini"
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/provider/ -run TestLoadRegistry -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/registry.go internal/provider/registry_test.go
git commit -m "feat(provider): registry accepts gemini provider type"
```

---

### Task 2: `provider.New` takes a `Keys` struct (pure refactor)

**Files:**
- Modify: `internal/provider/factory.go`
- Modify: `internal/provider/factory_test.go:40,79,112` (the three `New(reg, ..., "", "")` call sites)
- Modify: `internal/appapi/api.go:249`
- Modify: `internal/eval/quality_test.go:78`

**Interfaces:**
- Consumes: existing `New(reg Registry, modelID, openAIKey, anthropicKey string)`.
- Produces: `type Keys struct { OpenAI, Anthropic, Gemini string }` and `New(reg Registry, modelID string, keys Keys) (ChatProvider, error)`. Task 6 adds the `case "gemini"` that reads `keys.Gemini`; Task 7 threads `cfg.GeminiAPIKey` into the appapi call site.

- [ ] **Step 1: Change the factory signature**

In `internal/provider/factory.go`, replace the `New` function's signature and key references (body logic otherwise unchanged):

```go
// Keys carries the per-provider-family API keys the factory needs. Fields
// may be empty; New errors only when the selected model's family lacks its
// key.
type Keys struct {
	OpenAI    string
	Anthropic string
	Gemini    string
}

// New builds the right provider for a model ID using the registry.
func New(reg Registry, modelID string, keys Keys) (ChatProvider, error) {
	m, ok := reg.ByID(modelID)
	if !ok {
		return nil, fmt.Errorf("unknown model: %s", modelID)
	}
	switch m.Provider {
	case "openai":
		if keys.OpenAI == "" {
			return nil, AppError{"auth", "OpenAI API key not set.", false}
		}
		return NewOpenAI(keys.OpenAI, ""), nil
	case "anthropic":
		if keys.Anthropic == "" {
			return nil, AppError{"auth", "Anthropic API key not set.", false}
		}
		return NewAnthropic(keys.Anthropic, ""), nil
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
```

- [ ] **Step 2: Update all call sites**

`internal/provider/factory_test.go` — the three calls (lines ~40, ~79, ~112) become:

```go
	p, err := New(reg, "llama3.2", Keys{})
```

(same for `"qwen2.5"` and `"custom"` — empty `Keys{}` replaces the two `""` args).

`internal/appapi/api.go:249`:

```go
	prov, err := provider.New(a.reg, p.Model, provider.Keys{
		OpenAI:    a.cfg.OpenAIAPIKey,
		Anthropic: a.cfg.AnthropicAPIKey,
	})
```

(`Gemini:` is added in Task 7 when the config field exists.)

`internal/eval/quality_test.go:78`:

```go
			prov, err := provider.New(preg, modelID, provider.Keys{
				OpenAI:    cfg.OpenAIAPIKey,
				Anthropic: cfg.AnthropicAPIKey,
			})
```

- [ ] **Step 3: Run the full suite to verify the refactor is behavior-neutral**

Run: `go test ./...`
Expected: all PASS (this is a pure signature refactor; any failure means a missed call site — `grep -rn 'provider\.New(\|New(reg' --include='*.go' .` to find it).

- [ ] **Step 4: Commit**

```bash
git add internal/provider/factory.go internal/provider/factory_test.go internal/appapi/api.go internal/eval/quality_test.go
git commit -m "refactor(provider): factory takes a Keys struct instead of positional keys"
```

---

### Task 3: Gemini request mapping — events → contents, tools → declarations

**Files:**
- Create: `internal/provider/gemini.go`
- Test: `internal/provider/gemini_test.go`
- Modify: `go.mod` / `go.sum` (new dependency)

**Interfaces:**
- Consumes: `provider.Event`, `provider.ToolDef` (from `provider.go`, shown in the code below).
- Produces: `geminiContentsFromEvents(events []Event) []*genai.Content` and `buildGeminiTools(tools []ToolDef) []*genai.Tool` — pure functions Task 4's `Stream` calls. Also `NewGemini(apiKey, baseURL string) ChatProvider` (constructor only; `Stream` body lands in Task 4).

- [ ] **Step 1: Add the dependency**

```bash
go get google.golang.org/genai@v1.63.0
go mod tidy
```

Expected: `go.mod` gains `google.golang.org/genai v1.63.0` (plus indirect grpc/protobuf entries). All pure Go.

- [ ] **Step 2: Write the failing tests**

Create `internal/provider/gemini_test.go`:

```go
package provider

import (
	"encoding/json"
	"testing"

	"google.golang.org/genai"
)

func TestGeminiContentsFromEvents(t *testing.T) {
	events := []Event{
		{Kind: "user_message", Text: "add 2+2"},
		{Kind: "assistant_text", Text: "Let me compute."},
		{Kind: "assistant_tool_call", ToolCallID: "c1", ToolName: "safe_math", ToolInput: json.RawMessage(`{"expr":"2+2"}`)},
		{Kind: "tool_result", ToolCallID: "c1", ToolName: "safe_math", Text: "4"},
		{Kind: "assistant_text", Text: "It is 4."},
	}
	got := geminiContentsFromEvents(events)

	// user / model / user(functionResponse) / model — consecutive same-role
	// parts merge into one Content.
	if len(got) != 4 {
		t.Fatalf("len(contents) = %d, want 4", len(got))
	}
	if got[0].Role != genai.RoleUser || got[0].Parts[0].Text != "add 2+2" {
		t.Fatalf("contents[0] = %+v, want user text", got[0])
	}
	if got[1].Role != genai.RoleModel || len(got[1].Parts) != 2 {
		t.Fatalf("contents[1] = %+v, want model with text + functionCall parts", got[1])
	}
	fc := got[1].Parts[1].FunctionCall
	if fc == nil || fc.Name != "safe_math" || fc.Args["expr"] != "2+2" {
		t.Fatalf("functionCall = %+v, want safe_math{expr:2+2}", fc)
	}
	fr := got[2].Parts[0].FunctionResponse
	if got[2].Role != genai.RoleUser || fr == nil || fr.Name != "safe_math" || fr.Response["output"] != "4" {
		t.Fatalf("contents[2] = %+v, want user functionResponse output=4", got[2])
	}
	if got[3].Role != genai.RoleModel || got[3].Parts[0].Text != "It is 4." {
		t.Fatalf("contents[3] = %+v, want model text", got[3])
	}
}

func TestGeminiContentsFromEventsErrorResult(t *testing.T) {
	events := []Event{
		{Kind: "assistant_tool_call", ToolCallID: "c1", ToolName: "safe_math", ToolInput: json.RawMessage(`{}`)},
		{Kind: "tool_result", ToolCallID: "c1", ToolName: "safe_math", Text: "divide by zero", IsError: true},
	}
	got := geminiContentsFromEvents(events)
	if len(got) != 2 {
		t.Fatalf("len(contents) = %d, want 2", len(got))
	}
	fr := got[1].Parts[0].FunctionResponse
	if fr == nil || fr.Response["error"] != "divide by zero" {
		t.Fatalf("error result = %+v, want Response[error]", fr)
	}
}

func TestBuildGeminiTools(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"expr":{"type":"string"}},"required":["expr"]}`)
	tools := buildGeminiTools([]ToolDef{{Name: "safe_math", Description: "evaluate", InputSchema: schema}})
	if len(tools) != 1 || len(tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools = %+v, want one Tool with one declaration", tools)
	}
	d := tools[0].FunctionDeclarations[0]
	if d.Name != "safe_math" || d.Description != "evaluate" || d.ParametersJsonSchema == nil {
		t.Fatalf("declaration = %+v", d)
	}
	if buildGeminiTools(nil) != nil {
		t.Fatal("buildGeminiTools(nil) should be nil")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/provider/ -run 'TestGeminiContents|TestBuildGeminiTools' -v`
Expected: FAIL to compile — `geminiContentsFromEvents` and `buildGeminiTools` undefined.

- [ ] **Step 4: Implement the mapping**

Create `internal/provider/gemini.go`:

```go
package provider

import (
	"context"
	"encoding/json"

	"google.golang.org/genai"
)

type geminiProvider struct {
	apiKey  string
	baseURL string
}

// NewGemini builds a Gemini provider. baseURL may be empty for the default
// endpoint (tests pass an httptest URL).
func NewGemini(apiKey, baseURL string) ChatProvider {
	return &geminiProvider{apiKey: apiKey, baseURL: baseURL}
}

// Stream is implemented in Task 4. This stub keeps the package compiling
// until then.
func (p *geminiProvider) Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error) {
	panic("not implemented")
}

// geminiContentsFromEvents assembles Gemini contents from the canonical
// Event timeline. Gemini matches function responses by name (not call ID),
// so ToolCallID is dropped on the wire — the store keeps it authoritative.
// Consecutive same-role events merge into one Content with multiple parts.
func geminiContentsFromEvents(events []Event) []*genai.Content {
	var out []*genai.Content
	appendPart := func(role string, part *genai.Part) {
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Parts = append(out[n-1].Parts, part)
			return
		}
		out = append(out, &genai.Content{Role: role, Parts: []*genai.Part{part}})
	}
	for _, e := range events {
		switch e.Kind {
		case "user_message":
			appendPart(genai.RoleUser, genai.NewPartFromText(e.Text))
		case "assistant_text":
			appendPart(genai.RoleModel, genai.NewPartFromText(e.Text))
		case "assistant_tool_call":
			var args map[string]any
			if len(e.ToolInput) > 0 {
				_ = json.Unmarshal(e.ToolInput, &args)
			}
			appendPart(genai.RoleModel, &genai.Part{
				FunctionCall: &genai.FunctionCall{Name: e.ToolName, Args: args},
			})
		case "tool_result":
			resp := map[string]any{"output": e.Text}
			if e.IsError {
				resp = map[string]any{"error": e.Text}
			}
			appendPart(genai.RoleUser, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{Name: e.ToolName, Response: resp},
			})
		}
	}
	return out
}

// buildGeminiTools converts the tool catalog to functionDeclarations,
// passing our JSON Schema through the SDK's raw-schema field.
func buildGeminiTools(tools []ToolDef) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		var schema any
		if len(t.InputSchema) > 0 {
			_ = json.Unmarshal(t.InputSchema, &schema)
		}
		decls = append(decls, &genai.FunctionDeclaration{
			Name:                 t.Name,
			Description:          t.Description,
			ParametersJsonSchema: schema,
		})
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/provider/ -run 'TestGeminiContents|TestBuildGeminiTools' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/provider/gemini.go internal/provider/gemini_test.go
git commit -m "feat(provider): gemini event->content mapping and tool declarations"
```

---

### Task 4: Gemini streaming — text, system instruction, usage, finish reasons

**Files:**
- Modify: `internal/provider/gemini.go` (replace the `Stream` stub)
- Test: `internal/provider/gemini_test.go`

**Interfaces:**
- Consumes: `geminiContentsFromEvents`, `buildGeminiTools` (Task 3), `ChatRequest`/`Delta`/`Usage` (`provider.go`).
- Produces: a working `(*geminiProvider).Stream(ctx, ChatRequest) (<-chan Delta, error)` — text deltas, terminal `Delta{Done: true, StopReason, Usage}`. Tool-call frames land in Task 5.

- [ ] **Step 1: Write the failing tests**

The fake server speaks the wire format the SDK actually parses: SSE frames of `GenerateContentResponse` JSON on a `POST …/v1beta/models/{model}:streamGenerateContent?alt=sse` request, key in the `x-goog-api-key` header. Append to `internal/provider/gemini_test.go` (add `"context"`, `"fmt"`, `"net/http"`, `"net/http/httptest"`, `"strings"` imports):

```go
// newGeminiFake serves canned SSE frames and captures the request body.
func newGeminiFake(t *testing.T, frames []string, gotBody *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, ":streamGenerateContent") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if k := r.Header.Get("x-goog-api-key"); k != "test-key" {
			t.Errorf("x-goog-api-key = %q, want test-key", k)
		}
		if gotBody != nil {
			b, _ := io.ReadAll(r.Body)
			*gotBody = b
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\r\n\r\n", f)
			fl.Flush()
		}
	}))
}

func TestGeminiStreamText(t *testing.T) {
	var body []byte
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Hel"}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":45,"cachedContentTokenCount":80}}`,
	}, &body)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model:  "gemini-3-pro",
		System: "You are helpful.",
		Events: []Event{{Kind: "user_message", Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var sb strings.Builder
	var final Delta
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("delta err: %v", d.Err)
		}
		sb.WriteString(d.Text)
		if d.Done {
			final = d
		}
	}
	if sb.String() != "Hello" {
		t.Fatalf("assembled = %q, want %q", sb.String(), "Hello")
	}
	if final.StopReason != "end_turn" {
		t.Fatalf("StopReason = %q, want end_turn", final.StopReason)
	}
	if final.Usage == nil || final.Usage.InputTokens != 120 || final.Usage.OutputTokens != 45 || final.Usage.CachedInputTokens != 80 {
		t.Fatalf("Usage = %+v, want {120 45 80}", final.Usage)
	}

	// The posted request must carry systemInstruction and the user content.
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	if _, ok := req["systemInstruction"]; !ok {
		t.Fatalf("request lacks systemInstruction: %s", body)
	}
}

func TestGeminiStreamGroundingConcatenatedIntoSystem(t *testing.T) {
	var body []byte
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`,
	}, &body)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model:     "gemini-3-pro",
		System:    "SYS.",
		Grounding: "GROUND.",
		Events:    []Event{{Kind: "user_message", Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}
	s := string(body)
	if !strings.Contains(s, "SYS.") || !strings.Contains(s, "GROUND.") {
		t.Fatalf("system+grounding not in request: %s", s)
	}
}

func TestGeminiStreamMaxTokensStopReason(t *testing.T) {
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"x"}]},"finishReason":"MAX_TOKENS"}]}`,
	}, nil)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model:  "gemini-3-pro",
		Events: []Event{{Kind: "user_message", Text: "hi"}},
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
	if final.StopReason != "max_tokens" {
		t.Fatalf("StopReason = %q, want max_tokens", final.StopReason)
	}
}

func TestGeminiStreamLegacyMessagesFallback(t *testing.T) {
	var body []byte
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`,
	}, &body)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model:        "gemini-3-pro",
		CachedPrefix: "You are helpful.",
		Messages:     []Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "yes?"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}
	s := string(body)
	if !strings.Contains(s, `"hi"`) || !strings.Contains(s, `"yes?"`) || !strings.Contains(s, "You are helpful.") {
		t.Fatalf("legacy fallback request missing content: %s", s)
	}
}
```

Add `"io"` to the test file's imports for `io.ReadAll`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/provider/ -run TestGeminiStream -v`
Expected: PANIC "not implemented" (the Task 3 stub).

- [ ] **Step 3: Implement Stream**

Replace the stub in `internal/provider/gemini.go` (new imports: `"fmt"`, `"crypto/rand"`, `"encoding/hex"` — the latter two are used by Task 5's ID synthesis but add them now with `geminiCallID` below):

```go
func (p *geminiProvider) Stream(ctx context.Context, req ChatRequest) (<-chan Delta, error) {
	cc := &genai.ClientConfig{APIKey: p.apiKey, Backend: genai.BackendGeminiAPI}
	if p.baseURL != "" {
		cc.HTTPOptions.BaseURL = p.baseURL
	}
	client, err := genai.NewClient(ctx, cc)
	if err != nil {
		return nil, err
	}

	var contents []*genai.Content
	if len(req.Events) > 0 {
		contents = geminiContentsFromEvents(req.Events)
	} else {
		contents = make([]*genai.Content, 0, len(req.Messages))
		for _, m := range req.Messages {
			role := genai.RoleUser
			if m.Role == "assistant" {
				role = genai.RoleModel
			}
			contents = append(contents, genai.NewContentFromText(m.Content, genai.Role(role)))
		}
	}

	cfg := &genai.GenerateContentConfig{}
	sys := req.System
	if sys == "" {
		sys = req.CachedPrefix
	}
	if req.Grounding != "" {
		if sys != "" {
			sys += "\n\n"
		}
		sys += req.Grounding
	}
	if sys != "" {
		cfg.SystemInstruction = genai.NewContentFromText(sys, genai.RoleUser)
	}
	if tools := buildGeminiTools(req.Tools); len(tools) > 0 {
		cfg.Tools = tools
	}

	out := make(chan Delta)
	go func() {
		defer close(out)
		var (
			usage       Usage
			haveUsage   bool
			stopReason  string
			sawToolCall bool
		)
		for resp, serr := range client.Models.GenerateContentStream(ctx, req.Model, contents, cfg) {
			if serr != nil {
				out <- Delta{Done: true, Err: serr}
				return
			}
			if u := resp.UsageMetadata; u != nil {
				usage.InputTokens = int(u.PromptTokenCount)
				usage.OutputTokens = int(u.CandidatesTokenCount)
				usage.CachedInputTokens = int(u.CachedContentTokenCount)
				haveUsage = true
			}
			if len(resp.Candidates) == 0 {
				continue
			}
			cand := resp.Candidates[0]
			if cand.Content != nil {
				for _, part := range cand.Content.Parts {
					switch {
					case part.FunctionCall != nil:
						fc := part.FunctionCall
						input := json.RawMessage("{}")
						if len(fc.Args) > 0 {
							b, merr := json.Marshal(fc.Args)
							if merr != nil {
								out <- Delta{Done: true, Err: fmt.Errorf("gemini: functionCall args for %s: %w", fc.Name, merr)}
								return
							}
							input = b
						}
						id := fc.ID
						if id == "" {
							id = geminiCallID()
						}
						sawToolCall = true
						select {
						case out <- Delta{ToolCall: &ToolCall{ID: id, Name: fc.Name, Input: input}}:
						case <-ctx.Done():
							return
						}
					case part.Text != "" && !part.Thought:
						select {
						case out <- Delta{Text: part.Text}:
						case <-ctx.Done():
							return
						}
					}
				}
			}
			if cand.FinishReason != "" {
				switch cand.FinishReason {
				case genai.FinishReasonStop:
					stopReason = "end_turn"
				case genai.FinishReasonMaxTokens:
					stopReason = "max_tokens"
				default:
					stopReason = "error"
				}
			}
		}
		if sawToolCall {
			stopReason = "tool_use"
		}
		final := Delta{Done: true, StopReason: stopReason}
		if haveUsage {
			u := usage
			final.Usage = &u
		}
		out <- final
	}()
	return out, nil
}

// geminiCallID synthesizes a unique tool-call ID. Gemini matches function
// responses by name and usually omits IDs, but the shared event log needs
// IDs unique across the whole conversation (the Anthropic replay path
// dedupes results by ID), so a fixed counter would collide across turns.
func geminiCallID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "gemcall_" + hex.EncodeToString(b[:])
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/provider/ -run TestGemini -v`
Expected: all PASS.

- [ ] **Step 5: Run gofmt and the package suite**

Run: `gofmt -l internal/provider/ && go test ./internal/provider/`
Expected: gofmt prints nothing; tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/provider/gemini.go internal/provider/gemini_test.go
git commit -m "feat(provider): gemini streaming adapter — text, usage, finish reasons"
```

---

### Task 5: Gemini tool-call round trip and cancellation

**Files:**
- Test: `internal/provider/gemini_test.go` (implementation already landed in Task 4; this task proves the tool-call and cancellation paths and fixes anything they surface)

**Interfaces:**
- Consumes: `(*geminiProvider).Stream`, `geminiCallID` (Task 4).
- Produces: verified guarantees later code relies on: `Delta.ToolCall` frames carry unique non-empty IDs; terminal frame after tool calls has `StopReason == "tool_use"`; a canceled `ctx` terminates the stream promptly with a closed channel.

- [ ] **Step 1: Write the tests**

Append to `internal/provider/gemini_test.go`:

```go
func TestGeminiStreamToolCall(t *testing.T) {
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"safe_math","args":{"expr":"2+2"}}},{"functionCall":{"name":"safe_math","args":{"expr":"3+3"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`,
	}, nil)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model:  "gemini-3-pro",
		Tools:  []ToolDef{{Name: "safe_math", Description: "evaluate", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Events: []Event{{Kind: "user_message", Text: "add"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var calls []*ToolCall
	var final Delta
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("delta err: %v", d.Err)
		}
		if d.ToolCall != nil {
			calls = append(calls, d.ToolCall)
		}
		if d.Done {
			final = d
		}
	}
	if len(calls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(calls))
	}
	if calls[0].ID == "" || calls[1].ID == "" || calls[0].ID == calls[1].ID {
		t.Fatalf("IDs not unique/non-empty: %q, %q", calls[0].ID, calls[1].ID)
	}
	if calls[0].Name != "safe_math" || string(calls[0].Input) != `{"expr":"2+2"}` {
		t.Fatalf("call[0] = %+v", calls[0])
	}
	if final.StopReason != "tool_use" {
		t.Fatalf("StopReason = %q, want tool_use", final.StopReason)
	}
}

func TestGeminiStreamToolResultRoundTrip(t *testing.T) {
	// A replay containing a tool call + result must POST functionCall and
	// functionResponse parts back to the API.
	var body []byte
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"4"}]},"finishReason":"STOP"}]}`,
	}, &body)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "gemini-3-pro",
		Events: []Event{
			{Kind: "user_message", Text: "add 2+2"},
			{Kind: "assistant_tool_call", ToolCallID: "c1", ToolName: "safe_math", ToolInput: json.RawMessage(`{"expr":"2+2"}`)},
			{Kind: "tool_result", ToolCallID: "c1", ToolName: "safe_math", Text: "4"},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}
	s := string(body)
	if !strings.Contains(s, `"functionCall"`) || !strings.Contains(s, `"functionResponse"`) {
		t.Fatalf("request lacks functionCall/functionResponse: %s", s)
	}
}

func TestGeminiStreamCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\r\n\r\n", `{"candidates":[{"content":{"role":"model","parts":[{"text":"first"}]}}]}`)
		fl.Flush()
		select {
		case <-r.Context().Done(): // client hung up
		case <-release: // safety valve so the test can't wedge
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	p := NewGemini("test-key", srv.URL)
	ch, err := p.Stream(ctx, ChatRequest{
		Model:  "gemini-3-pro",
		Events: []Event{{Kind: "user_message", Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	first := <-ch
	if first.Text != "first" {
		t.Fatalf("first delta = %+v, want text 'first'", first)
	}
	cancel()
	// The channel must terminate (either a Done frame with a ctx error, or
	// simply closing). Drain with a timeout guard.
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate after cancel")
	}
}
```

Add `"time"` to the test imports.

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/provider/ -run 'TestGeminiStreamTool|TestGeminiStreamCancellation' -v`
Expected: PASS (Task 4's implementation already covers these paths). If any fail, fix `Stream` — the contract in this task's Interfaces block is what must hold, and these tests are the arbiter.

- [ ] **Step 3: Commit**

```bash
git add internal/provider/gemini_test.go
git commit -m "test(provider): gemini tool-call round trip, unique IDs, cancellation"
```

---

### Task 6: Error normalization for the Gemini vocabulary

**Files:**
- Modify: `internal/provider/errors.go`
- Test: `internal/provider/errors_test.go`

**Interfaces:**
- Consumes: `genai.APIError` (value-receiver `Error()`), existing `NormalizeError(error) AppError`.
- Produces: `NormalizeError` maps Gemini failures to the existing codes (`auth`, `rate_limit`, `context_length`, `network`) — structured `APIError` HTTP code first, string sniffing as fallback. No new codes.

- [ ] **Step 1: Write the failing tests**

Append to `internal/provider/errors_test.go` (match its existing table/case style — read it first; add `"fmt"` and `"google.golang.org/genai"` imports if absent):

```go
func TestNormalizeErrorGeminiAPIErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"api key invalid 400", genai.APIError{Code: 400, Message: "API key not valid. Please pass a valid API key.", Status: "INVALID_ARGUMENT"}, "auth"},
		{"permission denied 403", genai.APIError{Code: 403, Message: "permission denied", Status: "PERMISSION_DENIED"}, "auth"},
		{"unauthenticated 401", genai.APIError{Code: 401, Message: "unauthenticated", Status: "UNAUTHENTICATED"}, "auth"},
		{"resource exhausted 429", genai.APIError{Code: 429, Message: "quota exceeded", Status: "RESOURCE_EXHAUSTED"}, "rate_limit"},
		{"token overflow 400", genai.APIError{Code: 400, Message: "The input token count (2000001) exceeds the maximum number of tokens allowed (2000000).", Status: "INVALID_ARGUMENT"}, "context_length"},
		{"wrapped api error", fmt.Errorf("send: %w", genai.APIError{Code: 429, Message: "slow down", Status: "RESOURCE_EXHAUSTED"}), "rate_limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeError(tc.err); got.Code != tc.code {
				t.Fatalf("NormalizeError(%v).Code = %q, want %q", tc.err, got.Code, tc.code)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/provider/ -run TestNormalizeErrorGemini -v`
Expected: FAIL — the 403 case maps to `unknown`, the "API key not valid" 400 case maps to `unknown`, `RESOURCE_EXHAUSTED` without "429"/"rate limit" text maps to `unknown`, token-overflow maps to `unknown`. (Some cases may pass incidentally via existing substring matches; the failures prove the gap.)

- [ ] **Step 3: Implement**

In `internal/provider/errors.go`, add imports `"errors"` and `"google.golang.org/genai"`, and insert a structured check at the top of `NormalizeError`, before the string sniffing:

```go
func NormalizeError(err error) AppError {
	if err == nil {
		return AppError{}
	}
	// Structured Gemini errors first — HTTP code beats substring guessing.
	var gae genai.APIError
	if errors.As(err, &gae) {
		msg := strings.ToLower(gae.Message)
		switch {
		case gae.Code == 401 || gae.Code == 403 || strings.Contains(msg, "api key not valid"):
			return AppError{"auth", "Invalid or missing API key. Check your .env file.", false}
		case gae.Code == 429:
			return AppError{"rate_limit", "Rate limited by the provider. Wait a moment and retry.", true}
		case strings.Contains(msg, "exceeds the maximum number of tokens"):
			return AppError{"context_length", "Too much context. Trim the attached textbook scope and retry.", false}
		}
		// Other API errors fall through to the generic string sniffing below.
	}
	s := strings.ToLower(err.Error())
	// ... existing switch unchanged ...
```

Then extend two cases of the existing switch so plain-text Gemini errors (e.g. surfaced through a wrapped non-APIError path) still map:

```go
	case strings.Contains(s, "401") || strings.Contains(s, "unauthorized") || strings.Contains(s, "invalid api key") ||
		strings.Contains(s, "api key not valid") || strings.Contains(s, "permission_denied") || strings.Contains(s, "permission denied"):
		return AppError{"auth", "Invalid or missing API key. Check your .env file.", false}
	case strings.Contains(s, "429") || strings.Contains(s, "rate limit") || strings.Contains(s, "resource_exhausted") || strings.Contains(s, "resource exhausted"):
		return AppError{"rate_limit", "Rate limited by the provider. Wait a moment and retry.", true}
	case strings.Contains(s, "context length") || strings.Contains(s, "maximum context") || strings.Contains(s, "exceeds the maximum number of tokens"):
		return AppError{"context_length", "Too much context. Trim the attached textbook scope and retry.", false}
```

(The `network` case and `MaybeRemapLocal` are untouched — `local_unreachable` stays `openai_compat`-only.)

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/provider/ -run TestNormalizeError -v`
Expected: all PASS, including the pre-existing NormalizeError tests.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/errors.go internal/provider/errors_test.go
git commit -m "feat(provider): normalize gemini error vocabulary"
```

---

### Task 7: Wire it up — factory case, config key, startup validation

**Files:**
- Modify: `internal/provider/factory.go`
- Modify: `internal/config/config.go`
- Modify: `internal/appapi/api.go` (the `provider.New` call from Task 2)
- Modify: `internal/appapi/validate.go`
- Test: `internal/provider/factory_test.go`, `internal/appapi/validate_test.go`

**Interfaces:**
- Consumes: `Keys` (Task 2), `NewGemini` (Task 3), registry `gemini` entries (Task 1).
- Produces: `New` returns the Gemini adapter for `provider: gemini` models (auth `AppError` when `keys.Gemini` is empty); `config.Config.GeminiAPIKey string` loaded from `GEMINI_API_KEY`; `ValidateStartup` flags a missing key only when the registry holds a `gemini` model.

- [ ] **Step 1: Write the failing factory tests**

Append to `internal/provider/factory_test.go` (mirror the file's existing style):

```go
func TestNewGeminiFromRegistry(t *testing.T) {
	reg := Registry{Models: []ModelInfo{{ID: "gemini-3-pro", Provider: "gemini"}}}
	p, err := New(reg, "gemini-3-pro", Keys{Gemini: "k"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p == nil {
		t.Fatal("New returned nil provider")
	}
}

func TestNewGeminiRequiresKey(t *testing.T) {
	reg := Registry{Models: []ModelInfo{{ID: "gemini-3-pro", Provider: "gemini"}}}
	_, err := New(reg, "gemini-3-pro", Keys{})
	ae, ok := err.(AppError)
	if !ok || ae.Code != "auth" {
		t.Fatalf("err = %v, want AppError{auth}", err)
	}
}
```

- [ ] **Step 2: Write the failing validation tests**

Append to `internal/appapi/validate_test.go`:

```go
func TestValidateStartupRequiresGeminiKeyWhenGeminiModelRegistered(t *testing.T) {
	c, _ := goodConfig(t)
	reg := provider.Registry{Models: []provider.ModelInfo{
		{ID: "gemini-3-pro", Provider: "gemini"},
	}}
	issues := ValidateStartup(c, reg)
	if !hasIssueMentioning(issues, "GEMINI_API_KEY") {
		t.Fatalf("issues = %v, want GEMINI_API_KEY complaint", issues)
	}
	c.GeminiAPIKey = "k"
	issues = ValidateStartup(c, reg)
	if hasIssueMentioning(issues, "GEMINI_API_KEY") {
		t.Fatalf("issues = %v, key is set — no complaint expected", issues)
	}
}

func TestValidateStartupSkipsGeminiKeyWithoutGeminiModels(t *testing.T) {
	c, _ := goodConfig(t)
	reg := provider.Registry{Models: []provider.ModelInfo{
		{ID: "claude-opus-4-7", Provider: "anthropic"},
	}}
	c.AnthropicAPIKey = "k"
	issues := ValidateStartup(c, reg)
	if hasIssueMentioning(issues, "GEMINI_API_KEY") {
		t.Fatalf("issues = %v, no gemini model — no complaint expected", issues)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/provider/ -run TestNewGemini -v && go test ./internal/appapi/ -run TestValidateStartup -v`
Expected: FAIL — factory returns "unsupported provider: gemini"; `c.GeminiAPIKey` does not compile yet.

- [ ] **Step 4: Implement**

`internal/provider/factory.go` — add the case before `default`:

```go
	case "gemini":
		if keys.Gemini == "" {
			return nil, AppError{"auth", "Gemini API key not set.", false}
		}
		return NewGemini(keys.Gemini, ""), nil
```

`internal/config/config.go` — add the field after `AnthropicAPIKey`:

```go
	GeminiAPIKey       string
```

and the load line after the `AnthropicAPIKey` line in `Load`:

```go
		GeminiAPIKey:       strings.TrimSpace(os.Getenv("GEMINI_API_KEY")),
```

`internal/appapi/validate.go` — after the Anthropic check in `ValidateStartup`:

```go
	if needsGeminiKey(reg) && c.GeminiAPIKey == "" {
		issues = append(issues, "GEMINI_API_KEY is not set (required for the registered Gemini model).")
	}
```

and alongside `needsAnthropicKey`:

```go
func needsGeminiKey(reg provider.Registry) bool {
	for _, m := range reg.Models {
		if m.Provider == "gemini" {
			return true
		}
	}
	return false
}
```

`internal/appapi/api.go` — complete the Task 2 call site:

```go
	prov, err := provider.New(a.reg, p.Model, provider.Keys{
		OpenAI:    a.cfg.OpenAIAPIKey,
		Anthropic: a.cfg.AnthropicAPIKey,
		Gemini:    a.cfg.GeminiAPIKey,
	})
```

- [ ] **Step 5: Run the full suite**

Run: `go test ./...`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/provider/factory.go internal/provider/factory_test.go internal/config/config.go internal/appapi/validate.go internal/appapi/validate_test.go internal/appapi/api.go
git commit -m "feat(appapi): wire gemini — factory case, GEMINI_API_KEY, startup validation"
```

---

### Task 8: Examples, README, and smoke checklist

**Files:**
- Modify: `models.example.yaml`
- Modify: `.env.example`
- Modify: `README.md`
- Modify: `docs/SMOKE.md`

**Interfaces:**
- Consumes: everything shipped in Tasks 1–7.
- Produces: operator-facing docs. No code.

- [ ] **Step 1: models.example.yaml**

Update the `provider` comment line to include gemini:

```yaml
#   provider    — "anthropic", "openai", "gemini", or "openai_compat" (Ollama /
#                 LM Studio / vLLM / any OpenAI-Chat-Completions-compatible
#                 local server; also set base_url, and optionally api_key_env)
```

Add after the GPT-5.4 entry (before the Ollama comment block):

```yaml
  - display: Gemini 3 Pro
    id: gemini-3-pro
    provider: gemini
    max_context: 1000000
  - display: Gemini 3 Flash
    id: gemini-3-flash
    provider: gemini
    max_context: 1000000
```

(Model IDs are operator-editable with no recompile — if Google has since renamed them, the operator edits the `id:` lines, same contract as every other family.)

- [ ] **Step 2: .env.example**

After the `ANTHROPIC_API_KEY` line:

```
GEMINI_API_KEY=your-gemini-key-here
```

- [ ] **Step 3: README.md**

In **Prerequisites**, after the Anthropic bullet:

```markdown
- **Gemini API key** — optional, required only to chat with Gemini models
  listed in `models.yaml`.
```

In the **`models.yaml`** section, update the provider comment in the YAML example (`# "anthropic", "openai", "gemini", or "openai_compat"`).

In the **Configuration reference** table, after the `ANTHROPIC_API_KEY` row:

```markdown
| `GEMINI_API_KEY` | — | Gemini key (chat only). |
```

- [ ] **Step 4: docs/SMOKE.md**

Append six steps after step 64, matching the file's numbered-checkbox format (`NN. [ ] **Title.** body`):

```markdown
65. [ ] **A Gemini persona streams.** Add a persona pinned to a `gemini`
    model (or edit one), set `GEMINI_API_KEY`, restart. Send a message —
    tokens stream live and the reply carries the persona's name, color, and
    model chip.
66. [ ] **Stop persists the partial.** Send a long-form prompt to the Gemini
    persona, hit Stop mid-stream — the partial reply persists and survives a
    conversation switch and back.
67. [ ] **A Gemini persona calls tools.** With a Gemini persona whose
    `tools:` allows `safe_math` (or omits the whitelist), ask "use the
    safe_math tool to compute 12*34+56/7". The tool activity renders and the
    final answer is correct.
68. [ ] **Mention routing and the baton.** In a conversation pinned to a
    non-Gemini persona, send `@<gemini-persona-id> summarize the thread so
    far`. That turn is answered by the Gemini persona; the next unmentioned
    turn returns to the pinned persona and receives the attributed
    `From <Name> (<model>):` block.
69. [ ] **Footer counts look sane.** After a few Gemini turns, the context
    footer shows nonzero input/output tokens against the model's
    `max_context` denominator; on a repeated turn, cached tokens are
    plausible (Gemini implicit caching — may be 0 on small prompts, never
    negative or absurd).
70. [ ] **Missing key is a banner, not a crash.** Remove `GEMINI_API_KEY`
    from `.env`, restart with a `gemini` model still in `models.yaml` — the
    setup banner lists GEMINI_API_KEY; chatting with a non-Gemini persona
    still works.
```

- [ ] **Step 5: Verify docs build nothing broken (link check by eye) and commit**

Run: `go test ./... && gofmt -l internal/`
Expected: tests PASS; gofmt prints nothing (the `internal/rag/{chunker,embedding,ragindex}` gofmt drift is permanent and pre-existing — do NOT touch those files; `gofmt -l internal/` may list them, which is expected and ignored).

```bash
git add models.example.yaml .env.example README.md docs/SMOKE.md
git commit -m "docs: gemini setup — examples, README, smoke steps 65-70"
```

---

### Task 9: Final verification gate

**Files:** none (verification only)

- [ ] **Step 1: Full suite + build**

Run: `go test ./... && wails build`
Expected: all tests PASS; release binary builds. If `wails build` regenerated `frontend/wailsjs/` bindings with mode 755, run `chmod 644` on them and confirm `git status` shows no binding content changes (this plan adds no bound methods — a content diff there is a bug).

- [ ] **Step 2: Live smoke (operator)**

Run SMOKE.md steps 65–70 on this machine against the real Gemini API (needs a real `GEMINI_API_KEY`), then on the other platform before calling the cycle done. Check the boxes in `docs/SMOKE.md` as they pass and commit the checklist update:

```bash
git add docs/SMOKE.md
git commit -m "docs(smoke): gemini provider verified in-app — steps 65-70 pass"
```

- [ ] **Step 3: Push**

```bash
git push origin master
```
