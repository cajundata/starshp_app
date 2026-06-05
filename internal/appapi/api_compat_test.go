package appapi

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
)

// TestSendMessageRemapsLocalUnreachable boots an API wired to a registry
// containing a single openai_compat model whose base_url points at a TCP
// address with no listener. SendMessage must return an AppError with
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
	cfg := config.Config{
		AppDBPath:    filepath.Join(dir, "app.db"),
		LibraryDir:   filepath.Join(dir, "library"),
		ModelsConfig: filepath.Join(dir, "m.yaml"),
	}
	api := NewAPI(cfg, st, reg, nil)
	api.Startup(context.Background())

	_, err = api.SendMessage(conv.ID, "hi", "llama3.2")
	if err == nil {
		t.Fatal("expected error from SendMessage against a closed local port, got nil")
	}
	ae, ok := err.(provider.AppError)
	if !ok {
		t.Fatalf("error is %T, want provider.AppError: %v", err, err)
	}
	if ae.Code != "local_unreachable" {
		t.Errorf("Code = %q, want local_unreachable (raw err: %v)", ae.Code, err)
	}
	if !strings.Contains(ae.UserMessage, baseURL) {
		t.Errorf("UserMessage %q does not interpolate base URL %q", ae.UserMessage, baseURL)
	}
	if !ae.Retryable {
		t.Errorf("Retryable should be true on a network/transient error")
	}
}
