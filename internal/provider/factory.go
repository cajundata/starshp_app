package provider

import "fmt"

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
	default:
		return nil, fmt.Errorf("unsupported provider: %s", m.Provider)
	}
}
