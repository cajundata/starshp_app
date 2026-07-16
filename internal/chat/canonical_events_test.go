package chat

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/google/uuid"
)

// completedTurn persists one full turn: a user message, then a completed run
// for personaID/model with an optional tool round and one assistant_text per
// entry in texts. Returns the turn ID.
func completedTurn(t *testing.T, st *store.Store, convID, userText, personaID, model string, withTool bool, texts ...string) string {
	t.Helper()
	u, err := st.AppendUserMessage(convID, userText)
	if err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	if err := st.CreateRun(convID, u.TurnID, runID, "openai", model, "auto_grounded_default", personaID); err != nil {
		t.Fatal(err)
	}
	if withTool {
		callID := "call-" + runID[:8]
		if _, err := st.AppendAssistantToolCall(convID, u.TurnID, runID, callID,
			"safemath", json.RawMessage(`{"expression":"2+2"}`), nil); err != nil {
			t.Fatal(err)
		}
		if _, err := st.AppendToolResult(convID, u.TurnID, runID, callID,
			"safemath", "4", nil, false, 3); err != nil {
			t.Fatal(err)
		}
	}
	for _, txt := range texts {
		if _, err := st.AppendAssistantText(convID, u.TurnID, runID, txt); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CompleteRun(runID, store.RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}
	return u.TurnID
}

// currentTurn persists the in-flight turn: a user message plus an in_progress
// run, exactly the state runLoop sees on its first provider call.
func currentTurn(t *testing.T, st *store.Store, convID, userText, personaID, model string) (turnID, runID string) {
	t.Helper()
	u, err := st.AppendUserMessage(convID, userText)
	if err != nil {
		t.Fatal(err)
	}
	runID = uuid.NewString()
	if err := st.CreateRun(convID, u.TurnID, runID, "openai", model, "auto_grounded_default", personaID); err != nil {
		t.Fatal(err)
	}
	return u.TurnID, runID
}

// legacyCanonical is the pre-Spec-2 mapping, inlined verbatim as the
// byte-identical reference: every selected row passes through the six-field
// whitelist. If canonicalEvents diverges from this on a single-persona or
// no-persona thread, an existing conversation replays differently — the one
// failure this feature must never cause.
func legacyCanonical(rows []store.ConversationEvent) []provider.Event {
	out := make([]provider.Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, provider.Event{
			Kind: r.Kind, Text: r.Text,
			ToolCallID: r.ToolCallID, ToolName: r.ToolName,
			ToolInput: r.ToolInput, IsError: r.IsError,
		})
	}
	return out
}

func marshalEvents(t *testing.T, evs []provider.Event) string {
	t.Helper()
	b, err := json.Marshal(evs)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCanonicalEvents_SinglePersonaThreadIsByteIdenticalToLegacy(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("guard")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "q1", "scout", "m1", true, "a1")
	completedTurn(t, st, conv.ID, "q2", "scout", "m1", false, "a2")
	turnID, runID := currentTurn(t, st, conv.ID, "q3", "scout", "m1")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := marshalEvents(t, canonicalEvents(rows, turnID, "scout", nil))
	want := marshalEvents(t, legacyCanonical(rows))
	if got != want {
		t.Errorf("single-persona payload diverged from legacy:\n got %s\nwant %s", got, want)
	}
}

func TestCanonicalEvents_LegacyNoPersonaRunsAreByteIdenticalToLegacy(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("guard-legacy")
	if err != nil {
		t.Fatal(err)
	}
	// Runs recorded before personas existed: persona_id empty. They are the
	// current persona's own voice, never relabeled "From (unknown)".
	completedTurn(t, st, conv.ID, "q1", "", "m1", true, "a1")
	completedTurn(t, st, conv.ID, "q2", "", "m1", false, "a2")
	turnID, runID := currentTurn(t, st, conv.ID, "q3", "scout", "m2")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := marshalEvents(t, canonicalEvents(rows, turnID, "scout", nil))
	want := marshalEvents(t, legacyCanonical(rows))
	if got != want {
		t.Errorf("legacy no-persona payload diverged:\n got %s\nwant %s", got, want)
	}
}

