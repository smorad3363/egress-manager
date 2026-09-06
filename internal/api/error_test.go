package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/ipc"
)

func TestWriteMutationErrorDistinguishesBusyBeforeApply(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/routes/apply", nil)
	response := httptest.NewRecorder()
	WriteMutationError(response, request, "apply failed and rollback was attempted", &ipc.RemoteError{Code: "busy"})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"conflict"`) || !strings.Contains(response.Body.String(), "Another host mutation is in progress.") {
		t.Fatalf("busy mutation response = %d %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	WriteMutationError(response, request, "apply failed and rollback was attempted", &ipc.RemoteError{Code: "bypass_active"})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "Emergency bypass is active") {
		t.Fatalf("bypass mutation response = %d %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	WriteMutationError(response, request, "apply failed and rollback was attempted", &ipc.RemoteError{Code: "operation_failed"})
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"unavailable"`) {
		t.Fatalf("failed mutation response = %d %s", response.Code, response.Body.String())
	}
}
