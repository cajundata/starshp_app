package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractH1(t *testing.T) {
	cases := []struct{ in, want string }{
		{"# Hello World\n\nbody", "Hello World"},
		{"\n\n# Leading blank\n", "Leading blank"},
		{"## Only an H2\n", ""},
		{"no heading at all", ""},
		{"# Trimmed   \n", "Trimmed"},
	}
	for _, c := range cases {
		if got := ExtractH1(c.in); got != c.want {
			t.Errorf("ExtractH1(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStripH1(t *testing.T) {
	if got := StripH1("# Title\n\nThe body."); got != "The body." {
		t.Errorf("StripH1 with body = %q", got)
	}
	if got := StripH1("# Title only"); got != "" {
		t.Errorf("StripH1 no body = %q, want empty", got)
	}
	if got := StripH1("no heading\njust text"); got != "no heading\njust text" {
		t.Errorf("StripH1 no H1 = %q", got)
	}
	if got := StripH1("# Title\r\n\r\nThe body."); got != "The body." {
		t.Errorf("StripH1 CRLF = %q", got)
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Discussion Post Tone", "discussion-post-tone"},
		{"  Trim Me!  ", "trim-me"},
		{"Mixed CASE 123", "mixed-case-123"},
		{"日本語", "item"},
	}
	for _, c := range cases {
		if got := slugify(c.in); got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCreateReadList(t *testing.T) {
	l := New(t.TempDir())
	item, err := l.Create("# Revenue Tone\n\nBe precise about ASC 606.")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.Filename != "revenue-tone.md" || item.Name != "Revenue Tone" {
		t.Fatalf("Create returned %+v", item)
	}
	content, err := l.Read(item.Filename)
	if err != nil || !strings.Contains(content, "ASC 606") {
		t.Fatalf("Read = %q, err=%v", content, err)
	}
	items, err := l.List()
	if err != nil || len(items) != 1 || items[0].Name != "Revenue Tone" {
		t.Fatalf("List = %+v, err=%v", items, err)
	}
}

func TestCreateRequiresH1(t *testing.T) {
	l := New(t.TempDir())
	if _, err := l.Create("just a body, no heading"); err != ErrNoH1 {
		t.Fatalf("Create without H1 = %v, want ErrNoH1", err)
	}
}

func TestCreateCollisionSuffix(t *testing.T) {
	l := New(t.TempDir())
	a, err := l.Create("# Tone\n\nfirst")
	if err != nil {
		t.Fatalf("setup Create a: %v", err)
	}
	b, err := l.Create("# Tone\n\nsecond")
	if err != nil {
		t.Fatalf("setup Create b: %v", err)
	}
	c, err := l.Create("# Tone\n\nthird")
	if err != nil {
		t.Fatalf("setup Create c: %v", err)
	}
	if a.Filename != "tone.md" || b.Filename != "tone-2.md" || c.Filename != "tone-3.md" {
		t.Fatalf("collision filenames: %q %q %q", a.Filename, b.Filename, c.Filename)
	}
}

func TestSaveKeepsFilenameAcrossH1Change(t *testing.T) {
	l := New(t.TempDir())
	item, err := l.Create("# Original Name\n\nbody")
	if err != nil {
		t.Fatalf("setup Create: %v", err)
	}
	if err := l.Save(item.Filename, "# Renamed Display\n\nnew body"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	items, _ := l.List()
	if len(items) != 1 || items[0].Filename != "original-name.md" || items[0].Name != "Renamed Display" {
		t.Fatalf("after Save: %+v", items)
	}
}

func TestSaveRequiresH1(t *testing.T) {
	l := New(t.TempDir())
	item, err := l.Create("# Name\n\nbody")
	if err != nil {
		t.Fatalf("setup Create: %v", err)
	}
	if err := l.Save(item.Filename, "no heading now"); err != ErrNoH1 {
		t.Fatalf("Save without H1 = %v, want ErrNoH1", err)
	}
}

func TestDelete(t *testing.T) {
	l := New(t.TempDir())
	item, err := l.Create("# Gone\n\nbody")
	if err != nil {
		t.Fatalf("setup Create: %v", err)
	}
	if err := l.Delete(item.Filename); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	items, _ := l.List()
	if len(items) != 0 {
		t.Fatalf("expected empty after Delete, got %+v", items)
	}
}

func TestListEmptyAndMissingFolder(t *testing.T) {
	missing := New(filepath.Join(t.TempDir(), "does-not-exist"))
	if items, err := missing.List(); err != nil || len(items) != 0 {
		t.Fatalf("missing folder List = %+v, err=%v", items, err)
	}
	empty := New(t.TempDir())
	if items, err := empty.List(); err != nil || len(items) != 0 {
		t.Fatalf("empty folder List = %+v, err=%v", items, err)
	}
}

func TestListIgnoresNonMarkdownAndFallsBackToStem(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("# Not Markdown"), 0o644)
	os.WriteFile(filepath.Join(dir, "headless.md"), []byte("no heading here"), 0o644)
	items, err := New(dir).List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected only the .md file, got %+v", items)
	}
	if items[0].Filename != "headless.md" || items[0].Name != "headless" {
		t.Fatalf("stem fallback failed: %+v", items[0])
	}
}

func TestCreateUnicodeNameFallsBackToItemSlug(t *testing.T) {
	l := New(t.TempDir())
	item, err := l.Create("# 日本語\n\nbody")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.Filename != "item.md" || item.Name != "日本語" {
		t.Fatalf("unicode item = %+v", item)
	}
}

func TestSafeNameRejectsTraversal(t *testing.T) {
	l := New(t.TempDir())
	for _, bad := range []string{"../escape.md", "sub/file.md", "", ".."} {
		if err := l.Delete(bad); !errors.Is(err, ErrBadName) {
			t.Errorf("Delete(%q) = %v, want ErrBadName", bad, err)
		}
		if err := l.Save(bad, "# Title\n\nbody"); !errors.Is(err, ErrBadName) {
			t.Errorf("Save(%q) = %v, want ErrBadName", bad, err)
		}
		if _, err := l.Read(bad); !errors.Is(err, ErrBadName) {
			t.Errorf("Read(%q) = %v, want ErrBadName", bad, err)
		}
	}
}