// TestCanonicalEvents_ToolMetadataSurvivesForSamePersona is the chat-level
// half of the Gemini thought_signature invariant: a stored tool_metadata
// value on an assistant_tool_call row must reach the provider Event for the
// current turn's own-persona replay (Task 1's copy in canonicalEvents).
func TestCanonicalEvents_ToolMetadataSurvivesForSamePersona(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("meta")
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.AppendUserMessage(conv.ID, "add 2+2")
	if err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	if err := st.CreateRun(conv.ID, u.TurnID, runID, "gemini", "gemini-3-pro", "auto_grounded_default", "scout"); err != nil {
		t.Fatal(err)
	}
	meta := json.RawMessage(`{"thought_signature":"c2lnLWJ5dGVzLTE="}`)
	if _, err := st.AppendAssistantToolCall(conv.ID, u.TurnID, runID, "call_1",
		"safe_math", json.RawMessage(`{"expr":"2+2"}`), meta); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendToolResult(conv.ID, u.TurnID, runID, "call_1",
		"safe_math", "4", nil, false, 3); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteRun(runID, store.RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}
	turnID, curRunID := currentTurn(t, st, conv.ID, "and 3+3?", "scout", "gemini-3-pro")

	rows, err := st.GetProviderReplayEvents(conv.ID, curRunID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "scout", nil)
	found := false
	for _, e := range got {
		if e.Kind == store.EventKindAssistantToolCall && e.ToolCallID == "call_1" {
			found = true
			if string(e.ToolMetadata) != string(meta) {
				t.Fatalf("ToolMetadata = %s, want %s", e.ToolMetadata, meta)
			}
		}
	}
	if !found {
		t.Fatal("assistant_tool_call event not found in canonicalEvents output")
	}
}

type stubNamer map[string]string

func (m stubNamer) Name(id string) (string, bool) {
	n, ok := m[id]
	return n, ok
}

// countFromBlocks returns how many events carry a handoff attribution header.
func countFromBlocks(evs []provider.Event) int {
	n := 0
	for _, e := range evs {
		if strings.HasPrefix(e.Text, "From ") {
			n++
		}
	}
	return n
}

// The core Scout → Skeptic → Scout thread from the spec. At turn 3 Scout
// must see its own turn-1 events verbatim (tool blocks included), Skeptic's
// turn-2 text folded into an attributed user-role block (tool blocks
// dropped), and every operator message in order.
func TestCanonicalEvents_FoldsTheImmediateForeignPredecessor(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "find the angles", "scout", "m-scout", true, "scout answer")
	completedTurn(t, st, conv.ID, "@skeptic poke holes", "skeptic", "m-skeptic", true, "skeptic critique")
	turnID, runID := currentTurn(t, st, conv.ID, "respond to that", "scout", "m-scout")

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
		"user_message",        // find the angles
		"assistant_tool_call", // scout's own tool round survives
		"tool_result",
		"assistant_text", // scout answer
		"user_message",   // @skeptic poke holes (mention not stripped)
		"user_message",   // folded Skeptic baton
		"user_message",   // respond to that
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	if got[5].Text != "From Skeptic (m-skeptic):\nskeptic critique" {
		t.Errorf("baton = %q", got[5].Text)
	}
	if got[4].Text != "@skeptic poke holes" {
		t.Errorf("operator message altered: %q", got[4].Text)
	}
	// A foreign persona's tool events never appear in any payload: the only
	// tool pair present is scout's own from turn 1.
	toolEvents := 0
	for _, e := range got {
		if e.Kind == "assistant_tool_call" || e.Kind == "tool_result" {
			toolEvents++
		}
	}
	if toolEvents != 2 {
		t.Errorf("tool events = %d, want 2 (scout's own pair only)", toolEvents)
	}
}

