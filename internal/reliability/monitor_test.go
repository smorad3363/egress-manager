package reliability

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func TestMonitorDebouncesFailuresAndRecovery(t *testing.T) {
	now := time.Date(2026, 9, 6, 1, 0, 0, 0, time.UTC)
	observation := healthyDependencyObservation()
	monitor := &Monitor{
		Probes: []DependencyProbe{{Name: "singbox", Check: func(context.Context) (DependencyObservation, error) { return observation, nil }}},
		Now:    func() time.Time { return now },
	}
	monitor.RunOnce(context.Background())
	assertDependencyStatus(t, monitor.Snapshot(), domain.HealthHealthy, 0, true)

	observation = failedDependencyObservation("process_inactive")
	now = now.Add(time.Second)
	monitor.RunOnce(context.Background())
	assertDependencyStatus(t, monitor.Snapshot(), domain.HealthDegraded, 1, true)
	now = now.Add(time.Second)
	monitor.RunOnce(context.Background())
	assertDependencyStatus(t, monitor.Snapshot(), domain.HealthUnhealthy, 2, true)

	observation = healthyDependencyObservation()
	now = now.Add(time.Second)
	monitor.RunOnce(context.Background())
	status := assertDependencyStatus(t, monitor.Snapshot(), domain.HealthDegraded, 0, true)
	if status.Detail != "recovering" {
		t.Fatalf("first recovery detail = %q", status.Detail)
	}
	now = now.Add(time.Second)
	monitor.RunOnce(context.Background())
	assertDependencyStatus(t, monitor.Snapshot(), domain.HealthHealthy, 0, true)
}

func TestMonitorBoundsAndSanitizesProbeResults(t *testing.T) {
	secret := strings.Repeat("secret", 40)
	monitor := &Monitor{Probes: []DependencyProbe{
		{Name: "bad_error", Check: func(context.Context) (DependencyObservation, error) {
			return DependencyObservation{}, errors.New(secret)
		}},
		{Name: "bad_result", Check: func(context.Context) (DependencyObservation, error) {
			observation := healthyDependencyObservation()
			observation.Detail = secret
			return observation, nil
		}},
	}}
	monitor.RunOnce(context.Background())
	statuses := monitor.Snapshot()
	if len(statuses) != 2 || statuses[0].Detail != "probe_failed" || statuses[1].Detail != "invalid_probe_result" {
		t.Fatalf("sanitized statuses = %#v", statuses)
	}
	for _, status := range statuses {
		if strings.Contains(status.Detail, "secret") {
			t.Fatal("dependency status exposed probe error")
		}
	}
}

func TestMonitorRunsImmediatelyAndStopsWithContext(t *testing.T) {
	var calls atomic.Int32
	monitor := &Monitor{Probes: []DependencyProbe{{Name: "routing", Check: func(context.Context) (DependencyObservation, error) {
		calls.Add(1)
		return healthyDependencyObservation(), nil
	}}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- monitor.Run(ctx) }()
	for deadline := time.Now().Add(time.Second); calls.Load() == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil || calls.Load() != 1 {
		t.Fatalf("Run() error=%v calls=%d", err, calls.Load())
	}
}

func TestMonitorBoundsProbeTimeout(t *testing.T) {
	release := make(chan struct{})
	monitor := &Monitor{
		Probes: []DependencyProbe{{Name: "stuck", Check: func(context.Context) (DependencyObservation, error) {
			<-release
			return healthyDependencyObservation(), nil
		}}},
		Timeout: time.Millisecond,
	}
	started := time.Now()
	monitor.RunOnce(context.Background())
	close(release)
	if time.Since(started) > time.Second {
		t.Fatal("RunOnce did not bound an uncooperative probe")
	}
	status := assertDependencyStatus(t, monitor.Snapshot(), domain.HealthDegraded, 1, false)
	if status.Detail != "probe_timeout" {
		t.Fatalf("timeout detail = %q", status.Detail)
	}
}

func TestMonitorRejectsInvalidProbeSets(t *testing.T) {
	checks := []Monitor{
		{},
		{Probes: []DependencyProbe{{Name: "bad name", Check: func(context.Context) (DependencyObservation, error) { return healthyDependencyObservation(), nil }}}},
		{Probes: []DependencyProbe{{Name: "same", Check: func(context.Context) (DependencyObservation, error) { return healthyDependencyObservation(), nil }}, {Name: "same", Check: func(context.Context) (DependencyObservation, error) { return healthyDependencyObservation(), nil }}}},
	}
	for index := range checks {
		if err := checks[index].Validate(); err == nil {
			t.Fatalf("invalid monitor %d accepted", index)
		}
	}
}

func healthyDependencyObservation() DependencyObservation {
	return DependencyObservation{
		Status:              domain.HealthHealthy,
		ExecutableAvailable: domain.ProbePassed,
		ConfigurationValid:  domain.ProbePassed,
		ProcessRunning:      domain.ProbePassed,
		TransportReachable:  domain.ProbeUntestable,
		InternetReachable:   domain.ProbeUntestable,
		Detail:              "healthy",
	}
}

func assertDependencyStatus(t *testing.T, statuses []DependencyStatus, want domain.HealthStatus, failures int, hasSuccess bool) DependencyStatus {
	t.Helper()
	if len(statuses) != 1 {
		t.Fatalf("statuses = %#v", statuses)
	}
	status := statuses[0]
	if status.Status != want || status.ConsecutiveFailures != failures || (status.LastSuccess != nil) != hasSuccess {
		t.Fatalf("status = %#v, want status=%s failures=%d success=%t", status, want, failures, hasSuccess)
	}
	return status
}
