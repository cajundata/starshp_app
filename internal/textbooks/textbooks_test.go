package textbooks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScan(t *testing.T) {
	root := t.TempDir()
	bookDir := filepath.Join(root, "intermediate-accounting")
	os.MkdirAll(bookDir, 0o755)
	os.WriteFile(filepath.Join(bookDir, "chapter-01.md"), []byte("# C1\n## S\nbody"), 0o600)
	os.WriteFile(filepath.Join(bookDir, "chapter-18.md"), []byte("# C18\n## S\nbody"), 0o600)

	cfgPath := filepath.Join(root, "textbooks.yaml")
	os.WriteFile(cfgPath, []byte("textbooks:\n  - name: intermediate-accounting\n    chapter_dir: ./intermediate-accounting\n"), 0o600)

	books, err := Scan(cfgPath)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(books) != 1 || books[0].Name != "intermediate-accounting" {
		t.Fatalf("books = %+v", books)
	}
	if books[0].Error != "" {
		t.Fatalf("unexpected error on readable book: %q", books[0].Error)
	}
	if len(books[0].Chapters) != 2 || books[0].Chapters[0].Num != 1 || books[0].Chapters[1].Num != 18 {
		t.Fatalf("chapters = %+v", books[0].Chapters)
	}
}

// A missing chapter_dir must not poison the whole call. The book still appears
// in the listing with Error populated and zero chapters; the modal needs this
// to render the book as "(unavailable)" instead of failing to open.
func TestScanMissingChapterDirIsPerBookError(t *testing.T) {
	root := t.TempDir()
	goodDir := filepath.Join(root, "good")
	os.MkdirAll(goodDir, 0o755)
	os.WriteFile(filepath.Join(goodDir, "chapter-01.md"), []byte("# C1\nbody"), 0o600)

	cfgPath := filepath.Join(root, "textbooks.yaml")
	yaml := "textbooks:\n" +
		"  - name: good\n" +
		"    chapter_dir: ./good\n" +
		"  - name: missing\n" +
		"    chapter_dir: ./does-not-exist\n"
	os.WriteFile(cfgPath, []byte(yaml), 0o600)

	books, err := Scan(cfgPath)
	if err != nil {
		t.Fatalf("Scan returned error for per-book failure: %v", err)
	}
	if len(books) != 2 {
		t.Fatalf("books = %+v, want 2 entries", books)
	}
	byName := map[string]Book{}
	for _, b := range books {
		byName[b.Name] = b
	}
	good, ok := byName["good"]
	if !ok {
		t.Fatalf("good book missing from result: %+v", books)
	}
	if good.Error != "" {
		t.Fatalf("good.Error = %q, want empty", good.Error)
	}
	if len(good.Chapters) != 1 {
		t.Fatalf("good.Chapters = %+v, want one chapter", good.Chapters)
	}
	missing, ok := byName["missing"]
	if !ok {
		t.Fatalf("missing book absent from result: %+v", books)
	}
	if missing.Error == "" {
		t.Fatalf("missing.Error empty; want a not-exist explanation")
	}
	if len(missing.Chapters) != 0 {
		t.Fatalf("missing.Chapters = %+v, want zero", missing.Chapters)
	}
}
