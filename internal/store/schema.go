package store

const schemaSQL = `
CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY, title TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  pinned_model TEXT
);
CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  role TEXT NOT NULL, content TEXT NOT NULL, model TEXT,
  created_at INTEGER NOT NULL, rag_context TEXT, rag_sources TEXT,
  input_tokens INTEGER, output_tokens INTEGER, cached_input_tokens INTEGER
);
CREATE TABLE IF NOT EXISTS conversation_textbooks (
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  textbook_name TEXT NOT NULL, chapter_nums TEXT,
  PRIMARY KEY (conversation_id, textbook_name)
);
CREATE TABLE IF NOT EXISTS conversation_library_items (
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  item_name TEXT NOT NULL,
  PRIMARY KEY (conversation_id, item_name)
);
CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conversation_id);
`
