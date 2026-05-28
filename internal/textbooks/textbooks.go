// Package textbooks scans the configured markdown textbook directory.
package textbooks

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Chapter struct {
	Num  int    `json:"num"`
	Path string `json:"path"`
}

type Book struct {
	Name     string    `json:"name"`
	Chapters []Chapter `json:"chapters"`
	// Error is non-empty when the book's chapter_dir could not be read.
	// The book still appears in ListBooks so the UI can render it as
	// unavailable instead of failing to open the picker entirely.
	Error string `json:"error,omitempty"`
}

type yamlConfig struct {
	Textbooks []struct {
		Name       string `yaml:"name"`
		ChapterDir string `yaml:"chapter_dir"`
	} `yaml:"textbooks"`
}

var chapterRe = regexp.MustCompile(`chapter-0*([0-9]+)\.md$`)

// Scan loads the YAML config and lists each book's chapter files, sorted by
// chapter number. Returns an empty slice (not error) when no books configured.
func Scan(cfgPath string) ([]Book, error) {
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Book{}, nil
		}
		return nil, err
	}
	var cfg yamlConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	base := filepath.Dir(cfgPath)
	var books []Book
	for _, b := range cfg.Textbooks {
		dir := b.ChapterDir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(base, dir)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			// A single unreadable book must not poison ListBooks — the
			// picker would never open. Record the per-book error and move on.
			books = append(books, Book{Name: b.Name, Error: err.Error()})
			continue
		}
		var chs []Chapter
		for _, e := range entries {
			m := chapterRe.FindStringSubmatch(e.Name())
			if m == nil {
				continue
			}
			n, _ := strconv.Atoi(m[1])
			chs = append(chs, Chapter{Num: n, Path: filepath.Join(dir, e.Name())})
		}
		sort.Slice(chs, func(i, j int) bool { return chs[i].Num < chs[j].Num })
		books = append(books, Book{Name: b.Name, Chapters: chs})
	}
	return books, nil
}
