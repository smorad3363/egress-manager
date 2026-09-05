package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/config"
	"github.com/egress-manager/egress-manager/internal/domain"
	managedHAProxy "github.com/egress-manager/egress-manager/internal/haproxy"
	managedInterface "github.com/egress-manager/egress-manager/internal/interfaceoutbound"
	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/reliability"
	"github.com/egress-manager/egress-manager/internal/routeengine"
	managedSingBox "github.com/egress-manager/egress-manager/internal/singbox"
	"github.com/egress-manager/egress-manager/internal/system"
	managedXray "github.com/egress-manager/egress-manager/internal/xray"
)

type dependencyHealthRunner struct {
	inactiveSingBox bool
}

func (runner dependencyHealthRunner) Run(_ context.Context, command system.Command) (system.Result, error) {
	joined := command.Name + " " + strings.Join(command.Args, " ")
	if runner.inactiveSingBox && joined == "systemctl is-active --quiet egress-manager-sing-box.service" {
		return system.Result{ExitCode: 3}, errors.New("inactive")
	}
	return system.Result{ExitCode: 0}, nil
}

type unusedHAProxyRuntime struct{}

func (unusedHAProxyRuntime) Snapshot(context.Context) (managedHAProxy.RuntimeSnapshot, error) {
	return managedHAProxy.RuntimeSnapshot{}, errors.New("not configured")
}

func TestDependencyMonitorReportsDisabledComponentsAndDebouncesRuntimeFailure(t *testing.T) {
	directory := t.TempDir()
	configuration := config.Default(directory)
	configuration.ListenPort = 8080
	configuration.InterfaceRuntimeDirectory = filepath.Join(directory, "interfaces")
	configuration.InterfaceStatePath = filepath.Join(configuration.InterfaceRuntimeDirectory, "state.json")
	options := dependencyMonitorOptions{
		configuration:     configuration,
		runner:            dependencyHealthRunner{},
		collector:         inventory.Collector{},
		haproxyRuntime:    unusedHAProxyRuntime{},
		interfaceExecutor: managedInterface.Executor{},
		routeExecutor:     routeengine.Executor{},
		loadInterfacePlan: func(context.Context) (managedInterface.ExecutionPlan, error) {
			return managedInterface.ExecutionPlan{}, errors.New("not configured")
		},
		discoverXray: func(context.Context) (managedXray.Report, error) {
			return managedXray.Report{Installations: []managedXray.Installation{}}, nil
		},
	}
	monitor, err := newDependencyMonitor(options)
	if err != nil {
		t.Fatal(err)
	}
	monitor.RunOnce(context.Background())
	statuses := monitor.Snapshot()
	if len(statuses) != 6 {
		t.Fatalf("dependencies = %#v", statuses)
	}
	for _, status := range statuses {
		want := domain.HealthDisabled
		if status.Name == "system_tools" {
			want = domain.HealthHealthy
		}
		if status.Status != want {
			t.Fatalf("dependency %s status=%s want=%s", status.Name, status.Status, want)
		}
	}

	state, err := managedSingBox.ParseState(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := managedSingBox.BuildPlan(nil, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configuration.SingBoxConfigPath, plan.Candidate(), 0o600); err != nil {
		t.Fatal(err)
	}
	options.runner = dependencyHealthRunner{inactiveSingBox: true}
	monitor, err = newDependencyMonitor(options)
	if err != nil {
		t.Fatal(err)
	}
	monitor.RunOnce(context.Background())
	if status := dependencyByName(t, monitor.Snapshot(), "singbox"); status.Status != domain.HealthDegraded || status.ConfigurationValid != domain.ProbePassed || status.ProcessRunning != domain.ProbeFailed {
		t.Fatalf("first sing-box failure = %#v", status)
	}
	monitor.RunOnce(context.Background())
	if status := dependencyByName(t, monitor.Snapshot(), "singbox"); status.Status != domain.HealthUnhealthy || status.ConsecutiveFailures != 2 {
		t.Fatalf("second sing-box failure = %#v", status)
	}
}

func dependencyByName(t *testing.T, statuses []reliability.DependencyStatus, name string) reliability.DependencyStatus {
	t.Helper()
	for _, status := range statuses {
		if status.Name == name {
			return status
		}
	}
	t.Fatalf("dependency %q missing", name)
	return reliability.DependencyStatus{}
}
