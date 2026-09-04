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

type StoredHAProxyBackend struct {
	Backend   domain.HAProxyBackend
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

type StoredHAProxyFrontend struct {
	Frontend  domain.HAProxyFrontend
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (store *Store) CreateHAProxyBackend(ctx context.Context, backend domain.HAProxyBackend, at time.Time) (StoredHAProxyBackend, error) {
	definition, err := encodeHAProxyBackend(backend)
	if err != nil {
		return StoredHAProxyBackend{}, err
	}
	if at.IsZero() {
		return StoredHAProxyBackend{}, fmt.Errorf("HAProxy backend creation time is required")
	}
	at = at.UTC().Truncate(time.Second)
	_, err = store.database.ExecContext(ctx, `
        INSERT INTO haproxy_backends(id, name, definition, enabled, revision, created_at, updated_at)
        VALUES (?, ?, ?, ?, 1, ?, ?)
    `, string(backend.ID), backend.Name, string(definition), backend.Enabled, at.Unix(), at.Unix())
	if err := mapWriteError("create HAProxy backend", err); err != nil {
		return StoredHAProxyBackend{}, err
	}
	return StoredHAProxyBackend{Backend: backend, Revision: 1, CreatedAt: at, UpdatedAt: at}, nil
}

func (store *Store) UpdateHAProxyBackend(ctx context.Context, backend domain.HAProxyBackend, expectedRevision int64, at time.Time) (StoredHAProxyBackend, error) {
	definition, err := encodeHAProxyBackend(backend)
	if err != nil {
		return StoredHAProxyBackend{}, err
	}
	if expectedRevision < 1 || at.IsZero() {
		return StoredHAProxyBackend{}, fmt.Errorf("expected revision and update time are required")
	}
	at = at.UTC().Truncate(time.Second)
	result, err := store.database.ExecContext(ctx, `
        UPDATE haproxy_backends SET name = ?, definition = ?, enabled = ?, revision = revision + 1, updated_at = ?
        WHERE id = ? AND revision = ?
    `, backend.Name, string(definition), backend.Enabled, at.Unix(), string(backend.ID), expectedRevision)
	if err := mapWriteError("update HAProxy backend", err); err != nil {
		return StoredHAProxyBackend{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return StoredHAProxyBackend{}, fmt.Errorf("update HAProxy backend: read affected rows: %w", err)
	}
	if count != 1 {
		return StoredHAProxyBackend{}, ErrConflict
	}
	return store.HAProxyBackend(ctx, backend.ID)
}

func (store *Store) DeleteHAProxyBackend(ctx context.Context, id domain.ID, expectedRevision int64) error {
	if err := id.Validate("HAProxy backend id"); err != nil {
		return err
	}
	if expectedRevision < 1 {
		return fmt.Errorf("expected revision is required")
	}
	result, err := store.database.ExecContext(ctx, `DELETE FROM haproxy_backends WHERE id = ? AND revision = ?`, string(id), expectedRevision)
	if err := mapWriteError("delete HAProxy backend", err); err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete HAProxy backend: read affected rows: %w", err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (store *Store) HAProxyBackend(ctx context.Context, id domain.ID) (StoredHAProxyBackend, error) {
	if err := id.Validate("HAProxy backend id"); err != nil {
		return StoredHAProxyBackend{}, err
	}
	return scanHAProxyBackend(store.database.QueryRowContext(ctx, `SELECT definition, revision, created_at, updated_at FROM haproxy_backends WHERE id = ?`, string(id)))
}

func (store *Store) ListHAProxyBackends(ctx context.Context, after domain.ID, limit int) ([]StoredHAProxyBackend, error) {
	if err := validateHAProxyList(after, limit, "backend"); err != nil {
		return nil, err
	}
	rows, err := store.database.QueryContext(ctx, `SELECT definition, revision, created_at, updated_at FROM haproxy_backends WHERE id > ? ORDER BY id LIMIT ?`, string(after), limit)
	if err != nil {
		return nil, fmt.Errorf("list HAProxy backends: %w", err)
	}
	defer rows.Close()
	items := make([]StoredHAProxyBackend, 0)
	for rows.Next() {
		item, err := scanHAProxyBackend(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate HAProxy backends: %w", err)
	}
	return items, nil
}

func (store *Store) CreateHAProxyFrontend(ctx context.Context, frontend domain.HAProxyFrontend, at time.Time) (StoredHAProxyFrontend, error) {
	definition, err := encodeHAProxyFrontend(frontend)
	if err != nil {
		return StoredHAProxyFrontend{}, err
	}
	if at.IsZero() {
		return StoredHAProxyFrontend{}, fmt.Errorf("HAProxy frontend creation time is required")
	}
	at = at.UTC().Truncate(time.Second)
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return StoredHAProxyFrontend{}, fmt.Errorf("begin HAProxy frontend creation: %w", err)
	}
	if _, err = transaction.ExecContext(ctx, `INSERT INTO haproxy_frontends(id, name, definition, enabled, revision, created_at, updated_at) VALUES (?, ?, ?, ?, 1, ?, ?)`, string(frontend.ID), frontend.Name, string(definition), frontend.Enabled, at.Unix(), at.Unix()); err != nil {
		_ = transaction.Rollback()
		return StoredHAProxyFrontend{}, mapWriteError("create HAProxy frontend", err)
	}
	if err := replaceHAProxyFrontendBackends(ctx, transaction, frontend); err != nil {
		_ = transaction.Rollback()
		return StoredHAProxyFrontend{}, err
	}
	if err := transaction.Commit(); err != nil {
		return StoredHAProxyFrontend{}, fmt.Errorf("commit HAProxy frontend creation: %w", err)
	}
	return StoredHAProxyFrontend{Frontend: frontend, Revision: 1, CreatedAt: at, UpdatedAt: at}, nil
}

func (store *Store) UpdateHAProxyFrontend(ctx context.Context, frontend domain.HAProxyFrontend, expectedRevision int64, at time.Time) (StoredHAProxyFrontend, error) {
	definition, err := encodeHAProxyFrontend(frontend)
	if err != nil {
		return StoredHAProxyFrontend{}, err
	}
	if expectedRevision < 1 || at.IsZero() {
		return StoredHAProxyFrontend{}, fmt.Errorf("expected revision and update time are required")
	}
	at = at.UTC().Truncate(time.Second)
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return StoredHAProxyFrontend{}, fmt.Errorf("begin HAProxy frontend update: %w", err)
	}
	result, err := transaction.ExecContext(ctx, `UPDATE haproxy_frontends SET name = ?, definition = ?, enabled = ?, revision = revision + 1, updated_at = ? WHERE id = ? AND revision = ?`, frontend.Name, string(definition), frontend.Enabled, at.Unix(), string(frontend.ID), expectedRevision)
	if err != nil {
		_ = transaction.Rollback()
		return StoredHAProxyFrontend{}, mapWriteError("update HAProxy frontend", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		_ = transaction.Rollback()
		return StoredHAProxyFrontend{}, fmt.Errorf("update HAProxy frontend: read affected rows: %w", err)
	}
	if count != 1 {
		_ = transaction.Rollback()
		return StoredHAProxyFrontend{}, ErrConflict
	}
	if err := replaceHAProxyFrontendBackends(ctx, transaction, frontend); err != nil {
		_ = transaction.Rollback()
		return StoredHAProxyFrontend{}, err
	}
	if err := transaction.Commit(); err != nil {
		return StoredHAProxyFrontend{}, fmt.Errorf("commit HAProxy frontend update: %w", err)
	}
	return store.HAProxyFrontend(ctx, frontend.ID)
}

func (store *Store) DeleteHAProxyFrontend(ctx context.Context, id domain.ID, expectedRevision int64) error {
	if err := id.Validate("HAProxy frontend id"); err != nil {
		return err
	}
	if expectedRevision < 1 {
		return fmt.Errorf("expected revision is required")
	}
	result, err := store.database.ExecContext(ctx, `DELETE FROM haproxy_frontends WHERE id = ? AND revision = ?`, string(id), expectedRevision)
	if err != nil {
		return fmt.Errorf("delete HAProxy frontend: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete HAProxy frontend: read affected rows: %w", err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (store *Store) HAProxyFrontend(ctx context.Context, id domain.ID) (StoredHAProxyFrontend, error) {
	if err := id.Validate("HAProxy frontend id"); err != nil {
		return StoredHAProxyFrontend{}, err
	}
	return scanHAProxyFrontend(store.database.QueryRowContext(ctx, `SELECT definition, revision, created_at, updated_at FROM haproxy_frontends WHERE id = ?`, string(id)))
}

func (store *Store) ListHAProxyFrontends(ctx context.Context, after domain.ID, limit int) ([]StoredHAProxyFrontend, error) {
	if err := validateHAProxyList(after, limit, "frontend"); err != nil {
		return nil, err
	}
	rows, err := store.database.QueryContext(ctx, `SELECT definition, revision, created_at, updated_at FROM haproxy_frontends WHERE id > ? ORDER BY id LIMIT ?`, string(after), limit)
	if err != nil {
		return nil, fmt.Errorf("list HAProxy frontends: %w", err)
	}
	defer rows.Close()
	items := make([]StoredHAProxyFrontend, 0)
	for rows.Next() {
		item, err := scanHAProxyFrontend(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate HAProxy frontends: %w", err)
	}
	return items, nil
}

func replaceHAProxyFrontendBackends(ctx context.Context, transaction *sql.Tx, frontend domain.HAProxyFrontend) error {
	if _, err := transaction.ExecContext(ctx, `DELETE FROM haproxy_frontend_backends WHERE frontend_id = ?`, string(frontend.ID)); err != nil {
		return fmt.Errorf("clear HAProxy frontend backends: %w", err)
	}
	for position, backendID := range frontend.BackendIDs {
		if _, err := transaction.ExecContext(ctx, `INSERT INTO haproxy_frontend_backends(frontend_id, backend_id, position) VALUES (?, ?, ?)`, string(frontend.ID), string(backendID), position); err != nil {
			return mapWriteError("link HAProxy frontend backend", err)
		}
	}
	return nil
}

func encodeHAProxyBackend(backend domain.HAProxyBackend) ([]byte, error) {
	if err := backend.Validate(); err != nil {
		return nil, fmt.Errorf("validate HAProxy backend: %w", err)
	}
	return encodeHAProxyDocument(backend, "backend")
}

func encodeHAProxyFrontend(frontend domain.HAProxyFrontend) ([]byte, error) {
	if err := frontend.Validate(); err != nil {
		return nil, fmt.Errorf("validate HAProxy frontend: %w", err)
	}
	return encodeHAProxyDocument(frontend, "frontend")
}

func encodeHAProxyDocument(value any, kind string) ([]byte, error) {
	definition, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode HAProxy %s: %w", kind, err)
	}
	if len(definition) > 65536 {
		return nil, fmt.Errorf("HAProxy %s definition exceeds 65536 bytes", kind)
	}
	return definition, nil
}

func scanHAProxyBackend(scanner operationScanner) (StoredHAProxyBackend, error) {
	var stored StoredHAProxyBackend
	definition, revision, createdAt, updatedAt, err := scanHAProxyDocument(scanner, &stored.Backend, "backend")
	_ = definition
	if err != nil {
		return StoredHAProxyBackend{}, err
	}
	stored.Revision, stored.CreatedAt, stored.UpdatedAt = revision, createdAt, updatedAt
	if err := stored.Backend.Validate(); err != nil {
		return StoredHAProxyBackend{}, fmt.Errorf("validate stored HAProxy backend: %w", err)
	}
	return stored, nil
}

func scanHAProxyFrontend(scanner operationScanner) (StoredHAProxyFrontend, error) {
	var stored StoredHAProxyFrontend
	_, revision, createdAt, updatedAt, err := scanHAProxyDocument(scanner, &stored.Frontend, "frontend")
	if err != nil {
		return StoredHAProxyFrontend{}, err
	}
	stored.Revision, stored.CreatedAt, stored.UpdatedAt = revision, createdAt, updatedAt
	if err := stored.Frontend.Validate(); err != nil {
		return StoredHAProxyFrontend{}, fmt.Errorf("validate stored HAProxy frontend: %w", err)
	}
	return stored, nil
}

func scanHAProxyDocument(scanner operationScanner, output any, kind string) (string, int64, time.Time, time.Time, error) {
	var definition string
	var revision, createdAt, updatedAt int64
	if err := scanner.Scan(&definition, &revision, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", 0, time.Time{}, time.Time{}, ErrNotFound
		}
		return "", 0, time.Time{}, time.Time{}, fmt.Errorf("scan HAProxy %s: %w", kind, err)
	}
	decoder := json.NewDecoder(strings.NewReader(definition))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return "", 0, time.Time{}, time.Time{}, fmt.Errorf("decode stored HAProxy %s: %w", kind, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", 0, time.Time{}, time.Time{}, fmt.Errorf("decode stored HAProxy %s: trailing data", kind)
	}
	return definition, revision, time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC(), nil
}

func validateHAProxyList(after domain.ID, limit int, kind string) error {
	if after != "" {
		if err := after.Validate("HAProxy " + kind + " cursor"); err != nil {
			return err
		}
	}
	if limit < 1 || limit > 501 {
		return fmt.Errorf("HAProxy %s list limit must be between 1 and 501", kind)
	}
	return nil
}
