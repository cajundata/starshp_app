package appapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/library"
	"github.com/cajundata/starshp_app/internal/persona"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

func TestAssembleLibraryPreamble(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte("# Beta\nbeta body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# Alpha\nalpha body"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &API{lib: library.New(dir)}

	got, skipped, err := a.assembleLibraryPreamble([]string{"b.md", "a.md", "missing.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 || skipped[0] != "missing.md" {
		t.Fatalf("skipped = %v", skipped)
	}
	if !strings.Contains(got, "alpha body") || !strings.Contains(got, "beta body") {
		t.Fatalf("missing bodies: %q", got)
	}
	// sorted by display name (H1): Alpha before Beta
	if strings.Index(got, "alpha body") > strings.Index(got, "beta body") {
		t.Fatalf("expected Alpha before Beta in: %q", got)
	}
	// empty selection → empty preamble
	if p, _, _ := a.assembleLibraryPreamble(nil); p != "" {
		t.Fatalf("empty selection should yield empty preamble, got %q", p)
	}
}

func TestAssembleSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	libDir := filepath.Join(dir, "library")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(libDir, "zebra.md"), []byte("# Zebra tone\n\nBe concise."), 0o644)
	os.WriteFile(filepath.Join(libDir, "alpha.md"), []byte("# Alpha role\n\nYou are a tutor."), 0o644)

	st, err := store.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	conv, _ := st.CreateConversation("t")
	if err := st.SetActiveItems(conv.ID, []string{"zebra.md", "alpha.md"}); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(config.Config{LibraryDir: libDir}, st, provider.Registry{}, nil)
	prompt, skipped, err := api.assembleSystemPrompt(conv.ID, persona.Persona{})
	if err != nil {
		t.Fatalf("assembleSystemPrompt: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("expected nothing skipped, got %v", skipped)
	}
	// Bodies concatenate in display-name order: "Alpha role" before "Zebra tone".
	want := "You are a tutor.\n\nBe concise."
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

func TestAssembleSkipsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	libDir := filepath.Join(dir, "library")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "real.md"), []byte("# Real\n\nKeep me."), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	conv, err := st.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetActiveItems(conv.ID, []string{"real.md", "ghost.md"}); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(config.Config{LibraryDir: libDir}, st, provider.Registry{}, nil)
	prompt, skipped, err := api.assembleSystemPrompt(conv.ID, persona.Persona{})
	if err != nil {
		t.Fatalf("assembleSystemPrompt: %v", err)
	}
	if prompt != "Keep me." {
		t.Fatalf("prompt = %q, want %q", prompt, "Keep me.")
	}
	if len(skipped) != 1 || skipped[0] != "ghost.md" {
		t.Fatalf("skipped = %v, want [ghost.md]", skipped)
	}
}

func TestCreateLibraryItemRequiresH1(t *testing.T) {
	dir := t.TempDir()
	api := NewAPI(config.Config{LibraryDir: filepath.Join(dir, "library")}, nil, provider.Registry{}, nil)
	_, err := api.CreateLibraryItem("no heading here")
	if err == nil {
		t.Fatal("expected an error for content with no H1")
	}
	ae, ok := err.(provider.AppError)
	if !ok || ae.Code != "validation" {
		t.Fatalf("expected a validation AppError, got %#v", err)
	}
}

func TestGetActiveItemsPrunesOrphans(t *testing.T) {
	dir := t.TempDir()
	libDir := filepath.Join(dir, "library")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "real.md"), []byte("# Real\n\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	conv, err := st.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetActiveItems(conv.ID, []string{"real.md", "ghost.md"}); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(config.Config{LibraryDir: libDir}, st, provider.Registry{}, nil)
	live, err := api.GetActiveItems(conv.ID)
	if err != nil {
		t.Fatalf("GetActiveItems: %v", err)
	}
	if len(live) != 1 || live[0] != "real.md" {
		t.Fatalf("GetActiveItems = %v, want [real.md]", live)
	}
	// The orphan row must have been pruned from the store.
	persisted, _ := st.GetActiveItems(conv.ID)
	if len(persisted) != 1 || persisted[0] != "real.md" {
		t.Fatalf("orphan not pruned: %v", persisted)
	}
}

