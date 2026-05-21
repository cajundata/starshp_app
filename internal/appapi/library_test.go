package appapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

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
	prompt, skipped, err := api.assembleSystemPrompt(conv.ID)
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
	prompt, skipped, err := api.assembleSystemPrompt(conv.ID)
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
