package singbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/secrets"
	"github.com/egress-manager/egress-manager/internal/system"
)

type runnerStep struct {
	name     string
	contains []string
	err      error
}

type scriptedRunner struct {
	t       *testing.T
	steps   []runnerStep
	current int
}

func (runner *scriptedRunner) Run(_ context.Context, command system.Command) (system.Result, error) {
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
	exitCode := 0
	if step.err != nil {
		exitCode = 1
	}
	return system.Result{ExitCode: exitCode}, step.err
}

func executorStore(t *testing.T) *database.Store {
	t.Helper()
	connection, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "singbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return database.NewStore(connection)
}

func executorProtector(t *testing.T) *secrets.Protector {
	t.Helper()
	protector, err := secrets.NewProtector([32]byte{4, 3, 2, 1}, bytes.NewReader(bytes.Repeat([]byte{9}, 4096)))
	if err != nil {
		t.Fatal(err)
	}
	return protector
}

func executionPlan(t *testing.T, state State, fixture string) ExecutionPlan {
	t.Helper()
	imports, err := ParseImport(readFixture(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	credentials := map[domain.ID][]byte{imports[0].Outbound.ID: imports[0].CredentialDocument}
	plan, err := BuildPlan([]domain.Outbound{imports[0].Outbound}, credentials, state)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestExecutorEncryptsJournalValidatesInstallsRestartsAndCommits(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "sing-box.json")
	state, _ := ParseState(nil, false)
	plan := executionPlan(t, state, "vless.uri")
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{name: "sing-box", contains: []string{"check -c "}},
		{name: "systemctl", contains: []string{"restart " + ownedServiceName}},
		{name: "sing-box", contains: []string{"check -c " + configPath}},
		{name: "systemctl", contains: []string{"is-active --quiet " + ownedServiceName}},
	}}
	store := executorStore(t)
	executor := Executor{Runner: runner, Journal: store, Protector: executorProtector(t), ConfigPath: configPath}
	response, err := executor.Execute(context.Background(), "singbox_success", plan)
	if err != nil {
		t.Fatal(err)
	}
	if response.State != domain.TransactionCommitted || response.CandidateHash != plan.Plan.CandidateHash {
		t.Fatalf("response = %#v", response)
	}
	installed, err := os.ReadFile(configPath)
	if err != nil || !bytes.Equal(installed, plan.Candidate()) {
		t.Fatalf("installed candidate mismatch, error = %v", err)
	}
	operation, err := store.Operation(context.Background(), "singbox_success")
	if err != nil || operation.State != domain.TransactionCommitted {
		t.Fatalf("operation = %#v, error = %v", operation, err)
	}
	for _, journalValue := range []string{operation.RequestedChange, operation.PreviousSnapshot, operation.CandidateConfig, operation.FailureDetail} {
		if strings.Contains(journalValue, "00000000-0000-4000") {
			t.Fatal("operation journal leaked outbound credential")
		}
	}
}

func TestExecutorNativeValidationFailureDoesNotInstall(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "sing-box.json")
	state, _ := ParseState(nil, false)
	plan := executionPlan(t, state, "trojan.uri")
	runner := &scriptedRunner{t: t, steps: []runnerStep{{name: "sing-box", contains: []string{"check -c "}, err: errors.New("invalid configuration")}}}
	store := executorStore(t)
	_, err := (Executor{Runner: runner, Journal: store, Protector: executorProtector(t), ConfigPath: configPath}).Execute(context.Background(), "singbox_invalid", plan)
	if err == nil || !strings.Contains(err.Error(), "invalid configuration") {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("candidate installed after validation failure: %v", statErr)
	}
	operation, _ := store.Operation(context.Background(), "singbox_invalid")
	if operation.State != domain.TransactionFailed || operation.FailureDetail != "native_validation_failed" {
		t.Fatalf("operation = %#v", operation)
	}
}

func TestExecutorRestartFailureRollsBackInitialApply(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "sing-box.json")
	state, _ := ParseState(nil, false)
	plan := executionPlan(t, state, "hysteria2.uri")
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{name: "sing-box", contains: []string{"check -c "}},
		{name: "systemctl", contains: []string{"restart " + ownedServiceName}, err: errors.New("restart failed")},
		{name: "systemctl", contains: []string{"stop " + ownedServiceName}},
	}}
	store := executorStore(t)
	_, err := (Executor{Runner: runner, Journal: store, Protector: executorProtector(t), ConfigPath: configPath}).Execute(context.Background(), "singbox_rollback", plan)
	if err == nil || !strings.Contains(err.Error(), "restart failed") {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("initial candidate remains after rollback: %v", statErr)
	}
	operation, _ := store.Operation(context.Background(), "singbox_rollback")
	if operation.State != domain.TransactionRolledBack || operation.FailureDetail != "restart_failed" {
		t.Fatalf("operation = %#v", operation)
	}
}

func TestExecutorRecoversInterruptedInitialApply(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "sing-box.json")
	state, _ := ParseState(nil, false)
	plan := executionPlan(t, state, "socks5.uri")
	if err := os.WriteFile(configPath, plan.Candidate(), 0o600); err != nil {
		t.Fatal(err)
	}
	store := executorStore(t)
	protector := executorProtector(t)
	executor := Executor{Runner: &scriptedRunner{t: t, steps: []runnerStep{{name: "systemctl", contains: []string{"stop " + ownedServiceName}}}}, Journal: store, Protector: protector, ConfigPath: configPath}
	snapshot, err := executor.protectJournal("singbox_recover", "snapshot", []byte(`{"exists":false,"content":null}`))
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := executor.protectJournal("singbox_recover", "candidate", plan.Candidate())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	operation := domain.Transaction{ID: "singbox_recover", Operation: "singbox_apply", State: domain.TransactionPrepared, RequestedChange: "apply 1 enabled sing-box outbounds", PreviousSnapshot: snapshot, CandidateConfig: candidate, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionOperation(context.Background(), operation.ID, domain.TransactionPrepared, domain.TransactionValidated, now, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionOperation(context.Background(), operation.ID, domain.TransactionValidated, domain.TransactionApplying, now, ""); err != nil {
		t.Fatal(err)
	}
	if err := executor.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("interrupted candidate remains: %v", err)
	}
	loaded, _ := store.Operation(context.Background(), operation.ID)
	if loaded.State != domain.TransactionRolledBack || loaded.FailureDetail != "interrupted_operation" {
		t.Fatalf("operation = %#v", loaded)
	}
}
