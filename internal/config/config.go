// Package config loads runtime configuration from environment / .env.
package config

import (
	"os"
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
	TextbooksConfig    string
	ModelsConfig       string
	ContextTokenBudget int
	RAGTopK            int
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
		TextbooksConfig:    envOr("TEXTBOOKS_CONFIG", "textbooks.yaml"),
		ModelsConfig:       envOr("MODELS_CONFIG", "models.yaml"),
		ContextTokenBudget: envInt("CONTEXT_TOKEN_BUDGET", 2500),
		RAGTopK:            envInt("RAG_TOP_K", 8),
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
