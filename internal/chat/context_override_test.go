package chat

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

// spec2Canonical is Spec 2's persona-aware assembly, inlined verbatim as the
// byte-identical reference for Spec 3 (turn context overrides). A conversation
// with zero override rows must replay exactly as Spec 2 shipped it — row
// absence IS auto, so this guard is structural, not logical. If
// canonicalEvents with no overrides diverges from this, an existing
// conversation replays differently: the one failure this feature must never
// cause. Do not update this copy when chat.go changes — that is the point.
func spec2Canonical(rows []store.ConversationEvent, currentTurnID, currentPersonaID string, namer PersonaNamer) []provider.Event {
	predecessor := spec2Predecessor(rows, currentTurnID)
	out := make([]provider.Event, 0, len(rows))
	var batonTexts []string
	var batonPersona, batonModel string
	flushBaton := func() {
		if len(batonTexts) == 0 {
			return
		}
		name := batonPersona
		if namer != nil {
			if n, ok := namer.Name(batonPersona); ok {
				name = n
			}
		}
		out = append(out, provider.Event{
			Kind: store.EventKindUserMessage,
			Text: "From " + name + " (" + batonModel + "):\n" + strings.Join(batonTexts, "\n\n"),
		})
		batonTexts = nil
	}
	for _, r := range rows {
		if r.TurnID == currentTurnID {
			flushBaton()
		}
		foreign := r.PersonaID != "" && r.PersonaID != currentPersonaID
		switch {
		case r.Kind == store.EventKindUserMessage || !foreign:
			out = append(out, provider.Event{
				Kind: r.Kind, Text: r.Text,
				ToolCallID: r.ToolCallID, ToolName: r.ToolName,
				ToolInput: r.ToolInput, IsError: r.IsError,
			})
		case r.TurnID == predecessor && r.Kind == store.EventKindAssistantText:
			if len(batonTexts) == 0 {
				batonPersona, batonModel = r.PersonaID, r.Model
			}
			batonTexts = append(batonTexts, r.Text)
		}
	}
	flushBaton()
	return out
}

func spec2Predecessor(rows []store.ConversationEvent, currentTurnID string) string {
	prev := ""
	for _, r := range rows {
		if r.Kind != store.EventKindUserMessage {
			continue
		}
		if r.TurnID == currentTurnID {
			return prev
		}
		prev = r.TurnID
	}
	return ""
}

// TestZeroOverridesIsByteIdenticalToSpec2 re-runs Spec 2's payload fixtures —
// single-persona multi-turn with tools, the Scout → Skeptic → Scout thread,
// and legacy no-persona runs — through the full store → chat pipeline with
// zero override rows, and requires byte-identical provider payloads against
// the frozen Spec 2 reference. The spec's first test; everything else in
// Spec 3 is secondary to keeping this green.
func TestZeroOverridesIsByteIdenticalToSpec2(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T, st *store.Store, convID string) (turnID, runID, personaID string)
	}{
		{"single-persona multi-turn with tools", func(t *testing.T, st *store.Store, convID string) (string, string, string) {
			completedTurn(t, st, convID, "q1", "scout", "m1", true, "a1")
			completedTurn(t, st, convID, "q2", "scout", "m1", false, "a2")
			turnID, runID := currentTurn(t, st, convID, "q3", "scout", "m1")
			return turnID, runID, "scout"
		}},
		{"scout-skeptic-scout thread", func(t *testing.T, st *store.Store, convID string) (string, string, string) {
			completedTurn(t, st, convID, "find the angles", "scout", "m-scout", true, "scout answer")
			completedTurn(t, st, convID, "@skeptic poke holes", "skeptic", "m-skeptic", true, "skeptic critique")
			turnID, runID := currentTurn(t, st, convID, "respond to that", "scout", "m-scout")
			return turnID, runID, "scout"
		}},
		{"legacy no-persona runs", func(t *testing.T, st *store.Store, convID string) (string, string, string) {
			completedTurn(t, st, convID, "q1", "", "m1", true, "a1")
			completedTurn(t, st, convID, "q2", "", "m1", false, "a2")
			turnID, runID := currentTurn(t, st, convID, "q3", "scout", "m2")
			return turnID, runID, "scout"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t)
			conv, err := st.CreateConversation("guard")
			if err != nil {
				t.Fatal(err)
			}
			turnID, runID, personaID := tc.build(t, st, conv.ID)
			rows, err := st.GetProviderReplayEvents(conv.ID, runID)
			if err != nil {
				t.Fatal(err)
			}
			namer := stubNamer{"skeptic": "Skeptic", "scout": "Scout"}
			got := marshalEvents(t, canonicalEvents(rows, turnID, personaID, namer))
			want := marshalEvents(t, spec2Canonical(rows, turnID, personaID, namer))
			if got != want {
				t.Errorf("zero-override payload diverged from Spec 2:\n got %s\nwant %s", got, want)
			}
		})
	}
}

