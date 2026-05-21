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
