package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func (store *Store) CreateOperation(ctx context.Context, operation domain.Transaction) error {
	if err := operation.Validate(); err != nil {
		return fmt.Errorf("validate operation journal entry: %w", err)
	}
	_, err := store.database.ExecContext(ctx, `
        INSERT INTO operation_journal(
            id, operation, requested_change, previous_snapshot, candidate_config,
            state, created_at, updated_at, failure_detail
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)
    `,
		string(operation.ID), operation.Operation, []byte(operation.RequestedChange), []byte(operation.PreviousSnapshot),
		[]byte(operation.CandidateConfig), string(operation.State), operation.CreatedAt.UTC().Unix(), operation.UpdatedAt.UTC().Unix(),
	)
	return mapWriteError("create operation journal entry", err)
}

func (store *Store) TransitionOperation(ctx context.Context, id domain.ID, from, to domain.TransactionState, at time.Time, failureDetail string) error {
	if err := id.Validate("transaction id"); err != nil {
		return err
	}
	if !from.CanTransitionTo(to) {
		return fmt.Errorf("invalid transaction transition %s to %s", from, to)
	}
	if at.IsZero() {
		return fmt.Errorf("transaction transition time is required")
	}
	if len(failureDetail) > 512 {
		return fmt.Errorf("failure detail must not exceed 512 bytes")
	}
	var detail any
	if failureDetail != "" {
		detail = failureDetail
	}
	result, err := store.database.ExecContext(ctx, `
        UPDATE operation_journal
        SET state = ?, updated_at = ?, failure_detail = ?
        WHERE id = ? AND state = ?
    `, string(to), at.UTC().Unix(), detail, string(id), string(from))
	if err != nil {
		return fmt.Errorf("transition operation journal entry: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("transition operation journal entry: read affected rows: %w", err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (store *Store) Operation(ctx context.Context, id domain.ID) (domain.Transaction, error) {
	if err := id.Validate("transaction id"); err != nil {
		return domain.Transaction{}, err
	}
	return scanOperation(store.database.QueryRowContext(ctx, `
        SELECT id, operation, requested_change, previous_snapshot, candidate_config,
               state, created_at, updated_at, failure_detail
        FROM operation_journal
        WHERE id = ?
    `, string(id)))
}

func (store *Store) UnfinishedOperations(ctx context.Context, limit int) ([]domain.Transaction, error) {
	if limit < 1 || limit > 1_000 {
		return nil, fmt.Errorf("operation journal limit must be between 1 and 1000")
	}
	rows, err := store.database.QueryContext(ctx, `
        SELECT id, operation, requested_change, previous_snapshot, candidate_config,
               state, created_at, updated_at, failure_detail
        FROM operation_journal
        WHERE state NOT IN ('COMMITTED', 'ROLLED_BACK', 'FAILED')
        ORDER BY updated_at
        LIMIT ?
    `, limit)
	if err != nil {
		return nil, fmt.Errorf("read unfinished operation journal: %w", err)
	}
	defer rows.Close()
	operations := make([]domain.Transaction, 0)
	for rows.Next() {
		operation, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unfinished operation journal: %w", err)
	}
	return operations, nil
}

func (store *Store) CommittedOperations(ctx context.Context, limit int) ([]domain.Transaction, error) {
	if limit < 1 || limit > 1_000 {
		return nil, fmt.Errorf("committed operation journal limit must be between 1 and 1000")
	}
	rows, err := store.database.QueryContext(ctx, `
        SELECT id, operation, requested_change, previous_snapshot, candidate_config,
               state, created_at, updated_at, failure_detail
        FROM operation_journal
        WHERE state = 'COMMITTED'
        ORDER BY updated_at DESC, created_at DESC, rowid DESC
        LIMIT ?
    `, limit)
	if err != nil {
		return nil, fmt.Errorf("read committed operation journal: %w", err)
	}
	defer rows.Close()
	operations := make([]domain.Transaction, 0)
	for rows.Next() {
		operation, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate committed operation journal: %w", err)
	}
	return operations, nil
}

func (store *Store) DeleteFinishedOperationsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit < 1 || limit > 10_000 {
		return 0, fmt.Errorf("operation journal cleanup limit must be between 1 and 10000")
	}
	result, err := store.database.ExecContext(ctx, `
        DELETE FROM operation_journal
        WHERE id IN (
            SELECT id
            FROM operation_journal
            WHERE state IN ('COMMITTED', 'ROLLED_BACK', 'FAILED') AND updated_at < ?
            ORDER BY updated_at
            LIMIT ?
        )
    `, before.UTC().Unix(), limit)
	if err != nil {
		return 0, fmt.Errorf("delete finished operation journal entries: %w", err)
	}
	return result.RowsAffected()
}

type operationScanner interface {
	Scan(...any) error
}

func scanOperation(scanner operationScanner) (domain.Transaction, error) {
	var operation domain.Transaction
	var id, state string
	var requested, snapshot, candidate []byte
	var createdAt, updatedAt int64
	var failure sql.NullString
	if err := scanner.Scan(&id, &operation.Operation, &requested, &snapshot, &candidate, &state, &createdAt, &updatedAt, &failure); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Transaction{}, ErrNotFound
		}
		return domain.Transaction{}, fmt.Errorf("scan operation journal entry: %w", err)
	}
	operation.ID = domain.ID(id)
	operation.State = domain.TransactionState(state)
	operation.RequestedChange = string(requested)
	operation.PreviousSnapshot = string(snapshot)
	operation.CandidateConfig = string(candidate)
	operation.CreatedAt = time.Unix(createdAt, 0).UTC()
	operation.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if failure.Valid {
		operation.FailureDetail = failure.String
	}
	if err := operation.Validate(); err != nil {
		return domain.Transaction{}, fmt.Errorf("validate stored operation journal entry: %w", err)
	}
	return operation, nil
}
