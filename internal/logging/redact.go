package logging

import (
	"context"
	"log/slog"
	"strings"
)

const RedactedValue = "[REDACTED]"

var sensitiveKeyFragments = []string{
	"authorization",
	"cookie",
	"credential",
	"password",
	"private_key",
	"secret",
	"session",
	"token",
}

type RedactingHandler struct {
	next slog.Handler
}

func NewRedactingHandler(next slog.Handler) *RedactingHandler {
	return &RedactingHandler{next: next}
}

func (handler *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

func (handler *RedactingHandler) Handle(ctx context.Context, record slog.Record) error {
	redacted := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attribute slog.Attr) bool {
		redacted.AddAttrs(redactAttribute(attribute))
		return true
	})
	return handler.next.Handle(ctx, redacted)
}

func (handler *RedactingHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, 0, len(attributes))
	for _, attribute := range attributes {
		redacted = append(redacted, redactAttribute(attribute))
	}
	return &RedactingHandler{next: handler.next.WithAttrs(redacted)}
}

func (handler *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{next: handler.next.WithGroup(name)}
}

func redactAttribute(attribute slog.Attr) slog.Attr {
	attribute.Value = attribute.Value.Resolve()
	if sensitiveKey(attribute.Key) {
		return slog.String(attribute.Key, RedactedValue)
	}
	if attribute.Value.Kind() == slog.KindGroup {
		members := attribute.Value.Group()
		redacted := make([]slog.Attr, 0, len(members))
		for _, member := range members {
			redacted = append(redacted, redactAttribute(member))
		}
		return slog.Group(attribute.Key, attrsToAny(redacted)...)
	}
	return attribute
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(key)
	for _, fragment := range sensitiveKeyFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func attrsToAny(attributes []slog.Attr) []any {
	values := make([]any, len(attributes))
	for index := range attributes {
		values[index] = attributes[index]
	}
	return values
}
