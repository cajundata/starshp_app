package assignment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Loaded is the result of reading a companion _json directory.
type Loaded struct {
	Dir        string
	Manifest   Manifest
	Questions  []Question        // successfully loaded, in manifest order
	LoadErrors map[string]string // path -> error, for files listed but unreadable
}

// rawQuestion is the on-disk per-question JSON envelope.
type rawQuestion struct {
	Type     string          `json:"type"`
	Title    string          `json:"title"`
	Warnings []string        `json:"warnings"`
	Body     json.RawMessage `json:"body"`
}

// Load reads manifest.json and every per-question NNN.json it references.
// Files listed in the manifest but missing/unreadable are recorded in
// LoadErrors rather than failing the whole load.
func Load(dir string) (*Loaded, error) {
	mb, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var man Manifest
	if err := json.Unmarshal(mb, &man); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	out := &Loaded{Dir: dir, Manifest: man, LoadErrors: map[string]string{}}
	for _, entry := range man.Questions {
		jsonName := jsonFileFor(entry.Path)
		q, err := loadQuestion(filepath.Join(dir, jsonName), entry)
		if err != nil {
			out.LoadErrors[entry.Path] = err.Error()
			continue
		}
		out.Questions = append(out.Questions, q)
	}
	return out, nil
}

// jsonFileFor maps a manifest html path like "001.html" to its sibling
// "001.json". An extensionless path maps to "<path>.json".
func jsonFileFor(htmlPath string) string {
	return strings.TrimSuffix(htmlPath, filepath.Ext(htmlPath)) + ".json"
}

func loadQuestion(path string, entry ManifestEntry) (Question, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Question{}, fmt.Errorf("read %s: %w", path, err)
	}
	var raw rawQuestion
	if err := json.Unmarshal(b, &raw); err != nil {
		return Question{}, fmt.Errorf("parse %s: %w", path, err)
	}
	q := Question{Path: entry.Path, Title: raw.Title, Warnings: raw.Warnings}
	switch raw.Type {
	case string(TypeMultipleChoice):
		q.Type = TypeMultipleChoice
		var body MultipleChoiceBody
		if err := json.Unmarshal(raw.Body, &body); err != nil {
			return Question{}, fmt.Errorf("parse mc body %s: %w", path, err)
		}
		q.MultipleChoice = &body
	case string(TypeWorksheet):
		q.Type = TypeWorksheet
		var body WorksheetBody
		if err := json.Unmarshal(raw.Body, &body); err != nil {
			return Question{}, fmt.Errorf("parse worksheet body %s: %w", path, err)
		}
		q.Worksheet = &body
	default:
		q.Type = TypeUnsupported
	}
	return q, nil
}
