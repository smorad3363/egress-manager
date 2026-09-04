CREATE TABLE outbounds (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE,
    adapter TEXT NOT NULL,
    protocol TEXT NOT NULL,
    server_host TEXT NOT NULL,
    server_port INTEGER NOT NULL CHECK (server_port BETWEEN 1 AND 65535),
    definition TEXT NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK (length(id) BETWEEN 1 AND 64),
    CHECK (length(name) BETWEEN 1 AND 96),
    CHECK (length(adapter) BETWEEN 1 AND 32),
    CHECK (length(protocol) BETWEEN 1 AND 32),
    CHECK (length(server_host) BETWEEN 1 AND 253),
    CHECK (length(definition) BETWEEN 2 AND 65536),
    CHECK (updated_at >= created_at)
) STRICT;

CREATE TABLE outbound_credentials (
    outbound_id TEXT PRIMARY KEY REFERENCES outbounds(id) ON DELETE CASCADE,
    nonce BLOB NOT NULL CHECK (length(nonce) = 12),
    ciphertext BLOB NOT NULL CHECK (length(ciphertext) BETWEEN 17 AND 131088),
    updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_outbounds_enabled_adapter_id
    ON outbounds(enabled, adapter, id);

CREATE INDEX idx_outbounds_adapter_protocol_id
    ON outbounds(adapter, protocol, id);
