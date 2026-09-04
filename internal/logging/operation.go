// Package logging provides structured, secret-safe application logging.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
)

var operationIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type operationIDContextKey struct{}

func NewOperationID(random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	value := make([]byte, 16)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", fmt.Errorf("read operation ID randomness: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func ValidOperationID(value string) bool {
	return operationIDPattern.MatchString(value)
}

func WithOperationID(ctx context.Context, operationID string) context.Context {
	if !ValidOperationID(operationID) {
		return ctx
	}
	return context.WithValue(ctx, operationIDContextKey{}, operationID)
}

func OperationID(ctx context.Context) string {
	value, _ := ctx.Value(operationIDContextKey{}).(string)
	return value
}
