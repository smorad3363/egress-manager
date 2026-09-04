package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/reliability"
)

func TestStatusHumanAndJSONOutput(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	status := reliability.RecoveryStatus{Ready: false, RecoveryRequired: true, MutationLock: reliability.Status{Active: true, Owner: &reliability.Owner{PID: 42, OperationID: "route_apply", Component: "routing"}}, Unfinished: []reliability.OperationSummary{{ID: "interrupted", Operation: "route_engine_apply", State: domain.TransactionApplying, UpdatedAt: now}}, ObservedAt: now}
	fetch := func(context.Context, string, string) (reliability.RecoveryStatus, error) { return status, nil }
	var stdout, stderr bytes.Buffer
	if code := run([]string{"status", "--config", "/test/config.json", "--ipc-key", "/test/ipc.key"}, &stdout, &stderr, fetch); code != exitRecoveryRequired {
		t.Fatalf("human status exit = %d, stderr = %s", code, stderr.String())
	}
	for _, expected := range []string{"ready: no", "mutation lock: active", "lock owner: routing operation route_apply pid 42", "interrupted route_engine_apply APPLYING"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("human output omitted %q:\n%s", expected, stdout.String())
		}
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"status", "--json"}, &stdout, &stderr, fetch); code != exitRecoveryRequired {
		t.Fatalf("JSON status exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"recovery_required": true`) || strings.Contains(stdout.String(), `"previous_snapshot"`) {
		t.Fatalf("unexpected JSON output:\n%s", stdout.String())
	}
}

func TestStatusFailuresAndUsageHaveStableExitCodes(t *testing.T) {
	t.Parallel()

	fetch := func(context.Context, string, string) (reliability.RecoveryStatus, error) {
		return reliability.RecoveryStatus{}, errors.New("unavailable")
	}
	for _, test := range []struct {
		arguments []string
		want      int
	}{
		{arguments: nil, want: exitUsage},
		{arguments: []string{"recover"}, want: exitUsage},
		{arguments: []string{"status", "extra"}, want: exitUsage},
		{arguments: []string{"status"}, want: exitFailure},
	} {
		var stdout, stderr bytes.Buffer
		if got := run(test.arguments, &stdout, &stderr, fetch); got != test.want {
			t.Fatalf("run(%q) = %d, want %d", test.arguments, got, test.want)
		}
	}
}
