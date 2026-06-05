package appapi

import (
	"os"
	"path/filepath"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/textbooks"
)

// ValidateStartup returns human-readable setup problems (empty = OK). The
// registry is consulted so that key warnings only fire when at least one
// model needs that key, or — in the OpenAI case — when textbooks are
// configured (RAG embeddings need an OpenAI key regardless of chat model).
func ValidateStartup(c config.Config, reg provider.Registry) []string {
	var issues []string

	if needsOpenAIKey(c, reg) && c.OpenAIAPIKey == "" {
		issues = append(issues, "OPENAI_API_KEY is not set (required for the registered OpenAI model or textbook embeddings).")
	}
	if needsAnthropicKey(reg) && c.AnthropicAPIKey == "" {
		issues = append(issues, "ANTHROPIC_API_KEY is not set (required for the registered Anthropic model).")
	}

	if _, err := os.Stat(c.ModelsConfig); err != nil {
		issues = append(issues, "models.yaml not found at "+c.ModelsConfig+".")
	}
	if c.AppDBPath != "" {
		if f, err := os.OpenFile(c.AppDBPath, os.O_CREATE|os.O_WRONLY, 0o600); err != nil {
			issues = append(issues, "App database path not writable: "+c.AppDBPath)
		} else {
			f.Close()
		}
	}
	if c.LibraryDir != "" {
		writable := true
		if err := os.MkdirAll(c.LibraryDir, 0o755); err != nil {
			writable = false
		} else {
			probe := filepath.Join(c.LibraryDir, ".write-probe")
			if f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o600); err != nil {
				writable = false
			} else {
				f.Close()
				os.Remove(probe)
			}
		}
		if !writable {
			issues = append(issues, "Library folder is not writable: "+c.LibraryDir)
		}
	}
	return issues
}

func needsOpenAIKey(c config.Config, reg provider.Registry) bool {
	for _, m := range reg.Models {
		if m.Provider == "openai" {
			return true
		}
	}
	// RAG embeddings are OpenAI-only; if any books are configured, the key is required.
	if books, err := textbooks.Scan(c.TextbooksConfig); err == nil && len(books) > 0 {
		return true
	}
	return false
}

func needsAnthropicKey(reg provider.Registry) bool {
	for _, m := range reg.Models {
		if m.Provider == "anthropic" {
			return true
		}
	}
	return false
}
