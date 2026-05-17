package appapi

import (
	"path/filepath"
	"testing"

	"github.com/cajundata/discussion_engine/internal/config"
)

func TestValidateStartup(t *testing.T) {
	dir := t.TempDir()
	good := config.Config{OpenAIAPIKey: "k", AppDBPath: filepath.Join(dir, "a.db"),
		RAGDBPath: filepath.Join(dir, "r.db"), TextbooksConfig: filepath.Join(dir, "tb.yaml"),
		ModelsConfig: filepath.Join(dir, "m.yaml")}
	if issues := ValidateStartup(good); len(issues) != 1 { // missing models.yaml only
		t.Fatalf("expected 1 issue (models.yaml), got %v", issues)
	}
	bad := config.Config{}
	if issues := ValidateStartup(bad); len(issues) == 0 {
		t.Fatal("expected issues for empty config")
	}
}
