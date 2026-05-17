package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/cajundata/discussion_engine/internal/appapi"
	"github.com/cajundata/discussion_engine/internal/config"
	"github.com/cajundata/discussion_engine/internal/provider"
	"github.com/cajundata/discussion_engine/internal/rag"
	"github.com/cajundata/discussion_engine/internal/store"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func dataDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d, _ = os.Getwd()
	}
	p := filepath.Join(d, "discussion_engine")
	os.MkdirAll(p, 0o755)
	return p
}

func main() {
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.AppDBPath == "" {
		cfg.AppDBPath = filepath.Join(dataDir(), "app.db")
	}
	if cfg.RAGDBPath == "" {
		cfg.RAGDBPath = filepath.Join(dataDir(), "rag.db")
	}

	st, err := store.Open(cfg.AppDBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	reg, err := provider.LoadRegistry(cfg.ModelsConfig)
	if err != nil {
		log.Printf("warning: models registry: %v", err)
	}
	ragAdpt, err := rag.NewAdapter(rag.Options{
		RAGDBPath: cfg.RAGDBPath, EmbeddingModel: cfg.EmbeddingModel,
		OpenAIKey: cfg.OpenAIAPIKey,
	})
	if err != nil {
		log.Printf("warning: rag adapter: %v", err)
	}

	api := appapi.NewAPI(cfg, st, reg, ragAdpt)

	if err := wails.Run(&options.App{
		Title:  "Discussion Engine",
		Width:  1100,
		Height: 760,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup:  api.Startup,
		Bind:       []any{api},
	}); err != nil {
		log.Fatal(err)
	}
}
