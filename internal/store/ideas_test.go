package store

import "testing"

func TestPipelineSchemaTablesExist(t *testing.T) {
	st := openTestStore(t)
	tables := []string{
		"ideas", "idea_status_history", "idea_reviews",
		"idea_review_roles", "kill_criteria", "send_backs",
	}
	for _, name := range tables {
		var got string
		err := st.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
		if err != nil {
			t.Fatalf("table %q missing: %v", name, err)
		}
	}
}

func TestIdeaCRUD(t *testing.T) {
	st := openTestStore(t)
	i := Idea{
		ID: "id1", Title: "HDPE cooler mounts", Summary: "marine mounts",
		Pathway: "small_project", Status: "raw", FinancialFlag: true,
		Source: "import",
	}
	if err := st.CreateIdea(i); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetIdea("id1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "HDPE cooler mounts" || got.Pathway != "small_project" {
		t.Fatalf("get mismatch: %+v", got)
	}
	if !got.FinancialFlag {
		t.Fatalf("financial flag not persisted: %+v", got)
	}
	got.Summary = "updated"
	got.FinancialFlag = false
	if err := st.UpdateIdea(got); err != nil {
		t.Fatal(err)
	}
	reread, _ := st.GetIdea("id1")
	if reread.Summary != "updated" || reread.FinancialFlag {
		t.Fatalf("update not persisted: %+v", reread)
	}
	list, err := st.ListIdeas()
	if err != nil || len(list) != 1 {
		t.Fatalf("list want 1, got %d err=%v", len(list), err)
	}
	if err := st.DeleteIdea("id1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetIdea("id1"); err == nil {
		t.Fatal("expected error getting deleted idea")
	}
}

func TestSetIdeaStatusWritesHistoryAtomically(t *testing.T) {
	st := openTestStore(t)
	if err := st.CreateIdea(Idea{ID: "id1", Title: "t", Status: "raw", Source: "manual"}); err != nil {
		t.Fatal(err)
	}

	if err := st.SetIdeaStatus("id1", "triaged", "looks worth a look"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetIdea("id1")
	if got.Status != "triaged" {
		t.Fatalf("status want triaged, got %q", got.Status)
	}

	if err := st.SetIdeaStatus("id1", "killed", "no channel"); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetIdea("id1")
	if got.Status != "killed" || got.KillReason != "no channel" {
		t.Fatalf("kill not recorded: %+v", got)
	}

	hist, err := st.ListStatusHistory("id1")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("want 2 history rows, got %d", len(hist))
	}
	if hist[0].FromStatus != "raw" || hist[0].ToStatus != "triaged" {
		t.Fatalf("row0 wrong: %+v", hist[0])
	}
	if hist[1].ToStatus != "killed" || hist[1].Reason != "no channel" {
		t.Fatalf("row1 wrong: %+v", hist[1])
	}
}
