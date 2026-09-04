package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/database"
)

func openAuthService(t *testing.T, maximumFailures int) (*Service, *SessionManager, *database.Store) {
	t.Helper()
	databaseConnection, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := databaseConnection.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	store := database.NewStore(databaseConnection)
	sessions, err := NewSessionManager(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sessions.random = &cyclingReader{value: 1}
	hasher := testHasher()
	bucketKey := sha256.Sum256([]byte("test-only-bucket-key"))
	service, err := NewService(store, hasher, sessions, ThrottlePolicy{Window: time.Minute, MaxFailures: maximumFailures}, bucketKey)
	if err != nil {
		t.Fatal(err)
	}
	return service, sessions, store
}

type cyclingReader struct {
	value byte
}

func (reader *cyclingReader) Read(output []byte) (int, error) {
	for index := range output {
		output[index] = reader.value
		reader.value++
	}
	return len(output), nil
}

func TestLoginSessionAndCSRF(t *testing.T) {
	t.Parallel()

	service, sessions, _ := openAuthService(t, 5)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := service.ProvisionAdmin(ctx, "admin-primary", "Operator", "correct horse battery staple", now); err != nil {
		t.Fatal(err)
	}
	tokens, err := service.Login(ctx, "operator", "correct horse battery staple", netip.MustParseAddr("192.0.2.10"), now)
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Authenticate(ctx, tokens.SessionToken, now)
	if err != nil {
		t.Fatal(err)
	}
	if session.AdminID != "admin-primary" {
		t.Fatalf("session admin = %q", session.AdminID)
	}
	if err := sessions.VerifyCSRF(session, tokens.CSRFToken); err != nil {
		t.Fatal(err)
	}
	if err := sessions.VerifyCSRF(session, "invalid"); !errors.Is(err, ErrInvalidCSRF) {
		t.Fatalf("VerifyCSRF() error = %v, want ErrInvalidCSRF", err)
	}
	if err := sessions.Revoke(ctx, tokens.SessionToken, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Authenticate(ctx, tokens.SessionToken, now); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("Authenticate() error = %v, want ErrInvalidSession", err)
	}
}

func TestLoginThrottlesByHashedUserAndAddressBucket(t *testing.T) {
	t.Parallel()

	service, _, store := openAuthService(t, 2)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	address := netip.MustParseAddr("198.51.100.8")
	for attempt := 0; attempt < 2; attempt++ {
		_, err := service.Login(ctx, "missing-user", "wrong-password-long-enough", address, now.Add(time.Duration(attempt)*time.Second))
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
		}
	}
	if _, err := service.Login(ctx, "missing-user", "wrong-password-long-enough", address, now.Add(2*time.Second)); !errors.Is(err, ErrLoginThrottled) {
		t.Fatalf("Login() error = %v, want ErrLoginThrottled", err)
	}
	bucket := service.loginBucket("missing-user", address)
	count, err := store.CountRecentFailedAttempts(ctx, bucket, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("failure count = %d, want 2", count)
	}
}

func TestSessionCookieSecurityAttributes(t *testing.T) {
	t.Parallel()

	cookie := SessionCookie("egress_session", "opaque", time.Now().Add(time.Hour), true)
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("cookie lacks required security attributes: %#v", cookie)
	}
}
