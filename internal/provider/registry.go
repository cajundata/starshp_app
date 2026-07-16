package provider

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type ModelInfo struct {
	Display    string `yaml:"display" json:"display"`
	ID         string `yaml:"id" json:"id"`
	Provider   string `yaml:"provider" json:"provider"` // "openai" | "anthropic" | "openai_compat" | "gemini"
	MaxContext int    `yaml:"max_context,omitempty" json:"maxContext,omitempty"`
	BaseURL    string `yaml:"base_url,omitempty" json:"baseURL,omitempty"`
	APIKeyEnv  string `yaml:"api_key_env,omitempty" json:"apiKeyEnv,omitempty"`
	// ReasoningEffort is forwarded verbatim to the OpenAI chat.completions
	// request when set. Only valid for provider openai/openai_compat — some
	// models (e.g. GPT-5.6 Sol) reject function tools with their default
	// reasoning effort on /v1/chat/completions and require "none".
	ReasoningEffort string `yaml:"reasoning_effort,omitempty" json:"reasoningEffort,omitempty"`
	// InputModalities / OutputModalities declare what a model can consume and
	// produce. Both are optional and default to ["text"]; the only allowed
	// values today are "text" and "image" (image support, e.g. Nano Banana 2,
	// is not yet wired into the app — see the appapi startup gate that
	// disables any persona pinned to a model without "text" output).
	InputModalities  []string `yaml:"input_modalities,omitempty" json:"inputModalities,omitempty"`
	OutputModalities []string `yaml:"output_modalities,omitempty" json:"outputModalities,omitempty"`
}

type Registry struct {
	Models []ModelInfo `yaml:"models" json:"models"`
}

func LoadRegistry(path string) (Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, err
	}
	var r Registry
	if err := yaml.Unmarshal(raw, &r); err != nil {
		return Registry{}, err
	}
	for i := range r.Models {
		if len(r.Models[i].InputModalities) == 0 {
			r.Models[i].InputModalities = []string{"text"}
		}
		if len(r.Models[i].OutputModalities) == 0 {
			r.Models[i].OutputModalities = []string{"text"}
		}
	}
	for _, m := range r.Models {
		if err := validateModalities(m.ID, "input_modalities", m.InputModalities); err != nil {
			return Registry{}, err
		}
		if err := validateModalities(m.ID, "output_modalities", m.OutputModalities); err != nil {
			return Registry{}, err
		}
		switch m.Provider {
		case "openai_compat":
			if m.BaseURL == "" {
				return Registry{}, fmt.Errorf("model %s: base_url is required for provider openai_compat", m.ID)
			}
		case "openai", "anthropic":
			if m.BaseURL != "" {
				return Registry{}, fmt.Errorf("model %s: base_url is not allowed for provider %s", m.ID, m.Provider)
			}
		case "gemini":
			if m.BaseURL != "" {
				return Registry{}, fmt.Errorf("model %s: base_url is not allowed for provider gemini", m.ID)
			}
			if m.APIKeyEnv != "" {
				return Registry{}, fmt.Errorf("model %s: api_key_env is not allowed for provider gemini (set GEMINI_API_KEY)", m.ID)
			}
		}
		if m.ReasoningEffort != "" && m.Provider != "openai" && m.Provider != "openai_compat" {
			return Registry{}, fmt.Errorf("model %s: reasoning_effort is not allowed for provider %s", m.ID, m.Provider)
		}
	}
	return r, nil
}

var validModalities = map[string]bool{"text": true, "image": true}

// validateModalities rejects any value outside the closed set {text, image}.
// field is "input_modalities" or "output_modalities", for the error message.
func validateModalities(id, field string, vals []string) error {
	for _, v := range vals {
		if !validModalities[v] {
			return fmt.Errorf("model %s: %s value %q is not supported (want text or image)", id, field, v)
		}
	}
	return nil
}

func (r Registry) ByID(id string) (ModelInfo, bool) {
	for _, m := range r.Models {
		if m.ID == id {
			return m, true
		}
	}
	return ModelInfo{}, false
}