// always on a non-adjacent foreign turn produces the attributed block, in
// place, with tool blocks absent — Spec 2's immediate-predecessor treatment
// extended to any position. Multiple assistant_texts join into ONE block.
func TestAlwaysPinsANonAdjacentForeignTurn(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	pinned := completedTurn(t, st, conv.ID, "@skeptic scan this", "skeptic", "m-skeptic", true, "part one", "part two")
	completedTurn(t, st, conv.ID, "thanks", "scout", "m-scout", false, "scout ack")
	turnID, runID := currentTurn(t, st, conv.ID, "now use the findings", "scout", "m-scout")
	if err := st.SetTurnContextOverride(conv.ID, pinned, store.OverrideAlways); err != nil {
		t.Fatal(err)
	}

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "scout", stubNamer{"skeptic": "Skeptic"})

	kinds := make([]string, len(got))
	for i, e := range got {
		kinds[i] = e.Kind
	}
	want := []string{
		"user_message",   // @skeptic scan this
		"user_message",   // pinned Skeptic block, folded in place
		"user_message",   // thanks
		"assistant_text", // scout ack (own voice, verbatim)
		"user_message",   // now use the findings
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	if got[1].Text != "From Skeptic (m-skeptic):\npart one\n\npart two" {
		t.Errorf("pinned block = %q", got[1].Text)
	}
	// A foreign persona's tool events never appear in any payload, pinned or
	// not — Spec 2's dangling-ID reasoning is unchanged by pinning.
	for _, e := range got {
		if e.Kind == "assistant_tool_call" || e.Kind == "tool_result" {
			t.Errorf("foreign tool event leaked into a pinned payload: %+v", e)
		}
	}
	if n := countFromBlocks(got); n != 1 {
		t.Errorf("From-blocks = %d, want exactly 1", n)
	}
}

// always on the immediate predecessor must not duplicate it: the baton path
// already folds that turn, and the pin is satisfied by the same block.
func TestAlwaysOnTheImmediatePredecessorDoesNotDuplicate(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "q1", "scout", "m-scout", false, "scout one")
	pinned := completedTurn(t, st, conv.ID, "@skeptic poke", "skeptic", "m-skeptic", false, "skeptic critique")
	turnID, runID := currentTurn(t, st, conv.ID, "respond", "scout", "m-scout")
	if err := st.SetTurnContextOverride(conv.ID, pinned, store.OverrideAlways); err != nil {
		t.Fatal(err)
	}

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "scout", stubNamer{"skeptic": "Skeptic"})
	if n := countFromBlocks(got); n != 1 {
		t.Fatalf("From-blocks = %d, want exactly 1 (no double inclusion)", n)
	}
	found := false
	for _, e := range got {
		if e.Text == "From Skeptic (m-skeptic):\nskeptic critique" {
			found = true
		}
	}
	if !found {
		t.Errorf("predecessor baton missing from %+v", got)
	}
}

