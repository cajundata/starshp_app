package assignment

import (
	"fmt"
	"strings"
)

const mcSystem = `You are an expert accounting tutor solving a multiple-choice question.
Reason carefully, then call the submit_answer tool exactly once with your chosen
answerIndex, a one-line rationale in notes, your confidence, and any flags. If the
question appears to be missing information needed to answer, still pick your best
answer but add a flag with code "missing_information". After calling submit_answer, stop.`

const worksheetSystem = `You are an expert accounting tutor completing a worksheet exercise.
Work through the scenario and required items. Verify every numeric value with the
safe_math tool before reporting it. Fill the cells you are confident about by calling
submit_answer exactly once with a list of {id, value} entries — use only the cell keys
shown in ⟦ ⟧ tags (pass the full key including the tab prefix). Omit any cell you cannot
determine and explain why with a flag. Use flag code "missing_information" when the prompt
lacks needed data and "uncaptured_dropdown_options" when a dropdown's options were not
provided. After calling submit_answer, stop.`

// RenderPrompt produces the (system, user) prompt pair for a question.
func RenderPrompt(q Question) (system, user string) {
	switch q.Type {
	case TypeMultipleChoice:
		return mcSystem, renderMC(q)
	case TypeWorksheet:
		return worksheetSystem, renderWorksheet(q)
	default:
		return "You are an accounting tutor.",
			fmt.Sprintf("Title: %s\n(Unsupported question type; answer from background knowledge.)", q.Title)
	}
}

func renderMC(q Question) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Title: %s\n\n%s\n\nChoices:\n", q.Title, q.MultipleChoice.Stem)
	for _, ch := range q.MultipleChoice.Choices {
		fmt.Fprintf(&b, "  [%d] %s\n", ch.Index, ch.Text)
	}
	return b.String()
}

func renderWorksheet(q Question) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Title: %s\n\nScenario:\n%s\n\n", q.Title, q.Worksheet.Scenario)
	if len(q.Worksheet.Required) > 0 {
		b.WriteString("Required / given information:\n")
		for i, r := range q.Worksheet.Required {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, r)
		}
		b.WriteString("\n")
	}
	for ti, tab := range q.Worksheet.Tabs {
		fmt.Fprintf(&b, "== %s (tab %d) ==\n", tab.Label, ti)
		for _, tbl := range tab.Tables {
			for _, row := range tbl.Rows {
				renderRow(&b, ti, row)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("Fill the cells tagged ⟦tab::id⟧ by passing that exact key to submit_answer. Do not fill auto-computed cells.\n")
	return b.String()
}

func renderRow(b *strings.Builder, ti int, row Row) {
	if row.Label != "" {
		fmt.Fprintf(b, "%s:", row.Label)
	}
	for _, c := range row.Cells {
		switch {
		case c.CellType == "formula" && c.Formula != nil:
			fmt.Fprintf(b, " (auto: %s)", *c.Formula)
		case c.CellType == "readonly" && c.Value != nil:
			fmt.Fprintf(b, " %s", *c.Value)
		case c.Value != nil:
			fmt.Fprintf(b, " %s", *c.Value)
		case c.CellType == "input" || c.CellType == "dropdown":
			key := fmt.Sprintf("%d::%s", ti, c.ID)
			ctx := c.AriaLabel
			if c.CellType == "dropdown" && len(c.Options) > 0 {
				var texts []string
				for _, o := range c.Options {
					texts = append(texts, o.Text) // text only — never o.Correct
				}
				if ctx != "" {
					ctx += "; "
				}
				ctx += "options: " + strings.Join(texts, ", ")
			}
			if ctx != "" {
				fmt.Fprintf(b, " ⟦%s⟧(%s)", key, ctx)
			} else {
				fmt.Fprintf(b, " ⟦%s⟧", key)
			}
		}
	}
	b.WriteString("\n")
}
