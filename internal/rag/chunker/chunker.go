// Package chunker splits chapter Markdown files into semantically meaningful
// chunks with rich metadata for downstream embedding and retrieval.
package chunker

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/tiktoken-go/tokenizer"
)

// ChapterMeta holds metadata about the source chapter file.
type ChapterMeta struct {
	TextbookTitle string
	Edition       string
	ChapterNum    int
	ChapterTitle  string
	SourceFile    string
}

// Chunk represents a single chunk of chapter content with metadata.
type Chunk struct {
	ID              string // sha256-based, truncated to 16 hex chars
	TextbookTitle   string
	Edition         string
	ChapterNum      int
	ChapterTitle    string
	SectionHeading  string
	Subheading      string
	Content         string
	TokenCount      int
	ChunkOrder      int    // 0-based order within section
	SourceFile      string
	ChunkType       string // concept, rule, example, table, exercise-support
	ParentSectionID string // sha256-based, truncated to 16 hex chars
}

// maxContentLength is the maximum number of bytes accepted by ChunkFile.
// Prevents denial-of-service from extremely large inputs (T-06-01).
const maxContentLength = 50 * 1024 * 1024 // 50 MB

var (
	sectionHeading    = regexp.MustCompile(`^##\s+(.+)`)
	subsectionHeading = regexp.MustCompile(`^###\s+(.+)`)
	codeFenceStart    = regexp.MustCompile("^```")
)

// encoder caches the tiktoken encoder (thread-safe singleton).
var (
	encoderOnce sync.Once
	enc         tokenizer.Codec
	encErr      error
)

func getEncoder() (tokenizer.Codec, error) {
	encoderOnce.Do(func() {
		enc, encErr = tokenizer.Get(tokenizer.Cl100kBase)
	})
	return enc, encErr
}

// countTokens returns the token count for text using cl100k_base encoding.
func countTokens(text string) (int, error) {
	codec, err := getEncoder()
	if err != nil {
		return 0, fmt.Errorf("get tokenizer: %w", err)
	}
	tokens, _, err := codec.Encode(text)
	if err != nil {
		return 0, fmt.Errorf("encode tokens: %w", err)
	}
	return len(tokens), nil
}

// CountTokens returns the cl100k_base token count for text. Exported so callers
// outside the chunker (e.g. the RAG query path) can size text against the
// OpenAI embedding limit using the same tokenizer the index pipeline uses.
func CountTokens(text string) (int, error) {
	return countTokens(text)
}

// TruncateToTokens returns text limited to at most maxTokens cl100k_base tokens,
// reporting whether truncation occurred. It is used to cap RAG query text before
// embedding: OpenAI's embedding models reject inputs over 8192 tokens, so a
// pasted document degrades to best-effort grounding instead of failing the turn.
func TruncateToTokens(text string, maxTokens int) (string, bool, error) {
	if maxTokens <= 0 {
		return "", false, fmt.Errorf("truncate to tokens: maxTokens must be positive, got %d", maxTokens)
	}
	codec, err := getEncoder()
	if err != nil {
		return "", false, fmt.Errorf("get tokenizer: %w", err)
	}
	ids, _, err := codec.Encode(text)
	if err != nil {
		return "", false, fmt.Errorf("encode tokens: %w", err)
	}
	if len(ids) <= maxTokens {
		return text, false, nil
	}
	truncated, err := codec.Decode(ids[:maxTokens])
	if err != nil {
		return "", false, fmt.Errorf("decode tokens: %w", err)
	}
	return truncated, true, nil
}

// section represents a parsed section from the chapter.
type section struct {
	heading    string
	subheading string
	content    string
}

// ChunkFile splits chapter content into chunks at heading boundaries with
// token-aware sub-splitting for oversized sections.
func ChunkFile(content string, meta ChapterMeta, maxTokens int) ([]Chunk, error) {
	if len(content) == 0 {
		return nil, nil
	}

	// T-06-01: Validate content length to prevent DoS
	if len(content) > maxContentLength {
		return nil, fmt.Errorf("content exceeds maximum length of %d bytes", maxContentLength)
	}

	sections := parseSections(content)
	if len(sections) == 0 {
		return nil, nil
	}

	var chunks []Chunk
	for _, sec := range sections {
		sectionChunks, err := chunkSection(sec, meta, maxTokens)
		if err != nil {
			return nil, fmt.Errorf("chunk section %q: %w", sec.heading, err)
		}
		chunks = append(chunks, sectionChunks...)
	}

	return chunks, nil
}

// parseSections splits content into sections at ## headings, tracking ###
// subheadings within each section. Headings inside code fences are ignored.
func parseSections(content string) []section {
	lines := strings.Split(content, "\n")
	var sections []section
	var currentHeading string
	var currentSubheading string
	var currentContent strings.Builder
	inCodeFence := false

	flushSection := func() {
		if currentHeading != "" {
			sections = append(sections, section{
				heading:    currentHeading,
				subheading: currentSubheading,
				content:    currentContent.String(),
			})
		}
	}

	for _, line := range lines {
		// Track code fences
		if codeFenceStart.MatchString(line) {
			inCodeFence = !inCodeFence
			currentContent.WriteString(line)
			currentContent.WriteString("\n")
			continue
		}

		// Only detect headings outside code fences
		if !inCodeFence {
			if matches := sectionHeading.FindStringSubmatch(line); len(matches) >= 2 {
				flushSection()
				currentHeading = strings.TrimSpace(matches[1])
				currentSubheading = ""
				currentContent.Reset()
				currentContent.WriteString(line)
				currentContent.WriteString("\n")
				continue
			}

			if matches := subsectionHeading.FindStringSubmatch(line); len(matches) >= 2 {
				currentSubheading = strings.TrimSpace(matches[1])
				currentContent.WriteString(line)
				currentContent.WriteString("\n")
				continue
			}
		}

		currentContent.WriteString(line)
		currentContent.WriteString("\n")
	}

	flushSection()
	return sections
}

