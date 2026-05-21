package store

import "testing"

func TestActiveItemsReplaceAll(t *testing.T) {
	s := newTestStore(t)
	c, _ := s.CreateConversation("t")
	if err := s.SetActiveItems(c.ID, []string{"b.md", "a.md"}); err != nil {
		t.Fatalf("SetActiveItems: %v", err)
	}
	got, err := s.GetActiveItems(c.ID)
	if err != nil || len(got) != 2 || got[0] != "a.md" || got[1] != "b.md" {
		t.Fatalf("GetActiveItems = %v, err=%v", got, err)
	}
	// Replace-all: a new set drops the old one entirely.
	if err := s.SetActiveItems(c.ID, []string{"c.md"}); err != nil {
		t.Fatalf("SetActiveItems 2: %v", err)
	}
	got, _ = s.GetActiveItems(c.ID)
	if len(got) != 1 || got[0] != "c.md" {
		t.Fatalf("replace-all failed: %v", got)
	}
}

func TestActiveItemsCascadeDelete(t *testing.T) {
	s := newTestStore(t)
	c, _ := s.CreateConversation("t")
	if err := s.SetActiveItems(c.ID, []string{"a.md"}); err != nil {
		t.Fatalf("SetActiveItems: %v", err)
	}
	if err := s.DeleteConversation(c.ID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	got, _ := s.GetActiveItems(c.ID)
	if len(got) != 0 {
		t.Fatalf("cascade delete failed: %v", got)
	}
}
