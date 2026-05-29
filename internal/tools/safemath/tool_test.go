package safemath

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/tools"
)

func TestSafeMathTool_Metadata(t *testing.T) {
	tool := New()
	if tool.Name() != "safe_math" {
		t.Fatalf("name: %s", tool.Name())
	}
	if tool.Description() == "" || !strings.Contains(tool.Description(), "decimal") {
		t.Fatalf("description should mention decimal precision; got %q", tool.Description())
	}
	if !json.Valid(tool.InputSchema()) {
		t.Fatal("input schema must be valid JSON")
	}
}

func TestSafeMathTool_Execute_HappyPath(t *testing.T) {
	tool := New()
	res, err := tool.Execute(context.Background(), tools.ExecContext{},
		json.RawMessage(`{"expression":"50000 * 0.22 + 1000"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "12000" {
		t.Fatalf("output: %s", res.Output)
	}
	var meta struct {
		Norm   string `json:"normalized_expression"`
		Result string `json:"result_decimal_string"`
		Hash   string `json:"result_hash"`
	}
	if err := json.Unmarshal(res.Metadata, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Result != "12000" || meta.Hash == "" {
		t.Fatalf("metadata: %+v", meta)
	}
}

func TestSafeMathTool_Execute_ParseError(t *testing.T) {
	tool := New()
	res, err := tool.Execute(context.Background(), tools.ExecContext{},
		json.RawMessage(`{"expression":"1 +"}`))
	if err == nil {
		t.Fatal("expected parse error to be a Go error so registry maps it to execution_error")
	}
	if res.Output != "" {
		t.Fatalf("parse error must return empty output: %q", res.Output)
	}
}

func TestSafeMathTool_Timeout(t *testing.T) {
	tool := New()
	if tool.Timeout().Seconds() != 5 {
		t.Fatalf("timeout should be 5s; got %v", tool.Timeout())
	}
}
