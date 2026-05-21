package ragindex

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/cajundata/starshp_app/internal/rag/chunker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStore_CreatesTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	// Verify tables exist by querying them
	var count int
	err = store.db.QueryRow("SELECT COUNT(*) FROM chunks").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	err = store.db.QueryRow("SELECT COUNT(*) FROM embeddings").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	err = store.db.QueryRow("SELECT COUNT(*) FROM index_meta").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestInsertChunks_RoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	chunks := []chunker.Chunk{
		{
			ID:              "abc123",
			TextbookTitle:   "Accounting 101",
			Edition:         "5th",
			ChapterNum:      3,
			ChapterTitle:    "Journal Entries",
			SectionHeading:  "Debits and Credits",
			Subheading:      "Basic Rules",
			Content:         "Debit left, credit right.",
			TokenCount:      5,
			ChunkOrder:      0,
			SourceFile:      "chapter-03.md",
			ChunkType:       "concept",
			ParentSectionID: "parent123",
		},
	}

	err = store.InsertChunks(chunks)
	require.NoError(t, err)

	// Read back
	var id, title, edition, chapterTitle, sectionHeading, subheading, content, sourceFile, chunkType, parentSectionID string
	var chapterNum, tokenCount, chunkOrder int
	err = store.db.QueryRow("SELECT chunk_id, textbook_title, edition, chapter_num, chapter_title, section_heading, subheading, content, token_count, chunk_order, source_file, chunk_type, parent_section_id FROM chunks WHERE chunk_id = ?", "abc123").
		Scan(&id, &title, &edition, &chapterNum, &chapterTitle, &sectionHeading, &subheading, &content, &tokenCount, &chunkOrder, &sourceFile, &chunkType, &parentSectionID)
	require.NoError(t, err)

	assert.Equal(t, "abc123", id)
	assert.Equal(t, "Accounting 101", title)
	assert.Equal(t, "5th", edition)
	assert.Equal(t, 3, chapterNum)
	assert.Equal(t, "Journal Entries", chapterTitle)
	assert.Equal(t, "Debits and Credits", sectionHeading)
	assert.Equal(t, "Basic Rules", subheading)
	assert.Equal(t, "Debit left, credit right.", content)
	assert.Equal(t, 5, tokenCount)
	assert.Equal(t, 0, chunkOrder)
	assert.Equal(t, "chapter-03.md", sourceFile)
	assert.Equal(t, "concept", chunkType)
	assert.Equal(t, "parent123", parentSectionID)
}

func TestInsertEmbeddings_BlobRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	// Insert a chunk first (foreign key)
	chunks := []chunker.Chunk{
		{ID: "chunk1", TextbookTitle: "Test", ChapterNum: 1, Content: "test", TokenCount: 1, SourceFile: "test.md"},
	}
	require.NoError(t, store.InsertChunks(chunks))

	original := []float64{0.1, 0.2, 0.3, -0.5, 1.0}
	err = store.InsertEmbeddings([]string{"chunk1"}, [][]float64{original})
	require.NoError(t, err)

	// Read back the BLOB
	var blob []byte
	err = store.db.QueryRow("SELECT vector FROM embeddings WHERE chunk_id = ?", "chunk1").Scan(&blob)
	require.NoError(t, err)

	decoded := decodeVector(blob)
	require.Len(t, decoded, len(original))
	for i := range original {
		assert.InDelta(t, original[i], decoded[i], 1e-15)
	}
}

func TestEncodeDecodeVector_RoundTrip(t *testing.T) {
	original := []float64{0.0, 1.0, -1.0, 3.14159, math.MaxFloat64, math.SmallestNonzeroFloat64}
	encoded := encodeVector(original)
	decoded := decodeVector(encoded)

	require.Len(t, decoded, len(original))
	for i := range original {
		assert.Equal(t, original[i], decoded[i])
	}
}

