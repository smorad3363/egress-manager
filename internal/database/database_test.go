package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "egress-manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return database
}

func TestMigrationsAreIdempotent(t *testing.T) {
	t.Parallel()

	database := openTestDatabase(t)
	if err := Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(version) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migration count = %d, want 1", count)
	}
}

func TestStoreAdminSessionAndThrottleQueries(t *testing.T) {
	t.Parallel()

	database := openTestDatabase(t)
	store := NewStore(database)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	admin := Admin{
		ID:           "admin-primary",
		Username:     "operator",
		PasswordHash: "$argon2id$fixture",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := store.CreateAdmin(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAdmin(ctx, admin); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate CreateAdmin() error = %v, want ErrConflict", err)
	}
	loaded, err := store.AdminByUsername(ctx, "OPERATOR")
	if err != nil {
		t.Fatal(err)
	}
	if loaded != admin {
		t.Fatalf("AdminByUsername() = %#v, want %#v", loaded, admin)
	}

	tokenHash := sha256.Sum256([]byte("token"))
	csrfHash := sha256.Sum256([]byte("csrf"))
	session := Session{
		TokenHash:  tokenHash,
		AdminID:    admin.ID,
		CSRFHash:   csrfHash,
		CreatedAt:  now,
		ExpiresAt:  now.Add(time.Hour),
		LastSeenAt: now,
	}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	active, err := store.ActiveSession(ctx, tokenHash, now)
	if err != nil {
		t.Fatal(err)
	}
	if active.AdminID != admin.ID || active.TokenHash != tokenHash || active.CSRFHash != csrfHash {
		t.Fatalf("ActiveSession() = %#v", active)
	}
	if err := store.RevokeSession(ctx, tokenHash, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActiveSession(ctx, tokenHash, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked ActiveSession() error = %v, want ErrNotFound", err)
	}

	bucket := sha256.Sum256([]byte("operator|192.0.2.10"))
	for index := 0; index < 3; index++ {
		if err := store.RecordLoginAttempt(ctx, bucket, false, now.Add(time.Duration(index)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	count, err := store.CountRecentFailedAttempts(ctx, bucket, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("CountRecentFailedAttempts() = %d, want 3", count)
	}
}

func TestSettingsUpsert(t *testing.T) {
	t.Parallel()

	store := NewStore(openTestDatabase(t))
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.PutSetting(ctx, "panel_port", "43127", now); err != nil {
		t.Fatal(err)
	}
	if err := store.PutSetting(ctx, "panel_port", "43128", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	value, err := store.Setting(ctx, "panel_port")
	if err != nil {
		t.Fatal(err)
	}
	if value != "43128" {
		t.Fatalf("Setting() = %q, want 43128", value)
	}
}

func TestThrottleQueryUsesCompositeIndex(t *testing.T) {
	t.Parallel()

	database := openTestDatabase(t)
	var detail string
	row := database.QueryRow(`
        EXPLAIN QUERY PLAN
        SELECT COUNT(id)
        FROM login_attempts
        WHERE bucket_hash = ? AND succeeded = 0 AND attempted_at >= ?
    `, make([]byte, 32), time.Now().Add(-time.Hour).Unix())
	var identifier, parent, notUsed int
	if err := row.Scan(&identifier, &parent, &notUsed, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "idx_login_attempts_bucket_result_time") {
		t.Fatalf("query plan does not use throttle index: %s", detail)
	}
}

func TestRetentionQueryUsesTimeIndex(t *testing.T) {
	t.Parallel()

	database := openTestDatabase(t)
	var detail string
	row := database.QueryRow(`
        EXPLAIN QUERY PLAN
        SELECT id
        FROM login_attempts
        WHERE attempted_at < ?
        ORDER BY attempted_at
        LIMIT ?
    `, time.Now().Add(-24*time.Hour).Unix(), 1_000)
	var identifier, parent, notUsed int
	if err := row.Scan(&identifier, &parent, &notUsed, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "idx_login_attempts_retention") {
		t.Fatalf("query plan does not use retention index: %s", detail)
	}
}