// chunkSection converts a section into one or more chunks. If the section
// exceeds maxTokens, it is sub-split on paragraph boundaries.
func chunkSection(sec section, meta ChapterMeta, maxTokens int) ([]Chunk, error) {
	tokenCount, err := countTokens(sec.content)
	if err != nil {
		return nil, err
	}

	parentSectionID := generateParentSectionID(meta.TextbookTitle, meta.ChapterNum, sec.heading)

	if tokenCount <= maxTokens {
		chunk := buildChunk(sec.content, sec.heading, sec.subheading, meta, 0, tokenCount, parentSectionID)
		return []Chunk{chunk}, nil
	}

	// Sub-split oversized section
	paragraphs := splitIntoParagraphs(sec.content)
	var chunks []Chunk
	var accumulator strings.Builder
	accTokens := 0
	chunkOrder := 0

	for _, para := range paragraphs {
		paraTokens, err := countTokens(para)
		if err != nil {
			return nil, err
		}

		// If a single paragraph exceeds maxTokens, keep it as-is (atomic unit)
		if accumulator.Len() == 0 && paraTokens > maxTokens {
			chunk := buildChunk(para, sec.heading, sec.subheading, meta, chunkOrder, paraTokens, parentSectionID)
			chunks = append(chunks, chunk)
			chunkOrder++
			continue
		}

		// Would adding this paragraph exceed the limit?
		if accTokens+paraTokens > maxTokens && accumulator.Len() > 0 {
			tc, err := countTokens(accumulator.String())
			if err != nil {
				return nil, err
			}
			chunk := buildChunk(accumulator.String(), sec.heading, sec.subheading, meta, chunkOrder, tc, parentSectionID)
			chunks = append(chunks, chunk)
			chunkOrder++
			accumulator.Reset()
			accTokens = 0
		}

		accumulator.WriteString(para)
		accumulator.WriteString("\n\n")
		accTokens += paraTokens
	}

	// Flush remaining content
	if accumulator.Len() > 0 {
		remaining := accumulator.String()
		tc, err := countTokens(remaining)
		if err != nil {
			return nil, err
		}
		chunk := buildChunk(remaining, sec.heading, sec.subheading, meta, chunkOrder, tc, parentSectionID)
		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

// splitIntoParagraphs splits section content into paragraphs while preserving
// tables (consecutive | lines) and fenced code blocks as atomic units.
func splitIntoParagraphs(content string) []string {
	lines := strings.Split(content, "\n")
	var paragraphs []string
	var current strings.Builder
	inCodeFence := false
	inTable := false

	flushCurrent := func() {
		text := strings.TrimRight(current.String(), "\n")
		if text != "" {
			paragraphs = append(paragraphs, text)
		}
		current.Reset()
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track code fences
		if codeFenceStart.MatchString(line) {
			if !inCodeFence {
				// Starting a code fence: flush any previous paragraph
				if !inTable {
					flushCurrent()
				}
				inCodeFence = true
			} else {
				// Ending a code fence
				inCodeFence = false
				current.WriteString(line)
				current.WriteString("\n")
				flushCurrent()
				continue
			}
			current.WriteString(line)
			current.WriteString("\n")
			continue
		}

		if inCodeFence {
			current.WriteString(line)
			current.WriteString("\n")
			continue
		}

		// Track table blocks
		if strings.HasPrefix(trimmed, "|") {
			if !inTable {
				flushCurrent()
				inTable = true
			}
			current.WriteString(line)
			current.WriteString("\n")
			continue
		}

		if inTable {
			// Non-table line ends the table block
			inTable = false
			flushCurrent()
		}

		// Empty line = paragraph boundary
		if trimmed == "" {
			flushCurrent()
			continue
		}

		current.WriteString(line)
		current.WriteString("\n")
	}

	flushCurrent()
	return paragraphs
}

// buildChunk creates a Chunk with all metadata populated.
func buildChunk(content, heading, subheading string, meta ChapterMeta, order, tokenCount int, parentSectionID string) Chunk {
	return Chunk{
		ID:              generateChunkID(meta.TextbookTitle, meta.ChapterNum, heading, order),
		TextbookTitle:   meta.TextbookTitle,
		Edition:         meta.Edition,
		ChapterNum:      meta.ChapterNum,
		ChapterTitle:    meta.ChapterTitle,
		SectionHeading:  heading,
		Subheading:      subheading,
		Content:         content,
		TokenCount:      tokenCount,
		ChunkOrder:      order,
		SourceFile:      meta.SourceFile,
		ChunkType:       DetectChunkType(content),
		ParentSectionID: parentSectionID,
	}
}

// generateChunkID produces a deterministic 16-hex-char ID from chunk identity fields.
func generateChunkID(textbook string, chapterNum int, sectionHeading string, chunkOrder int) string {
	input := textbook + fmt.Sprint(chapterNum) + sectionHeading + fmt.Sprint(chunkOrder)
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", hash)[:16]
}

// generateParentSectionID produces a deterministic 16-hex-char ID for the parent section.
func generateParentSectionID(textbook string, chapterNum int, sectionHeading string) string {
	input := textbook + fmt.Sprint(chapterNum) + sectionHeading
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", hash)[:16]
}
