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
	if len(books[0].Chapters) != 2 || books[0].Chapters[0].Num != 1 || books[0].Chapters[1].Num != 18 {
		t.Fatalf("chapters = %+v", books[0].Chapters)
	}
}
