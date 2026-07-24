CREATE TABLE conversations (
    id TEXT PRIMARY KEY,
    issuer TEXT NOT NULL,
    subject TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    stored_bytes INTEGER NOT NULL DEFAULT 0 CHECK (stored_bytes >= 0)
);
CREATE INDEX conversations_owner_updated
    ON conversations (issuer, subject, updated_at DESC, id);
CREATE INDEX conversations_expiry
    ON conversations (expires_at, id);

CREATE TABLE turns (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed', 'canceled', 'interrupted')),
    error_code TEXT NOT NULL DEFAULT '',
    started_at INTEGER NOT NULL,
    completed_at INTEGER,
    UNIQUE (conversation_id, sequence)
);

CREATE UNIQUE INDEX turns_one_running
    ON turns (conversation_id)
    WHERE status = 'running';
CREATE INDEX turns_conversation_sequence
    ON turns (conversation_id, sequence);

CREATE TABLE messages (
    turn_id TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'tool')),
    text TEXT NOT NULL,
    tool_calls_json BLOB NOT NULL,
    tool_results_json BLOB NOT NULL,
    stored_bytes INTEGER NOT NULL CHECK (stored_bytes >= 0),
    PRIMARY KEY (turn_id, sequence)
);
