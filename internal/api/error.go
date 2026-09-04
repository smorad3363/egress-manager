package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/egress-manager/egress-manager/internal/ipc"
	"github.com/egress-manager/egress-manager/internal/logging"
)

type ErrorCode string

const (
	CodeBadRequest     ErrorCode = "bad_request"
	CodeUnauthorized   ErrorCode = "unauthorized"
	CodeForbidden      ErrorCode = "forbidden"
	CodeNotFound       ErrorCode = "not_found"
	CodeConflict       ErrorCode = "conflict"
	CodeRateLimited    ErrorCode = "rate_limited"
	CodeInternal       ErrorCode = "internal_error"
	CodeUnavailable    ErrorCode = "unavailable"
	CodeInvalidCSRF    ErrorCode = "invalid_csrf"
	CodeMethodNotAllow ErrorCode = "method_not_allowed"
)

type APIError struct {
	Status  int
	Code    ErrorCode
	Message string
	Cause   error
}

func (apiError *APIError) Error() string {
	return string(apiError.Code)
}

func (apiError *APIError) Unwrap() error {
	return apiError.Cause
}

func NewError(status int, code ErrorCode, message string, cause error) *APIError {
	return &APIError{Status: status, Code: code, Message: message, Cause: cause}
}

type errorDocument struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code        ErrorCode `json:"code"`
	Message     string    `json:"message"`
	OperationID string    `json:"operation_id,omitempty"`
}

func WriteError(writer http.ResponseWriter, request *http.Request, err error) {
	apiError := &APIError{}
	if !errors.As(err, &apiError) {
		apiError = NewError(http.StatusInternalServerError, CodeInternal, "An internal error occurred.", err)
	}
	status := apiError.Status
	if status < 400 || status > 599 {
		status = http.StatusInternalServerError
		apiError.Code = CodeInternal
		apiError.Message = "An internal error occurred."
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(errorDocument{Error: errorBody{
		Code:        apiError.Code,
		Message:     apiError.Message,
		OperationID: logging.OperationID(request.Context()),
	}})
}

func WriteJSON(writer http.ResponseWriter, status int, value any) error {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	return json.NewEncoder(writer).Encode(value)
}

func WriteMutationError(writer http.ResponseWriter, request *http.Request, failureMessage string, err error) {
	if ipc.IsRemoteError(err, "recovery_required") {
		WriteError(writer, request, NewError(http.StatusServiceUnavailable, CodeUnavailable, "Host recovery is required before mutation.", err))
		return
	}
	if ipc.IsRemoteError(err, "busy") {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Another host mutation is in progress.", err))
		return
	}
	WriteError(writer, request, NewError(http.StatusServiceUnavailable, CodeUnavailable, failureMessage, err))
}
