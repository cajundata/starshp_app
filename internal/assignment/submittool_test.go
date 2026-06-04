package assignment

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cajundata/starshp_app/internal/tools"
)

func TestSubmitAnswerTool_RegistersAndValidates(t *testing.T) {
	mc := firstMC(t)
	reg := tools.NewRegistry(time.Second)
	if err := reg.Register(NewSubmitAnswer(mc)); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Valid input: not an error, output is a confirmation.
	out, isErr, _, err := reg.Execute(context.Background(), tools.ExecContext{},
		"submit_answer", json.RawMessage(`{"confidence":"high","answerIndex":1}`))
	if err != nil || isErr {
		t.Fatalf("valid answer should succeed: isErr=%v err=%v", isErr, err)
	}
	if out.Output == "" {
		t.Error("expected a non-empty confirmation output")
	}
	// Out-of-range index: registry schema validation marks it is_error.
	_, isErr, _, _ = reg.Execute(context.Background(), tools.ExecContext{},
		"submit_answer", json.RawMessage(`{"confidence":"high","answerIndex":9}`))
	if !isErr {
		t.Fatal("out-of-range answerIndex should be a tool-result error")
	}
}
