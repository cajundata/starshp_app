// Package library stores reusable prompt/context snippets as individual
// markdown files in a folder on disk. Each file's H1 is its display name;
// the filename is a frozen slug that serves as the stable item ID.
package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ErrNoH1 is returned by Create and Save when the content has no H1 heading.
var ErrNoH1 = errors.New(`item must contain an H1 heading (e.g. "# Title")`)

// ErrBadName is returned when a filename argument is not a bare file name.
var ErrBadName = errors.New("invalid item filename")

// Library is a folder of markdown snippet files.
type Library struct{ dir string }

// New returns a Library backed by dir. The folder is created lazily on the
// first write; a missing folder reads as an empty library.
func New(dir string) *Library { return &Library{dir: dir} }

// Item is one library entry as shown in the panel list.
type Item struct {
	Filename string `json:"filename"` // stable ID, e.g. "discussion-tone.md"
	Name     string `json:"name"`     // display name (the H1), or the stem if none
	Error    string `json:"error"`    // non-empty if the file could not be read
}

var h1Re = regexp.MustCompile(`(?m)^#[ \t]+(.+?)[ \t]*$`)

// ExtractH1 returns the text of the first H1 heading, or "" if there is none.
func ExtractH1(content string) string {
	m := h1Re.FindStringSubmatch(content)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// StripH1 removes the first H1 heading line from content and returns the rest,
// trimmed of surrounding blank lines.
func StripH1(content string) string {
	loc := h1Re.FindStringIndex(content)
	if loc == nil {
		return strings.TrimSpace(content)
	}
	return strings.TrimSpace(content[:loc[0]] + content[loc[1]:])
}

var slugDropRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify converts a display name into a lowercase, no-space filename stem.
// A name with no slug-able characters yields "item".
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugDropRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "item"
	}
	return s
}

// safeName rejects anything that is not a bare file name (no path separators,
// no ".."), so a caller cannot escape the library folder.
func safeName(filename string) (string, error) {
	if filename == "" || filename != filepath.Base(filename) || strings.Contains(filename, "..") {
		return "", ErrBadName
	}
	return filename, nil
}

// List scans the folder and returns one Item per ".md" file, sorted by display
// name. A missing folder is an empty library. An unreadable file still yields a
// row, with Error set.
func (l *Library) List() ([]Item, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Item{}, nil
		}
		return nil, err
	}
	items := []Item{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		raw, err := os.ReadFile(filepath.Join(l.dir, e.Name()))
		if err != nil {
			items = append(items, Item{Filename: e.Name(), Name: stem, Error: err.Error()})
			continue
		}
		name := ExtractH1(string(raw))
		if name == "" {
			name = stem
		}
		items = append(items, Item{Filename: e.Name(), Name: name})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return items, nil
}

// Read returns the raw markdown content of one item.
func (l *Library) Read(filename string) (string, error) {
	name, err := safeName(filename)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(l.dir, name))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// Create writes a new item. The filename is a unique slug derived from the
// content's H1; a numeric suffix breaks collisions. Returns ErrNoH1 if the
// content has no H1.
func (l *Library) Create(content string) (Item, error) {
	h1 := ExtractH1(content)
	if h1 == "" {
		return Item{}, ErrNoH1
	}
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return Item{}, err
	}
	stem := slugify(h1)
	filename := stem + ".md"
	for n := 2; ; n++ {
		_, statErr := os.Stat(filepath.Join(l.dir, filename))
		if os.IsNotExist(statErr) {
			break
		}
		if statErr != nil {
			return Item{}, fmt.Errorf("checking filename availability: %w", statErr)
		}
		filename = stem + "-" + strconv.Itoa(n) + ".md"
	}
	if err := os.WriteFile(filepath.Join(l.dir, filename), []byte(content), 0o644); err != nil {
		return Item{}, err
	}
	return Item{Filename: filename, Name: h1}, nil
}

// Save overwrites an existing item's content. The filename never changes, even
// if the H1 (display name) does. Returns ErrNoH1 if the content has no H1.
func (l *Library) Save(filename, content string) error {
	name, err := safeName(filename)
	if err != nil {
		return err
	}
	if ExtractH1(content) == "" {
		return ErrNoH1
	}
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	// os.WriteFile creates the file if absent; Save is only ever called with
	// a filename from Create/List, so in practice it always overwrites.
	return os.WriteFile(filepath.Join(l.dir, name), []byte(content), 0o644)
}

// Delete removes an item file.
func (l *Library) Delete(filename string) error {
	name, err := safeName(filename)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(l.dir, name))
}
