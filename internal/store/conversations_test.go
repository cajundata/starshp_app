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
