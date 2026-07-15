package persona

import (
	"os"
	"path/filepath"
	"testing"
)

var (
	models  = []string{"claude-opus-4-8", "gpt-5"}
	toolset = []string{"safe_math", "search_textbook"}
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRegistryValid(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "scout.md", `---
name: Scout
model: claude-opus-4-8
color: "#4fb3ff"
tools: [safe_math]
library: [style-guide]
---
You are Scout.
You find opportunities.
`)
	r := LoadRegistry(dir, models, toolset)
	if len(r.Issues) != 0 {
		t.Fatalf("unexpected issues: %v", r.Issues)
	}
	if len(r.Personas) != 1 {
		t.Fatalf("want 1 persona, got %d", len(r.Personas))
	}
	p := r.Personas[0]
	if p.ID != "scout" {
		t.Errorf("ID = %q, want scout", p.ID)
	}
	if p.Name != "Scout" {
		t.Errorf("Name = %q, want Scout", p.Name)
	}
	if p.Model != "claude-opus-4-8" {
		t.Errorf("Model = %q", p.Model)
	}
	if p.Color != "#4fb3ff" {
		t.Errorf("Color = %q", p.Color)
	}
	if len(p.Tools) != 1 || p.Tools[0] != "safe_math" {
		t.Errorf("Tools = %v", p.Tools)
	}
	if len(p.Library) != 1 || p.Library[0] != "style-guide" {
		t.Errorf("Library = %v", p.Library)
	}
	if p.Prompt != "You are Scout.\nYou find opportunities." {
		t.Errorf("Prompt = %q", p.Prompt)
	}
	got, ok := r.ByID("scout")
	if !ok || got.ID != "scout" {
		t.Errorf("ByID(scout) = %v, %v", got, ok)
	}
}

func TestLoadRegistryRejections(t *testing.T) {
	cases := []struct{ file, body, wantReason string }{
		{"nomodel.md", "---\nname: X\nmodel: nope-9\n---\nbody\n", "unknown model"},
		{"badtool.md", "---\nname: X\nmodel: gpt-5\ntools: [teleport]\n---\nbody\n", "unknown tool"},
		{"badcolor.md", "---\nname: X\nmodel: gpt-5\ncolor: \"blurple\"\n---\nbody\n", "invalid color"},
		{"noname.md", "---\nmodel: gpt-5\n---\nbody\n", "name is required"},
		{"nofm.md", "no frontmatter here\n", "missing frontmatter"},
		{"Bad Name.md", "---\nname: X\nmodel: gpt-5\n---\nbody\n", "invalid persona id"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, c.file, c.body)
			r := LoadRegistry(dir, models, toolset)
			if len(r.Personas) != 0 {
				t.Fatalf("want persona rejected, got %v", r.Personas)
			}
			if len(r.Issues) != 1 {
				t.Fatalf("want 1 issue, got %v", r.Issues)
			}
			if r.Issues[0].Reason != c.wantReason {
				t.Errorf("Reason = %q, want %q", r.Issues[0].Reason, c.wantReason)
			}
		})
	}
}

func TestLoadRegistryOneBadFileDoesNotDisableTheRest(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "good.md", "---\nname: Good\nmodel: gpt-5\n---\nbody\n")
	write(t, dir, "bad.md", "---\nname: Bad\nmodel: nope-9\n---\nbody\n")
	r := LoadRegistry(dir, models, toolset)
	if len(r.Personas) != 1 || r.Personas[0].ID != "good" {
		t.Fatalf("want only good to load, got %v", r.Personas)
	}
	if len(r.Issues) != 1 || r.Issues[0].File != "bad.md" {
		t.Fatalf("want one issue for bad.md, got %v", r.Issues)
	}
}

func TestLoadRegistryMissingDirIsEmptyNotAnError(t *testing.T) {
	r := LoadRegistry(filepath.Join(t.TempDir(), "absent"), models, toolset)
	if len(r.Personas) != 0 || len(r.Issues) != 0 {
		t.Fatalf("want empty registry with no issues, got %+v", r)
	}
}

func TestLoadRegistryAssignsColorWhenOmitted(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "skeptic.md", "---\nname: Skeptic\nmodel: gpt-5\n---\nbody\n")
	r := LoadRegistry(dir, models, toolset)
	if len(r.Personas) != 1 {
		t.Fatalf("want 1 persona, got %v", r.Issues)
	}
	c := r.Personas[0].Color
	if c == "" {
		t.Fatal("color was not auto-assigned")
	}
	// Deterministic: a second load of the same ID yields the same color.
	r2 := LoadRegistry(dir, models, toolset)
	if r2.Personas[0].Color != c {
		t.Errorf("color not deterministic: %q then %q", c, r2.Personas[0].Color)
	}
}

func TestSeedWritesAssistantOnlyWhenDirAbsent(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "personas")

	if err := Seed(dir, "gpt-5"); err != nil {
		t.Fatal(err)
	}
	r := LoadRegistry(dir, models, toolset)
	if len(r.Personas) != 1 || r.Personas[0].ID != "assistant" {
		t.Fatalf("want a seeded assistant, got %+v", r)
	}
	if r.Personas[0].Model != "gpt-5" {
		t.Errorf("seeded model = %q, want gpt-5", r.Personas[0].Model)
	}

	// An existing directory is never written to, even if the user emptied it.
	if err := os.Remove(filepath.Join(dir, "assistant.md")); err != nil {
		t.Fatal(err)
	}
	if err := Seed(dir, "gpt-5"); err != nil {
		t.Fatal(err)
	}
	if r := LoadRegistry(dir, models, toolset); len(r.Personas) != 0 {
		t.Fatalf("Seed re-seeded an existing directory: %+v", r)
	}
}

func TestSeedNoModelsIsANoOp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "personas")
	if err := Seed(dir, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("Seed created a directory with no model to point at")
	}
}

// Name satisfies chat.PersonaNamer: the handoff attribution line resolves a
// persona ID to its display name through this method.
func TestRegistryNameResolvesDisplayName(t *testing.T) {
	r := Registry{Personas: []Persona{{ID: "scout", Name: "Scout"}}}
	if n, ok := r.Name("scout"); !ok || n != "Scout" {
		t.Errorf("Name(scout) = (%q, %v), want (Scout, true)", n, ok)
	}
	if _, ok := r.Name("ghost"); ok {
		t.Error("Name(ghost) resolved; want ok=false")
	}
}
