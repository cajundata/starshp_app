package eval

import (
	"context"
	"testing"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/eval/fakeprovider"
	"github.com/cajundata/starshp_app/internal/provider"
)

// The run_started event must carry attribution, so the frontend can color the
// bubble the moment it appears rather than after the run completes.
func TestRunStartedCarriesPersonaAndModel(t *testing.T) {
	st := openStore(t)
	svc := chat.New(st)
	c, err := st.CreateConversation("t")
	if err != nil {
		t.Fatal(err)
	}
	sink := &CaptureSink{}

	prov := &fakeprovider.Scripted{Iterations: [][]provider.Delta{
		{{Text: "hi"}, {Done: true, StopReason: "end_turn"}},
	}}

	_, err = svc.Send(context.Background(), chat.SendParams{
		ConversationID: c.ID,
		UserText:       "hello",
		SystemPrompt:   "be brief",
		Model:          "claude-opus-4-8",
		PersonaID:      "scout",
		Provider:       prov,
		ProviderName:   "anthropic",
		RetrievalMode:  chat.RetrievalAutoGroundedDefault,
		Sink:           sink,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var started *chat.SinkEvent
	for i := range sink.Events {
		if sink.Events[i].Kind == chat.SinkRunStarted {
			started = &sink.Events[i]
			break
		}
	}
	if started == nil {
		t.Fatal("no run_started event emitted")
	}
	if got := started.Payload["personaID"]; got != "scout" {
		t.Errorf("personaID = %v, want scout", got)
	}
	if got := started.Payload["modelID"]; got != "claude-opus-4-8" {
		t.Errorf("modelID = %v, want claude-opus-4-8", got)
	}
	if got := started.Payload["provider"]; got != "anthropic" {
		t.Errorf("provider = %v, want anthropic", got)
	}

	// And it is persisted, so a reopened conversation agrees with the live view.
	run, err := st.GetRun(started.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.PersonaID != "scout" {
		t.Errorf("runs.persona_id = %q, want scout", run.PersonaID)
	}
}
