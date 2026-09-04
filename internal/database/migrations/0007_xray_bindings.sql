CREATE TABLE xray_bindings (
    id TEXT PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 64),
    inbound_tag TEXT NOT NULL CHECK(length(inbound_tag) BETWEEN 1 AND 128),
    outbound_tag TEXT NOT NULL CHECK(length(outbound_tag) BETWEEN 1 AND 128),
    enabled INTEGER NOT NULL CHECK(enabled IN (0, 1)),
    revision INTEGER NOT NULL CHECK(revision >= 1),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX idx_xray_bindings_enabled_inbound
    ON xray_bindings(inbound_tag)
    WHERE enabled = 1;

CREATE INDEX idx_xray_bindings_outbound
    ON xray_bindings(outbound_tag)
    WHERE enabled = 1;
