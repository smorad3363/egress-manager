// Package app wires Egress Manager components into runnable services.
package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"

	projectlog "github.com/egress-manager/egress-manager/internal/logging"
)

func NewLogger(level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	return slog.New(projectlog.NewRedactingHandler(handler))
}

func deriveAuthenticationKey(ipcKey [32]byte) ([32]byte, error) {
	if ipcKey == [32]byte{} {
		return [32]byte{}, fmt.Errorf("IPC key must not be zero")
	}
	mac := hmac.New(sha256.New, ipcKey[:])
	_, _ = mac.Write([]byte("egress-manager/login-throttle/v1"))
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result, nil
}
