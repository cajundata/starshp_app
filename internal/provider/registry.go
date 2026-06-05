package provider

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type ModelInfo struct {
	Display    string `yaml:"display" json:"display"`
	ID         string `yaml:"id" json:"id"`
	Provider   string `yaml:"provider" json:"provider"` // "openai" | "anthropic" | "openai_compat"
	MaxContext int    `yaml:"max_context,omitempty" json:"maxContext,omitempty"`
	BaseURL    string `yaml:"base_url,omitempty" json:"baseURL,omitempty"`
	APIKeyEnv  string `yaml:"api_key_env,omitempty" json:"apiKeyEnv,omitempty"`
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
	for _, m := range r.Models {
		switch m.Provider {
		case "openai_compat":
			if m.BaseURL == "" {
				return Registry{}, fmt.Errorf("model %s: base_url is required for provider openai_compat", m.ID)
			}
		case "openai", "anthropic":
			if m.BaseURL != "" {
				return Registry{}, fmt.Errorf("model %s: base_url is not allowed for provider %s", m.ID, m.Provider)
			}
		}
	}
	return r, nil
}

func (r Registry) ByID(id string) (ModelInfo, bool) {
	for _, m := range r.Models {
		if m.ID == id {
			return m, true
		}
	}
	return ModelInfo{}, false
}
