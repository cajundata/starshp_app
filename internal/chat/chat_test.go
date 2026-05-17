package chat

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/cajundata/discussion_engine/internal/provider"
	"github.com/cajundata/discussion_engine/internal/store"
)

type fakeProvider struct{ gotPrefix string }

func (f *fakeProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.Delta, error) {
	f.gotPrefix = req.CachedPrefix
	ch := make(chan provider.Delta, 2)
	ch <- provider.Delta{Text: "Drafted post"}
	ch <- provider.Delta{Done: true}
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

	fp := &fakeProvider{}
	svc := New(st)

	var streamed string
	final, err := svc.Send(context.Background(), SendParams{
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
	msgs, _ := st.ListMessages(conv.ID)
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("messages = %+v", msgs)
	}
	if msgs[1].Model != "claude-opus-4-7" || msgs[1].RAGContext != "CTX: revenue rules" {
		t.Fatalf("assistant msg missing model/rag: %+v", msgs[1])
	}
	var srcs []map[string]any
	if json.Unmarshal([]byte(msgs[1].RAGSources), &srcs); len(srcs) != 1 {
		t.Fatalf("rag sources not persisted: %q", msgs[1].RAGSources)
	}
}
