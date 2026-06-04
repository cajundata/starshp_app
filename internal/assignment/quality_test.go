package assignment

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/tools/safemath"
)

// TestQuality_SolvesRealQuestions runs the orchestrator against a real provider
// over the bundled fixtures. Skipped without API keys so a keyless CI run stays
// green. Set STARSHP_EVAL_MODEL to override the default model.
func TestQuality_SolvesRealQuestions(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("quality eval requires OPENAI_API_KEY or ANTHROPIC_API_KEY")
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	model := os.Getenv("STARSHP_EVAL_MODEL")
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	pname := "openai"
	if strings.HasPrefix(model, "claude-") {
		pname = "anthropic"
	}
	pf := func(string) (provider.ChatProvider, string, error) {
		reg := provider.Registry{Models: []provider.ModelInfo{{ID: model, Provider: pname}}}
		p, perr := provider.New(reg, model, cfg.OpenAIAPIKey, cfg.AnthropicAPIKey)
		return p, pname, perr
	}
	st := openStore(t)
	orc := New(st, chat.New(st), pf, Options{
		Model: model, Concurrency: 2, Grounding: NoGrounding{},
		SafeMath: safemath.New(), Emit: func(string, any) {},
	})

	asgID, err := orc.Run(context.Background(), tmpAssignmentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	items, _ := st.ListAssignmentItems(asgID)
	answered := 0
	for _, it := range items {
		if it.Status == "answered" {
			answered++
		}
	}
	if answered == 0 {
		t.Fatal("expected at least one answered item end-to-end")
	}
	t.Logf("answered %d/%d items with model %s", answered, len(items), model)
}
