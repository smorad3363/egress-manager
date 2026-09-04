CREATE TABLE egress_routes (
    id TEXT PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 64),
    name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 96),
    source_kind TEXT NOT NULL CHECK(source_kind IN ('interface', 'subnet', 'listener', 'xray_inbound')),
    source_value TEXT NOT NULL CHECK(length(source_value) BETWEEN 1 AND 253),
    outbound_id TEXT NOT NULL REFERENCES outbounds(id) ON DELETE RESTRICT,
    fallback_outbound_id TEXT REFERENCES outbounds(id) ON DELETE RESTRICT,
    definition TEXT NOT NULL CHECK(length(definition) BETWEEN 2 AND 65536),
    enabled INTEGER NOT NULL CHECK(enabled IN (0, 1)),
    revision INTEGER NOT NULL CHECK(revision >= 1),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX idx_egress_routes_enabled_source
    ON egress_routes(source_kind, source_value)
    WHERE enabled = 1;

CREATE INDEX idx_egress_routes_outbound
    ON egress_routes(outbound_id);

CREATE INDEX idx_egress_routes_fallback_outbound
    ON egress_routes(fallback_outbound_id)
    WHERE fallback_outbound_id IS NOT NULL;
