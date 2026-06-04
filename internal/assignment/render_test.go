package assignment

import (
	"strings"
	"testing"
)

func TestRenderPrompt_MultipleChoice(t *testing.T) {
	loaded, err := Load(testdataDir(t))
	if err != nil {
		t.Fatal(err)
	}
	var mc Question
	for _, q := range loaded.Questions {
		if q.Type == TypeMultipleChoice {
			mc = q
		}
	}
	system, user := RenderPrompt(mc)
	if !strings.Contains(system, "submit_answer") {
		t.Error("system prompt must instruct calling submit_answer")
	}
	if !strings.Contains(user, mc.MultipleChoice.Stem) {
		t.Error("user prompt must contain the stem")
	}
	for i, ch := range mc.MultipleChoice.Choices {
		if !strings.Contains(user, ch.Text) {
			t.Errorf("user prompt missing choice %d text", i)
		}
	}
}

func TestRenderPrompt_WorksheetTagsAnswerableCells(t *testing.T) {
	q := loadWorksheet(t)
	system, user := RenderPrompt(q)
	if !strings.Contains(system, "safe_math") {
		t.Error("worksheet system prompt must require safe_math verification")
	}
	if !strings.Contains(user, q.Worksheet.Scenario) {
		t.Error("user prompt must contain the scenario")
	}
	// Every answerable cell key must appear tagged.
	for _, ref := range AnswerableCells(q) {
		if !strings.Contains(user, "⟦"+ref.Key+"⟧") {
			t.Errorf("answerable cell %s not tagged in prompt", ref.Key)
		}
	}
	// Req B (tab index 2) c2_r13 is a formula -> its key must NOT be tagged answerable.
	if strings.Contains(user, "⟦2::0_table0_cell_c2_r13⟧") {
		t.Error("formula cell (Req B c2_r13) must not be tagged as answerable")
	}
}
