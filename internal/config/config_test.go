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

func TestLoadResolvesRelativeConfigsAgainstConfigPath(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ok")
	base := t.TempDir()
	t.Setenv("CONFIG_PATH", base)
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantTextbooks := filepath.Join(base, "textbooks.yaml")
	if c.TextbooksConfig != wantTextbooks {
		t.Errorf("TextbooksConfig = %q, want %q", c.TextbooksConfig, wantTextbooks)
	}
	wantModels := filepath.Join(base, "models.yaml")
	if c.ModelsConfig != wantModels {
		t.Errorf("ModelsConfig = %q, want %q", c.ModelsConfig, wantModels)
	}
}

func TestLoadKeepsAbsoluteConfigsWhenConfigPathSet(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ok")
	t.Setenv("CONFIG_PATH", t.TempDir())
	absTextbooks := filepath.Join(t.TempDir(), "elsewhere", "textbooks.yaml")
	t.Setenv("TEXTBOOKS_CONFIG", absTextbooks)
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.TextbooksConfig != absTextbooks {
		t.Errorf("TextbooksConfig = %q, want %q (absolute path must be unchanged)", c.TextbooksConfig, absTextbooks)
	}
}

func TestLoadWithoutConfigPathKeepsBareConfigNames(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ok")
	t.Setenv("CONFIG_PATH", "")
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.TextbooksConfig != "textbooks.yaml" {
		t.Errorf("TextbooksConfig = %q, want textbooks.yaml", c.TextbooksConfig)
	}
	if c.ModelsConfig != "models.yaml" {
		t.Errorf("ModelsConfig = %q, want models.yaml", c.ModelsConfig)
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
}
