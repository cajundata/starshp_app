package store

import "testing"

func TestDisplayEventsCarryPersonaAndModel(t *testing.T) {
	s := openTestStore(t)
	c, _ := s.CreateConversation("t")
	u, _ := s.AppendUserMessage(c.ID, "hi")
	if err := s.CreateRun(c.ID, u.TurnID, "run-1", "anthropic", "claude-opus-4-8", "auto_grounded_default", "scout"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendAssistantText(c.ID, u.TurnID, "run-1", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteRun("run-1", RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}

	events, err := s.GetConversationDisplayEvents(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawAssistant bool
	for _, e := range events {
		switch e.Kind {
		case EventKindUserMessage:
			if e.PersonaID != "" || e.Model != "" {
				t.Errorf("user_message carries attribution: persona=%q model=%q", e.PersonaID, e.Model)
			}
		case EventKindAssistantText:
			sawAssistant = true
			if e.PersonaID != "scout" {
				t.Errorf("assistant_text PersonaID = %q, want scout", e.PersonaID)
			}
			if e.Model != "claude-opus-4-8" {
				t.Errorf("assistant_text Model = %q", e.Model)
			}
		}
	}
	if !sawAssistant {
		t.Fatal("no assistant_text event returned")
	}
}

func TestDisplayEventsTolerateRunsWithoutAPersona(t *testing.T) {
	s := openTestStore(t)
	c, _ := s.CreateConversation("t")
	u, _ := s.AppendUserMessage(c.ID, "hi")
	if err := s.CreateRun(c.ID, u.TurnID, "run-1", "openai", "gpt-5", "auto_grounded_default", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendAssistantText(c.ID, u.TurnID, "run-1", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteRun("run-1", RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}
	events, err := s.GetConversationDisplayEvents(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Kind != EventKindAssistantText {
			continue
		}
		if e.PersonaID != "" {
			t.Errorf("PersonaID = %q, want empty", e.PersonaID)
		}
		if e.Model != "gpt-5" {
			t.Errorf("Model = %q, want gpt-5 (the model is known even when the persona is not)", e.Model)
		}
	}
}

// TestReplayJoinPreservesUserMessageAndAttributesAssistant proves the
// load-bearing property the LEFT JOIN exists for: a conversation with a user
// message and an attributed assistant run must replay with BOTH the user
// message present AND the assistant events carrying the persona/model that
// produced them. An INNER JOIN would silently drop the user_message (it has
// no run_id); this test fails loudly if that regression is reintroduced. It
// also exercises GetProviderReplayEvents, so the same guarantee is checked on
// the path that feeds the LLM provider, not just the display path.
func TestReplayJoinPreservesUserMessageAndAttributesAssistant(t *testing.T) {
	s := openTestStore(t)
	c, _ := s.CreateConversation("t")
	u, err := s.AppendUserMessage(c.ID, "what is revenue recognition?")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRun(c.ID, u.TurnID, "run-1", "anthropic", "claude-opus-4-8", "auto_grounded_default", "scout"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendAssistantText(c.ID, u.TurnID, "run-1", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteRun("run-1", RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		get  func() ([]ConversationEvent, error)
	}{
		{"display", func() ([]ConversationEvent, error) { return s.GetConversationDisplayEvents(c.ID) }},
		{"provider replay", func() ([]ConversationEvent, error) { return s.GetProviderReplayEvents(c.ID, "") }},
	} {
		events, err := tc.get()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var sawUser, sawAssistant bool
		for _, e := range events {
			switch e.Kind {
			case EventKindUserMessage:
				sawUser = true
				if e.Text != "what is revenue recognition?" {
					t.Errorf("%s: user_message text = %q", tc.name, e.Text)
				}
			case EventKindAssistantText:
				sawAssistant = true
				if e.PersonaID != "scout" {
					t.Errorf("%s: assistant_text PersonaID = %q, want scout", tc.name, e.PersonaID)
				}
				if e.Model != "claude-opus-4-8" {
					t.Errorf("%s: assistant_text Model = %q, want claude-opus-4-8", tc.name, e.Model)
				}
			}
		}
		if !sawUser {
			t.Errorf("%s: user_message was dropped from replay (LEFT JOIN regressed to an INNER JOIN?)", tc.name)
		}
		if !sawAssistant {
			t.Errorf("%s: no assistant_text event returned", tc.name)
		}
	}
}
