package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/secrets"
)

type StoredOutbound struct {
	Outbound  domain.Outbound `json:"outbound"`
	Revision  int64           `json:"revision"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type NewOutbound struct {
	Outbound           domain.Outbound
	CredentialDocument []byte
}

func (store *Store) CreateOutbounds(ctx context.Context, inputs []NewOutbound, at time.Time) ([]StoredOutbound, error) {
	if len(inputs) == 0 || len(inputs) > 128 || at.IsZero() {
		return nil, fmt.Errorf("between 1 and 128 outbounds and a creation time are required")
	}
	type preparedOutbound struct {
		input      NewOutbound
		definition []byte
		envelope   secrets.Envelope
	}
	prepared := make([]preparedOutbound, 0, len(inputs))
	for _, input := range inputs {
		definition, envelope, err := store.prepareOutbound(input.Outbound, input.CredentialDocument)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, preparedOutbound{input: input, definition: definition, envelope: envelope})
	}
	at = at.UTC().Truncate(time.Second)
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin outbound batch creation: %w", err)
	}
	rollback := func(operationErr error) ([]StoredOutbound, error) {
		_ = transaction.Rollback()
		return nil, operationErr
	}
	stored := make([]StoredOutbound, 0, len(prepared))
	for _, item := range prepared {
		outbound := item.input.Outbound
		_, err := transaction.ExecContext(ctx, `
            INSERT INTO outbounds(id, name, adapter, protocol, server_host, server_port, definition, enabled, revision, created_at, updated_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
        `, string(outbound.ID), outbound.Name, string(outbound.Adapter), string(outbound.Type), outbound.Server.Host, outbound.Server.Port, string(item.definition), outbound.Enabled, at.Unix(), at.Unix())
		if err := mapWriteError("create outbound batch", err); err != nil {
			return rollback(err)
		}
		if _, err := transaction.ExecContext(ctx, `INSERT INTO outbound_credentials(outbound_id, nonce, ciphertext, updated_at) VALUES (?, ?, ?, ?)`, string(outbound.ID), item.envelope.Nonce, item.envelope.Ciphertext, at.Unix()); err != nil {
			return rollback(mapWriteError("create outbound batch credential", err))
		}
		stored = append(stored, StoredOutbound{Outbound: outbound, Revision: 1, CreatedAt: at, UpdatedAt: at})
	}
	if err := transaction.Commit(); err != nil {
		return nil, fmt.Errorf("commit outbound batch creation: %w", err)
	}
	return stored, nil
}

func (store *Store) CreateOutbound(ctx context.Context, outbound domain.Outbound, credentialDocument []byte, at time.Time) (StoredOutbound, error) {
	definition, envelope, err := store.prepareOutbound(outbound, credentialDocument)
	if err != nil {
		return StoredOutbound{}, err
	}
	if at.IsZero() {
		return StoredOutbound{}, fmt.Errorf("outbound creation time is required")
	}
	at = at.UTC().Truncate(time.Second)
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return StoredOutbound{}, fmt.Errorf("begin outbound creation: %w", err)
	}
	rollback := func(operationErr error) (StoredOutbound, error) {
		_ = transaction.Rollback()
		return StoredOutbound{}, operationErr
	}
	_, err = transaction.ExecContext(ctx, `
        INSERT INTO outbounds(id, name, adapter, protocol, server_host, server_port, definition, enabled, revision, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
    `, string(outbound.ID), outbound.Name, string(outbound.Adapter), string(outbound.Type), outbound.Server.Host, outbound.Server.Port, string(definition), outbound.Enabled, at.Unix(), at.Unix())
	if err := mapWriteError("create outbound", err); err != nil {
		return rollback(err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO outbound_credentials(outbound_id, nonce, ciphertext, updated_at) VALUES (?, ?, ?, ?)`, string(outbound.ID), envelope.Nonce, envelope.Ciphertext, at.Unix()); err != nil {
		return rollback(mapWriteError("create outbound credential", err))
	}
	if err := transaction.Commit(); err != nil {
		return StoredOutbound{}, fmt.Errorf("commit outbound creation: %w", err)
	}
	return StoredOutbound{Outbound: outbound, Revision: 1, CreatedAt: at, UpdatedAt: at}, nil
}

func (store *Store) UpdateOutbound(ctx context.Context, outbound domain.Outbound, expectedRevision int64, replacementCredential []byte, at time.Time) (StoredOutbound, error) {
	definition, err := encodeOutbound(outbound)
	if err != nil {
		return StoredOutbound{}, err
	}
	if store.secretProtector == nil {
		return StoredOutbound{}, fmt.Errorf("outbound secret protector is required")
	}
	if expectedRevision < 1 || at.IsZero() {
		return StoredOutbound{}, fmt.Errorf("expected revision and update time are required")
	}
	var replacement *secrets.Envelope
	if replacementCredential != nil {
		envelope, err := store.secretProtector.Seal(outboundSecretContext(outbound.ID), replacementCredential)
		if err != nil {
			return StoredOutbound{}, fmt.Errorf("protect outbound credential: %w", err)
		}
		replacement = &envelope
	}
	at = at.UTC().Truncate(time.Second)
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return StoredOutbound{}, fmt.Errorf("begin outbound update: %w", err)
	}
	rollback := func(operationErr error) (StoredOutbound, error) {
		_ = transaction.Rollback()
		return StoredOutbound{}, operationErr
	}
	if replacement == nil {
		var existingDefinition string
		if err := transaction.QueryRowContext(ctx, `SELECT definition FROM outbounds WHERE id = ? AND revision = ?`, string(outbound.ID), expectedRevision).Scan(&existingDefinition); errors.Is(err, sql.ErrNoRows) {
			return rollback(ErrConflict)
		} else if err != nil {
			return rollback(fmt.Errorf("read outbound before update: %w", err))
		}
		existing, err := decodeOutboundDefinition(existingDefinition)
		if err != nil {
			return rollback(err)
		}
		if !sameCredentialShape(existing, outbound) {
			return rollback(fmt.Errorf("outbound endpoint, adapter, protocol, capabilities, or secret metadata changed without replacement credential"))
		}
	}
	result, err := transaction.ExecContext(ctx, `
        UPDATE outbounds
        SET name = ?, adapter = ?, protocol = ?, server_host = ?, server_port = ?, definition = ?, enabled = ?, revision = revision + 1, updated_at = ?
        WHERE id = ? AND revision = ?
    `, outbound.Name, string(outbound.Adapter), string(outbound.Type), outbound.Server.Host, outbound.Server.Port, string(definition), outbound.Enabled, at.Unix(), string(outbound.ID), expectedRevision)
	if err := mapWriteError("update outbound", err); err != nil {
		return rollback(err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return rollback(fmt.Errorf("update outbound: read affected rows: %w", err))
	}
	if count != 1 {
		return rollback(ErrConflict)
	}
	if replacement != nil {
		if _, err := transaction.ExecContext(ctx, `UPDATE outbound_credentials SET nonce = ?, ciphertext = ?, updated_at = ? WHERE outbound_id = ?`, replacement.Nonce, replacement.Ciphertext, at.Unix(), string(outbound.ID)); err != nil {
			return rollback(mapWriteError("replace outbound credential", err))
		}
	}
	if err := transaction.Commit(); err != nil {
		return StoredOutbound{}, fmt.Errorf("commit outbound update: %w", err)
	}
	return store.Outbound(ctx, outbound.ID)
}

func (store *Store) DeleteOutbound(ctx context.Context, id domain.ID, expectedRevision int64) error {
	if err := id.Validate("outbound id"); err != nil {
		return err
	}
	if expectedRevision < 1 {
		return fmt.Errorf("expected revision is required")
	}
	result, err := store.database.ExecContext(ctx, `DELETE FROM outbounds WHERE id = ? AND revision = ?`, string(id), expectedRevision)
	if err := mapWriteError("delete outbound", err); err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete outbound: read affected rows: %w", err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (store *Store) CloneOutbound(ctx context.Context, sourceID domain.ID, expectedRevision int64, clone domain.Outbound, at time.Time) (StoredOutbound, error) {
	definition, err := encodeOutbound(clone)
	if err != nil {
		return StoredOutbound{}, err
	}
	if store.secretProtector == nil {
		return StoredOutbound{}, fmt.Errorf("outbound secret protector is required")
	}
	if err := sourceID.Validate("source outbound id"); err != nil {
		return StoredOutbound{}, err
	}
	if sourceID == clone.ID || expectedRevision < 1 || at.IsZero() {
		return StoredOutbound{}, fmt.Errorf("distinct clone ID, expected revision, and creation time are required")
	}
	at = at.UTC().Truncate(time.Second)
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return StoredOutbound{}, fmt.Errorf("begin outbound clone: %w", err)
	}
	rollback := func(operationErr error) (StoredOutbound, error) {
		_ = transaction.Rollback()
		return StoredOutbound{}, operationErr
	}
	var sourceDefinition string
	var nonce, ciphertext []byte
	err = transaction.QueryRowContext(ctx, `
        SELECT o.definition, c.nonce, c.ciphertext
        FROM outbounds o JOIN outbound_credentials c ON c.outbound_id = o.id
        WHERE o.id = ? AND o.revision = ?
    `, string(sourceID), expectedRevision).Scan(&sourceDefinition, &nonce, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return rollback(ErrConflict)
	}
	if err != nil {
		return rollback(fmt.Errorf("read source outbound for clone: %w", err))
	}
	source, err := decodeOutboundDefinition(sourceDefinition)
	if err != nil {
		return rollback(err)
	}
	if !sameCredentialShape(source, clone) {
		return rollback(fmt.Errorf("outbound clone must preserve endpoint, adapter, protocol, capabilities, and secret metadata"))
	}
	document, err := store.secretProtector.Open(outboundSecretContext(sourceID), secrets.Envelope{Nonce: nonce, Ciphertext: ciphertext})
	if err != nil {
		return rollback(fmt.Errorf("open source outbound credential: %w", err))
	}
	envelope, err := store.secretProtector.Seal(outboundSecretContext(clone.ID), document)
	if err != nil {
		return rollback(fmt.Errorf("protect cloned outbound credential: %w", err))
	}
	_, err = transaction.ExecContext(ctx, `
        INSERT INTO outbounds(id, name, adapter, protocol, server_host, server_port, definition, enabled, revision, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
    `, string(clone.ID), clone.Name, string(clone.Adapter), string(clone.Type), clone.Server.Host, clone.Server.Port, string(definition), clone.Enabled, at.Unix(), at.Unix())
	if err := mapWriteError("clone outbound", err); err != nil {
		return rollback(err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO outbound_credentials(outbound_id, nonce, ciphertext, updated_at) VALUES (?, ?, ?, ?)`, string(clone.ID), envelope.Nonce, envelope.Ciphertext, at.Unix()); err != nil {
		return rollback(mapWriteError("create cloned outbound credential", err))
	}
	if err := transaction.Commit(); err != nil {
		return StoredOutbound{}, fmt.Errorf("commit outbound clone: %w", err)
	}
	return StoredOutbound{Outbound: clone, Revision: 1, CreatedAt: at, UpdatedAt: at}, nil
}

func (store *Store) Outbound(ctx context.Context, id domain.ID) (StoredOutbound, error) {
	if err := id.Validate("outbound id"); err != nil {
		return StoredOutbound{}, err
	}
	return scanOutbound(store.database.QueryRowContext(ctx, `SELECT definition, revision, created_at, updated_at FROM outbounds WHERE id = ?`, string(id)))
}

func (store *Store) ListOutbounds(ctx context.Context, after domain.ID, limit int) ([]StoredOutbound, error) {
	if after != "" {
		if err := after.Validate("outbound cursor"); err != nil {
			return nil, err
		}
	}
	if limit < 1 || limit > 501 {
		return nil, fmt.Errorf("outbound list limit must be between 1 and 501")
	}
	rows, err := store.database.QueryContext(ctx, `SELECT definition, revision, created_at, updated_at FROM outbounds WHERE id > ? ORDER BY id LIMIT ?`, string(after), limit)
	if err != nil {
		return nil, fmt.Errorf("list outbounds: %w", err)
	}
	defer rows.Close()
	items := make([]StoredOutbound, 0)
	for rows.Next() {
		item, err := scanOutbound(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbounds: %w", err)
	}
	return items, nil
}

func (store *Store) ListEnabledOutbounds(ctx context.Context, adapter domain.OutboundAdapter, after domain.ID, limit int) ([]StoredOutbound, error) {
	if err := adapter.Validate(); err != nil {
		return nil, err
	}
	if after != "" {
		if err := after.Validate("outbound cursor"); err != nil {
			return nil, err
		}
	}
	if limit < 1 || limit > 501 {
		return nil, fmt.Errorf("outbound list limit must be between 1 and 501")
	}
	rows, err := store.database.QueryContext(ctx, `
        SELECT definition, revision, created_at, updated_at
        FROM outbounds
        WHERE enabled = 1 AND adapter = ? AND id > ?
        ORDER BY id
        LIMIT ?
    `, string(adapter), string(after), limit)
	if err != nil {
		return nil, fmt.Errorf("list enabled outbounds: %w", err)
	}
	defer rows.Close()
	items := make([]StoredOutbound, 0)
	for rows.Next() {
		item, err := scanOutbound(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled outbounds: %w", err)
	}
	return items, nil
}

func (store *Store) OutboundCredential(ctx context.Context, id domain.ID) ([]byte, error) {
	if store.secretProtector == nil {
		return nil, fmt.Errorf("outbound secret protector is required")
	}
	if err := id.Validate("outbound id"); err != nil {
		return nil, err
	}
	var nonce, ciphertext []byte
	err := store.database.QueryRowContext(ctx, `SELECT nonce, ciphertext FROM outbound_credentials WHERE outbound_id = ?`, string(id)).Scan(&nonce, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read outbound credential: %w", err)
	}
	document, err := store.secretProtector.Open(outboundSecretContext(id), secrets.Envelope{Nonce: nonce, Ciphertext: ciphertext})
	if err != nil {
		return nil, fmt.Errorf("open outbound credential: %w", err)
	}
	return document, nil
}

func (store *Store) prepareOutbound(outbound domain.Outbound, credentialDocument []byte) ([]byte, secrets.Envelope, error) {
	definition, err := encodeOutbound(outbound)
	if err != nil {
		return nil, secrets.Envelope{}, err
	}
	if store.secretProtector == nil {
		return nil, secrets.Envelope{}, fmt.Errorf("outbound secret protector is required")
	}
	envelope, err := store.secretProtector.Seal(outboundSecretContext(outbound.ID), credentialDocument)
	if err != nil {
		return nil, secrets.Envelope{}, fmt.Errorf("protect outbound credential: %w", err)
	}
	return definition, envelope, nil
}

func encodeOutbound(outbound domain.Outbound) ([]byte, error) {
	if err := outbound.Validate(); err != nil {
		return nil, fmt.Errorf("validate outbound: %w", err)
	}
	definition, err := json.Marshal(outbound)
	if err != nil {
		return nil, fmt.Errorf("encode outbound: %w", err)
	}
	if len(definition) > 65536 {
		return nil, fmt.Errorf("outbound definition exceeds 65536 bytes")
	}
	return definition, nil
}

func scanOutbound(scanner operationScanner) (StoredOutbound, error) {
	var stored StoredOutbound
	var definition string
	var createdAt, updatedAt int64
	if err := scanner.Scan(&definition, &stored.Revision, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StoredOutbound{}, ErrNotFound
		}
		return StoredOutbound{}, fmt.Errorf("scan outbound: %w", err)
	}
	outbound, err := decodeOutboundDefinition(definition)
	if err != nil {
		return StoredOutbound{}, err
	}
	stored.Outbound = outbound
	stored.CreatedAt = time.Unix(createdAt, 0).UTC()
	stored.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return stored, nil
}

func decodeOutboundDefinition(definition string) (domain.Outbound, error) {
	var outbound domain.Outbound
	decoder := json.NewDecoder(strings.NewReader(definition))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&outbound); err != nil {
		return domain.Outbound{}, fmt.Errorf("decode stored outbound: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return domain.Outbound{}, fmt.Errorf("decode stored outbound: trailing data")
	}
	if err := outbound.Validate(); err != nil {
		return domain.Outbound{}, fmt.Errorf("validate stored outbound: %w", err)
	}
	return outbound, nil
}

func sameCredentialShape(left, right domain.Outbound) bool {
	return left.Adapter == right.Adapter && left.Type == right.Type && left.Server == right.Server && left.Capabilities == right.Capabilities && slices.Equal(left.SecretMetadata, right.SecretMetadata)
}

func outboundSecretContext(id domain.ID) string {
	return "outbound:" + string(id)
}
