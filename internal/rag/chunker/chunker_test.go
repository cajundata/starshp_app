package chunker

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testMeta() ChapterMeta {
	return ChapterMeta{
		TextbookTitle: "Financial Accounting",
		Edition:       "5th",
		ChapterNum:    3,
		ChapterTitle:  "Adjusting Entries",
		SourceFile:    "chapter-03.md",
	}
}

func TestChunkFile_ThreeSections(t *testing.T) {
	content := "## Section One\n\nParagraph about section one.\n\n## Section Two\n\nParagraph about section two.\n\n## Section Three\n\nParagraph about section three.\n"
	meta := testMeta()

	chunks, err := ChunkFile(content, meta, 1000)
	require.NoError(t, err)
	assert.Len(t, chunks, 3)
	assert.Equal(t, "Section One", chunks[0].SectionHeading)
	assert.Equal(t, "Section Two", chunks[1].SectionHeading)
	assert.Equal(t, "Section Three", chunks[2].SectionHeading)
}

func TestChunkFile_OversizedSectionSubSplits(t *testing.T) {
	// Create a section with content exceeding 1000 tokens (each word ~1 token)
	var sb strings.Builder
	sb.WriteString("## Big Section\n\n")
	for i := 0; i < 20; i++ {
		sb.WriteString("Paragraph " + strings.Repeat("word ", 80) + "end.\n\n")
	}
	meta := testMeta()

	chunks, err := ChunkFile(sb.String(), meta, 1000)
	require.NoError(t, err)
	assert.Greater(t, len(chunks), 1, "oversized section should be sub-split")
	for _, c := range chunks {
		assert.LessOrEqual(t, c.TokenCount, 1100, "each chunk should be roughly within maxTokens (allow margin for atomic units)")
	}
}

func TestChunkFile_TablesPreserved(t *testing.T) {
	content := "## Table Section\n\nSome intro text.\n\n| Header1 | Header2 |\n|---------|----------|\n| val1    | val2     |\n| val3    | val4     |\n\nMore text after table.\n"
	meta := testMeta()

	chunks, err := ChunkFile(content, meta, 1000)
	require.NoError(t, err)
	require.Len(t, chunks, 1)

	// Table lines should all be in the same chunk
	assert.Contains(t, chunks[0].Content, "| Header1 | Header2 |")
	assert.Contains(t, chunks[0].Content, "| val3    | val4     |")
}

func TestChunkFile_CodeBlocksPreserved(t *testing.T) {
	content := "## Code Section\n\nSome text.\n\n```\nline1\nline2\nline3\n```\n\nText after code.\n"
	meta := testMeta()

	chunks, err := ChunkFile(content, meta, 1000)
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	assert.Contains(t, chunks[0].Content, "```\nline1\nline2\nline3\n```")
}

func TestChunkFile_HeadingsInsideCodeFenceIgnored(t *testing.T) {
	content := "## Real Section\n\nSome text.\n\n```\n## Fake Heading Inside Fence\nMore code.\n```\n\nMore text.\n"
	meta := testMeta()

	chunks, err := ChunkFile(content, meta, 1000)
	require.NoError(t, err)
	// The fake heading inside the fence should NOT create a new section
	assert.Len(t, chunks, 1)
	assert.Equal(t, "Real Section", chunks[0].SectionHeading)
}

func TestChunkFile_DeterministicIDs(t *testing.T) {
	content := "## Section A\n\nContent of A.\n\n## Section B\n\nContent of B.\n"
	meta := testMeta()

	chunks1, err := ChunkFile(content, meta, 1000)
	require.NoError(t, err)
	chunks2, err := ChunkFile(content, meta, 1000)
	require.NoError(t, err)

	require.Len(t, chunks1, 2)
	require.Len(t, chunks2, 2)
	assert.Equal(t, chunks1[0].ID, chunks2[0].ID)
	assert.Equal(t, chunks1[1].ID, chunks2[1].ID)
}

func TestChunkFile_IDsDifferOnOrder(t *testing.T) {
	content := "## Section A\n\nContent of A.\n\n## Section B\n\nContent of B.\n"
	meta := testMeta()

	chunks, err := ChunkFile(content, meta, 1000)
	require.NoError(t, err)
	require.Len(t, chunks, 2)
	assert.NotEqual(t, chunks[0].ID, chunks[1].ID)
}

