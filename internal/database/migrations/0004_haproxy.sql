CREATE TABLE haproxy_backends (
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

CREATE TABLE haproxy_frontends (
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

CREATE TABLE haproxy_frontend_backends (
    frontend_id TEXT NOT NULL REFERENCES haproxy_frontends(id) ON DELETE CASCADE,
    backend_id TEXT NOT NULL REFERENCES haproxy_backends(id) ON DELETE RESTRICT,
    position INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY (frontend_id, backend_id),
    UNIQUE (frontend_id, position)
) STRICT;

CREATE INDEX idx_haproxy_backends_enabled_id
    ON haproxy_backends(enabled, id);

CREATE INDEX idx_haproxy_frontends_enabled_id
    ON haproxy_frontends(enabled, id);

CREATE INDEX idx_haproxy_frontend_backends_backend
    ON haproxy_frontend_backends(backend_id, frontend_id);
