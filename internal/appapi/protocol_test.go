package appapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/library"
	"github.com/cajundata/starshp_app/internal/persona"
)

// Spec 2.1: with two or more personas loaded, the assembled prompt carries the
// app-assembled team protocol between the persona body and the library items,
// naming the current persona and listing the roster sorted by ID.
func TestAssembleSystemPromptIncludesTeamProtocol(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.md"), []byte("# Alpha\n\nALPHA BODY\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &API{cfg: config.Config{LibraryDir: dir}, lib: library.New(dir), st: testStore(t),
		personas: persona.Registry{Personas: []persona.Persona{
			// Deliberately unsorted: the roster must sort by ID.
			{ID: "skeptic", Name: "Skeptic", Model: "gpt-5"},
			{ID: "scout", Name: "Scout", Model: "gpt-5"},
		}}}
	c, err := a.st.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.st.SetActiveItems(c.ID, []string{"alpha.md"}); err != nil {
		t.Fatal(err)
	}

	p := persona.Persona{ID: "scout", Name: "Scout", Model: "gpt-5", Prompt: "YOU ARE SCOUT"}
	got, _, err := a.assembleSystemPrompt(c.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "## Working arrangement") {
		t.Fatalf("prompt lacks protocol block:\n%q", got)
	}
	if !strings.Contains(got, "You are Scout (@scout)") {
		t.Fatalf("protocol does not name the current persona:\n%q", got)
	}
	if !strings.Contains(got, "The team: Scout (@scout), Skeptic (@skeptic).") {
		t.Fatalf("roster missing or unsorted:\n%q", got)
	}
	// Order: persona body, protocol, library items.
	body, proto, lib := strings.Index(got, "YOU ARE SCOUT"), strings.Index(got, "## Working arrangement"), strings.Index(got, "ALPHA BODY")
	if !(body < proto && proto < lib) {
		t.Fatalf("expected body < protocol < library, got %d, %d, %d in:\n%q", body, proto, lib, got)
	}
}

// Spec 2.1: a solo-persona registry reads nothing about teammates — the prompt
// is byte-identical to the pre-protocol assembly.
func TestAssembleSystemPromptSoloPersonaOmitsProtocol(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.md"), []byte("# Alpha\n\nALPHA BODY\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &API{cfg: config.Config{LibraryDir: dir}, lib: library.New(dir), st: testStore(t),
		personas: persona.Registry{Personas: []persona.Persona{
			{ID: "scout", Name: "Scout", Model: "gpt-5"},
		}}}
	c, err := a.st.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.st.SetActiveItems(c.ID, []string{"alpha.md"}); err != nil {
		t.Fatal(err)
	}

	p := persona.Persona{ID: "scout", Name: "Scout", Model: "gpt-5", Prompt: "YOU ARE SCOUT"}
	got, _, err := a.assembleSystemPrompt(c.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	if want := "YOU ARE SCOUT\n\nALPHA BODY"; got != want {
		t.Fatalf("solo prompt must be byte-identical to pre-2.1 assembly:\ngot  %q\nwant %q", got, want)
	}
}
