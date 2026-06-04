package assignment

import "testing"

func loadWorksheet(t *testing.T) Question {
	t.Helper()
	loaded, err := Load(testdataDir(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range loaded.Questions {
		if q.Path == "004.html" {
			return q
		}
	}
	t.Fatal("004.html not loaded")
	return Question{}
}

func TestAnswerableCells_ExcludesFormulaReadonlyPrefilled(t *testing.T) {
	q := loadWorksheet(t)
	refs := AnswerableCells(q)
	keys := map[string]bool{}
	for _, r := range refs {
		keys[r.Key] = true
	}
	// Req A1 (tab index 0) c2_r0 is a blank input -> answerable.
	if !keys["0::0_table0_cell_c2_r0"] {
		t.Error("Req A1 blank input c2_r0 should be answerable")
	}
	// Req B (tab index 2) c2_r13 is a formula -> NOT answerable.
	if keys["2::0_table0_cell_c2_r13"] {
		t.Error("Req B formula c2_r13 must not be answerable")
	}
	// Req A1 (tab index 0) c0_r0 is prefilled ("1") -> NOT answerable.
	if keys["0::0_table0_cell_c0_r0"] {
		t.Error("prefilled c0_r0 must not be answerable")
	}
	if len(refs) == 0 {
		t.Fatal("expected some answerable cells")
	}
}

func TestAnswerableCells_KeysAreUnique(t *testing.T) {
	q := loadWorksheet(t)
	refs := AnswerableCells(q)
	seen := map[string]bool{}
	for _, r := range refs {
		if seen[r.Key] {
			t.Fatalf("duplicate answerable key %q", r.Key)
		}
		seen[r.Key] = true
	}
}

func TestAnswerableCells_MultipleChoiceIsNil(t *testing.T) {
	loaded, _ := Load(testdataDir(t))
	for _, q := range loaded.Questions {
		if q.Type == TypeMultipleChoice {
			if AnswerableCells(q) != nil {
				t.Fatal("MC question has no answerable cells")
			}
			return
		}
	}
	t.Fatal("no MC question loaded")
}
