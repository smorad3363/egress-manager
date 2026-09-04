package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mattn/go-sqlite3"
)

var (
	ErrNotFound = errors.New("record not found")
	ErrConflict = errors.New("record conflicts with existing data")
)

type Store struct {
	database *sql.DB
}

func NewStore(database *sql.DB) *Store {
	return &Store{database: database}
}

type Admin struct {
	ID           string
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (store *Store) CreateAdmin(ctx context.Context, admin Admin) error {
	_, err := store.database.ExecContext(ctx, `
        INSERT INTO admins(id, username, password_hash, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?)
    `, admin.ID, admin.Username, admin.PasswordHash, admin.CreatedAt.UTC().Unix(), admin.UpdatedAt.UTC().Unix())
	return mapWriteError("create admin", err)
}

func (store *Store) AdminByUsername(ctx context.Context, username string) (Admin, error) {
	var admin Admin
	var createdAt, updatedAt int64
	err := store.database.QueryRowContext(ctx, `
        SELECT id, username, password_hash, created_at, updated_at
        FROM admins
        WHERE username = ? COLLATE NOCASE
    `, username).Scan(&admin.ID, &admin.Username, &admin.PasswordHash, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Admin{}, ErrNotFound
	}
	if err != nil {
		return Admin{}, fmt.Errorf("read admin: %w", err)
	}
	admin.CreatedAt = time.Unix(createdAt, 0).UTC()
	admin.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return admin, nil
}

type Session struct {
	TokenHash  [32]byte
	AdminID    string
	CSRFHash   [32]byte
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	RevokedAt  *time.Time
}

func (store *Store) CreateSession(ctx context.Context, session Session) error {
	_, err := store.database.ExecContext(ctx, `
        INSERT INTO sessions(token_hash, admin_id, csrf_hash, created_at, expires_at, last_seen_at, revoked_at)
        VALUES (?, ?, ?, ?, ?, ?, NULL)
    `, session.TokenHash[:], session.AdminID, session.CSRFHash[:], session.CreatedAt.UTC().Unix(), session.ExpiresAt.UTC().Unix(), session.LastSeenAt.UTC().Unix())
	return mapWriteError("create session", err)
}

func (store *Store) ActiveSession(ctx context.Context, tokenHash [32]byte, now time.Time) (Session, error) {
	var session Session
	var token, csrf []byte
	var createdAt, expiresAt, lastSeenAt int64
	err := store.database.QueryRowContext(ctx, `
        SELECT token_hash, admin_id, csrf_hash, created_at, expires_at, last_seen_at
        FROM sessions
        WHERE token_hash = ? AND revoked_at IS NULL AND expires_at > ?
    `, tokenHash[:], now.UTC().Unix()).Scan(&token, &session.AdminID, &csrf, &createdAt, &expiresAt, &lastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("read active session: %w", err)
	}
	if len(token) != len(session.TokenHash) || len(csrf) != len(session.CSRFHash) {
		return Session{}, fmt.Errorf("read active session: invalid hash length")
	}
	copy(session.TokenHash[:], token)
	copy(session.CSRFHash[:], csrf)
	session.CreatedAt = time.Unix(createdAt, 0).UTC()
	session.ExpiresAt = time.Unix(expiresAt, 0).UTC()
	session.LastSeenAt = time.Unix(lastSeenAt, 0).UTC()
	return session, nil
}

func (store *Store) RevokeSession(ctx context.Context, tokenHash [32]byte, now time.Time) error {
	result, err := store.database.ExecContext(ctx, `
        UPDATE sessions SET revoked_at = ?
        WHERE token_hash = ? AND revoked_at IS NULL
    `, now.UTC().Unix(), tokenHash[:])
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return requireAffected("revoke session", result)
}

func (store *Store) DeleteExpiredSessions(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit < 1 || limit > 10_000 {
		return 0, fmt.Errorf("session cleanup limit must be between 1 and 10000")
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin session cleanup: %w", err)
	}
	rollback := func(operationErr error) (int64, error) {
		_ = transaction.Rollback()
		return 0, operationErr
	}

	revokedResult, err := transaction.ExecContext(ctx, `
        DELETE FROM sessions
        WHERE token_hash IN (
            SELECT token_hash
            FROM sessions
            WHERE revoked_at IS NOT NULL
            ORDER BY revoked_at
            LIMIT ?
        )
    `, limit)
	if err != nil {
		return rollback(fmt.Errorf("delete revoked sessions: %w", err))
	}
	revoked, err := revokedResult.RowsAffected()
	if err != nil {
		return rollback(fmt.Errorf("read revoked session count: %w", err))
	}

	expired := int64(0)
	remaining := int64(limit) - revoked
	if remaining > 0 {
		expiredResult, err := transaction.ExecContext(ctx, `
            DELETE FROM sessions
            WHERE token_hash IN (
                SELECT token_hash
                FROM sessions
                WHERE revoked_at IS NULL AND expires_at <= ?
                ORDER BY expires_at
                LIMIT ?
            )
        `, now.UTC().Unix(), remaining)
		if err != nil {
			return rollback(fmt.Errorf("delete expired sessions: %w", err))
		}
		expired, err = expiredResult.RowsAffected()
		if err != nil {
			return rollback(fmt.Errorf("read expired session count: %w", err))
		}
	}
	if err := transaction.Commit(); err != nil {
		return 0, fmt.Errorf("commit session cleanup: %w", err)
	}
	return revoked + expired, nil
}

func (store *Store) RecordLoginAttempt(ctx context.Context, bucketHash [32]byte, succeeded bool, at time.Time) error {
	_, err := store.database.ExecContext(ctx, `
        INSERT INTO login_attempts(bucket_hash, succeeded, attempted_at)
        VALUES (?, ?, ?)
    `, bucketHash[:], succeeded, at.UTC().Unix())
	return mapWriteError("record login attempt", err)
}

func (store *Store) CountRecentFailedAttempts(ctx context.Context, bucketHash [32]byte, since time.Time) (int, error) {
	var count int
	err := store.database.QueryRowContext(ctx, `
        SELECT COUNT(id)
        FROM login_attempts
        WHERE bucket_hash = ? AND succeeded = 0 AND attempted_at >= ?
    `, bucketHash[:], since.UTC().Unix()).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count recent failed login attempts: %w", err)
	}
	return count, nil
}

func (store *Store) DeleteLoginAttemptsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit < 1 || limit > 100_000 {
		return 0, fmt.Errorf("login attempt cleanup limit must be between 1 and 100000")
	}
	result, err := store.database.ExecContext(ctx, `
        DELETE FROM login_attempts
        WHERE id IN (
            SELECT id
            FROM login_attempts
            WHERE attempted_at < ?
            ORDER BY attempted_at
            LIMIT ?
        )
    `, before.UTC().Unix(), limit)
	if err != nil {
		return 0, fmt.Errorf("delete old login attempts: %w", err)
	}
	return result.RowsAffected()
}

func (store *Store) PutSetting(ctx context.Context, key, value string, at time.Time) error {
	_, err := store.database.ExecContext(ctx, `
        INSERT INTO settings(key, value, updated_at)
        VALUES (?, ?, ?)
        ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
    `, key, value, at.UTC().Unix())
	return mapWriteError("write setting", err)
}

func (store *Store) Setting(ctx context.Context, key string) (string, error) {
	var value string
	err := store.database.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read setting: %w", err)
	}
	return value, nil
}

func mapWriteError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var sqliteError sqlite3.Error
	if errors.As(err, &sqliteError) && sqliteError.Code == sqlite3.ErrConstraint {
		return fmt.Errorf("%s: %w", operation, ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func requireAffected(operation string, result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: read affected rows: %w", operation, err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
