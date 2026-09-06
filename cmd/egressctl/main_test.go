package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/ipc"
	"github.com/egress-manager/egress-manager/internal/reliability"
	"github.com/egress-manager/egress-manager/internal/routeengine"
)

func TestStatusHumanAndJSONOutput(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	status := reliability.RecoveryStatus{Ready: false, RecoveryRequired: true, MutationLock: reliability.Status{Active: true, Owner: &reliability.Owner{PID: 42, OperationID: "route_apply", Component: "routing"}}, Unfinished: []reliability.OperationSummary{{ID: "interrupted", Operation: "route_engine_apply", State: domain.TransactionApplying, UpdatedAt: now}}, Dependencies: []reliability.DependencyStatus{{Name: "singbox", DependencyObservation: reliability.DependencyObservation{Status: domain.HealthDegraded}, LastChecked: now, ConsecutiveFailures: 1}}, ObservedAt: now}
	fetch := func(context.Context, string, string) (reliability.RecoveryStatus, error) { return status, nil }
	recover := func(context.Context, string, string) (reliability.RecoveryReport, error) {
		return reliability.RecoveryReport{}, nil
	}
	bypass := func(context.Context, string, string) (routeengine.BypassResponse, error) {
		return routeengine.BypassResponse{}, nil
	}
	privileged := func() bool { return true }
	var stdout, stderr bytes.Buffer
	if code := run([]string{"status", "--config", "/test/config.json", "--ipc-key", "/test/ipc.key"}, &stdout, &stderr, fetch, recover, bypass, privileged); code != exitRecoveryRequired {
		t.Fatalf("human status exit = %d, stderr = %s", code, stderr.String())
	}
	for _, expected := range []string{"ready: no", "mutation lock: active", "lock owner: routing operation route_apply pid 42", "interrupted route_engine_apply APPLYING", "singbox: degraded failures 1"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("human output omitted %q:\n%s", expected, stdout.String())
		}
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"status", "--json"}, &stdout, &stderr, fetch, recover, bypass, privileged); code != exitRecoveryRequired {
		t.Fatalf("JSON status exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"recovery_required": true`) || strings.Contains(stdout.String(), `"previous_snapshot"`) {
		t.Fatalf("unexpected JSON output:\n%s", stdout.String())
	}
}

func TestRecoverHumanAndJSONOutput(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 4, 30, 0, 0, time.UTC)
	report := reliability.RecoveryReport{Succeeded: true, Steps: []reliability.StepResult{{Component: "interface", Succeeded: true, StartedAt: now, EndedAt: now}}, StartedAt: now, EndedAt: now}
	fetch := func(context.Context, string, string) (reliability.RecoveryStatus, error) {
		return reliability.RecoveryStatus{}, nil
	}
	recover := func(context.Context, string, string) (reliability.RecoveryReport, error) { return report, nil }
	bypass := func(context.Context, string, string) (routeengine.BypassResponse, error) {
		return routeengine.BypassResponse{}, nil
	}
	privileged := func() bool { return true }
	var stdout, stderr bytes.Buffer
	if code := run([]string{"recover"}, &stdout, &stderr, fetch, recover, bypass, privileged); code != exitOK {
		t.Fatalf("recover exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "recovery: succeeded") || !strings.Contains(stdout.String(), "- interface: succeeded") {
		t.Fatalf("unexpected recovery output:\n%s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"recover", "--json"}, &stdout, &stderr, fetch, recover, bypass, privileged); code != exitOK || !strings.Contains(stdout.String(), `"succeeded": true`) {
		t.Fatalf("JSON recovery exit = %d, stdout = %s, stderr = %s", code, stdout.String(), stderr.String())
	}
	report.Succeeded = false
	report.FailedComponent = "interface"
	if code := run([]string{"recover"}, &stdout, &stderr, fetch, recover, bypass, privileged); code != exitFailure {
		t.Fatalf("failed recovery exit = %d", code)
	}
	recover = func(context.Context, string, string) (reliability.RecoveryReport, error) {
		return reliability.RecoveryReport{}, &ipc.RemoteError{Code: "busy"}
	}
	if code := run([]string{"recover"}, &stdout, &stderr, fetch, recover, bypass, privileged); code != exitBusy {
		t.Fatalf("busy recovery exit = %d", code)
	}
}

func TestStatusFailuresAndUsageHaveStableExitCodes(t *testing.T) {
	t.Parallel()

	fetch := func(context.Context, string, string) (reliability.RecoveryStatus, error) {
		return reliability.RecoveryStatus{}, errors.New("unavailable")
	}
	recover := func(context.Context, string, string) (reliability.RecoveryReport, error) {
		return reliability.RecoveryReport{}, errors.New("unavailable")
	}
	bypass := func(context.Context, string, string) (routeengine.BypassResponse, error) {
		return routeengine.BypassResponse{}, errors.New("unavailable")
	}
	privileged := func() bool { return true }
	for _, test := range []struct {
		arguments []string
		want      int
	}{
		{arguments: nil, want: exitUsage},
		{arguments: []string{"unknown"}, want: exitUsage},
		{arguments: []string{"status", "extra"}, want: exitUsage},
		{arguments: []string{"status"}, want: exitFailure},
		{arguments: []string{"recover"}, want: exitFailure},
		{arguments: []string{"bypass"}, want: exitFailure},
	} {
		var stdout, stderr bytes.Buffer
		if got := run(test.arguments, &stdout, &stderr, fetch, recover, bypass, privileged); got != test.want {
			t.Fatalf("run(%q) = %d, want %d", test.arguments, got, test.want)
		}
	}
}

func TestBypassOutputBusyAndPrivilegeExitCodes(t *testing.T) {
	t.Parallel()
	fetch := func(context.Context, string, string) (reliability.RecoveryStatus, error) {
		return reliability.RecoveryStatus{}, nil
	}
	recover := func(context.Context, string, string) (reliability.RecoveryReport, error) {
		return reliability.RecoveryReport{}, nil
	}
	response := routeengine.BypassResponse{TransactionID: "bypass_transaction", State: domain.TransactionCommitted}
	bypass := func(context.Context, string, string) (routeengine.BypassResponse, error) { return response, nil }
	var stdout, stderr bytes.Buffer
	if code := run([]string{"bypass"}, &stdout, &stderr, fetch, recover, bypass, func() bool { return true }); code != exitOK || !strings.Contains(stdout.String(), "bypass: activated") {
		t.Fatalf("bypass exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	response.AlreadyActive = true
	if code := run([]string{"bypass", "--json"}, &stdout, &stderr, fetch, recover, bypass, func() bool { return true }); code != exitOK || !strings.Contains(stdout.String(), `"already_active": true`) {
		t.Fatalf("JSON bypass exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	bypass = func(context.Context, string, string) (routeengine.BypassResponse, error) {
		return routeengine.BypassResponse{}, &ipc.RemoteError{Code: "busy"}
	}
	if code := run([]string{"bypass"}, &stdout, &stderr, fetch, recover, bypass, func() bool { return true }); code != exitBusy {
		t.Fatalf("busy bypass exit=%d", code)
	}
	if code := run([]string{"bypass"}, &stdout, &stderr, fetch, recover, bypass, func() bool { return false }); code != exitPrivilege {
		t.Fatalf("unprivileged bypass exit=%d", code)
	}
	if code := run([]string{"recover"}, &stdout, &stderr, fetch, recover, bypass, func() bool { return false }); code != exitPrivilege {
		t.Fatalf("unprivileged recovery exit=%d", code)
	}
}
