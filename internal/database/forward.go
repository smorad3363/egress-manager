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

type StoredPortForward struct {
	Forward   domain.PortForward
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (store *Store) CreatePortForward(ctx context.Context, forward domain.PortForward, at time.Time) (StoredPortForward, error) {
	definition, err := encodePortForward(forward)
	if err != nil {
		return StoredPortForward{}, err
	}
	if at.IsZero() {
		return StoredPortForward{}, fmt.Errorf("port forward creation time is required")
	}
	at = at.UTC().Truncate(time.Second)
	_, err = store.database.ExecContext(ctx, `
        INSERT INTO port_forwards(id, name, definition, enabled, revision, created_at, updated_at)
        VALUES (?, ?, ?, ?, 1, ?, ?)
    `, string(forward.ID), forward.Name, string(definition), forward.Enabled, at.Unix(), at.Unix())
	if err := mapWriteError("create port forward", err); err != nil {
		return StoredPortForward{}, err
	}
	return StoredPortForward{Forward: forward, Revision: 1, CreatedAt: at, UpdatedAt: at}, nil
}

func (store *Store) UpdatePortForward(ctx context.Context, forward domain.PortForward, expectedRevision int64, at time.Time) (StoredPortForward, error) {
	definition, err := encodePortForward(forward)
	if err != nil {
		return StoredPortForward{}, err
	}
	if expectedRevision < 1 || at.IsZero() {
		return StoredPortForward{}, fmt.Errorf("expected revision and update time are required")
	}
	at = at.UTC().Truncate(time.Second)
	result, err := store.database.ExecContext(ctx, `
        UPDATE port_forwards
        SET name = ?, definition = ?, enabled = ?, revision = revision + 1, updated_at = ?
        WHERE id = ? AND revision = ?
    `, forward.Name, string(definition), forward.Enabled, at.Unix(), string(forward.ID), expectedRevision)
	if err := mapWriteError("update port forward", err); err != nil {
		return StoredPortForward{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return StoredPortForward{}, fmt.Errorf("update port forward: read affected rows: %w", err)
	}
	if count != 1 {
		return StoredPortForward{}, ErrConflict
	}
	stored, err := store.PortForward(ctx, forward.ID)
	if err != nil {
		return StoredPortForward{}, err
	}
	return stored, nil
}

func (store *Store) DeletePortForward(ctx context.Context, id domain.ID, expectedRevision int64) error {
	if err := id.Validate("port forward id"); err != nil {
		return err
	}
	if expectedRevision < 1 {
		return fmt.Errorf("expected revision is required")
	}
	result, err := store.database.ExecContext(ctx, `DELETE FROM port_forwards WHERE id = ? AND revision = ?`, string(id), expectedRevision)
	if err != nil {
		return fmt.Errorf("delete port forward: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete port forward: read affected rows: %w", err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (store *Store) PortForward(ctx context.Context, id domain.ID) (StoredPortForward, error) {
	if err := id.Validate("port forward id"); err != nil {
		return StoredPortForward{}, err
	}
	return scanPortForward(store.database.QueryRowContext(ctx, `
        SELECT definition, revision, created_at, updated_at
        FROM port_forwards
        WHERE id = ?
    `, string(id)))
}

func (store *Store) ListPortForwards(ctx context.Context, after domain.ID, limit int) ([]StoredPortForward, error) {
	if after != "" {
		if err := after.Validate("port forward cursor"); err != nil {
			return nil, err
		}
	}
	if limit < 1 || limit > 501 {
		return nil, fmt.Errorf("port forward list limit must be between 1 and 501")
	}
	rows, err := store.database.QueryContext(ctx, `
        SELECT definition, revision, created_at, updated_at
        FROM port_forwards
        WHERE id > ?
        ORDER BY id
        LIMIT ?
    `, string(after), limit)
	if err != nil {
		return nil, fmt.Errorf("list port forwards: %w", err)
	}
	defer rows.Close()
	forwards := make([]StoredPortForward, 0)
	for rows.Next() {
		forward, err := scanPortForward(rows)
		if err != nil {
			return nil, err
		}
		forwards = append(forwards, forward)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate port forwards: %w", err)
	}
	return forwards, nil
}

func encodePortForward(forward domain.PortForward) ([]byte, error) {
	if err := forward.Validate(); err != nil {
		return nil, fmt.Errorf("validate port forward: %w", err)
	}
	definition, err := json.Marshal(forward)
	if err != nil {
		return nil, fmt.Errorf("encode port forward: %w", err)
	}
	if len(definition) > 65536 {
		return nil, fmt.Errorf("port forward definition exceeds 65536 bytes")
	}
	return definition, nil
}

func scanPortForward(scanner operationScanner) (StoredPortForward, error) {
	var stored StoredPortForward
	var definition string
	var createdAt, updatedAt int64
	if err := scanner.Scan(&definition, &stored.Revision, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StoredPortForward{}, ErrNotFound
		}
		return StoredPortForward{}, fmt.Errorf("scan port forward: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(definition))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored.Forward); err != nil {
		return StoredPortForward{}, fmt.Errorf("decode stored port forward: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return StoredPortForward{}, fmt.Errorf("decode stored port forward: trailing data")
	}
	if err := stored.Forward.Validate(); err != nil {
		return StoredPortForward{}, fmt.Errorf("validate stored port forward: %w", err)
	}
	stored.CreatedAt = time.Unix(createdAt, 0).UTC()
	stored.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return stored, nil
}
