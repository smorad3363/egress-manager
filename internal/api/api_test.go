package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/logging"
)

func TestWriteErrorDoesNotExposeCause(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/api/private", nil)
	request = request.WithContext(logging.WithOperationID(request.Context(), "0123456789abcdef0123456789abcdef"))
	response := httptest.NewRecorder()
	WriteError(response, request, NewError(http.StatusUnauthorized, CodeUnauthorized, "Authentication required.", errors.New("password=leaked")))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "password=leaked") {
		t.Fatalf("response leaked internal cause: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "0123456789abcdef0123456789abcdef") {
		t.Fatalf("response omitted operation ID: %s", response.Body.String())
	}
}

func TestOperationIDAndSecurityMiddleware(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := SecurityHeaders(OperationIDs(logger, bytes.NewReader(make([]byte, 16)), http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if logging.OperationID(request.Context()) == "" {
			t.Fatal("operation ID missing from context")
		}
		writer.WriteHeader(http.StatusNoContent)
	})))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	if !logging.ValidOperationID(response.Header().Get(OperationIDHeader)) {
		t.Fatalf("invalid operation ID header: %q", response.Header().Get(OperationIDHeader))
	}
	for _, header := range []string{"Cache-Control", "Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options"} {
		if response.Header().Get(header) == "" {
			t.Fatalf("security header %s is missing", header)
		}
	}
}

func TestHealthHandlerHidesDependencyErrors(t *testing.T) {
	t.Parallel()

	handler := HealthHandler{
		Version: "test",
		Timeout: time.Second,
		Checks: []HealthCheck{
			{Name: "database", Critical: true, Check: func(context.Context) error { return errors.New("secret DSN") }},
			{Name: "optional", Critical: false, Check: func(context.Context) error { return nil }},
		},
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "secret DSN") {
		t.Fatalf("health response leaked dependency error: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"database":"failed"`) {
		t.Fatalf("health response omitted failed dependency: %s", response.Body.String())
	}
}
