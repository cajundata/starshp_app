package provider

import (
	"os"
	"path/filepath"
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
