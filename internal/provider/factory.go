package provider

import (
	"fmt"
	"os"
)

// New builds the right provider for a model ID using the registry.
func New(reg Registry, modelID, openAIKey, anthropicKey string) (ChatProvider, error) {
	m, ok := reg.ByID(modelID)
	if !ok {
		return nil, fmt.Errorf("unknown model: %s", modelID)
	}
	switch m.Provider {
	case "openai":
		if openAIKey == "" {
			return nil, AppError{"auth", "OpenAI API key not set.", false}
		}
		return NewOpenAI(openAIKey, ""), nil
	case "anthropic":
		if anthropicKey == "" {
			return nil, AppError{"auth", "Anthropic API key not set.", false}
		}
		return NewAnthropic(anthropicKey, ""), nil
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
