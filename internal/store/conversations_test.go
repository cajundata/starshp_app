package store

import (
	"testing"

	"github.com/cajundata/starshp_app/internal/provider"
)

func TestConversationLifecycle(t *testing.T) {
	s := newTestStore(t)
	c, err := s.CreateConversation("Revenue recognition post")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if _, err := s.AddMessage(c.ID, "user", "Draft a post", "", "", "", nil); err != nil {
		t.Fatalf("AddMessage user: %v", err)
	}
	if _, err := s.AddMessage(c.ID, "assistant", "Here is a draft", "claude-opus-4-7", "ctx", `["ch18"]`, nil); err != nil {
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

	if err := s.SetConversationMeta(c.ID, "claude-opus-4-7"); err != nil {
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
		if _, err := s.AddMessage(c.ID, role, txt, "", "", "", nil); err != nil {
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

func TestAddMessageWithUsageRoundTrip(t *testing.T) {
	s := newTestStore(t)
	c, _ := s.CreateConversation("usage")

	u := &provider.Usage{InputTokens: 1200, OutputTokens: 450, CachedInputTokens: 800}
	if _, err := s.AddMessage(c.ID, "assistant", "answer", "claude-opus-4-7", "", "", u); err != nil {
		t.Fatalf("AddMessage with usage: %v", err)
	}
	msgs, err := s.ListMessages(c.ID)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ListMessages = %d msgs, err=%v", len(msgs), err)
	}
	got := msgs[0]
	if got.InputTokens == nil || *got.InputTokens != 1200 {
		t.Fatalf("InputTokens = %v, want 1200", got.InputTokens)
	}
	if got.OutputTokens == nil || *got.OutputTokens != 450 {
		t.Fatalf("OutputTokens = %v, want 450", got.OutputTokens)
	}
	if got.CachedInputTokens == nil || *got.CachedInputTokens != 800 {
		t.Fatalf("CachedInputTokens = %v, want 800", got.CachedInputTokens)
	}
}

func TestAddMessageNilUsageLeavesNullColumns(t *testing.T) {
	s := newTestStore(t)
	c, _ := s.CreateConversation("nil-usage")

	if _, err := s.AddMessage(c.ID, "user", "hi", "", "", "", nil); err != nil {
		t.Fatalf("AddMessage nil usage: %v", err)
	}
	msgs, _ := s.ListMessages(c.ID)
	if len(msgs) != 1 {
		t.Fatalf("got %d msgs", len(msgs))
	}
	if msgs[0].InputTokens != nil || msgs[0].OutputTokens != nil || msgs[0].CachedInputTokens != nil {
		t.Fatalf("nil usage should leave columns NULL, got %+v", msgs[0])
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
