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
