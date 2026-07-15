// Package persona is the registry of named assistants. Each persona is one
// markdown file with YAML frontmatter in <app-dir>/personas/: the filename stem
// is the stable ID, the frontmatter carries the assigned model, display name,
// color, tool whitelist, and auto-attached library items, and the body is the
// system prompt.
//
// A persona that fails validation is disabled and reported as an Issue, never
// fatal: a typo in one file must not lock the operator out of the app.
package persona

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Persona is one named assistant. Prompt is excluded from JSON: the frontend
// renders names and colors, and has no use for the system prompt.
type Persona struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Model   string   `json:"model"`
	Color   string   `json:"color"`
	Tools   []string `json:"tools,omitempty"`
	Library []string `json:"library,omitempty"`
	Prompt  string   `json:"-"`
}

// Issue is one rejected persona file, surfaced to the operator so a persona
// that silently vanished from the picker is explainable.
type Issue struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

type Registry struct {
	Personas []Persona `json:"personas"`
	Issues   []Issue   `json:"issues"`
}

func (r Registry) ByID(id string) (Persona, bool) {
	for _, p := range r.Personas {
		if p.ID == id {
			return p, true
		}
	}
	return Persona{}, false
}

// Name resolves a persona ID to its display name. It satisfies the
// chat.PersonaNamer interface (declared there), so a handoff block can be
// attributed without chat importing this package.
func (r Registry) Name(id string) (string, bool) {
	p, ok := r.ByID(id)
	return p.Name, ok
}

type frontmatter struct {
	Name    string   `yaml:"name"`
	Model   string   `yaml:"model"`
	Color   string   `yaml:"color"`
	Tools   []string `yaml:"tools"`
	Library []string `yaml:"library"`
}

var idRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// LoadRegistry reads every .md file in dir. It never writes and never returns
// an error: a missing directory is an empty registry, and every other failure
// becomes an Issue. knownModels and knownTools are the names a persona may
// reference; anything else is a typo and disables that persona.
func LoadRegistry(dir string, knownModels, knownTools []string) Registry {
	var r Registry
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return r
		}
		return Registry{Issues: []Issue{{File: dir, Reason: "cannot read personas folder: " + err.Error()}}}
	}
	modelOK := set(knownModels)
	toolOK := set(knownTools)
	seen := map[string]string{} // id -> file that claimed it

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.EqualFold(filepath.Ext(name), ".md") {
			continue
		}
		p, reason := parseFile(dir, name, modelOK, toolOK)
		if reason != "" {
			r.Issues = append(r.Issues, Issue{File: name, Reason: reason})
			continue
		}
		if prior, dup := seen[p.ID]; dup {
			r.Issues = append(r.Issues, Issue{File: name, Reason: "duplicate persona id, already defined by " + prior})
			continue
		}
		seen[p.ID] = name
		r.Personas = append(r.Personas, p)
	}
	sort.Slice(r.Personas, func(i, j int) bool {
		return strings.ToLower(r.Personas[i].Name) < strings.ToLower(r.Personas[j].Name)
	})
	sort.Slice(r.Issues, func(i, j int) bool { return r.Issues[i].File < r.Issues[j].File })
	return r
}

// parseFile validates one persona file. It returns a non-empty reason instead
// of an error because every rejection is reported, not propagated.
func parseFile(dir, filename string, modelOK, toolOK map[string]bool) (Persona, string) {
	id := strings.TrimSuffix(filename, filepath.Ext(filename))
	if !idRe.MatchString(id) {
		return Persona{}, "invalid persona id"
	}
	raw, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		return Persona{}, "cannot read file: " + err.Error()
	}
	fmText, body, ok := splitFrontmatter(string(raw))
	if !ok {
		return Persona{}, "missing frontmatter"
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
		return Persona{}, "invalid frontmatter: " + err.Error()
	}
	if strings.TrimSpace(fm.Name) == "" {
		return Persona{}, "name is required"
	}
	if !modelOK[fm.Model] {
		return Persona{}, "unknown model"
	}
	for _, t := range fm.Tools {
		if !toolOK[t] {
			return Persona{}, "unknown tool"
		}
	}
	color := strings.TrimSpace(fm.Color)
	if color == "" {
		color = assignColor(id)
	} else if !ValidColor(color) {
		return Persona{}, "invalid color"
	}
	return Persona{
		ID:      id,
		Name:    strings.TrimSpace(fm.Name),
		Model:   fm.Model,
		Color:   color,
		Tools:   fm.Tools,
		Library: fm.Library,
		Prompt:  body,
	}, ""
}

// splitFrontmatter separates a leading `---`-fenced YAML block from the body.
// The body is returned trimmed. ok is false when there is no opening fence or
// no closing fence.
func splitFrontmatter(raw string) (fm, body string, ok bool) {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	const open = "---\n"
	if !strings.HasPrefix(s, open) {
		return "", "", false
	}
	rest := s[len(open):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", false
	}
	after := strings.TrimPrefix(rest[end+len("\n---"):], "\n")
	return rest[:end], strings.TrimSpace(after), true
}

func set(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
