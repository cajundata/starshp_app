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
	// With envPath "", filepath.Dir("") is "." and filepath.Join cleans the
	// result back to the bare name — pin that documented edge case.
	if c.TextbooksConfig != "textbooks.yaml" {
		t.Errorf("TextbooksConfig = %q, want textbooks.yaml", c.TextbooksConfig)
	}
	if c.ModelsConfig != "models.yaml" {
		t.Errorf("ModelsConfig = %q, want models.yaml", c.ModelsConfig)
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

func TestLoadResolvesRelativeConfigsAgainstEnvDir(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ok")
	dir := t.TempDir()
	c, err := Load(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := filepath.Join(dir, "textbooks.yaml"); c.TextbooksConfig != want {
		t.Errorf("TextbooksConfig = %q, want %q", c.TextbooksConfig, want)
	}
	if want := filepath.Join(dir, "models.yaml"); c.ModelsConfig != want {
		t.Errorf("ModelsConfig = %q, want %q", c.ModelsConfig, want)
	}
}

func TestLoadKeepsAbsoluteConfigPaths(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ok")
	absModels := filepath.Join(t.TempDir(), "elsewhere", "models.yaml")
	t.Setenv("MODELS_CONFIG", absModels)
	c, err := Load(filepath.Join(t.TempDir(), ".env"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ModelsConfig != absModels {
		t.Errorf("ModelsConfig = %q, want %q (absolute path must be unchanged)", c.ModelsConfig, absModels)
	}
}

func TestAppDirHonorsStarshpHome(t *testing.T) {
	want := filepath.Join(t.TempDir(), "custom-home")
	t.Setenv("STARSHP_HOME", want)
	got, err := AppDir()
	if err != nil {
		t.Fatalf("AppDir: %v", err)
	}
	if got != want {
		t.Errorf("AppDir() = %q, want %q", got, want)
	}
	if fi, statErr := os.Stat(got); statErr != nil || !fi.IsDir() {
		t.Errorf("AppDir did not create the directory: stat err=%v", statErr)
	}
}

func TestAppDirFallsBackToUserConfigDir(t *testing.T) {
	t.Setenv("STARSHP_HOME", "")
	got, err := AppDir()
	if err != nil {
		t.Fatalf("AppDir: %v", err)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	if want := filepath.Join(base, "starshp_app"); got != want {
		t.Errorf("AppDir() = %q, want %q", got, want)
	}
	if fi, statErr := os.Stat(got); statErr != nil || !fi.IsDir() {
		t.Errorf("AppDir did not create or find the directory: stat err=%v", statErr)
	}
}
