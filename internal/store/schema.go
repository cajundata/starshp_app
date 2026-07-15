package store

const schemaSQL = `
CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY, title TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  pinned_model TEXT,
  pinned_persona TEXT,
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
  persona_id                TEXT,
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
CREATE TABLE IF NOT EXISTS turn_context_overrides (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    turn_id         TEXT NOT NULL PRIMARY KEY,
    state           TEXT NOT NULL CHECK (state IN ('always','never'))
);
CREATE TABLE IF NOT EXISTS ideas (
  id              TEXT PRIMARY KEY,
  title           TEXT NOT NULL,
  summary         TEXT NOT NULL DEFAULT '',
  pathway         TEXT,
  status          TEXT NOT NULL CHECK (status IN (
                      'raw','triaged','in_review','validating',
                      'go','parked','killed')),
  kill_reason     TEXT,
  financial_flag  INTEGER NOT NULL DEFAULT 0,
  source          TEXT NOT NULL DEFAULT 'manual' CHECK (source IN (
                      'manual','scout','import')),
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS idea_status_history (
  id          TEXT PRIMARY KEY,
  idea_id     TEXT NOT NULL REFERENCES ideas(id) ON DELETE CASCADE,
  from_status TEXT,
  to_status   TEXT NOT NULL,
  reason      TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS idea_reviews (
  id              TEXT PRIMARY KEY,
  idea_id         TEXT NOT NULL REFERENCES ideas(id) ON DELETE CASCADE,
  conversation_id TEXT,
  pathway         TEXT NOT NULL,
  model           TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL CHECK (status IN (
                      'in_progress','completed','parked','cancelled','errored')),
  bluf_verdict    TEXT,
  bluf_json       TEXT,
  document_md     TEXT,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS idea_review_roles (
  id            TEXT PRIMARY KEY,
  review_id     TEXT NOT NULL REFERENCES idea_reviews(id) ON DELETE CASCADE,
  seq           INTEGER NOT NULL,
  role_key      TEXT NOT NULL,
  role_name     TEXT NOT NULL,
  status        TEXT NOT NULL CHECK (status IN (
                    'pending','running','done','errored','cancelled')),
  verdict       TEXT,
  findings_json TEXT,
  run_id        TEXT,
  error         TEXT,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS kill_criteria (
  id          TEXT PRIMARY KEY,
  idea_id     TEXT NOT NULL REFERENCES ideas(id) ON DELETE CASCADE,
  review_id   TEXT REFERENCES idea_reviews(id) ON DELETE SET NULL,
  metric      TEXT NOT NULL,
  threshold   TEXT NOT NULL,
  review_date INTEGER NOT NULL,
  on_miss     TEXT NOT NULL CHECK (on_miss IN ('kill','park','halt')),
  status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN (
                  'pending','met','missed','resolved')),
  notes       TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS send_backs (
  id          TEXT PRIMARY KEY,
  review_id   TEXT NOT NULL REFERENCES idea_reviews(id) ON DELETE CASCADE,
  from_role   TEXT NOT NULL,
  question    TEXT NOT NULL,
  answer      TEXT,
  effect      TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','answered')),
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idea_status_history_idea
  ON idea_status_history(idea_id, created_at);
CREATE INDEX IF NOT EXISTS idea_reviews_idea ON idea_reviews(idea_id);
CREATE INDEX IF NOT EXISTS idea_review_roles_review
  ON idea_review_roles(review_id, seq);
CREATE INDEX IF NOT EXISTS kill_criteria_idea ON kill_criteria(idea_id);
CREATE INDEX IF NOT EXISTS kill_criteria_due
  ON kill_criteria(review_date) WHERE status = 'pending';
`
