// Package assignment loads companion-exported question sets and solves them in
// a bounded-concurrent fan-out over the agentic chat loop.
package assignment

import "encoding/json"

// Type is the companion question kind.
type Type string

const (
	TypeMultipleChoice Type = "multipleChoice"
	TypeWorksheet      Type = "worksheet"
	TypeUnsupported    Type = "unsupported" // any companion type we do not solve
)

// Manifest is the companion's _json/manifest.json.
type Manifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	GeneratedFrom string          `json:"generatedFrom"`
	Count         int             `json:"count"`
	Questions     []ManifestEntry `json:"questions"`
}

type ManifestEntry struct {
	Path  string `json:"path"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

// Question is one fully-loaded companion question (NNN.json).
type Question struct {
	Path           string
	Type           Type
	Title          string
	Warnings       []string
	MultipleChoice *MultipleChoiceBody // set when Type == TypeMultipleChoice
	Worksheet      *WorksheetBody      // set when Type == TypeWorksheet
}

type MultipleChoiceBody struct {
	Stem    string   `json:"stem"`
	Choices []Choice `json:"choices"`
}

type Choice struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

type WorksheetBody struct {
	Scenario string   `json:"scenario"`
	Required []string `json:"required"`
	Tabs     []Tab    `json:"tabs"`
}

type Tab struct {
	Label  string  `json:"label"`
	Tables []Table `json:"tables"`
}

type Table struct {
	Headers []string `json:"headers"`
	Rows    []Row    `json:"rows"`
}

type Row struct {
	Label string `json:"label"`
	Cells []Cell `json:"cells"`
}

// Cell mirrors a companion worksheet cell. Value/Formula are pointers so a JSON
// null (blank, answerable) is distinguishable from an empty string.
// Options is RawMessage because the companion emits either [] or an array of
// {index,text,correct} objects depending on the cell type.
type Cell struct {
	ID        string          `json:"id"`
	CellType  string          `json:"cellType"` // input | dropdown | readonly | formula
	AriaLabel string          `json:"ariaLabel"`
	Formula   *string         `json:"formula"`
	Value     *string         `json:"value"`
	Options   json.RawMessage `json:"options"`
}
