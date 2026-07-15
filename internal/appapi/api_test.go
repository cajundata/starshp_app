package appapi

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/config"
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
