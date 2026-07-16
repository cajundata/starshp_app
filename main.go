package main

import (
	"embed"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cajundata/starshp_app/internal/appapi"
	"github.com/cajundata/starshp_app/internal/config"
	"github.com/cajundata/starshp_app/internal/imagestore"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/rag"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	appDir, err := config.AppDir()
	if err != nil {
		log.Fatalf("app dir: %v", err)
	}
	cfg, err := config.Load(filepath.Join(appDir, ".env"))
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.AppDBPath == "" {
		cfg.AppDBPath = filepath.Join(appDir, "app.db")
	}
	if cfg.RAGDBPath == "" {
		cfg.RAGDBPath = filepath.Join(appDir, "rag.db")
	}
	if cfg.LibraryDir == "" {
		cfg.LibraryDir = filepath.Join(appDir, "library")
	}
	if cfg.PersonaDir == "" {
		cfg.PersonaDir = filepath.Join(appDir, "personas")
	}
	if cfg.ImagesDir == "" {
		cfg.ImagesDir = filepath.Join(appDir, "images")
	}
	// Books live under <app-dir>/textbooks/<book>/chapter-NN.md by convention.
	// Pre-create the parent so a fresh install has the expected shape.
	if err := os.MkdirAll(filepath.Join(appDir, "textbooks"), 0o755); err != nil {
		log.Fatalf("textbooks dir: %v", err)
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

	// Generated images are served to the webview from the app dir; the handler
	// receives every request the embedded bundle can't satisfy.
	img, err := imagestore.New(cfg.ImagesDir)
	if err != nil {
		log.Printf("warning: image store: %v", err)
	}

	if err := wails.Run(&options.App{
		Title:       "Starshp",
		Width:       1100,
		Height:      760,
		AssetServer: &assetserver.Options{Assets: assets, Handler: imageHandler(img)},
		OnStartup:   api.Startup,
		Bind:        []any{api},
	}); err != nil {
		log.Fatal(err)
	}
}

// imageHandler serves /appimages/<hash>.png from the image store; a store
// that failed to initialize degrades to 404s (broken images render as the
// frontend's placeholder), never a startup crash.
func imageHandler(img *imagestore.Store) http.Handler {
	if img == nil {
		return http.NotFoundHandler()
	}
	return img.Handler()
}
