package haproxy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/system"
)

type haproxyRunnerStep struct {
	name     string
	contains []string
	err      error
	after    func()
}

type haproxyRunner struct {
	t       *testing.T
	steps   []haproxyRunnerStep
	current int
}

func (runner *haproxyRunner) Run(_ context.Context, command system.Command) (system.Result, error) {
	runner.t.Helper()
	if runner.current >= len(runner.steps) {
		runner.t.Fatalf("unexpected command: %s %s", command.Name, strings.Join(command.Args, " "))
	}
	step := runner.steps[runner.current]
	runner.current++
	if command.Name != step.name {
		runner.t.Fatalf("command name = %q, want %q", command.Name, step.name)
	}
	actual := strings.Join(command.Args, " ")
	for _, expected := range step.contains {
		if !strings.Contains(actual, expected) {
			runner.t.Fatalf("command %q omitted %q", actual, expected)
		}
	}
	if step.after != nil {
		step.after()
	}
	exit := 0
	if step.err != nil {
		exit = 1
	}
	return system.Result{ExitCode: exit}, step.err
}

type switchRuntime struct {
	unhealthy bool
	calls     int
}

func (runtime *switchRuntime) Snapshot(context.Context) (RuntimeSnapshot, error) {
	runtime.calls++
	if runtime.unhealthy {
		return RuntimeSnapshot{}, errors.New("runtime unavailable")
	}
	return RuntimeSnapshot{Info: RuntimeInfo{Name: "HAProxy", Version: "3.0", PID: 42}, Stats: []RuntimeStat{}}, nil
}

func haproxyTestStore(t *testing.T) *database.Store {
	t.Helper()
	connection, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "haproxy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return database.NewStore(connection)
}

func executorTestPlan(t *testing.T, content []byte, exists bool) Plan {
	t.Helper()
	frontends, backends := testHAProxyDesired()
	state, err := ParseState(content, exists)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(testSettings(), frontends, backends, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestExecutorValidatesInstallsReloadsVerifiesAndCommits(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "haproxy.cfg")
	pidPath := filepath.Join(directory, "haproxy.pid")
	plan := executorTestPlan(t, nil, false)
	runner := &haproxyRunner{t: t, steps: []haproxyRunnerStep{{name: "haproxy", contains: []string{"-c -f "}}, {name: "haproxy", contains: []string{"-D -W", "-f " + configPath, "-p " + pidPath}}}}
	runtime := &switchRuntime{}
	store := haproxyTestStore(t)
	response, err := (Executor{Runner: runner, Journal: store, Runtime: runtime, ConfigPath: configPath, PIDPath: pidPath}).Execute(context.Background(), "haproxy_success", `{}`, plan)
	if err != nil {
		t.Fatal(err)
	}
	if response.State != domain.TransactionCommitted || response.Runtime.Info.PID != 42 {
		t.Fatalf("response = %#v", response)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != plan.Candidate {
		t.Fatal("installed candidate differs from plan")
	}
	operation, err := store.Operation(context.Background(), "haproxy_success")
	if err != nil || operation.State != domain.TransactionCommitted {
		t.Fatalf("operation = %#v, error = %v", operation, err)
	}
	if runner.current != len(runner.steps) {
		t.Fatalf("commands = %d, want %d", runner.current, len(runner.steps))
	}
}

func TestExecutorNativeValidationFailureDoesNotInstall(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "haproxy.cfg")
	pidPath := filepath.Join(directory, "haproxy.pid")
	plan := executorTestPlan(t, nil, false)
	runner := &haproxyRunner{t: t, steps: []haproxyRunnerStep{{name: "haproxy", contains: []string{"-c -f "}, err: errors.New("invalid config")}}}
	store := haproxyTestStore(t)
	_, err := (Executor{Runner: runner, Journal: store, Runtime: &switchRuntime{}, ConfigPath: configPath, PIDPath: pidPath}).Execute(context.Background(), "haproxy_invalid", `{}`, plan)
	if err == nil || !strings.Contains(err.Error(), "invalid config") {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("candidate installed after validation failure: %v", statErr)
	}
	operation, _ := store.Operation(context.Background(), "haproxy_invalid")
	if operation.State != domain.TransactionFailed || operation.FailureDetail != "native_validation_failed" {
		t.Fatalf("operation = %#v", operation)
	}
}

func TestExecutorVerificationFailureRestoresPreviousConfig(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "haproxy.cfg")
	pidPath := filepath.Join(directory, "haproxy.pid")
	oldPlan := executorTestPlan(t, nil, false)
	if err := os.WriteFile(configPath, []byte(oldPlan.Candidate), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte("41\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	frontends, backends := testHAProxyDesired()
	frontends[0].Port = 8443
	state, _ := ParseState([]byte(oldPlan.Candidate), true)
	newPlan, err := BuildPlan(testSettings(), frontends, backends, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &switchRuntime{unhealthy: true}
	runner := &haproxyRunner{t: t, steps: []haproxyRunnerStep{
		{name: "haproxy", contains: []string{"-c -f "}},
		{name: "haproxy", contains: []string{"-sf 41"}},
		{name: "haproxy", contains: []string{"-c -f "}},
		{name: "haproxy", contains: []string{"-sf 41"}, after: func() { runtime.unhealthy = false }},
	}}
	store := haproxyTestStore(t)
	executor := Executor{Runner: runner, Journal: store, Runtime: runtime, ConfigPath: configPath, PIDPath: pidPath, Timeout: time.Millisecond}
	if _, err := executor.Execute(context.Background(), "haproxy_rollback", `{}`, newPlan); err == nil || !strings.Contains(err.Error(), "runtime unavailable") {
		t.Fatalf("Execute() error = %v", err)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != oldPlan.Candidate {
		t.Fatal("previous HAProxy configuration was not restored")
	}
	operation, _ := store.Operation(context.Background(), "haproxy_rollback")
	if operation.State != domain.TransactionRolledBack || operation.FailureDetail != "verification_failed" {
		t.Fatalf("operation = %#v", operation)
	}
}

func TestExecutorRecoversInterruptedInitialApply(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "haproxy.cfg")
	pidPath := filepath.Join(directory, "haproxy.pid")
	plan := executorTestPlan(t, nil, false)
	if err := os.WriteFile(configPath, []byte(plan.Candidate), 0o600); err != nil {
		t.Fatal(err)
	}
	store := haproxyTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	snapshot, _ := json.Marshal(recoverySnapshot{Exists: false, Content: nil})
	operation := domain.Transaction{ID: "haproxy_restart", Operation: "haproxy_apply", State: domain.TransactionPrepared, RequestedChange: `{}`, PreviousSnapshot: string(snapshot), CandidateConfig: plan.Candidate, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionOperation(context.Background(), operation.ID, domain.TransactionPrepared, domain.TransactionValidated, now, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionOperation(context.Background(), operation.ID, domain.TransactionValidated, domain.TransactionApplying, now, ""); err != nil {
		t.Fatal(err)
	}
	executor := Executor{Runner: &haproxyRunner{t: t}, Journal: store, Runtime: &switchRuntime{}, ConfigPath: configPath, PIDPath: pidPath}
	if err := executor.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("interrupted initial candidate remains: %v", err)
	}
	loaded, _ := store.Operation(context.Background(), operation.ID)
	if loaded.State != domain.TransactionRolledBack || loaded.FailureDetail != "interrupted_operation" {
		t.Fatalf("operation = %#v", loaded)
	}
}