// never on the immediate predecessor means the next persona gets no baton —
// identical to Spec 2's errored-predecessor case. Not an error. The whole
// exchange goes: the excluded turn's operator message vanishes too.
func TestNeverOnTheImmediatePredecessorMeansNoBaton(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "q1", "scout", "m-scout", false, "scout one")
	excluded := completedTurn(t, st, conv.ID, "@skeptic poke", "skeptic", "m-skeptic", false, "skeptic critique")
	turnID, runID := currentTurn(t, st, conv.ID, "q3", "scout", "m-scout")
	if err := st.SetTurnContextOverride(conv.ID, excluded, store.OverrideNever); err != nil {
		t.Fatal(err)
	}

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "scout", stubNamer{"skeptic": "Skeptic"})
	if n := countFromBlocks(got); n != 0 {
		t.Errorf("From-blocks = %d, want 0 (no baton to pass)", n)
	}
	for _, e := range got {
		if e.Text == "@skeptic poke" || e.Text == "skeptic critique" {
			t.Errorf("excluded turn leaked into payload: %q", e.Text)
		}
	}
}

// always on the current persona's own turn is a forward guarantee, not a
// format change: the payload is byte-identical to the same thread with no
// override. (Fixtures use no tools so tool-call IDs — derived from random
// run IDs — cannot differ between the two conversations.)
func TestAlwaysOnAnOwnPersonaTurnStaysVerbatim(t *testing.T) {
	st := openStore(t)

	build := func(name string) (convID, firstTurn, currentTurnID, runID string) {
		conv, err := st.CreateConversation(name)
		if err != nil {
			t.Fatal(err)
		}
		firstTurn = completedTurn(t, st, conv.ID, "q1", "scout", "m-scout", false, "a1")
		completedTurn(t, st, conv.ID, "q2", "scout", "m-scout", false, "a2")
		currentTurnID, runID = currentTurn(t, st, conv.ID, "q3", "scout", "m-scout")
		return conv.ID, firstTurn, currentTurnID, runID
	}

	pinConv, pinTurn, pinCurrent, pinRun := build("pinned")
	if err := st.SetTurnContextOverride(pinConv, pinTurn, store.OverrideAlways); err != nil {
		t.Fatal(err)
	}
	plainConv, _, plainCurrent, plainRun := build("plain")

	pinRows, err := st.GetProviderReplayEvents(pinConv, pinRun)
	if err != nil {
		t.Fatal(err)
	}
	plainRows, err := st.GetProviderReplayEvents(plainConv, plainRun)
	if err != nil {
		t.Fatal(err)
	}
	got := marshalEvents(t, canonicalEvents(pinRows, pinCurrent, "scout", nil))
	want := marshalEvents(t, canonicalEvents(plainRows, plainCurrent, "scout", nil))
	if got != want {
		t.Errorf("own-persona pin changed the payload:\n got %s\nwant %s", got, want)
	}
}

// Defensive never-skip: the store already filters never turns, but if a row
// slips through (constructed here by hand), canonicalEvents drops it too —
// except the current turn, which an override never governs (rule 2).
func TestChatSkipsNeverRowsDefensivelyButNeverTheCurrentTurn(t *testing.T) {
	rows := []store.ConversationEvent{
		{TurnID: "t1", Kind: store.EventKindUserMessage, Text: "q1", ContextOverride: store.OverrideNever},
		{TurnID: "t1", RunID: "r1", PersonaID: "scout", Model: "m", Kind: store.EventKindAssistantText, Text: "a1", ContextOverride: store.OverrideNever},
		{TurnID: "t2", Kind: store.EventKindUserMessage, Text: "q2", ContextOverride: store.OverrideNever},
	}
	// t2 is the current turn: its never row must NOT hide its own prompt.
	got := canonicalEvents(rows, "t2", "scout", nil)
	if len(got) != 1 || got[0].Text != "q2" {
		t.Errorf("payload = %+v, want exactly the current turn's user message", got)
	}
}
