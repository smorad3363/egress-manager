CREATE TABLE admins (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL COLLATE NOCASE UNIQUE
        CHECK (length(username) BETWEEN 3 AND 64),
    password_hash TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE sessions (
    token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32),
    admin_id TEXT NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    csrf_hash BLOB NOT NULL CHECK (length(csrf_hash) = 32),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    revoked_at INTEGER,
    CHECK (expires_at > created_at),
    CHECK (last_seen_at >= created_at)
) STRICT;

CREATE INDEX idx_sessions_active_expiry
    ON sessions(expires_at)
    WHERE revoked_at IS NULL;

CREATE INDEX idx_sessions_admin_active
    ON sessions(admin_id, expires_at)
    WHERE revoked_at IS NULL;

CREATE INDEX idx_sessions_revoked_cleanup
    ON sessions(revoked_at)
    WHERE revoked_at IS NOT NULL;

CREATE TABLE login_attempts (
    id INTEGER PRIMARY KEY,
    bucket_hash BLOB NOT NULL CHECK (length(bucket_hash) = 32),
    succeeded INTEGER NOT NULL CHECK (succeeded IN (0, 1)),
    attempted_at INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_login_attempts_bucket_result_time
    ON login_attempts(bucket_hash, succeeded, attempted_at DESC);

CREATE INDEX idx_login_attempts_retention
    ON login_attempts(attempted_at);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE operation_journal (
    id TEXT PRIMARY KEY,
    operation TEXT NOT NULL,
    requested_change BLOB NOT NULL,
    previous_snapshot BLOB NOT NULL,
    candidate_config BLOB NOT NULL,
    state TEXT NOT NULL CHECK (state IN (
        'PREPARED', 'VALIDATED', 'APPLYING', 'VERIFYING',
        'COMMITTED', 'ROLLING_BACK', 'ROLLED_BACK', 'FAILED'
    )),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    failure_detail TEXT
) STRICT;

CREATE INDEX idx_operation_journal_unfinished
    ON operation_journal(updated_at)
    WHERE state NOT IN ('COMMITTED', 'ROLLED_BACK', 'FAILED');
