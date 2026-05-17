package appapi

import (
	"os"

	"github.com/cajundata/discussion_engine/internal/config"
)

// ValidateStartup returns human-readable setup problems (empty = OK).
func ValidateStartup(c config.Config) []string {
	var issues []string
	if c.OpenAIAPIKey == "" {
		issues = append(issues, "OPENAI_API_KEY is not set (required for textbook embeddings).")
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
	return issues
}
