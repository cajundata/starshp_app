package ragindex

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"github.com/cajundata/starshp_app/internal/rag/chunker"
	_ "modernc.org/sqlite"
)

// Store provides read/write access to a SQLite-backed RAG index.
type Store struct {
	db *sql.DB
}

// ScoredChunk pairs a chunk with its cosine similarity score to a query vector.
type ScoredChunk struct {
	chunker.Chunk
	Score float64
}

// NewStore opens (or creates) a SQLite database at dbPath, applies the schema,
// and returns a Store ready for use.
func NewStore(dbPath string) (Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(wal)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return Store{}, fmt.Errorf("open sqlite: %w", err)
	}

	if _, err := db.Exec(CreateTablesSQL); err != nil {
		db.Close()
		return Store{}, fmt.Errorf("create tables: %w", err)
	}

	return Store{db: db}, nil
}

// Close releases the database connection.
func (s Store) Close() error {
	return s.db.Close()
}

// SetMeta inserts or replaces a key-value pair in index_meta.
func (s Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(
		"INSERT OR REPLACE INTO index_meta (key, value) VALUES (?, ?)",
		key, value,
	)
	return err
}

// GetMeta retrieves a value from index_meta by key.
// Returns an empty string if the key is not found.
func (s Store) GetMeta(key string) (string, error) {
	var value string
	err := s.db.QueryRow(
		"SELECT value FROM index_meta WHERE key = ?", key,
	).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// InsertChunks stores all chunk metadata in a single transaction.
func (s Store) InsertChunks(chunks []chunker.Chunk) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO chunks
		(chunk_id, textbook_title, edition, chapter_num, chapter_title,
		 section_heading, subheading, content, token_count, chunk_order,
		 source_file, chunk_type, parent_section_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert chunks: %w", err)
	}
	defer stmt.Close()

	for _, c := range chunks {
		if _, err := stmt.Exec(
			c.ID, c.TextbookTitle, c.Edition, c.ChapterNum, c.ChapterTitle,
			c.SectionHeading, c.Subheading, c.Content, c.TokenCount, c.ChunkOrder,
			c.SourceFile, c.ChunkType, c.ParentSectionID,
		); err != nil {
			return fmt.Errorf("insert chunk %s: %w", c.ID, err)
		}
	}

	return tx.Commit()
}

// InsertEmbeddings stores embedding vectors as binary BLOBs in a single transaction.
// chunkIDs and vectors must have the same length.
func (s Store) InsertEmbeddings(chunkIDs []string, vectors [][]float64) error {
	if len(chunkIDs) != len(vectors) {
		return fmt.Errorf("chunkIDs length %d != vectors length %d", len(chunkIDs), len(vectors))
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		"INSERT OR REPLACE INTO embeddings (chunk_id, vector, dimension) VALUES (?, ?, ?)",
	)
	if err != nil {
		return fmt.Errorf("prepare insert embeddings: %w", err)
	}
	defer stmt.Close()

	for i, id := range chunkIDs {
		blob := encodeVector(vectors[i])
		if _, err := stmt.Exec(id, blob, len(vectors[i])); err != nil {
			return fmt.Errorf("insert embedding %s: %w", id, err)
		}
	}

	return tx.Commit()
}

// QueryTopK retrieves the top-K chunks most similar to queryVec by cosine similarity.
// All chunks with embeddings are scanned and scored in-process.
func (s Store) QueryTopK(ctx context.Context, queryVec []float64, k int) ([]ScoredChunk, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.chunk_id, c.textbook_title, c.edition, c.chapter_num,
		        c.chapter_title, c.section_heading, c.subheading, c.content,
		        c.token_count, c.chunk_order, c.source_file, c.chunk_type,
		        c.parent_section_id, e.vector
		 FROM chunks c JOIN embeddings e ON c.chunk_id = e.chunk_id`)
	if err != nil {
		return nil, fmt.Errorf("query chunks: %w", err)
	}
	defer rows.Close()

	var results []ScoredChunk
	for rows.Next() {
		var sc ScoredChunk
		var blob []byte
		if err := rows.Scan(
			&sc.ID, &sc.TextbookTitle, &sc.Edition, &sc.ChapterNum,
			&sc.ChapterTitle, &sc.SectionHeading, &sc.Subheading, &sc.Content,
			&sc.TokenCount, &sc.ChunkOrder, &sc.SourceFile, &sc.ChunkType,
			&sc.ParentSectionID, &blob,
		); err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		storedVec := decodeVector(blob)
		sc.Score = cosineSimilarity(queryVec, storedVec)
		results = append(results, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > k {
		results = results[:k]
	}

	return results, nil
}

// DeleteByTextbook removes all chunks and their embeddings for the given textbook.
func (s Store) DeleteByTextbook(textbookTitle string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Delete embeddings first (foreign key reference)
	if _, err := tx.Exec(
		"DELETE FROM embeddings WHERE chunk_id IN (SELECT chunk_id FROM chunks WHERE textbook_title = ?)",
		textbookTitle,
	); err != nil {
		return fmt.Errorf("delete embeddings: %w", err)
	}

	if _, err := tx.Exec(
		"DELETE FROM chunks WHERE textbook_title = ?",
		textbookTitle,
	); err != nil {
		return fmt.Errorf("delete chunks: %w", err)
	}

	return tx.Commit()
}

// encodeVector converts a float64 slice to a little-endian binary blob.
func encodeVector(vec []float64) []byte {
	blob := make([]byte, len(vec)*8)
	for i, v := range vec {
		binary.LittleEndian.PutUint64(blob[i*8:(i+1)*8], math.Float64bits(v))
	}
	return blob
}

// decodeVector converts a little-endian binary blob to a float64 slice.
func decodeVector(blob []byte) []float64 {
	n := len(blob) / 8
	vec := make([]float64, n)
	for i := range n {
		vec[i] = math.Float64frombits(binary.LittleEndian.Uint64(blob[i*8 : (i+1)*8]))
	}
	return vec
}

// cosineSimilarity computes the dot product of two vectors.
// When vectors are L2-normalized (as OpenAI embeddings are), dot product equals
// cosine similarity.
func cosineSimilarity(a, b []float64) float64 {
	var dot float64
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot
}
