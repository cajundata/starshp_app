package store

import "testing"

func TestCreateRun_StartsInProgressInactive(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	user, _ := st.AppendUserMessage(conv.ID, "q")
	if err := st.CreateRun(conv.ID, user.TurnID, "r1", "openai", "gpt-x",
		"auto_grounded_default"); err != nil {
		t.Fatal(err)
	}
	run, err := st.GetRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "in_progress" {
		t.Fatalf("status want in_progress, got %q", run.Status)
	}
	if run.ActiveForReplay {
		t.Fatal("new run must NOT be active_for_replay")
	}
}

func TestCompleteRun_ActivatesAndDemotesPriorActive(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	user, _ := st.AppendUserMessage(conv.ID, "q")
	_ = st.CreateRun(conv.ID, user.TurnID, "r1", "openai", "gpt-x", "auto_grounded_default")
	if err := st.CompleteRun("r1", RunTotals{
		InputTokens: 10, OutputTokens: 5,
	}, "end_turn"); err != nil {
		t.Fatal(err)
	}
	r1, _ := st.GetRun("r1")
	if !r1.ActiveForReplay || r1.Status != "completed" {
		t.Fatalf("r1 should be active completed; got %+v", r1)
	}
	// Regenerate: second run for the same turn.
	_ = st.CreateRun(conv.ID, user.TurnID, "r2", "openai", "gpt-x", "auto_grounded_default")
	_ = st.CompleteRun("r2", RunTotals{}, "end_turn")
	r1, _ = st.GetRun("r1")
	r2, _ := st.GetRun("r2")
	if r1.ActiveForReplay {
		t.Fatal("r1 should have been demoted")
	}
	if !r2.ActiveForReplay {
		t.Fatal("r2 should be the active run after completion")
	}
}

func TestCompleteRun_RollsBackWhenNotInProgress(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	user, _ := st.AppendUserMessage(conv.ID, "q")
	_ = st.CreateRun(conv.ID, user.TurnID, "r1", "openai", "gpt-x", "auto_grounded_default")
	_ = st.CompleteRun("r1", RunTotals{}, "end_turn")
	// Now r1 is completed; another regeneration started and was cancelled.
	_ = st.CreateRun(conv.ID, user.TurnID, "r2", "openai", "gpt-x", "auto_grounded_default")
	_ = st.MarkRunCancelled("r2", "user_cancelled")
	// Late completion of r2 must roll back and leave r1 active.
	err := st.CompleteRun("r2", RunTotals{}, "end_turn")
	if err == nil {
		t.Fatal("late completion of non-in_progress run must return an error")
	}
	r1, _ := st.GetRun("r1")
	r2, _ := st.GetRun("r2")
	if !r1.ActiveForReplay {
		t.Fatal("r1 should remain active after rollback")
	}
	if r2.Status != "cancelled" {
		t.Fatalf("r2 should still be cancelled; got %q", r2.Status)
	}
	if r2.ActiveForReplay {
		t.Fatal("r2 must not be active after rolled-back late completion")
	}
}

func TestMarkRunErrored_DoesNotTouchOtherActive(t *testing.T) {
	st := openTestStore(t)
	conv, _ := st.CreateConversation("c")
	user, _ := st.AppendUserMessage(conv.ID, "q")
	_ = st.CreateRun(conv.ID, user.TurnID, "r1", "openai", "gpt-x", "auto_grounded_default")
	_ = st.CompleteRun("r1", RunTotals{}, "end_turn")
	_ = st.CreateRun(conv.ID, user.TurnID, "r2", "openai", "gpt-x", "auto_grounded_default")
	if err := st.MarkRunErrored("r2", "provider_error", "rate_limit", "429"); err != nil {
		t.Fatal(err)
	}
	r1, _ := st.GetRun("r1")
	r2, _ := st.GetRun("r2")
	if !r1.ActiveForReplay {
		t.Fatal("r1 must remain active")
	}
	if r2.Status != "errored" || r2.ActiveForReplay {
		t.Fatalf("r2 want errored & inactive; got %+v", r2)
	}
}
