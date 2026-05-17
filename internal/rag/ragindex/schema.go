// Package ragindex provides SQLite-backed storage for RAG chunk embeddings
// with in-process cosine similarity search and staleness detection.
package ragindex

// SchemaVersion tracks the database schema version for migration support.
const SchemaVersion = 1

// ChunkingPolicyVersion tracks the chunking algorithm version.
// Increment when the chunking logic changes to trigger reindexing.
const ChunkingPolicyVersion = 1

// CreateTablesSQL contains the DDL for all ragindex tables and indexes.
const CreateTablesSQL = `
CREATE TABLE IF NOT EXISTS index_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS chunks (
    chunk_id         TEXT PRIMARY KEY,
    textbook_title   TEXT NOT NULL,
    edition          TEXT,
    chapter_num      INTEGER NOT NULL,
    chapter_title    TEXT,
    section_heading  TEXT,
    subheading       TEXT,
    content          TEXT NOT NULL,
    token_count      INTEGER NOT NULL,
    chunk_order      INTEGER NOT NULL,
    source_file      TEXT NOT NULL,
    chunk_type       TEXT,
    parent_section_id TEXT
);
CREATE TABLE IF NOT EXISTS embeddings (
    chunk_id  TEXT PRIMARY KEY REFERENCES chunks(chunk_id),
    vector    BLOB NOT NULL,
    dimension INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chunks_textbook ON chunks(textbook_title);
CREATE INDEX IF NOT EXISTS idx_chunks_chapter ON chunks(chapter_num);
CREATE INDEX IF NOT EXISTS idx_chunks_source ON chunks(source_file);
`
