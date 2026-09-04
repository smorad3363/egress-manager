package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactingHandlerRemovesSecretAttributes(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := slog.New(NewRedactingHandler(slog.NewJSONHandler(&output, nil))).With(
		"component", "auth",
		"session_token", "with-secret",
	)
	logger.InfoContext(context.Background(), "login",
		"username", "operator",
		"password", "never-log-this",
		"outbound_uuid", "never-log-uuid",
		"pre_shared_key", "never-log-psk",
		slog.Group("request", "authorization", "Bearer leak", "method", "POST"),
	)
	text := output.String()
	for _, secret := range []string{"with-secret", "never-log-this", "never-log-uuid", "never-log-psk", "Bearer leak"} {
		if strings.Contains(text, secret) {
			t.Fatalf("structured log leaked %q: %s", secret, text)
		}
	}
	if count := strings.Count(text, RedactedValue); count != 5 {
		t.Fatalf("redaction count = %d, want 5: %s", count, text)
	}
	if !strings.Contains(text, `"username":"operator"`) || !strings.Contains(text, `"method":"POST"`) {
		t.Fatalf("non-secret fields were lost: %s", text)
	}
}

func TestOperationID(t *testing.T) {
	t.Parallel()

	operationID, err := NewOperationID(bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if !ValidOperationID(operationID) {
		t.Fatalf("operation ID is invalid: %q", operationID)
	}
	ctx := WithOperationID(context.Background(), operationID)
	if got := OperationID(ctx); got != operationID {
		t.Fatalf("OperationID() = %q, want %q", got, operationID)
	}
	if got := OperationID(WithOperationID(ctx, "hostile\nvalue")); got != operationID {
		t.Fatalf("invalid replacement changed operation ID: %q", got)
	}
}
