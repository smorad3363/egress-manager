CREATE TABLE egress_relays (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    listen_address TEXT NOT NULL,
    listen_port INTEGER NOT NULL CHECK(listen_port BETWEEN 1 AND 65535),
    network TEXT NOT NULL CHECK(network IN ('tcp', 'udp', 'tcp,udp')),
    destination_host TEXT NOT NULL,
    destination_port INTEGER NOT NULL CHECK(destination_port BETWEEN 1 AND 65535),
    outbound_id TEXT NOT NULL REFERENCES outbounds(id) ON DELETE RESTRICT,
    source_cidrs TEXT NOT NULL DEFAULT '[]',
    definition TEXT NOT NULL,
    enabled INTEGER NOT NULL CHECK(enabled IN (0, 1)),
    revision INTEGER NOT NULL CHECK(revision >= 1),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_egress_relays_outbound ON egress_relays(outbound_id);
CREATE INDEX idx_egress_relays_enabled_listener ON egress_relays(enabled, listen_address, listen_port, network);
