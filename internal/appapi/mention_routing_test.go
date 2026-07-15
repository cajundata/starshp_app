package appapi

import (
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/provider"
)

func twoPersonaAPI(t *testing.T) *API {
	t.Helper()
	return newPersonaAPI(t, map[string]string{
		"scout.md":   "---\nname: Scout\nmodel: gpt-5\n---\nYou are Scout.\n",
		"skeptic.md": "---\nname: Skeptic\nmodel: gpt-5\n---\nYou are Skeptic.\n",
	})
}

// A leading mention routes exactly one turn to the mentioned persona,
// overriding the picker.
func TestRoutePersonaMentionOverridesThePicker(t *testing.T) {
	a := twoPersonaAPI(t)
	p, err := a.routePersona("@skeptic poke holes", "scout")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "skeptic" {
		t.Errorf("routed to %q, want skeptic", p.ID)
	}
}

// Case-insensitive: @Skeptic routes to skeptic.
func TestRoutePersonaMentionIsCaseInsensitive(t *testing.T) {
	a := twoPersonaAPI(t)
	p, err := a.routePersona("@Skeptic poke holes", "scout")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "skeptic" {
		t.Errorf("routed to %q, want skeptic", p.ID)
	}
}

// No mention → the picker's persona, exactly as before.
func TestRoutePersonaNoMentionUsesThePicker(t *testing.T) {
	a := twoPersonaAPI(t)
	p, err := a.routePersona("ask @skeptic about it", "scout") // not leading
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "scout" {
		t.Errorf("routed to %q, want scout", p.ID)
	}
}

// An unresolvable mention is a hard error that lists the real persona IDs —
// never a silent substitution, never fuzzy matching.
func TestRoutePersonaUnknownMentionListsAvailablePersonas(t *testing.T) {
	a := twoPersonaAPI(t)
	_, err := a.routePersona("@skpetic poke holes", "scout")
	if err == nil {
		t.Fatal("unknown mention returned nil error")
	}
	ae, ok := err.(provider.AppError)
	if !ok {
		t.Fatalf("error is %T, want provider.AppError", err)
	}
	if ae.Code != "config" {
		t.Errorf("Code = %q, want config", ae.Code)
	}
	for _, want := range []string{`"skpetic"`, "scout", "skeptic"} {
		if !strings.Contains(ae.UserMessage, want) {
			t.Errorf("UserMessage = %q, want it to contain %s", ae.UserMessage, want)
		}
	}
}

// A mention when zero personas loaded falls back to the explain-the-failures
// message from Spec 1 rather than "Available: " with an empty list.
func TestRoutePersonaMentionWithNoValidPersonasExplainsItself(t *testing.T) {
	a := newPersonaAPI(t, map[string]string{
		"broken.md": "---\nname: Broken\nmodel: no-such-model\n---\nbody\n",
	})
	_, err := a.routePersona("@scout hi", "scout")
	ae, ok := err.(provider.AppError)
	if !ok {
		t.Fatalf("error is %T, want provider.AppError", err)
	}
	if !strings.Contains(ae.UserMessage, "broken.md") {
		t.Errorf("UserMessage = %q, want it to name the failing file", ae.UserMessage)
	}
}

// SendMessage never writes pinned_persona — pinning is the frontend's
// separate SetConversationPersona call, which a mentioned turn does not
// change. The pin must still read "scout" after a @skeptic send attempt.
func TestSendMessageWithMentionDoesNotRepin(t *testing.T) {
	a := twoPersonaAPI(t)
	c, err := a.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetConversationPersona(c.ID, "scout"); err != nil {
		t.Fatal(err)
	}
	// Errors at provider.New (no API key) — after routing, before any write.
	_ = a.SendMessage(c.ID, "@skeptic poke holes", "scout")
	convs, err := a.st.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	for _, cv := range convs {
		if cv.ID == c.ID && cv.PinnedPersona != "scout" {
			t.Errorf("PinnedPersona = %q, want scout (unchanged)", cv.PinnedPersona)
		}
	}
}

// An unresolvable mention persists nothing: no user message, no run. The
// operator's text stays in the composer, and a reload shows no new turn.
func TestSendMessageUnknownMentionPersistsNothing(t *testing.T) {
	a := twoPersonaAPI(t)
	c, err := a.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SendMessage(c.ID, "@skpetic poke holes", "scout"); err == nil {
		t.Fatal("SendMessage with unknown mention returned nil")
	}
	events, err := a.st.GetConversationDisplayEvents(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Errorf("events persisted after a rejected mention: %+v", events)
	}
}
