package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
)

type StoredRelay struct {
	Relay     domain.Relay `json:"relay"`
	Revision  int64        `json:"revision"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

func (store *Store) CreateRelay(ctx context.Context, relay domain.Relay, at time.Time) (StoredRelay, error) {
	definition, sourceCIDRs, err := encodeRelay(relay)
	if err != nil {
		return StoredRelay{}, err
	}
	if at.IsZero() {
		return StoredRelay{}, fmt.Errorf("relay creation time is required")
	}
	at = at.UTC().Truncate(time.Second)
	_, err = store.database.ExecContext(ctx, `
		INSERT INTO egress_relays(id, name, listen_address, listen_port, network, destination_host, destination_port, outbound_id, source_cidrs, definition, enabled, revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`, string(relay.ID), relay.Name, relay.ListenAddress, relay.ListenPort, string(relay.Network), relay.Destination.Host, relay.Destination.Port, string(relay.OutboundID), sourceCIDRs, string(definition), relay.Enabled, at.Unix(), at.Unix())
	if err := mapWriteError("create relay", err); err != nil {
		return StoredRelay{}, err
	}
	return StoredRelay{Relay: relay, Revision: 1, CreatedAt: at, UpdatedAt: at}, nil
}

func (store *Store) UpdateRelay(ctx context.Context, relay domain.Relay, expectedRevision int64, at time.Time) (StoredRelay, error) {
	definition, sourceCIDRs, err := encodeRelay(relay)
	if err != nil {
		return StoredRelay{}, err
	}
	if expectedRevision < 1 || at.IsZero() {
		return StoredRelay{}, fmt.Errorf("expected revision and update time are required")
	}
	at = at.UTC().Truncate(time.Second)
	result, err := store.database.ExecContext(ctx, `
		UPDATE egress_relays
		SET name = ?, listen_address = ?, listen_port = ?, network = ?, destination_host = ?, destination_port = ?, outbound_id = ?, source_cidrs = ?, definition = ?, enabled = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND revision = ?
	`, relay.Name, relay.ListenAddress, relay.ListenPort, string(relay.Network), relay.Destination.Host, relay.Destination.Port, string(relay.OutboundID), sourceCIDRs, string(definition), relay.Enabled, at.Unix(), string(relay.ID), expectedRevision)
	if err := mapWriteError("update relay", err); err != nil {
		return StoredRelay{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return StoredRelay{}, fmt.Errorf("update relay: read affected rows: %w", err)
	}
	if count != 1 {
		return StoredRelay{}, ErrConflict
	}
	return store.Relay(ctx, relay.ID)
}

func (store *Store) DeleteRelay(ctx context.Context, id domain.ID, expectedRevision int64) error {
	if err := id.Validate("relay id"); err != nil {
		return err
	}
	if expectedRevision < 1 {
		return fmt.Errorf("expected revision is required")
	}
	result, err := store.database.ExecContext(ctx, `DELETE FROM egress_relays WHERE id = ? AND revision = ?`, string(id), expectedRevision)
	if err := mapWriteError("delete relay", err); err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete relay: read affected rows: %w", err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (store *Store) Relay(ctx context.Context, id domain.ID) (StoredRelay, error) {
	if err := id.Validate("relay id"); err != nil {
		return StoredRelay{}, err
	}
	return scanRelay(store.database.QueryRowContext(ctx, `SELECT definition, revision, created_at, updated_at FROM egress_relays WHERE id = ?`, string(id)))
}

func (store *Store) ListRelays(ctx context.Context, after domain.ID, limit int) ([]StoredRelay, error) {
	if after != "" {
		if err := after.Validate("relay cursor"); err != nil {
			return nil, err
		}
	}
	if limit < 1 || limit > 501 {
		return nil, fmt.Errorf("relay list limit must be between 1 and 501")
	}
	rows, err := store.database.QueryContext(ctx, `SELECT definition, revision, created_at, updated_at FROM egress_relays WHERE id > ? ORDER BY id LIMIT ?`, string(after), limit)
	if err != nil {
		return nil, fmt.Errorf("list relays: %w", err)
	}
	defer rows.Close()
	items := make([]StoredRelay, 0)
	for rows.Next() {
		item, err := scanRelay(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate relays: %w", err)
	}
	return items, nil
}

func encodeRelay(relay domain.Relay) ([]byte, string, error) {
	if err := relay.Validate(); err != nil {
		return nil, "", fmt.Errorf("validate relay: %w", err)
	}
	definition, err := json.Marshal(relay)
	if err != nil {
		return nil, "", fmt.Errorf("encode relay: %w", err)
	}
	if len(definition) > 65536 {
		return nil, "", fmt.Errorf("relay definition exceeds 65536 bytes")
	}
	sources, err := json.Marshal(relay.SourceCIDRs)
	if err != nil {
		return nil, "", fmt.Errorf("encode relay source CIDRs: %w", err)
	}
	return definition, string(sources), nil
}

func scanRelay(scanner operationScanner) (StoredRelay, error) {
	var stored StoredRelay
	var definition string
	var createdAt, updatedAt int64
	if err := scanner.Scan(&definition, &stored.Revision, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StoredRelay{}, ErrNotFound
		}
		return StoredRelay{}, fmt.Errorf("scan relay: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(definition))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored.Relay); err != nil {
		return StoredRelay{}, fmt.Errorf("decode stored relay: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return StoredRelay{}, fmt.Errorf("decode stored relay: trailing data")
	}
	if err := stored.Relay.Validate(); err != nil {
		return StoredRelay{}, fmt.Errorf("validate stored relay: %w", err)
	}
	stored.CreatedAt = time.Unix(createdAt, 0).UTC()
	stored.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return stored, nil
}
