package chat

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

type fakeProvider struct {
	gotPrefix string
	usage     *provider.Usage // optional usage to emit on terminal Done
}

func (f *fakeProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.Delta, error) {
	f.gotPrefix = req.CachedPrefix
	ch := make(chan provider.Delta, 2)
	ch <- provider.Delta{Text: "Drafted post"}
	ch <- provider.Delta{Done: true, Usage: f.usage}
	close(ch)
	return ch, nil
}

type fakeRetriever struct{}

func (fakeRetriever) Retrieve(ctx context.Context, q string) (string, string, error) {
	return "CTX: revenue rules", `[{"book":"ia","chapter":18}]`, nil
}

func TestSendPersistsAndAssemblesPrefix(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "app.db"))
	defer st.Close()
	conv, _ := st.CreateConversation("t")

	fp := &fakeProvider{usage: &provider.Usage{InputTokens: 1200, OutputTokens: 450, CachedInputTokens: 800}}
	svc := New(st)

	var streamed string
	final, usage, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID,
		UserText:       "Draft a post on ASC 606",
		SystemPrompt:   "You are an accounting tutor.",
		Model:          "claude-opus-4-7",
		Provider:       fp,
		Retriever:      fakeRetriever{},
	}, func(tok string) { streamed += tok })
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if streamed != "Drafted post" || final != "Drafted post" {
		t.Fatalf("stream=%q final=%q", streamed, final)
	}
	if fp.gotPrefix != "You are an accounting tutor.\n\nCTX: revenue rules" {
		t.Fatalf("prefix assembly wrong: %q", fp.gotPrefix)
	}
	if usage == nil || usage.InputTokens != 1200 || usage.OutputTokens != 450 || usage.CachedInputTokens != 800 {
		t.Fatalf("returned usage = %+v, want {1200, 450, 800}", usage)
	}
	msgs, _ := st.ListMessages(conv.ID)
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("messages = %+v", msgs)
	}
	if msgs[1].Model != "claude-opus-4-7" || msgs[1].RAGContext != "CTX: revenue rules" {
		t.Fatalf("assistant msg missing model/rag: %+v", msgs[1])
	}
	if msgs[1].InputTokens == nil || *msgs[1].InputTokens != 1200 {
		t.Fatalf("persisted InputTokens = %v, want 1200", msgs[1].InputTokens)
	}
	if msgs[1].OutputTokens == nil || *msgs[1].OutputTokens != 450 {
		t.Fatalf("persisted OutputTokens = %v, want 450", msgs[1].OutputTokens)
	}
	if msgs[1].CachedInputTokens == nil || *msgs[1].CachedInputTokens != 800 {
		t.Fatalf("persisted CachedInputTokens = %v, want 800", msgs[1].CachedInputTokens)
	}
	var srcs []map[string]any
	if json.Unmarshal([]byte(msgs[1].RAGSources), &srcs); len(srcs) != 1 {
		t.Fatalf("rag sources not persisted: %q", msgs[1].RAGSources)
	}
}

// cancelProvider sends one token then blocks until ctx is cancelled, then
// closes the channel — mimicking a real provider that honours context cancellation.
type cancelProvider struct {
	firstTokenSent chan struct{} // closed after sending the first delta
}

func (p *cancelProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.Delta, error) {
	ch := make(chan provider.Delta)
	go func() {
		defer close(ch)
		ch <- provider.Delta{Text: "partial"}
		close(p.firstTokenSent)
		// Block until the caller cancels the context (simulating stream abort).
		<-ctx.Done()
	}()
	return ch, nil
}

// TestSendContextCancelPersistsPartial proves that cancelling the context
// while a stream is in flight causes Send to return and the partial assistant
// message ("partial") to be persisted in the store — satisfying the spec
// requirement "cancel ctx → partial message persisted as-is".
func TestSendContextCancelPersistsPartial(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	conv, _ := st.CreateConversation("cancel-test")

	firstTokenSent := make(chan struct{})
	fp := &cancelProvider{firstTokenSent: firstTokenSent}
	svc := New(st)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		text, _, err := svc.Send(ctx, SendParams{
			ConversationID: conv.ID,
			UserText:       "hello",
			SystemPrompt:   "",
			Model:          "test-model",
			Provider:       fp,
		}, nil)
		done <- result{text, err}
	}()

	// Wait for the first token to be delivered, then cancel.
	select {
	case <-firstTokenSent:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first token")
	}
	cancel()

	// Send must return within 2 seconds of the cancel.
	select {
	case res := <-done:
		// Send may return a nil or context error — both are acceptable.
		_ = res.err
		if !strings.Contains(res.text, "partial") {
			t.Fatalf("expected partial text to contain %q, got %q", "partial", res.text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not return after context cancel")
	}

	// Verify the partial message was persisted in the store.
	msgs, err := st.ListMessages(conv.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	// Expect user + assistant messages.
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(msgs))
	}
	asst := msgs[len(msgs)-1]
	if asst.Role != "assistant" {
		t.Fatalf("last message role = %q, want assistant", asst.Role)
	}
	if !strings.Contains(asst.Content, "partial") {
		t.Fatalf("persisted assistant content = %q, want it to contain %q", asst.Content, "partial")
	}
}

func TestSendNoUsageLeavesNilAndPersistsNull(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "app.db"))
	defer st.Close()
	conv, _ := st.CreateConversation("no-usage")

	fp := &fakeProvider{} // usage is nil
	svc := New(st)

	_, usage, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID,
		UserText:       "hi",
		SystemPrompt:   "",
		Model:          "test",
		Provider:       fp,
	}, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if usage != nil {
		t.Fatalf("usage = %+v, want nil", usage)
	}
	msgs, _ := st.ListMessages(conv.ID)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[1].InputTokens != nil || msgs[1].OutputTokens != nil || msgs[1].CachedInputTokens != nil {
		t.Fatalf("persisted token cols should be nil, got %+v", msgs[1])
	}
}