func TestReadDeleteRejectBadName(t *testing.T) {
	dir := t.TempDir()
	api := NewAPI(config.Config{LibraryDir: filepath.Join(dir, "library")}, nil, provider.Registry{}, nil)
	if _, err := api.ReadLibraryItem("../escape.md"); err == nil {
		t.Fatal("ReadLibraryItem: expected error for traversal filename")
	} else if ae, ok := err.(provider.AppError); !ok || ae.Code != "validation" {
		t.Fatalf("ReadLibraryItem: expected validation AppError, got %#v", err)
	}
	if err := api.DeleteLibraryItem("../escape.md"); err == nil {
		t.Fatal("DeleteLibraryItem: expected error for traversal filename")
	} else if ae, ok := err.(provider.AppError); !ok || ae.Code != "validation" {
		t.Fatalf("DeleteLibraryItem: expected validation AppError, got %#v", err)
	}
}

// Persona body first (identity), then the persona's own library items, then the
// conversation's — and an item claimed by both appears once.
func TestAssembleSystemPromptOrdersPersonaThenLibrary(t *testing.T) {
	dir := t.TempDir()
	writeLib := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeLib("alpha.md", "# Alpha\n\nALPHA BODY\n")
	writeLib("zulu.md", "# Zulu\n\nZULU BODY\n")

	a := &API{cfg: config.Config{LibraryDir: dir}, lib: library.New(dir), st: testStore(t)}
	c, err := a.st.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.st.SetActiveItems(c.ID, []string{"zulu.md", "alpha.md"}); err != nil {
		t.Fatal(err)
	}

	p := persona.Persona{ID: "scout", Name: "Scout", Model: "gpt-5",
		Prompt:  "YOU ARE SCOUT",
		Library: []string{"alpha"}, // no extension: normalized to alpha.md
	}
	got, skipped, err := a.assembleSystemPrompt(c.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v", skipped)
	}
	want := "YOU ARE SCOUT\n\nALPHA BODY\n\nZULU BODY"
	if got != want {
		t.Errorf("prompt =\n%q\nwant\n%q", got, want)
	}
}

// TestAssembleSystemPromptPutsPersonaItemsBeforeConversationItems verifies
// that the system prompt is assembled in the correct groups: persona body,
// then persona's library items, then conversation's items. This test inverts
// the alphabetical order against the grouping logic, so a naive
// merge-then-sort-then-dedup implementation would fail (alpha would come
// before zulu alphabetically, violating the expected grouping). Only a
// correctly-grouped implementation can pass.
func TestAssembleSystemPromptPutsPersonaItemsBeforeConversationItems(t *testing.T) {
	dir := t.TempDir()
	writeLib := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeLib("alpha.md", "# Alpha\n\nALPHA BODY\n")
	writeLib("zulu.md", "# Zulu\n\nZULU BODY\n")

	a := &API{cfg: config.Config{LibraryDir: dir}, lib: library.New(dir), st: testStore(t)}
	c, err := a.st.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	// Conversation attaches both alpha and zulu.
	if err := a.st.SetActiveItems(c.ID, []string{"alpha.md", "zulu.md"}); err != nil {
		t.Fatal(err)
	}

	// Persona claims only zulu in its library.
	p := persona.Persona{ID: "scout", Name: "Scout", Model: "gpt-5",
		Prompt:  "YOU ARE SCOUT",
		Library: []string{"zulu"}, // no extension: normalized to zulu.md
	}
	got, skipped, err := a.assembleSystemPrompt(c.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v", skipped)
	}
	// Correct output: persona body, then persona's claimed item (zulu),
	// then conversation-only item (alpha). This inverts alphabetical order,
	// so a naive sort would fail.
	want := "YOU ARE SCOUT\n\nZULU BODY\n\nALPHA BODY"
	if got != want {
		t.Errorf("prompt =\n%q\nwant\n%q", got, want)
	}
}
