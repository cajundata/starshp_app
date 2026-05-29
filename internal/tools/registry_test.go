// External test package (tools_test) so it can import the probe helper, which
// itself imports tools — an in-package test importing probe would form a cycle.
package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/tools"
	"github.com/cajundata/starshp_app/internal/tools/probe"
)

func TestRegistry_RegisterAndCatalog(t *testing.T) {
	reg := tools.NewRegistry(5 * time.Second)
	if err := reg.Register(probe.New("p1", `{"type":"object"}`)); err != nil {
		t.Fatal(err)
	}
	cat := reg.Catalog()
	if len(cat) != 1 || cat[0].Name != "p1" {
		t.Fatalf("catalog mismatch: %+v", cat)
	}
}

func TestRegistry_RegisterRejectsDuplicate(t *testing.T) {
	reg := tools.NewRegistry(5 * time.Second)
	_ = reg.Register(probe.New("p1", `{"type":"object"}`))
	if err := reg.Register(probe.New("p1", `{"type":"object"}`)); err == nil {
		t.Fatal("duplicate registration should fail")
	}
}

func TestRegistry_RegisterRejectsInvalidSchema(t *testing.T) {
	reg := tools.NewRegistry(5 * time.Second)
	if err := reg.Register(probe.New("p1", `not-json`)); err == nil {
		t.Fatal("invalid schema should fail")
	}
}

func TestRegistry_Execute_UnknownTool(t *testing.T) {
	reg := tools.NewRegistry(5 * time.Second)
	_, isErr, _, err := reg.Execute(context.Background(),
		tools.ExecContext{}, "missing", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unknown tool should be a tool-result error, not a Go error; got %v", err)
	}
	if !isErr {
		t.Fatal("unknown tool must surface as is_error=true tool result")
	}
}

func TestRegistry_Execute_SchemaInvalid(t *testing.T) {
	reg := tools.NewRegistry(5 * time.Second)
	schema := `{"type":"object","properties":{"x":{"type":"integer"}},"required":["x"],"additionalProperties":false}`
	_ = reg.Register(probe.New("p1", schema))
	res, isErr, _, err := reg.Execute(context.Background(),
		tools.ExecContext{}, "p1", json.RawMessage(`{"x":"not-int"}`))
	if err != nil {
		t.Fatalf("schema invalid must surface as tool-result error: %v", err)
	}
	if !isErr {
		t.Fatal("schema invalid input must be is_error=true")
	}
	if res.Output == "" {
		t.Fatal("schema invalid result must include validator message in Output")
	}
}

func TestRegistry_Execute_Timeout(t *testing.T) {
	reg := tools.NewRegistry(5 * time.Millisecond)
	p := probe.New("slow", `{"type":"object"}`)
	p.Delay = 50 * time.Millisecond
	_ = reg.Register(p)
	_, isErr, _, err := reg.Execute(context.Background(),
		tools.ExecContext{}, "slow", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("timeout must surface as tool-result error: %v", err)
	}
	if !isErr {
		t.Fatal("timeout must be is_error=true")
	}
}

func TestRegistry_Execute_PassesExecContext(t *testing.T) {
	reg := tools.NewRegistry(time.Second)
	p := probe.New("p1", `{"type":"object"}`)
	_ = reg.Register(p)
	ec := tools.ExecContext{
		ConversationID: "c1",
		TurnID:         "t1",
		RunID:          "r1",
		RetrievalMode:  chat.RetrievalAutoGroundedDefault,
		TextbookScope:  []string{"intermediate-accounting"},
	}
	_, _, _, err := reg.Execute(context.Background(), ec, "p1", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	got := p.LastExecContext()
	if got.ConversationID != "c1" || got.TurnID != "t1" || got.RunID != "r1" {
		t.Fatalf("ExecContext IDs mismatch: %+v", got)
	}
	if got.RetrievalMode != chat.RetrievalAutoGroundedDefault {
		t.Fatalf("RetrievalMode mismatch: %v", got.RetrievalMode)
	}
	if len(got.TextbookScope) != 1 || got.TextbookScope[0] != "intermediate-accounting" {
		t.Fatalf("TextbookScope mismatch: %v", got.TextbookScope)
	}
}

func TestRegistry_Execute_ToolRaisedError(t *testing.T) {
	reg := tools.NewRegistry(time.Second)
	p := probe.New("p1", `{"type":"object"}`)
	p.Err = errors.New("boom")
	_ = reg.Register(p)
	_, isErr, _, err := reg.Execute(context.Background(),
		tools.ExecContext{}, "p1", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("tool error must surface as tool-result error, not Go error: %v", err)
	}
	if !isErr {
		t.Fatal("tool-raised error must be is_error=true")
	}
}
