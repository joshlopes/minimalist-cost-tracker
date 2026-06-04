CREATE TABLE IF NOT EXISTS sessions (
    id              TEXT PRIMARY KEY,   -- session_id from hook
    cwd             TEXT,
    model           TEXT,
    started_at      DATETIME,
    ended_at        DATETIME,
    input_tokens    INTEGER DEFAULT 0,
    output_tokens   INTEGER DEFAULT 0,
    cache_read_tokens  INTEGER DEFAULT 0,
    cache_write_tokens INTEGER DEFAULT 0,
    cost_usd        REAL DEFAULT 0,
    transcript_path TEXT
);

CREATE TABLE IF NOT EXISTS skill_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  TEXT NOT NULL REFERENCES sessions(id),
    skill_name  TEXT NOT NULL,
    tool_use_id TEXT,
    occurred_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS tool_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  TEXT NOT NULL REFERENCES sessions(id),
    tool_name   TEXT NOT NULL,
    tool_use_id TEXT,
    occurred_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_skill_events_session ON skill_events(session_id);
CREATE INDEX IF NOT EXISTS idx_tool_events_session  ON tool_events(session_id);
CREATE INDEX IF NOT EXISTS idx_sessions_ended       ON sessions(ended_at);
