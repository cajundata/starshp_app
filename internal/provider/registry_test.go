package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRegistryWithMaxContext(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "models.yaml")
	os.WriteFile(p, []byte(`models:
  - display: Claude Opus 4.7
    id: claude-opus-4-7
    provider: anthropic
    max_context: 200000
  - display: GPT-5.4
    id: gpt-5.4-2026-03-05
    provider: openai
`), 0o600)
	reg, err := LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	claude, _ := reg.ByID("claude-opus-4-7")
	if claude.MaxContext != 200000 {
		t.Fatalf("MaxContext = %d, want 200000", claude.MaxContext)
	}
	gpt, _ := reg.ByID("gpt-5.4-2026-03-05")
	if gpt.MaxContext != 0 {
		t.Fatalf("omitted max_context should yield 0, got %d", gpt.MaxContext)
	}
}

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
	qwen, ok := reg.ByID("qwen2.5")
	if !ok {
		t.Fatal("qwen2.5 not in registry")
	}
	if qwen.APIKeyEnv != "LM_STUDIO_TOKEN" {
		t.Errorf("qwen.APIKeyEnv = %q, want LM_STUDIO_TOKEN", qwen.APIKeyEnv)
	}
}

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
	if !strings.Contains(err.Error(), "base_url") {
		t.Errorf("openai error %q does not mention base_url", err)
	}
}

func writeRegistry(t *testing.T, yaml string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "models.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write models.yaml: %v", err)
	}
	return p
}

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

func TestLoadRegistryAcceptsReasoningEffortOnOpenAI(t *testing.T) {
	p := writeRegistry(t, `models:
  - display: GPT-5.6 Sol
    id: gpt-5.6-sol
    provider: openai
    reasoning_effort: none
`)
	r, err := LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	m, ok := r.ByID("gpt-5.6-sol")
	if !ok || m.ReasoningEffort != "none" {
		t.Fatalf("ByID = %+v, %v; want ReasoningEffort=none", m, ok)
	}
}

func TestLoadRegistryAcceptsReasoningEffortOnOpenAICompat(t *testing.T) {
	p := writeRegistry(t, `models:
  - display: LM Studio Local
    id: local-model
    provider: openai_compat
    base_url: http://localhost:1234/v1
    reasoning_effort: low
`)
	r, err := LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	m, ok := r.ByID("local-model")
	if !ok || m.ReasoningEffort != "low" {
		t.Fatalf("ByID = %+v, %v; want ReasoningEffort=low", m, ok)
	}
}

func TestLoadRegistryRejectsReasoningEffortOnGemini(t *testing.T) {
	p := writeRegistry(t, `models:
  - display: Gemini 3 Pro
    id: gemini-3-pro
    provider: gemini
    reasoning_effort: none
`)
	_, err := LoadRegistry(p)
	if err == nil {
		t.Fatal("LoadRegistry accepted reasoning_effort on a gemini model; want error")
	}
	if !strings.Contains(err.Error(), "reasoning_effort") {
		t.Errorf("error %q does not mention reasoning_effort", err)
	}
	if !strings.Contains(err.Error(), "gemini-3-pro") {
		t.Errorf("error %q does not mention the offending model id", err)
	}
}

func TestLoadRegistryRejectsReasoningEffortOnAnthropic(t *testing.T) {
	p := writeRegistry(t, `models:
  - display: Claude Opus 4.7
    id: claude-opus-4-7
    provider: anthropic
    reasoning_effort: none
`)
	_, err := LoadRegistry(p)
	if err == nil {
		t.Fatal("LoadRegistry accepted reasoning_effort on an anthropic model; want error")
	}
	if !strings.Contains(err.Error(), "reasoning_effort") {
		t.Errorf("error %q does not mention reasoning_effort", err)
	}
}
