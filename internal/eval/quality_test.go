package eval

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/cajundata/starshp_app/internal/tools"
	"github.com/cajundata/starshp_app/internal/tools/safemath"
	"gopkg.in/yaml.v3"
)

type fixture struct {
	Name                           string   `yaml:"name"`
	Prompt                         string   `yaml:"prompt"`
	ExpectedSubstrings             []string `yaml:"expected_substrings"`
	ExpectedMinToolCalls           int      `yaml:"expected_min_tool_calls"`
	ExpectedToolsCalledAtLeastOnce []string `yaml:"expected_tools_called_at_least_once"`
	MaxIterations                  int      `yaml:"max_iterations"`
	ModelID                        string   `yaml:"model_id"` // optional override
}

// TestQualityFixtures runs each YAML fixture against a real provider and asserts
// on the final answer text, tool-call counts, and which tools were invoked. It
// is gated on API keys: with neither OPENAI_API_KEY nor ANTHROPIC_API_KEY set it
// skips, so a keyless CI run stays green. Fixtures use deterministic, hand-
// verifiable arithmetic and definition prompts; tax-year-specific fixtures land
// alongside the Phase 3 tax tools.
func TestQualityFixtures(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("quality eval requires OPENAI_API_KEY or ANTHROPIC_API_KEY")
	}
	paths, err := filepath.Glob("testdata/fixtures/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no quality fixtures found under testdata/fixtures")
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var fx fixture
		if err := yaml.Unmarshal(b, &fx); err != nil {
			t.Fatal(err)
		}
		t.Run(fx.Name, func(t *testing.T) {
			st := openStore(t)
			conv, _ := st.CreateConversation(fx.Name)
			svc := chat.New(st)
			reg := tools.NewRegistry(30 * time.Second)
			_ = reg.Register(safemath.New())
			// search_textbook is deliberately omitted: Phase 1 fixtures attach
			// no textbooks, so the model answers from background knowledge.
			modelID := fx.ModelID
			if modelID == "" {
				modelID = "claude-sonnet-4-6"
			}
			providerName := providerForModel(modelID)
			// Build a real provider from the user's environment config.
			preg := provider.Registry{Models: []provider.ModelInfo{
				{ID: modelID, Provider: providerName},
			}}
			prov, err := provider.New(preg, modelID, provider.Keys{
				OpenAI:    cfg.OpenAIAPIKey,
				Anthropic: cfg.AnthropicAPIKey,
			})
			if err != nil {
				t.Skipf("no provider for %s: %v", modelID, err)
			}
			if fx.MaxIterations > 0 {
				t.Setenv("STARSHP_MAX_TOOL_ITERATIONS", strconv.Itoa(fx.MaxIterations))
			}
			sink := &CaptureSink{}
			if _, err = svc.Send(context.Background(), chat.SendParams{
				ConversationID: conv.ID, UserText: fx.Prompt, Model: modelID,
				Provider: prov, ProviderName: providerName,
				Registry: reg, Resolver: emptyResolver{},
				RetrievalMode: chat.RetrievalAutoGroundedDefault,
				Sink:          sink,
			}, nil); err != nil {
				t.Fatalf("send: %v", err)
			}

			display, _ := st.GetConversationDisplayEvents(conv.ID)
			var finalText strings.Builder
			calledTools := map[string]int{}
			for _, ev := range display {
				switch ev.Kind {
				case store.EventKindAssistantText:
					finalText.WriteString(ev.Text)
					finalText.WriteString("\n")
				case store.EventKindAssistantToolCall:
					calledTools[ev.ToolName]++
				}
			}
			txt := finalText.String()
			for _, sub := range fx.ExpectedSubstrings {
				if !strings.Contains(txt, sub) {
					t.Errorf("missing expected substring %q in final answer:\n%s", sub, txt)
				}
			}
			total := 0
			for _, n := range calledTools {
				total += n
			}
			if total < fx.ExpectedMinToolCalls {
				t.Errorf("only %d tool calls; want >= %d", total, fx.ExpectedMinToolCalls)
			}
			for _, name := range fx.ExpectedToolsCalledAtLeastOnce {
				if calledTools[name] == 0 {
					t.Errorf("expected at least one call to %q; got 0", name)
				}
			}
		})
	}
}

func providerForModel(id string) string {
	if strings.HasPrefix(id, "claude-") {
		return "anthropic"
	}
	return "openai"
}
