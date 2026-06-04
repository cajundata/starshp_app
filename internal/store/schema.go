package store

const schemaSQL = `
CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY, title TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  pinned_model TEXT,
  retrieval_mode TEXT NOT NULL DEFAULT 'auto_grounded_default'
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
CREATE TABLE IF NOT EXISTS conversation_events (
  id              TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  turn_id         TEXT NOT NULL,
  run_id          TEXT,
  sequence_index  INTEGER NOT NULL,
  kind            TEXT NOT NULL CHECK (kind IN (
                      'user_message','assistant_text',
                      'assistant_tool_call','tool_result')),
  text            TEXT,
  tool_call_id    TEXT,
  tool_name       TEXT,
  tool_input      TEXT,
  tool_metadata   TEXT,
  tool_result_hash TEXT,
  tool_latency_ms INTEGER,
  is_error        INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS runs (
  id                        TEXT PRIMARY KEY,
  conversation_id           TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  turn_id                   TEXT NOT NULL,
  status                    TEXT NOT NULL CHECK (status IN (
                                'in_progress','completed','errored','cancelled')),
  active_for_replay         INTEGER NOT NULL DEFAULT 0,
  provider                  TEXT NOT NULL,
  model                     TEXT NOT NULL,
  retrieval_mode            TEXT NOT NULL,
  grounding_meta            TEXT,
  started_at                INTEGER NOT NULL,
  ended_at                  INTEGER,
  terminal_reason           TEXT,
  error_code                TEXT,
  error_message             TEXT,
  total_input_tokens        INTEGER NOT NULL DEFAULT 0,
  total_output_tokens       INTEGER NOT NULL DEFAULT 0,
  total_cached_input_tokens INTEGER NOT NULL DEFAULT 0,
  total_tool_calls          INTEGER NOT NULL DEFAULT 0,
  total_iterations          INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS conversation_events_conv_seq
  ON conversation_events(conversation_id, sequence_index);
CREATE INDEX IF NOT EXISTS conversation_events_turn
  ON conversation_events(turn_id);
CREATE INDEX IF NOT EXISTS conversation_events_run
  ON conversation_events(run_id);
CREATE INDEX IF NOT EXISTS runs_conv_turn ON runs(conversation_id, turn_id);
CREATE UNIQUE INDEX IF NOT EXISTS runs_one_active_per_turn
  ON runs(turn_id) WHERE active_for_replay = 1;
CREATE TABLE IF NOT EXISTS assignments (
  id              TEXT PRIMARY KEY,
  source_dir      TEXT NOT NULL,
  title           TEXT NOT NULL,
  manifest_hash   TEXT NOT NULL,
  model           TEXT NOT NULL,
  grounding_scope TEXT,
  status          TEXT NOT NULL CHECK (status IN (
                      'in_progress','completed','cancelled','errored')),
  total_items     INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS assignment_items (
  id              TEXT PRIMARY KEY,
  assignment_id   TEXT NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
  seq             INTEGER NOT NULL,
  source_path     TEXT NOT NULL,
  type            TEXT NOT NULL,
  title           TEXT,
  run_id          TEXT,
  conversation_id TEXT,
  status          TEXT NOT NULL CHECK (status IN (
                      'pending','solving','answered','no_answer',
                      'errored','cancelled','unsupported')),
  confidence      TEXT,
  answer_json     TEXT,
  flags_json      TEXT,
  answer_path     TEXT,
  error           TEXT,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS assignment_items_assignment
  ON assignment_items(assignment_id, seq);
CREATE INDEX IF NOT EXISTS assignment_items_run ON assignment_items(run_id);
`
