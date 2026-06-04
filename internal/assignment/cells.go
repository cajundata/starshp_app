package assignment

import "fmt"

// CellRef identifies one answerable worksheet cell plus the context a model
// needs to fill it. Key is unique across the whole worksheet (cell IDs only
// repeat per-tab, so Key is tab-qualified). Options carries option TEXT only —
// never the captured Correct flag (answer-key leak prevention).
type CellRef struct {
	Key        string // "<tabIndex>::<cellID>" — unique across the worksheet
	TabIndex   int
	TabLabel   string
	CellID     string // raw companion cell id (unique only within its tab)
	RowLabel   string
	AriaLabel  string
	IsDropdown bool
	Options    []string
}

// AnswerableCells returns the cells a solver must fill for a worksheet, in
// stable (tab, table, row, cell) order. A cell is answerable iff its cellType
// is input or dropdown AND its value is null (blank). readonly, formula, and
// prefilled (value != null) cells are excluded — the latter two are computed
// or given. Returns nil for non-worksheet questions.
func AnswerableCells(q Question) []CellRef {
	if q.Type != TypeWorksheet || q.Worksheet == nil {
		return nil
	}
	var out []CellRef
	for ti, tab := range q.Worksheet.Tabs {
		for _, tbl := range tab.Tables {
			for _, row := range tbl.Rows {
				for _, c := range row.Cells {
					if (c.CellType == "input" || c.CellType == "dropdown") && c.Value == nil {
						var opts []string
						for _, o := range c.Options {
							opts = append(opts, o.Text)
						}
						out = append(out, CellRef{
							Key:        fmt.Sprintf("%d::%s", ti, c.ID),
							TabIndex:   ti,
							TabLabel:   tab.Label,
							CellID:     c.ID,
							RowLabel:   row.Label,
							AriaLabel:  c.AriaLabel,
							IsDropdown: c.CellType == "dropdown",
							Options:    opts,
						})
					}
				}
			}
		}
	}
	return out
}
