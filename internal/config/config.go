// Package config loads runtime configuration from environment / .env.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	OpenAIAPIKey       string
	AnthropicAPIKey    string
	EmbeddingModel     string
	AppDBPath          string
	RAGDBPath          string
	LibraryDir         string
	PersonaDir         string
	TextbooksConfig    string
	ModelsConfig       string
	ContextTokenBudget int
	RAGTopK            int
}

// AppDir returns the per-user application directory, creating it if needed.
// STARSHP_HOME overrides the location (use an absolute path); otherwise it is
// os.UserConfigDir()/starshp_app — %APPDATA%\starshp_app on Windows,
// ~/.config/starshp_app on Linux, ~/Library/Application Support/starshp_app
// on macOS.
func AppDir() (string, error) {
	dir := strings.TrimSpace(os.Getenv("STARSHP_HOME"))
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "starshp_app")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Load reads .env at envPath (if non-empty and present), then resolves config
// from the environment with defaults.
func Load(envPath string) (Config, error) {
	if envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			if err := godotenv.Load(envPath); err != nil {
				return Config{}, err
			}
		}
	}
	c := Config{
		OpenAIAPIKey:       strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		AnthropicAPIKey:    strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")),
		EmbeddingModel:     envOr("EMBEDDING_MODEL", "text-embedding-3-small"),
		AppDBPath:          os.Getenv("APP_DB_PATH"),
		RAGDBPath:          os.Getenv("RAG_DB_PATH"),
		LibraryDir:         os.Getenv("LIBRARY_DIR"),
		PersonaDir:         os.Getenv("PERSONA_DIR"),
		TextbooksConfig:    envOr("TEXTBOOKS_CONFIG", "textbooks.yaml"),
		ModelsConfig:       envOr("MODELS_CONFIG", "models.yaml"),
		ContextTokenBudget: envInt("CONTEXT_TOKEN_BUDGET", 2500),
		RAGTopK:            envInt("RAG_TOP_K", 8),
	}
	// Resolve relative config-file paths against the directory that contains
	// .env (the app directory), so they do not depend on the process working
	// directory. Absolute paths are left as-is. When envPath is "", base is "."
	// and filepath.Join cleans the result back to the bare name.
	base := filepath.Dir(envPath)
	if !filepath.IsAbs(c.TextbooksConfig) {
		c.TextbooksConfig = filepath.Join(base, c.TextbooksConfig)
	}
	if !filepath.IsAbs(c.ModelsConfig) {
		c.ModelsConfig = filepath.Join(base, c.ModelsConfig)
	}
	return c, nil
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
