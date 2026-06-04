package assignment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// writeAnswerFile writes <dir>/_answers/NNN.json mirroring the companion's
// per-question convention. answerRaw is the verbatim submit_answer input.
// Returns the written path.
func writeAnswerFile(dir, sourcePath, qType, title, runID string, answerRaw json.RawMessage) (string, error) {
	outDir := filepath.Join(dir, "_answers")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	envelope := map[string]any{
		"schemaVersion": 1,
		"source":        sourcePath,
		"type":          qType,
		"title":         title,
		"answer":        answerRaw,
		"runId":         runID,
		"solvedAt":      time.Now().UnixMilli(),
	}
	b, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(outDir, jsonFileFor(sourcePath))
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
