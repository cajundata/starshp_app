package assignment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAnswerFile_MirrorsSchema(t *testing.T) {
	dir := t.TempDir()
	ans := Answer{Confidence: "medium", Cells: []CellValue{{ID: "x", Value: "1"}}}
	rawAns, _ := json.Marshal(ans)
	path, err := writeAnswerFile(dir, "004.html", "worksheet", "Ex 7-4", "r1", rawAns)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "004.json" {
		t.Fatalf("want 004.json, got %s", filepath.Base(path))
	}
	if filepath.Base(filepath.Dir(path)) != "_answers" {
		t.Fatalf("want parent dir _answers, got %s", filepath.Dir(path))
	}
	b, _ := os.ReadFile(path)
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["source"] != "004.html" || got["type"] != "worksheet" || got["runId"] != "r1" {
		t.Fatalf("envelope mismatch: %v", got)
	}
	if _, ok := got["answer"]; !ok {
		t.Fatal("answer field missing")
	}
	if got["title"] != "Ex 7-4" {
		t.Fatalf("title mismatch: %v", got["title"])
	}
}
