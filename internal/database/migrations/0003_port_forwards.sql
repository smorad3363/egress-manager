CREATE TABLE port_forwards (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE,
    definition TEXT NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK (length(id) BETWEEN 1 AND 64),
    CHECK (length(name) BETWEEN 1 AND 96),
    CHECK (length(definition) BETWEEN 2 AND 65536),
    CHECK (updated_at >= created_at)
) STRICT;

CREATE INDEX idx_port_forwards_enabled_id
    ON port_forwards(enabled, id);
