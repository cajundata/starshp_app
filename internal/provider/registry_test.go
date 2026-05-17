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
