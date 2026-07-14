package store

import "testing"

func TestConversationLifecycle(t *testing.T) {
	s := newTestStore(t)
	c, err := s.CreateConversation("Revenue recognition post")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	if err := s.SetConversationTextbooks(c.ID, []TextbookScope{{Name: "intermediate-accounting", Chapters: []int{18}}}); err != nil {
		t.Fatalf("SetConversationTextbooks: %v", err)
	}
	scope, _ := s.GetConversationTextbooks(c.ID)
	if len(scope) != 1 || scope[0].Name != "intermediate-accounting" || scope[0].Chapters[0] != 18 {
		t.Fatalf("scope mismatch: %+v", scope)
	}

	if err := s.SetConversationPinned(c.ID, "claude-opus-4-7", "scout"); err != nil {
		t.Fatalf("SetConversationPinned: %v", err)
	}
	list, _ := s.ListConversations()
	if len(list) != 1 || list[0].PinnedModel != "claude-opus-4-7" || list[0].PinnedPersona != "scout" {
		t.Fatalf("pinned model/persona not persisted: %+v", list)
	}

	if err := s.DeleteConversation(c.ID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	list, _ = s.ListConversations()
	if len(list) != 0 {
		t.Fatalf("conversation not deleted: %d remain", len(list))
	}
}

func TestSetConversationTitle(t *testing.T) {
	s := newTestStore(t)
	c, _ := s.CreateConversation("New conversation")
	if err := s.SetConversationTitle(c.ID, "Revenue recognition draft"); err != nil {
		t.Fatalf("SetConversationTitle: %v", err)
	}
	list, _ := s.ListConversations()
	if len(list) != 1 || list[0].Title != "Revenue recognition draft" {
		t.Fatalf("title not updated: %+v", list)
	}
}

func TestRetrievalMode_RoundTrip(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	got, err := st.GetRetrievalMode(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "auto_grounded_default" {
		t.Fatalf("default mode = %q, want auto_grounded_default", got)
	}
	if err := st.SetRetrievalMode(conv.ID, "agentic_only"); err != nil {
		t.Fatal(err)
	}
	if got, _ = st.GetRetrievalMode(conv.ID); got != "agentic_only" {
		t.Fatalf("after set, mode = %q, want agentic_only", got)
	}
}
