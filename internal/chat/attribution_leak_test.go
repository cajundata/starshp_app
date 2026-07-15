package chat

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/provider"
)

// TestCanonicalEvents_NoPersonaOrModelLeak guards the six-field whitelist in
// canonicalEvents (Kind, Text, ToolCallID, ToolName, ToolInput, IsError). That
// whitelist is the only thing keeping PersonaID and Model — which live right
// next to those fields on store.ConversationEvent, joined in from runs for
// display attribution — out of the payload sent to the LLM provider. If a
// future edit widens the whitelist, or provider.Event grows a field that
// canonicalEvents starts populating from attribution data, this test must
// fail loudly.
func TestCanonicalEvents_NoPersonaOrModelLeak(t *testing.T) {
	st := openStore(t)
	conv, err := st.CreateConversation("c")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(st)
	sink := &captureSink{}

	const sentinelPersona = "leak-sentinel-persona-zzz"
	const sentinelModel = "leak-sentinel-model-zzz"

	if _, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID,
		UserText:       "hi",
		SystemPrompt:   "system",
		Model:          sentinelModel,
		PersonaID:      sentinelPersona,
		Provider:       oneShotProvider{text: "hello"},
		Resolver:       emptyResolver{},
		RetrievalMode:  RetrievalAutoGroundedDefault,
		Sink:           sink,
	}, nil); err != nil {
		t.Fatal(err)
	}

	rows, err := st.GetProviderReplayEvents(conv.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	// Sanity check: the source rows must actually carry the attribution, or
	// the rest of this test would pass vacuously.
	var sawAttributedRow bool
	for _, r := range rows {
		if r.PersonaID == sentinelPersona && r.Model == sentinelModel {
			sawAttributedRow = true
		}
	}
	if !sawAttributedRow {
		t.Fatalf("test setup broken: no row carries persona/model attribution: %+v", rows)
	}

	events := canonicalEvents(rows, rows[0].TurnID, sentinelPersona, nil)

	// Structural guard: provider.Event must not have grown a field that looks
	// like it could carry persona/model attribution.
	et := reflect.TypeOf(provider.Event{})
	for i := 0; i < et.NumField(); i++ {
		name := strings.ToLower(et.Field(i).Name)
		if strings.Contains(name, "persona") || strings.Contains(name, "model") {
			t.Fatalf("provider.Event grew a field that looks like attribution (%s) — "+
				"canonicalEvents' whitelist needs re-review", et.Field(i).Name)
		}
	}

	// Behavioral guard: nothing in the marshaled payload actually sent to the
	// provider contains the sentinel persona/model strings.
	for _, e := range events {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), sentinelPersona) {
			t.Errorf("provider event leaked persona ID: %s", b)
		}
		if strings.Contains(string(b), sentinelModel) {
			t.Errorf("provider event leaked model: %s", b)
		}
	}
}
