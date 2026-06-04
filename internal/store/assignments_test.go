package store

import "testing"

func TestAssignmentLifecycle(t *testing.T) {
	st := openTestStore(t)
	a := Assignment{
		ID: "a1", SourceDir: "/d", Title: "mod04", ManifestHash: "h",
		Model: "m", Status: "in_progress", TotalItems: 2,
	}
	if err := st.CreateAssignment(a); err != nil {
		t.Fatal(err)
	}
	it := AssignmentItem{
		ID: "i1", AssignmentID: "a1", Seq: 0, SourcePath: "001.html",
		Type: "multipleChoice", Title: "Item 1", Status: "pending",
	}
	if err := st.CreateAssignmentItem(it); err != nil {
		t.Fatal(err)
	}
	it.Status = "answered"
	it.Confidence = "high"
	it.AnswerJSON = `{"answerIndex":1}`
	it.RunID = "r1"
	if err := st.UpdateAssignmentItem(it); err != nil {
		t.Fatal(err)
	}
	items, err := st.ListAssignmentItems("a1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != "answered" || items[0].Confidence != "high" {
		t.Fatalf("item not updated: %+v", items)
	}
	if items[0].RunID != "r1" || items[0].AnswerJSON != `{"answerIndex":1}` {
		t.Fatalf("item fields not persisted: %+v", items[0])
	}
	got, err := st.GetAssignment("a1")
	if err != nil || got.Title != "mod04" {
		t.Fatalf("get assignment: %+v err=%v", got, err)
	}
	list, _ := st.ListAssignments()
	if len(list) != 1 {
		t.Fatalf("want 1 assignment, got %d", len(list))
	}
	if err := st.UpdateAssignmentStatus("a1", "completed"); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetAssignment("a1")
	if got.Status != "completed" {
		t.Fatalf("status want completed, got %q", got.Status)
	}
}

func TestSetConversationAssignment_HidesFromList(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateAssignment(Assignment{ID: "a1", SourceDir: "/d", Title: "t",
		ManifestHash: "h", Model: "m", Status: "in_progress"})
	normal, _ := st.CreateConversation("normal")
	item, _ := st.CreateConversation("item")
	if err := st.SetConversationAssignment(item.ID, "a1"); err != nil {
		t.Fatal(err)
	}
	convs, _ := st.ListConversations()
	for _, c := range convs {
		if c.ID == item.ID {
			t.Fatal("assignment-tagged conversation must be hidden from ListConversations")
		}
	}
	var sawNormal bool
	for _, c := range convs {
		if c.ID == normal.ID {
			sawNormal = true
		}
	}
	if !sawNormal {
		t.Fatal("normal conversation should still be listed")
	}
}

func TestFindAssignmentByManifest(t *testing.T) {
	st := openTestStore(t)
	if _, ok, err := st.FindAssignmentByManifest("/d", "h"); err != nil || ok {
		t.Fatalf("expected not found; ok=%v err=%v", ok, err)
	}
	_ = st.CreateAssignment(Assignment{ID: "a1", SourceDir: "/d", Title: "t",
		ManifestHash: "h", Model: "m", Status: "completed"})
	got, ok, err := st.FindAssignmentByManifest("/d", "h")
	if err != nil || !ok || got.ID != "a1" {
		t.Fatalf("expected a1; got %+v ok=%v err=%v", got, ok, err)
	}
}

func TestAssignmentScope_RoundTrip(t *testing.T) {
	st := openTestStore(t)
	if err := st.CreateAssignment(Assignment{
		ID: "a1", SourceDir: "/d", Title: "t", ManifestHash: "h",
		Model: "m", Status: "in_progress", TotalItems: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// Default: no scope.
	got, err := st.GetAssignmentScope("a1")
	if err != nil || got != nil {
		t.Fatalf("want nil scope, got %v err %v", got, err)
	}

	// Set two whole-book scopes.
	if err := st.SetAssignmentScope("a1", []TextbookScope{{Name: "blaw"}, {Name: "audit"}}); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetAssignmentScope("a1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "blaw" || got[1].Name != "audit" {
		t.Fatalf("unexpected scope: %+v", got)
	}

	// Empty clears it back to NULL → nil.
	if err := st.SetAssignmentScope("a1", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetAssignmentScope("a1"); got != nil {
		t.Fatalf("want nil after clear, got %+v", got)
	}
}

func TestGetAssignmentItem(t *testing.T) {
	st := openTestStore(t)
	if err := st.CreateAssignment(Assignment{
		ID: "a1", SourceDir: "/d", Title: "t", ManifestHash: "h",
		Model: "m", Status: "completed", TotalItems: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAssignmentItem(AssignmentItem{
		ID: "i1", AssignmentID: "a1", Seq: 3, SourcePath: "003.html",
		Type: "multipleChoice", Title: "Item 3", Status: "answered", Confidence: "low",
	}); err != nil {
		t.Fatal(err)
	}

	got, ok, err := st.GetAssignmentItem("a1", 3)
	if err != nil || !ok {
		t.Fatalf("expected found, got ok=%v err=%v", ok, err)
	}
	if got.ID != "i1" || got.SourcePath != "003.html" || got.Confidence != "low" {
		t.Fatalf("unexpected item: %+v", got)
	}

	if _, ok, err := st.GetAssignmentItem("a1", 99); ok || err != nil {
		t.Fatalf("expected ok=false err=nil for missing seq, got ok=%v err=%v", ok, err)
	}
}
