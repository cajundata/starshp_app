package appapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/cajundata/starshp_app/internal/tools/safemath"
	"github.com/cajundata/starshp_app/internal/tools/searchtextbook"
)

// testStore opens a fresh store.Store backed by a temp-dir database. No other
// appapi test file defines a shared store helper (each opens store.Open
// inline), so this is the one persona_test.go and library_test.go share.
func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// allToolNames must list exactly the tools the app can register. If a tool is
// added and this list is not updated, every persona naming it is rejected —
// a silent, confusing failure. This test makes that impossible.
func TestAllToolNamesMatchesTheRegisterableTools(t *testing.T) {
	live := []string{
		safemath.New().Name(),
		searchtextbook.New(nil, nil, 4000).Name(),
	}
	sort.Strings(live)
	got := append([]string(nil), allToolNames...)
	sort.Strings(got)
	if len(got) != len(live) {
		t.Fatalf("allToolNames = %v, registerable tools = %v", got, live)
	}
	for i := range live {
		if got[i] != live[i] {
			t.Errorf("allToolNames = %v, registerable tools = %v", got, live)
			break
		}
	}
}

func newPersonaAPI(t *testing.T, files map[string]string) *API {
	t.Helper()
	dir := t.TempDir()
	pdir := filepath.Join(dir, "personas")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(pdir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{
		PersonaDir: pdir,
		LibraryDir: filepath.Join(dir, "library"),
		AppDBPath:  filepath.Join(dir, "app.db"),
	}
	reg := provider.Registry{Models: []provider.ModelInfo{
		{ID: "gpt-5", Display: "GPT-5", Provider: "openai"},
	}}
	st := testStore(t) // match the helper the other appapi tests use
	return NewAPI(cfg, st, reg, nil)
}

func TestPersonasBindingReturnsLoadedPersonas(t *testing.T) {
	a := newPersonaAPI(t, map[string]string{
		"scout.md": "---\nname: Scout\nmodel: gpt-5\ncolor: \"#4fb3ff\"\n---\nYou are Scout.\n",
	})
	ps := a.Personas()
	if len(ps) != 1 || ps[0].ID != "scout" {
		t.Fatalf("Personas() = %+v", ps)
	}
	// Persona.Prompt is legitimately non-empty in memory — SendMessage's
	// a.personas.ByID needs the body to build the system prompt. What must not
	// happen is the body crossing the Wails JSON boundary to the frontend, which
	// is what `json:"-"` guarantees. Assert that, not the in-memory field.
	b, err := json.Marshal(ps[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "You are Scout") {
		t.Errorf("Persona.Prompt leaked into JSON: %s", b)
	}
}

func TestStartupIssuesReportsRejectedPersonas(t *testing.T) {
	a := newPersonaAPI(t, map[string]string{
		"broken.md": "---\nname: Broken\nmodel: no-such-model\n---\nbody\n",
	})
	var found bool
	for _, s := range a.StartupIssues() {
		if strings.Contains(s, "broken.md") && strings.Contains(s, "unknown model") {
			found = true
		}
	}
	if !found {
		t.Errorf("StartupIssues() = %v, want a line naming broken.md and unknown model", a.StartupIssues())
	}
}

// An unknown persona ID is a hard error. Falling back to a default persona
// would attribute output to an assistant the operator did not choose — the
// exact failure this feature exists to prevent.
func TestSendMessageRejectsAnUnknownPersona(t *testing.T) {
	a := newPersonaAPI(t, map[string]string{
		"scout.md": "---\nname: Scout\nmodel: gpt-5\n---\nYou are Scout.\n",
	})
	c, err := a.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	err = a.SendMessage(c.ID, "hello", "ghost")
	if err == nil {
		t.Fatal("SendMessage with an unknown persona returned nil")
	}
	ae, ok := err.(provider.AppError)
	if !ok {
		t.Fatalf("error is %T, want provider.AppError", err)
	}
	if ae.Code != "config" {
		t.Errorf("Code = %q, want config", ae.Code)
	}
}

// A personas folder that exists but yields nothing valid must explain itself.
// The operator sees why the picker is empty instead of a bare "unknown
// assistant" for a persona they never got the chance to select.
func TestSendMessageWithNoValidPersonasNamesTheValidationFailures(t *testing.T) {
	a := newPersonaAPI(t, map[string]string{
		"broken.md": "---\nname: Broken\nmodel: no-such-model\n---\nbody\n",
	})
	c, err := a.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	err = a.SendMessage(c.ID, "hello", "")
	ae, ok := err.(provider.AppError)
	if !ok {
		t.Fatalf("error is %T, want provider.AppError", err)
	}
	if ae.Code != "config" {
		t.Errorf("Code = %q, want config", ae.Code)
	}
	if !strings.Contains(ae.UserMessage, "broken.md") {
		t.Errorf("UserMessage = %q, want it to name the file that failed", ae.UserMessage)
	}
}

// A persona pinned to a model whose image output renders through the gemini
// adapter must load normally: gemini image output is renderable, so this is
// no longer a gate failure (see TestDisableUnrenderablePersonas in
// api_test.go for the non-gemini image-only case, which still disables).
func TestPersonaOnGeminiImageOnlyModelIsKept(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "personas")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "painter.md"),
		[]byte("---\nname: Painter\nmodel: nano-banana-2\n---\nYou paint.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		PersonaDir: pdir,
		LibraryDir: filepath.Join(dir, "library"),
		AppDBPath:  filepath.Join(dir, "app.db"),
	}
	reg := provider.Registry{Models: []provider.ModelInfo{
		{ID: "nano-banana-2", Display: "Nano Banana 2", Provider: "gemini", OutputModalities: []string{"image"}},
	}}
	a := NewAPI(cfg, testStore(t), reg, nil)

	ps := a.Personas()
	if len(ps) != 1 || ps[0].ID != "painter" {
		t.Fatalf("Personas() = %+v, want painter kept (gemini image-output model is renderable)", ps)
	}
	for _, s := range a.StartupIssues() {
		if strings.Contains(s, "painter.md") {
			t.Errorf("StartupIssues() = %v, want no issue naming painter.md", a.StartupIssues())
		}
	}
}

// A persona pinned to a model that has no explicit output_modalities (the
// default text-only case) must load normally — the gate only fires on an
// explicit non-text list.
func TestPersonaOnDefaultModalityModelUnaffected(t *testing.T) {
	a := newPersonaAPI(t, map[string]string{
		"scout.md": "---\nname: Scout\nmodel: gpt-5\n---\nYou are Scout.\n",
	})
	ps := a.Personas()
	if len(ps) != 1 || ps[0].ID != "scout" {
		t.Fatalf("Personas() = %+v, want scout loaded (default text-output model)", ps)
	}
}

func TestSeedsAnAssistantWhenThePersonaDirIsAbsent(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		PersonaDir: filepath.Join(dir, "personas"), // does not exist
		LibraryDir: filepath.Join(dir, "library"),
	}
	reg := provider.Registry{Models: []provider.ModelInfo{{ID: "gpt-5", Display: "GPT-5", Provider: "openai"}}}
	a := NewAPI(cfg, testStore(t), reg, nil)
	ps := a.Personas()
	if len(ps) != 1 || ps[0].ID != "assistant" {
		t.Fatalf("Personas() = %+v, want a seeded assistant", ps)
	}
	if ps[0].Model != "gpt-5" {
		t.Errorf("seeded model = %q, want the first model in the registry", ps[0].Model)
	}
}
