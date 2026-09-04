package api

import (
	"crypto/rand"
	"io"
	"log/slog"
	"net/http"

	"github.com/egress-manager/egress-manager/internal/logging"
)

const OperationIDHeader = "X-Operation-ID"

func OperationIDs(logger *slog.Logger, random io.Reader, next http.Handler) http.Handler {
	if random == nil {
		random = rand.Reader
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		operationID := request.Header.Get(OperationIDHeader)
		if !logging.ValidOperationID(operationID) {
			generated, err := logging.NewOperationID(random)
			if err != nil {
				logger.ErrorContext(request.Context(), "operation ID generation failed", "error", err)
				WriteError(writer, request, NewError(http.StatusServiceUnavailable, CodeUnavailable, "Service temporarily unavailable.", err))
				return
			}
			operationID = generated
		}
		ctx := logging.WithOperationID(request.Context(), operationID)
		writer.Header().Set(OperationIDHeader, operationID)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(writer, request)
	})
}

func Recover(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(request.Context(), "HTTP panic recovered", "operation_id", logging.OperationID(request.Context()))
				WriteError(writer, request, NewError(http.StatusInternalServerError, CodeInternal, "An internal error occurred.", nil))
			}
		}()
		next.ServeHTTP(writer, request)
	})
}
