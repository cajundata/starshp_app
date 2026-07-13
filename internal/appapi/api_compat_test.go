package appapi

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

// TestSendMessageRemapsLocalUnreachable boots an API wired to a registry
// containing a single openai_compat model whose base_url points at a TCP
// address with no listener. The agentic SendMessage surfaces provider errors
// via the run-errored event (returning nil), so the emitted event must carry
// code local_unreachable and the base URL interpolated into the message.
func TestSendMessageRemapsLocalUnreachable(t *testing.T) {
	// Reserve an OS-assigned port, then close it so dialling fails immediately.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	baseURL := "http://" + addr + "/v1"

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	conv, err := st.CreateConversation("")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	reg := provider.Registry{Models: []provider.ModelInfo{{
		Display: "Llama 3.2 (local)", ID: "llama3.2",
		Provider: "openai_compat", BaseURL: baseURL,
	}}}
	// SendMessage's third argument is a persona ID, not a model ID (Task 6). A
	// persona pointing at the local model must exist before NewAPI loads the
	// registry.
	pdir := filepath.Join(dir, "personas")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatalf("mkdir personas: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "local.md"),
		[]byte("---\nname: Local\nmodel: llama3.2\n---\nLocal assistant.\n"), 0o644); err != nil {
		t.Fatalf("write persona: %v", err)
	}
	cfg := config.Config{
		AppDBPath:    filepath.Join(dir, "app.db"),
		LibraryDir:   filepath.Join(dir, "library"),
		PersonaDir:   pdir,
		ModelsConfig: filepath.Join(dir, "m.yaml"),
	}
	api := NewAPI(cfg, st, reg, nil)
	api.Startup(context.Background())
	// Capture the run-errored event the agentic loop emits (errors flow through
	// the sink, not the SendMessage return value).
	var gotCode, gotMsg string
	api.emit = func(_ string, payload any) {
		m, ok := payload.(map[string]any)
		if !ok {
			return
		}
		if c, ok := m["errorCode"].(string); ok {
			gotCode = c
		}
		if msg, ok := m["errorMessage"].(string); ok {
			gotMsg = msg
		}
	}

	if err = api.SendMessage(conv.ID, "hi", "local"); err != nil {
		t.Fatalf("SendMessage returned a non-nil error: %v", err)
	}
	if gotCode != "local_unreachable" {
		t.Errorf("emitted errorCode = %q, want local_unreachable", gotCode)
	}
	if !strings.Contains(gotMsg, baseURL) {
		t.Errorf("emitted errorMessage %q does not interpolate base URL %q", gotMsg, baseURL)
	}
}
