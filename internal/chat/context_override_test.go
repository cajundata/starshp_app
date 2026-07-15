package chat

import (
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
