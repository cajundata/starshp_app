package store

import "testing"

func TestPresetsCRUD(t *testing.T) {
	s := newTestStore(t)
	p, err := s.CreatePreset("Acct discussion post", "You are an accounting tutor.")
	if err != nil {
		t.Fatalf("CreatePreset: %v", err)
	}
	if p.ID == "" {
		t.Fatal("expected generated ID")
	}
	got, err := s.ListPresets()
	if err != nil || len(got) != 1 || got[0].Name != "Acct discussion post" {
		t.Fatalf("ListPresets = %+v, err=%v", got, err)
	}
	if err := s.UpdatePreset(p.ID, "Renamed", "New prompt"); err != nil {
		t.Fatalf("UpdatePreset: %v", err)
	}
	if err := s.DeletePreset(p.ID); err != nil {
		t.Fatalf("DeletePreset: %v", err)
	}
	got, _ = s.ListPresets()
	if len(got) != 0 {
		t.Fatalf("expected 0 presets, got %d", len(got))
	}
}
