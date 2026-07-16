package appapi

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/persona"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

// When ragAdpt is nil but the conversation has a textbook scope, SendMessage
// must not panic. It will fail earlier at provider.New (no API key / unknown
// model) and return a normalized error — the point is: no nil-pointer crash.
func TestSendMessageNilRagAdapterNoPanic(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	conv, _ := st.CreateConversation("t")
	if err := st.SetConversationTextbooks(conv.ID, []store.TextbookScope{{Name: "ia", Chapters: []int{1}}}); err != nil {
		t.Fatalf("SetConversationTextbooks: %v", err)
	}

	reg := provider.Registry{Models: []provider.ModelInfo{{Display: "X", ID: "m1", Provider: "openai"}}}
	api := NewAPI(config.Config{}, st, reg, nil) // ragAdpt == nil on purpose

	// Must return an error (no OpenAI key), and crucially must NOT panic.
	err = api.SendMessage(conv.ID, "hello", "m1")
	if err == nil {
		t.Fatal("expected an error (no API key), got nil")
	}
	// Sanity: it's a provider AppError, not a panic/raw.
	if !strings.Contains(strings.ToLower(err.Error()), "key") {
		t.Logf("note: error was %q (acceptable as long as no panic)", err.Error())
	}
}

// disableUnrenderablePersonas gates on renderability, not just modality:
// text always renders; image renders only through the gemini adapter. A
// model with no OutputModalities at all is treated as text-capable.
func TestDisableUnrenderablePersonas(t *testing.T) {
	models := provider.Registry{Models: []provider.ModelInfo{
		{ID: "text-model", Provider: "openai", OutputModalities: []string{"text"}},
		{ID: "nano-banana", Provider: "gemini", OutputModalities: []string{"text", "image"}},
		{ID: "gemini-image-only", Provider: "gemini", OutputModalities: []string{"image"}},
		{ID: "openai-image-only", Provider: "openai", OutputModalities: []string{"image"}},
		{ID: "no-modalities", Provider: "anthropic"},
	}}
	reg := persona.Registry{Personas: []persona.Persona{
		{ID: "writer", Model: "text-model"},
		{ID: "artist", Model: "nano-banana"},
		{ID: "pure-artist", Model: "gemini-image-only"},
		{ID: "broken", Model: "openai-image-only"},
		{ID: "vintage", Model: "no-modalities"},
	}}

	got := disableUnrenderablePersonas(reg, models)

	kept := map[string]bool{}
	for _, p := range got.Personas {
		kept[p.ID] = true
	}
	for _, want := range []string{"writer", "artist", "pure-artist", "vintage"} {
		if !kept[want] {
			t.Errorf("persona %s should be kept; issues: %+v", want, got.Issues)
		}
	}
	if kept["broken"] {
		t.Error("persona pinned to image-only non-gemini model must be disabled")
	}
	if len(got.Issues) != 1 || got.Issues[0].File != "broken.md" {
		t.Fatalf("issues = %+v, want exactly broken.md", got.Issues)
	}
}

func TestTitleFromText(t *testing.T) {
	if got := titleFromText("  Draft a post on ASC 606  "); got != "Draft a post on ASC 606" {
		t.Fatalf("got %q", got)
	}
	if got := titleFromText(""); got != "New conversation" {
		t.Fatalf("empty: got %q", got)
	}
	long := ""
	for i := 0; i < 100; i++ {
		long += "x"
	}
	got := titleFromText(long)
	if []rune(got)[len([]rune(got))-1] != '…' || len([]rune(got)) != 61 {
		t.Fatalf("truncation wrong: len=%d got=%q", len([]rune(got)), got)
	}
	if got := titleFromText("line1\nline2"); got != "line1 line2" {
		t.Fatalf("newline: got %q", got)
	}
}

// TestCancelMessageNoInFlightIsNoop ensures that calling CancelMessage when no
// stream is in flight does not panic (guards the nil cancelInFlight path).
func TestCancelMessageNoInFlightIsNoop(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	reg := provider.Registry{Models: []provider.ModelInfo{{Display: "X", ID: "m1", Provider: "openai"}}}
	api := NewAPI(config.Config{}, st, reg, nil)
	// Must not panic.
	api.CancelMessage()
}
