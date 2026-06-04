package assignment

import "encoding/json"

// FlagCodes is the closed vocabulary for submit_answer flags.
var FlagCodes = []string{
	"missing_information",
	"uncaptured_dropdown_options",
	"ambiguous_requirement",
	"out_of_scope",
	"low_confidence",
}

// Flag is one structured concern about a question/answer.
type Flag struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
	CellID string `json:"cellId,omitempty"`
}

// Answer is the parsed submit_answer payload (the tool input). For MC,
// AnswerIndex is set; for worksheets, Cells is set.
type Answer struct {
	Confidence  string      `json:"confidence"`
	AnswerIndex *int        `json:"answerIndex,omitempty"`
	AnswerText  string      `json:"answerText,omitempty"`
	Cells       []CellValue `json:"cells,omitempty"`
	Flags       []Flag      `json:"flags,omitempty"`
	Notes       string      `json:"notes,omitempty"`
}

// CellValue is one worksheet cell answer. ID is the tab-qualified cell key
// (CellRef.Key, e.g. "0::0_table0_cell_c2_r0"), not a bare companion cell id.
type CellValue struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// BuildSubmitAnswerSchema builds a JSON Schema tailored to one question:
// MC bounds answerIndex to the choice count; worksheet enumerates answerable
// cell keys. Flags use the closed FlagCodes vocabulary.
func BuildSubmitAnswerSchema(q Question) json.RawMessage {
	flagSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"code":   map[string]any{"enum": toAny(FlagCodes)},
			"detail": map[string]any{"type": "string"},
			"cellId": map[string]any{"type": "string"},
		},
		"required":             []string{"code", "detail"},
		"additionalProperties": false,
	}
	props := map[string]any{
		"confidence": map[string]any{"enum": []any{"high", "medium", "low"}},
		"flags":      map[string]any{"type": "array", "items": flagSchema},
		"notes":      map[string]any{"type": "string"},
	}
	required := []string{"confidence"}

	switch q.Type {
	case TypeMultipleChoice:
		max := 0
		if q.MultipleChoice != nil && len(q.MultipleChoice.Choices) > 0 {
			max = len(q.MultipleChoice.Choices) - 1
		}
		props["answerIndex"] = map[string]any{"type": "integer", "minimum": 0, "maximum": max}
		props["answerText"] = map[string]any{"type": "string"}
		required = append(required, "answerIndex")
	case TypeWorksheet:
		var keys []string
		for _, ref := range AnswerableCells(q) {
			keys = append(keys, ref.Key)
		}
		props["cells"] = map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":    map[string]any{"enum": toAny(keys)},
					"value": map[string]any{"type": "string"},
				},
				"required":             []string{"id", "value"},
				"additionalProperties": false,
			},
		}
		required = append(required, "cells")
	}

	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
