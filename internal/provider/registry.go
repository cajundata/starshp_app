package provider

import (
	"os"

	"gopkg.in/yaml.v3"
)

type ModelInfo struct {
	Display  string `yaml:"display" json:"display"`
	ID       string `yaml:"id" json:"id"`
	Provider string `yaml:"provider" json:"provider"` // "openai" | "anthropic"
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
