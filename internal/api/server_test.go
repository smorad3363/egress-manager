package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/auth"
	"github.com/egress-manager/egress-manager/internal/database"
)

type healthyControl struct {
	calls int
}

func (control *healthyControl) Health(context.Context) error {
	control.calls++
	return nil
}

func newTestAPIServer(t *testing.T) (*Server, *healthyControl) {
	t.Helper()
	connection, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	store := database.NewStore(connection)
	hasher := auth.PasswordHasher{
		Params: auth.Argon2Params{MemoryKiB: 64, Time: 1, Threads: 1, SaltBytes: 16, KeyBytes: 32},
		Random: bytes.NewReader(bytes.Repeat([]byte{0x21}, 4096)),
	}
	sessions, err := auth.NewSessionManager(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	bucketKey := sha256.Sum256([]byte("api-test-bucket-key"))
	authService, err := auth.NewService(store, hasher, sessions, auth.DefaultThrottlePolicy(), bucketKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := authService.ProvisionAdmin(context.Background(), "admin-primary", "operator", "correct horse battery staple", now); err != nil {
		t.Fatal(err)
	}
	control := &healthyControl{}
	health := HealthHandler{Version: "test", Checks: []HealthCheck{{Name: "database", Critical: true, Check: connection.PingContext}}}
	server, err := NewServer(
		ServerConfig{SessionCookieName: "egress_session", SecureCookies: true},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		authService,
		sessions,
		control,
		health,
	)
	if err != nil {
		t.Fatal(err)
	}
	server.now = func() time.Time { return now }
	return server, control
}

func TestUnauthenticatedControlEndpointIsRejected(t *testing.T) {
	t.Parallel()

	server, control := newTestAPIServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/control/health", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if control.calls != 0 {
		t.Fatalf("privileged control called %d times without authentication", control.calls)
	}
}

func TestHealthSupportsHead(t *testing.T) {
	t.Parallel()

	server, _ := newTestAPIServer(t)
	request := httptest.NewRequest(http.MethodHead, "/api/v1/health", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Body.Len() != 0 {
		t.Fatalf("HEAD response contained a body: %q", response.Body.String())
	}
}

func TestLoginSessionCSRFControlAndLogoutFlow(t *testing.T) {
	t.Parallel()

	server, control := newTestAPIServer(t)
	handler := server.Handler()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"operator","password":"correct horse battery staple"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	loginRequest.RemoteAddr = "192.0.2.20:54321"
	loginRecorder := httptest.NewRecorder()
	handler.ServeHTTP(loginRecorder, loginRequest)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRecorder.Code, loginRecorder.Body.String())
	}
	var loginBody loginResponse
	if err := json.Unmarshal(loginRecorder.Body.Bytes(), &loginBody); err != nil {
		t.Fatal(err)
	}
	cookies := loginRecorder.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %#v", cookies)
	}

	controlRequest := httptest.NewRequest(http.MethodGet, "/api/v1/control/health", nil)
	controlRequest.AddCookie(cookies[0])
	controlResponse := httptest.NewRecorder()
	handler.ServeHTTP(controlResponse, controlRequest)
	if controlResponse.Code != http.StatusOK || control.calls != 1 {
		t.Fatalf("control status = %d, calls = %d, body = %s", controlResponse.Code, control.calls, controlResponse.Body.String())
	}

	missingCSRFRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	missingCSRFRequest.AddCookie(cookies[0])
	missingCSRFResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingCSRFResponse, missingCSRFRequest)
	if missingCSRFResponse.Code != http.StatusForbidden {
		t.Fatalf("logout without CSRF status = %d", missingCSRFResponse.Code)
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutRequest.AddCookie(cookies[0])
	logoutRequest.Header.Set(CSRFHeader, loginBody.CSRFToken)
	logoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logoutRequest)
	if logoutResponse.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, body = %s", logoutResponse.Code, logoutResponse.Body.String())
	}
}

func TestLoginRejectsUnknownJSONAndForwardedAddressDoesNotReplacePeer(t *testing.T) {
	t.Parallel()

	server, _ := newTestAPIServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"operator","password":"correct horse battery staple","admin":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", "203.0.113.99")
	request.RemoteAddr = "192.0.2.30:1234"
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}
