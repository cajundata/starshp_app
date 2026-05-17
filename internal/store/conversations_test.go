package store

import "testing"

func TestConversationLifecycle(t *testing.T) {
	s := newTestStore(t)
	c, err := s.CreateConversation("Revenue recognition post")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if _, err := s.AddMessage(c.ID, "user", "Draft a post", "", "", ""); err != nil {
		t.Fatalf("AddMessage user: %v", err)
	}
	if _, err := s.AddMessage(c.ID, "assistant", "Here is a draft", "claude-opus-4-7", "ctx", `["ch18"]`); err != nil {
		t.Fatalf("AddMessage assistant: %v", err)
	}
	msgs, err := s.ListMessages(c.ID)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("ListMessages = %d msgs, err=%v", len(msgs), err)
	}
	if msgs[1].Model != "claude-opus-4-7" || msgs[1].RAGContext != "ctx" {
		t.Fatalf("assistant msg not persisted correctly: %+v", msgs[1])
	}

	if err := s.SetConversationTextbooks(c.ID, []TextbookScope{{Name: "intermediate-accounting", Chapters: []int{18}}}); err != nil {
		t.Fatalf("SetConversationTextbooks: %v", err)
	}
	scope, _ := s.GetConversationTextbooks(c.ID)
	if len(scope) != 1 || scope[0].Name != "intermediate-accounting" || scope[0].Chapters[0] != 18 {
		t.Fatalf("scope mismatch: %+v", scope)
	}

	if err := s.SetConversationMeta(c.ID, "preset-1", "claude-opus-4-7"); err != nil {
		t.Fatalf("SetConversationMeta: %v", err)
	}

	if err := s.DeleteConversation(c.ID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	msgs, _ = s.ListMessages(c.ID)
	if len(msgs) != 0 {
		t.Fatalf("cascade delete failed: %d messages remain", len(msgs))
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

func TestListMessagesStableOrderWithinSameSecond(t *testing.T) {
	s := newTestStore(t)
	c, _ := s.CreateConversation("order")
	// Insert several messages rapidly; created_at (unix seconds) will collide.
	want := []string{"u1", "a1", "u2", "a2", "u3"}
	for i, txt := range want {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if _, err := s.AddMessage(c.ID, role, txt, "", "", ""); err != nil {
			t.Fatalf("AddMessage %d: %v", i, err)
		}
	}
	msgs, err := s.ListMessages(c.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != len(want) {
		t.Fatalf("got %d msgs, want %d", len(msgs), len(want))
	}
	for i := range want {
		if msgs[i].Content != want[i] {
			t.Fatalf("order mismatch at %d: got %q, want %q (full: %+v)", i, msgs[i].Content, want[i], msgs)
		}
	}
}
