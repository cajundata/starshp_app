package store

import (
	"encoding/json"
	"testing"
)

func TestGetSubmittedAnswer_ReturnsLatestSubmitAnswerInput(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	user, _ := st.AppendUserMessage(conv.ID, "q")
	_ = st.CreateRun(conv.ID, user.TurnID, "r1", "openai", "m", "auto_grounded_default")
	want := json.RawMessage(`{"confidence":"high","answerIndex":2}`)
	if _, err := st.AppendAssistantToolCall(conv.ID, user.TurnID, "r1",
		"call_1", "submit_answer", want); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetSubmittedAnswer("r1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("want %s, got %s", want, got)
	}
}

func TestGetSubmittedAnswer_EmptyWhenNoneSubmitted(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	user, _ := st.AppendUserMessage(conv.ID, "q")
	_ = st.CreateRun(conv.ID, user.TurnID, "r1", "openai", "m", "auto_grounded_default")
	_, _ = st.AppendAssistantText(conv.ID, user.TurnID, "r1", "no tool call here")
	got, err := st.GetSubmittedAnswer("r1")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %s", got)
	}
}
