package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
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
	if count != 3 {
		t.Fatalf("migration count = %d, want 3", count)
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

func TestPortForwardRepositoryUsesOptimisticRevisionAndKeysetPagination(t *testing.T) {
	t.Parallel()

	store := NewStore(openTestDatabase(t))
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	forward := domain.PortForward{
		ID: "forward_a", Name: "Forward A", Protocols: []domain.TransportProtocol{domain.ProtocolTCP},
		ListenAddress: netip.MustParseAddr("203.0.113.10"), ListenPorts: []domain.PortRange{{From: 8443, To: 8443}},
		RemoteAddress: netip.MustParseAddr("10.10.0.5"), RemotePortStart: 443, Enabled: false,
	}
	created, err := store.CreatePortForward(ctx, forward, now)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 {
		t.Fatalf("created revision = %d", created.Revision)
	}
	forward.Enabled = true
	updated, err := store.UpdatePortForward(ctx, forward, 1, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || !updated.Forward.Enabled {
		t.Fatalf("updated = %#v", updated)
	}
	if _, err := store.UpdatePortForward(ctx, forward, 1, now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v, want ErrConflict", err)
	}
	second := forward
	second.ID = "forward_b"
	second.Name = "Forward B"
	if _, err := store.CreatePortForward(ctx, second, now); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListPortForwards(ctx, "forward_a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Forward.ID != "forward_b" {
		t.Fatalf("keyset page = %#v", page)
	}
	if err := store.DeletePortForward(ctx, forward.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete error = %v, want ErrConflict", err)
	}
	if err := store.DeletePortForward(ctx, forward.ID, 2); err != nil {
		t.Fatal(err)
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

func TestOperationJournalTransitionsAndUnfinishedIndex(t *testing.T) {
	t.Parallel()

	database := openTestDatabase(t)
	store := NewStore(database)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	operation := domain.Transaction{
		ID: "nat_001", Operation: "nat_apply", State: domain.TransactionPrepared,
		RequestedChange: `{}`, PreviousSnapshot: `absent`, CandidateConfig: `table ip egm_nat4 {}`,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionOperation(ctx, operation.ID, domain.TransactionPrepared, domain.TransactionValidated, now.Add(time.Second), ""); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionOperation(ctx, operation.ID, domain.TransactionPrepared, domain.TransactionFailed, now.Add(2*time.Second), "stale"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale transition error = %v, want ErrConflict", err)
	}
	loaded, err := store.Operation(ctx, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != domain.TransactionValidated || loaded.PreviousSnapshot != "absent" {
		t.Fatalf("operation = %#v", loaded)
	}
	unfinished, err := store.UnfinishedOperations(ctx, 10)
	if err != nil || len(unfinished) != 1 {
		t.Fatalf("unfinished = %#v, error = %v", unfinished, err)
	}

	var detail string
	row := database.QueryRow(`
        EXPLAIN QUERY PLAN
        SELECT id
        FROM operation_journal
        WHERE state NOT IN ('COMMITTED', 'ROLLED_BACK', 'FAILED')
        ORDER BY updated_at
        LIMIT ?
    `, 100)
	var identifier, parent, notUsed int
	if err := row.Scan(&identifier, &parent, &notUsed, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "idx_operation_journal_unfinished") {
		t.Fatalf("query plan does not use unfinished-operation index: %s", detail)
	}

	row = database.QueryRow(`
        EXPLAIN QUERY PLAN
        SELECT id
        FROM operation_journal
        WHERE state IN ('COMMITTED', 'ROLLED_BACK', 'FAILED') AND updated_at < ?
        ORDER BY updated_at
        LIMIT ?
    `, now.Add(time.Hour).Unix(), 100)
	if err := row.Scan(&identifier, &parent, &notUsed, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "idx_operation_journal_finished_retention") {
		t.Fatalf("query plan does not use finished-operation retention index: %s", detail)
	}
}
