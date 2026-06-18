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

func TestKillCriteriaAndDueReviews(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateIdea(Idea{ID: "id1", Title: "Home automation", Status: "validating", Source: "import"})

	overdue := KillCriterion{
		ID: "k1", IdeaID: "id1", Metric: "Paid installs", Threshold: ">=2 in 30d",
		ReviewDate: 1000, OnMiss: "kill", Status: "pending",
	}
	future := KillCriterion{
		ID: "k2", IdeaID: "id1", Metric: "Churn", Threshold: "<10%/mo",
		ReviewDate: 9_000_000_000_000, OnMiss: "park", Status: "pending",
	}
	resolved := KillCriterion{
		ID: "k3", IdeaID: "id1", Metric: "Capital", Threshold: "<=1500",
		ReviewDate: 500, OnMiss: "halt", Status: "resolved",
	}
	for _, k := range []KillCriterion{overdue, future, resolved} {
		if err := st.AddKillCriterion(k); err != nil {
			t.Fatal(err)
		}
	}

	all, err := st.ListKillCriteria("id1")
	if err != nil || len(all) != 3 {
		t.Fatalf("list want 3, got %d err=%v", len(all), err)
	}

	due, err := st.ListDueKillCriteria(2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].CriterionID != "k1" {
		t.Fatalf("due want [k1], got %+v", due)
	}
	if due[0].IdeaTitle != "Home automation" || due[0].OnMiss != "kill" {
		t.Fatalf("due row not joined to idea: %+v", due[0])
	}
}

func TestListDueKillCriteria_SkipsKilledIdeas(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateIdea(Idea{ID: "live", Title: "Live", Status: "validating", Source: "manual"})
	_ = st.CreateIdea(Idea{ID: "dead", Title: "Dead", Status: "killed", Source: "manual"})
	_ = st.CreateIdea(Idea{ID: "rest", Title: "Rested", Status: "parked", Source: "manual"})

	// One overdue, pending criterion on each idea.
	for _, id := range []string{"live", "dead", "rest"} {
		if err := st.AddKillCriterion(KillCriterion{
			ID: "k-" + id, IdeaID: id, Metric: "m", Threshold: "t",
			ReviewDate: 1000, OnMiss: "kill", Status: "pending",
		}); err != nil {
			t.Fatal(err)
		}
	}

	due, err := st.ListDueKillCriteria(2000)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, d := range due {
		got[d.IdeaID] = true
	}
	// Killed ideas are permanent, so their reviews must not resurface. Parked
	// ideas are revisited later, so their reviews should still surface.
	if got["dead"] {
		t.Fatalf("killed idea's criterion must not surface: %+v", due)
	}
	if !got["live"] || !got["rest"] {
		t.Fatalf("live and parked ideas must surface: %+v", due)
	}
}
