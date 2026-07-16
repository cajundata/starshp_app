package provider

import (
	"fmt"
	"os"
)

// Keys carries the per-provider-family API keys the factory needs. Fields
// may be empty; New errors only when the selected model's family lacks its
// key.
type Keys struct {
	OpenAI    string
	Anthropic string
	Gemini    string
}

// New builds the right provider for a model ID using the registry.
func New(reg Registry, modelID string, keys Keys) (ChatProvider, error) {
	m, ok := reg.ByID(modelID)
	if !ok {
		return nil, fmt.Errorf("unknown model: %s", modelID)
	}
	switch m.Provider {
	case "openai":
		if keys.OpenAI == "" {
			return nil, AppError{"auth", "OpenAI API key not set.", false}
		}
		return NewOpenAI(keys.OpenAI, ""), nil
	case "anthropic":
		if keys.Anthropic == "" {
			return nil, AppError{"auth", "Anthropic API key not set.", false}
		}
		return NewAnthropic(keys.Anthropic, ""), nil
	case "openai_compat":
		// LoadRegistry already rejects an empty BaseURL; keep the guard for
		// programmatically-built registries (e.g., tests).
		if m.BaseURL == "" {
			return nil, fmt.Errorf("model %s: base_url required for openai_compat", m.ID)
		}
		return NewOpenAI(resolveCompatKey(m), m.BaseURL), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", m.Provider)
	}
}

// resolveCompatKey returns the API key for an openai_compat model,
// falling back to "local" (the OpenAI SDK requires non-empty).
func resolveCompatKey(m ModelInfo) string {
	if m.APIKeyEnv != "" {
		if v := os.Getenv(m.APIKeyEnv); v != "" {
			return v
		}
	}
	return "local"
}