func TestChunkFile_Subheadings(t *testing.T) {
	content := "## Main Section\n\nIntro.\n\n### Subsection A\n\nSub content A.\n\n### Subsection B\n\nSub content B.\n"
	meta := testMeta()

	chunks, err := ChunkFile(content, meta, 1000)
	require.NoError(t, err)
	// All content under ## Main Section, with ### creating subheading entries
	// The exact behavior: ### creates sub-chunks within the section
	require.GreaterOrEqual(t, len(chunks), 1)
	for _, c := range chunks {
		assert.Equal(t, "Main Section", c.SectionHeading)
	}
}

func TestChunkFile_EmptyContent(t *testing.T) {
	meta := testMeta()

	chunks, err := ChunkFile("", meta, 1000)
	require.NoError(t, err)
	assert.Empty(t, chunks)
}

func TestChunkFile_MetadataPopulated(t *testing.T) {
	content := "## Test Section\n\nSome content.\n"
	meta := testMeta()

	chunks, err := ChunkFile(content, meta, 1000)
	require.NoError(t, err)
	require.Len(t, chunks, 1)

	c := chunks[0]
	assert.Equal(t, "Financial Accounting", c.TextbookTitle)
	assert.Equal(t, "5th", c.Edition)
	assert.Equal(t, 3, c.ChapterNum)
	assert.Equal(t, "Adjusting Entries", c.ChapterTitle)
	assert.Equal(t, "chapter-03.md", c.SourceFile)
	assert.NotEmpty(t, c.ID)
	assert.NotEmpty(t, c.ParentSectionID)
	assert.Greater(t, c.TokenCount, 0)
}

func TestChunkFile_TokenCountAccuracy(t *testing.T) {
	content := "## Token Test\n\nHello world this is a test of token counting.\n"
	meta := testMeta()

	chunks, err := ChunkFile(content, meta, 1000)
	require.NoError(t, err)
	require.Len(t, chunks, 1)

	// Verify token count is reasonable (not zero, not absurdly high)
	assert.Greater(t, chunks[0].TokenCount, 5)
	assert.Less(t, chunks[0].TokenCount, 100)
}

func TestChunkFile_ContentTooLarge(t *testing.T) {
	// Content exceeding maxContentLength should return an error
	meta := testMeta()
	huge := strings.Repeat("x", maxContentLength+1)

	_, err := ChunkFile(huge, meta, 1000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum length")
}

func TestChunkFile_SingleOversizedParagraphPreserved(t *testing.T) {
	// A single paragraph (table) exceeding maxTokens should be kept as-is
	var sb strings.Builder
	sb.WriteString("## Giant Table Section\n\n")
	// Build a huge table that exceeds 50 tokens
	sb.WriteString("| Col1 | Col2 | Col3 |\n")
	sb.WriteString("|------|------|------|\n")
	for i := 0; i < 100; i++ {
		sb.WriteString("| data | data | data |\n")
	}
	meta := testMeta()

	chunks, err := ChunkFile(sb.String(), meta, 50)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(chunks), 1)
	// The table should remain intact in one chunk even though it exceeds maxTokens
	found := false
	for _, c := range chunks {
		if strings.Contains(c.Content, "| Col1 | Col2 | Col3 |") {
			found = true
			// Table should not be split
			assert.Contains(t, c.Content, "| data | data | data |")
		}
	}
	assert.True(t, found, "table should be present in output")
}

func TestChunkFile_NoHeadingsReturnsEmpty(t *testing.T) {
	// Content without any ## headings should return empty
	content := "Just plain text without any headings.\n\nMore plain text.\n"
	meta := testMeta()

	chunks, err := ChunkFile(content, meta, 1000)
	require.NoError(t, err)
	assert.Empty(t, chunks)
}

func TestChunkFile_ChunkTypeDetected(t *testing.T) {
	// Verify that ChunkFile integrates with DetectChunkType
	content := "## Table Section\n\n| A | B |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |\n"
	meta := testMeta()

	chunks, err := ChunkFile(content, meta, 1000)
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	assert.Equal(t, "table", chunks[0].ChunkType)
}