func TestCosineSimilarity_IdenticalNormalized(t *testing.T) {
	// Normalized vector
	v := []float64{0.5773502691896258, 0.5773502691896258, 0.5773502691896258}
	sim := cosineSimilarity(v, v)
	assert.InDelta(t, 1.0, sim, 1e-10)
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float64{1.0, 0.0, 0.0}
	b := []float64{0.0, 1.0, 0.0}
	sim := cosineSimilarity(a, b)
	assert.InDelta(t, 0.0, sim, 1e-10)
}

func TestQueryTopK_ReturnsSortedByScore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	chunks := []chunker.Chunk{
		{ID: "c1", TextbookTitle: "Test", ChapterNum: 1, Content: "first", TokenCount: 1, SourceFile: "test.md", SectionHeading: "S1", ChunkType: "concept"},
		{ID: "c2", TextbookTitle: "Test", ChapterNum: 1, Content: "second", TokenCount: 1, SourceFile: "test.md", SectionHeading: "S2", ChunkType: "concept"},
		{ID: "c3", TextbookTitle: "Test", ChapterNum: 1, Content: "third", TokenCount: 1, SourceFile: "test.md", SectionHeading: "S3", ChunkType: "concept"},
	}
	require.NoError(t, store.InsertChunks(chunks))

	// c1 most similar to query, c3 least
	queryVec := []float64{1.0, 0.0, 0.0}
	vecs := [][]float64{
		{0.9, 0.1, 0.0},  // c1 - most similar
		{0.5, 0.5, 0.0},  // c2 - middle
		{0.0, 0.0, 1.0},  // c3 - least similar
	}
	require.NoError(t, store.InsertEmbeddings([]string{"c1", "c2", "c3"}, vecs))

	results, err := store.QueryTopK(context.Background(), queryVec, 2)
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "c1", results[0].ID)
	assert.Equal(t, "c2", results[1].ID)
	assert.Greater(t, results[0].Score, results[1].Score)
}

func TestQueryTopK_KLargerThanTotal(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	chunks := []chunker.Chunk{
		{ID: "c1", TextbookTitle: "Test", ChapterNum: 1, Content: "only one", TokenCount: 1, SourceFile: "test.md"},
	}
	require.NoError(t, store.InsertChunks(chunks))
	require.NoError(t, store.InsertEmbeddings([]string{"c1"}, [][]float64{{1.0, 0.0}}))

	results, err := store.QueryTopK(context.Background(), []float64{1.0, 0.0}, 100)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestSetMeta_GetMeta_RoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	require.NoError(t, store.SetMeta("embedding_model", "text-embedding-3-small"))
	val, err := store.GetMeta("embedding_model")
	require.NoError(t, err)
	assert.Equal(t, "text-embedding-3-small", val)

	// Update
	require.NoError(t, store.SetMeta("embedding_model", "text-embedding-3-large"))
	val, err = store.GetMeta("embedding_model")
	require.NoError(t, err)
	assert.Equal(t, "text-embedding-3-large", val)
}

func TestGetMeta_NotFound_ReturnsEmpty(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	val, err := store.GetMeta("nonexistent")
	require.NoError(t, err)
	assert.Equal(t, "", val)
}

func TestDeleteByTextbook(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	chunks := []chunker.Chunk{
		{ID: "c1", TextbookTitle: "Book A", ChapterNum: 1, Content: "a", TokenCount: 1, SourceFile: "a.md"},
		{ID: "c2", TextbookTitle: "Book B", ChapterNum: 1, Content: "b", TokenCount: 1, SourceFile: "b.md"},
	}
	require.NoError(t, store.InsertChunks(chunks))
	require.NoError(t, store.InsertEmbeddings([]string{"c1", "c2"}, [][]float64{{1.0}, {0.5}}))

	require.NoError(t, store.DeleteByTextbook("Book A"))

	var count int
	err = store.db.QueryRow("SELECT COUNT(*) FROM chunks").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	err = store.db.QueryRow("SELECT COUNT(*) FROM embeddings").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}
