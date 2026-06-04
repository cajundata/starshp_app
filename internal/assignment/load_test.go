package assignment

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func testdataDir(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "mod04", "_json")
}

func TestLoad_ParsesManifestAndQuestions(t *testing.T) {
	loaded, err := Load(testdataDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Manifest.Count != 24 {
		t.Fatalf("manifest count want 24, got %d", loaded.Manifest.Count)
	}
	byPath := map[string]Question{}
	for _, q := range loaded.Questions {
		byPath[q.Path] = q
	}
	mc, ok := byPath["001.html"]
	if !ok {
		t.Fatal("001.html not loaded")
	}
	if mc.Type != TypeMultipleChoice {
		t.Fatalf("001 type want multipleChoice, got %q", mc.Type)
	}
	if mc.MultipleChoice == nil || len(mc.MultipleChoice.Choices) != 4 {
		t.Fatalf("001 should have 4 choices, got %+v", mc.MultipleChoice)
	}
	if mc.MultipleChoice.Stem == "" {
		t.Fatal("001 stem should be non-empty")
	}

	ws, ok := byPath["004.html"]
	if !ok {
		t.Fatal("004.html not loaded")
	}
	if ws.Type != TypeWorksheet {
		t.Fatalf("004 type want worksheet, got %q", ws.Type)
	}
	if ws.Worksheet == nil || ws.Worksheet.Scenario == "" {
		t.Fatal("004 worksheet should have a scenario")
	}
	if len(ws.Worksheet.Tabs) == 0 {
		t.Fatal("004 should have tabs")
	}
	var found bool
	for _, tab := range ws.Worksheet.Tabs {
		for _, tbl := range tab.Tables {
			for _, row := range tbl.Rows {
				for _, c := range row.Cells {
					if c.ID == "0_table0_cell_c2_r0" {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("expected cell id 0_table0_cell_c2_r0 in 004 worksheet")
	}
}

func TestCellOptions_ParseBothForms(t *testing.T) {
	var empty Cell
	if err := json.Unmarshal([]byte(`{"id":"a","cellType":"dropdown","options":[]}`), &empty); err != nil {
		t.Fatal(err)
	}
	if len(empty.Options) != 0 {
		t.Fatalf("empty options should be empty, got %v", empty.Options)
	}
	var filled Cell
	raw := `{"id":"b","cellType":"dropdown","options":[{"index":0,"text":"Cash","correct":true},{"index":1,"text":"Revenue","correct":false}]}`
	if err := json.Unmarshal([]byte(raw), &filled); err != nil {
		t.Fatal(err)
	}
	if len(filled.Options) != 2 || filled.Options[0].Text != "Cash" || !filled.Options[0].Correct {
		t.Fatalf("filled options mismatch: %+v", filled.Options)
	}
}