// A foreign persona that is neither the current persona nor the immediate
// predecessor is omitted entirely — its operator message stays.
func TestCanonicalEvents_OmitsANonAdjacentForeignTurn(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "q1", "scout", "m-scout", false, "scout one")
	completedTurn(t, st, conv.ID, "@skeptic poke", "skeptic", "m-skeptic", false, "skeptic critique")
	completedTurn(t, st, conv.ID, "q3", "scout", "m-scout", false, "scout two")
	turnID, runID := currentTurn(t, st, conv.ID, "q4", "scout", "m-scout")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "scout", stubNamer{"skeptic": "Skeptic"})

	for _, e := range got {
		if strings.Contains(e.Text, "skeptic critique") {
			t.Errorf("non-adjacent foreign output leaked into payload: %q", e.Text)
		}
	}
	if n := countFromBlocks(got); n != 0 {
		t.Errorf("From-blocks = %d, want 0", n)
	}
	// The operator's messages all survive, including the mentioned one.
	var userTexts []string
	for _, e := range got {
		if e.Kind == "user_message" {
			userTexts = append(userTexts, e.Text)
		}
	}
	if !reflect.DeepEqual(userTexts, []string{"q1", "@skeptic poke", "q3", "q4"}) {
		t.Errorf("user messages = %v", userTexts)
	}
}

// An errored predecessor has no completed active run, so there is no baton —
// not an error. The next persona sees the operator's messages and its own
// history only.
func TestCanonicalEvents_ErroredPredecessorMeansNoBaton(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "q1", "scout", "m-scout", false, "scout one")
	u, err := st.AppendUserMessage(conv.ID, "@skeptic poke")
	if err != nil {
		t.Fatal(err)
	}
	erroredRun := uuid.NewString()
	if err := st.CreateRun(conv.ID, u.TurnID, erroredRun, "openai", "m-skeptic", "auto_grounded_default", "skeptic"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRunErrored(erroredRun, "provider_error", "auth", "no key"); err != nil {
		t.Fatal(err)
	}
	turnID, runID := currentTurn(t, st, conv.ID, "q3", "scout", "m-scout")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "scout", stubNamer{"skeptic": "Skeptic"})
	if n := countFromBlocks(got); n != 0 {
		t.Errorf("From-blocks = %d, want 0 (no baton to pass)", n)
	}
}

// A deleted persona file: the namer cannot resolve the ID, so the
// attribution line falls back to the literal ID — consistent with how the
// bubbles render a deleted persona.
func TestCanonicalEvents_NamerFallsBackToTheLiteralID(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "@skeptic poke", "skeptic", "m-skeptic", false, "critique")
	turnID, runID := currentTurn(t, st, conv.ID, "and?", "scout", "m-scout")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, namer := range []PersonaNamer{stubNamer{}, nil} {
		got := canonicalEvents(rows, turnID, "scout", namer)
		found := false
		for _, e := range got {
			if e.Text == "From skeptic (m-skeptic):\ncritique" {
				found = true
			}
		}
		if !found {
			t.Errorf("namer=%v: no literal-ID baton in %+v", namer, got)
		}
	}
}

// A foreign predecessor whose run produced several assistant_text events
// (multi-iteration tool runs do this) folds into ONE attributed block.
func TestCanonicalEvents_JoinsMultipleForeignTextsIntoOneBaton(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("thread")
	if err != nil {
		t.Fatal(err)
	}
	completedTurn(t, st, conv.ID, "@skeptic poke", "skeptic", "m-skeptic", true, "part one", "part two")
	turnID, runID := currentTurn(t, st, conv.ID, "and?", "scout", "m-scout")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "scout", stubNamer{"skeptic": "Skeptic"})
	if n := countFromBlocks(got); n != 1 {
		t.Fatalf("From-blocks = %d, want exactly 1", n)
	}
	want := "From Skeptic (m-skeptic):\npart one\n\npart two"
	found := false
	for _, e := range got {
		if e.Text == want {
			found = true
		}
	}
	if !found {
		t.Errorf("joined baton %q not found in %+v", want, got)
	}
}

