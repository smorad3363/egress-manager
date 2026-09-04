package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
)

type StoredXrayBinding struct {
	Binding   domain.XrayBinding `json:"binding"`
	Revision  int64              `json:"revision"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}

func (store *Store) CreateXrayBinding(ctx context.Context, binding domain.XrayBinding, at time.Time) (StoredXrayBinding, error) {
	if err := binding.Validate(); err != nil {
		return StoredXrayBinding{}, fmt.Errorf("validate Xray binding: %w", err)
	}
	if at.IsZero() {
		return StoredXrayBinding{}, fmt.Errorf("Xray binding creation time is required")
	}
	at = at.UTC().Truncate(time.Second)
	_, err := store.database.ExecContext(ctx, `
        INSERT INTO xray_bindings(id, inbound_tag, outbound_tag, enabled, revision, created_at, updated_at)
        VALUES (?, ?, ?, ?, 1, ?, ?)
    `, string(binding.ID), binding.InboundTag, binding.OutboundTag, binding.Enabled, at.Unix(), at.Unix())
	if err := mapWriteError("create Xray binding", err); err != nil {
		return StoredXrayBinding{}, err
	}
	return StoredXrayBinding{Binding: binding, Revision: 1, CreatedAt: at, UpdatedAt: at}, nil
}

func (store *Store) UpdateXrayBinding(ctx context.Context, binding domain.XrayBinding, expectedRevision int64, at time.Time) (StoredXrayBinding, error) {
	if err := binding.Validate(); err != nil {
		return StoredXrayBinding{}, fmt.Errorf("validate Xray binding: %w", err)
	}
	if expectedRevision < 1 || at.IsZero() {
		return StoredXrayBinding{}, fmt.Errorf("expected revision and update time are required")
	}
	at = at.UTC().Truncate(time.Second)
	result, err := store.database.ExecContext(ctx, `
        UPDATE xray_bindings
        SET inbound_tag = ?, outbound_tag = ?, enabled = ?, revision = revision + 1, updated_at = ?
        WHERE id = ? AND revision = ?
    `, binding.InboundTag, binding.OutboundTag, binding.Enabled, at.Unix(), string(binding.ID), expectedRevision)
	if err := mapWriteError("update Xray binding", err); err != nil {
		return StoredXrayBinding{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return StoredXrayBinding{}, fmt.Errorf("update Xray binding: read affected rows: %w", err)
	}
	if count != 1 {
		return StoredXrayBinding{}, ErrConflict
	}
	return store.XrayBinding(ctx, binding.ID)
}

func (store *Store) DeleteXrayBinding(ctx context.Context, id domain.ID, expectedRevision int64) error {
	if err := id.Validate("Xray binding id"); err != nil {
		return err
	}
	if expectedRevision < 1 {
		return fmt.Errorf("expected revision is required")
	}
	result, err := store.database.ExecContext(ctx, `DELETE FROM xray_bindings WHERE id = ? AND revision = ?`, string(id), expectedRevision)
	if err := mapWriteError("delete Xray binding", err); err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete Xray binding: read affected rows: %w", err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (store *Store) XrayBinding(ctx context.Context, id domain.ID) (StoredXrayBinding, error) {
	if err := id.Validate("Xray binding id"); err != nil {
		return StoredXrayBinding{}, err
	}
	return scanXrayBinding(store.database.QueryRowContext(ctx, `
        SELECT id, inbound_tag, outbound_tag, enabled, revision, created_at, updated_at
        FROM xray_bindings WHERE id = ?
    `, string(id)))
}

func (store *Store) ListXrayBindings(ctx context.Context, after domain.ID, limit int) ([]StoredXrayBinding, error) {
	if after != "" {
		if err := after.Validate("Xray binding cursor"); err != nil {
			return nil, err
		}
	}
	if limit < 1 || limit > MaximumBindingsPage+1 {
		return nil, fmt.Errorf("Xray binding list limit must be between 1 and %d", MaximumBindingsPage+1)
	}
	rows, err := store.database.QueryContext(ctx, `
        SELECT id, inbound_tag, outbound_tag, enabled, revision, created_at, updated_at
        FROM xray_bindings WHERE id > ? ORDER BY id LIMIT ?
    `, string(after), limit)
	if err != nil {
		return nil, fmt.Errorf("list Xray bindings: %w", err)
	}
	defer rows.Close()
	items := []StoredXrayBinding{}
	for rows.Next() {
		item, err := scanXrayBinding(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Xray bindings: %w", err)
	}
	return items, nil
}

const MaximumBindingsPage = 500

func scanXrayBinding(scanner operationScanner) (StoredXrayBinding, error) {
	var stored StoredXrayBinding
	var id string
	var createdAt, updatedAt int64
	if err := scanner.Scan(&id, &stored.Binding.InboundTag, &stored.Binding.OutboundTag, &stored.Binding.Enabled, &stored.Revision, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StoredXrayBinding{}, ErrNotFound
		}
		return StoredXrayBinding{}, fmt.Errorf("scan Xray binding: %w", err)
	}
	stored.Binding.ID = domain.ID(id)
	if err := stored.Binding.Validate(); err != nil {
		return StoredXrayBinding{}, fmt.Errorf("validate stored Xray binding: %w", err)
	}
	stored.CreatedAt = time.Unix(createdAt, 0).UTC()
	stored.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return stored, nil
}