// imageTurn persists a completed turn whose run emitted images (with optional
// interleaved text before each image is not modeled here — hashes only).
func imageTurn(t *testing.T, st *store.Store, convID, userText, personaID, model string, hashes ...string) string {
	t.Helper()
	u, err := st.AppendUserMessage(convID, userText)
	if err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	if err := st.CreateRun(convID, u.TurnID, runID, "gemini", model, "auto_grounded_default", personaID); err != nil {
		t.Fatal(err)
	}
	for _, h := range hashes {
		if _, err := st.AppendAssistantImage(convID, u.TurnID, runID, h); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CompleteRun(runID, store.RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}
	return u.TurnID
}

func TestCanonicalEvents_OwnImageKeepsHash(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	imageTurn(t, st, conv.ID, "draw a cat", "artist", "gemini-3-pro-image", strings.Repeat("aa", 32))
	turnID, runID := currentTurn(t, st, conv.ID, "make the sky darker", "artist", "gemini-3-pro-image")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "artist", nil)
	// user / assistant_image / user — the artist keeps its own image event.
	if len(got) != 3 {
		t.Fatalf("events = %+v, want 3", got)
	}
	if got[1].Kind != store.EventKindAssistantImage || got[1].ImageHash != strings.Repeat("aa", 32) {
		t.Fatalf("own image event = %+v", got[1])
	}
}

func TestCanonicalEvents_ForeignImageTextualizesInBaton(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	prompt := "draw a cat sitting on " + strings.Repeat("a very long fence ", 20) // > 120 runes
	imageTurn(t, st, conv.ID, prompt, "artist", "gemini-3-pro-image", strings.Repeat("aa", 32))
	turnID, runID := currentTurn(t, st, conv.ID, "what do you think of it?", "copywriter", "claude-x")

	rows, err := st.GetProviderReplayEvents(conv.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "copywriter", nil)
	// user(prompt) / attributed baton block / user(current) — no image event survives.
	for _, e := range got {
		if e.Kind == store.EventKindAssistantImage {
			t.Fatalf("foreign image leaked as event: %+v", e)
		}
	}
	var baton string
	for _, e := range got {
		if strings.HasPrefix(e.Text, "From artist (gemini-3-pro-image):") {
			baton = e.Text
		}
	}
	if baton == "" {
		t.Fatalf("no attributed baton block in %+v", got)
	}
	if !strings.Contains(baton, `[image — generated from: "`) {
		t.Fatalf("baton lacks image line: %q", baton)
	}
	if !strings.Contains(baton, "…") {
		t.Fatalf("prompt not truncated at 120 runes: %q", baton)
	}
	if strings.Contains(baton, strings.Repeat("aa", 32)) {
		t.Fatalf("baton leaks raw hash: %q", baton)
	}
}

func TestInflateImages_CapNewestAndSkipMissing(t *testing.T) {
	images := newFakeImages()
	var events []provider.Event
	var hashes []string
	for i := 0; i < 8; i++ {
		h, _ := images.Put([]byte{byte(i)})
		hashes = append(hashes, h)
		events = append(events, provider.Event{Kind: store.EventKindAssistantImage, ImageHash: h})
	}
	// Delete the newest image's file: it must not consume a cap slot.
	delete(images.files, hashes[7])

	inflateImages(events, images, 6)

	if len(events[7].ImageData) != 0 {
		t.Fatal("deleted image inflated")
	}
	// Newest 6 readable images (indexes 6,5,4,3,2,1) carry bytes; 0 does not.
	for _, i := range []int{6, 5, 4, 3, 2, 1} {
		if len(events[i].ImageData) == 0 {
			t.Fatalf("events[%d] not inflated", i)
		}
	}
	if len(events[0].ImageData) != 0 {
		t.Fatal("event beyond cap inflated")
	}
}
