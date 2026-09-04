package xray

import (
	"bytes"
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
	"github.com/egress-manager/egress-manager/internal/secrets"
	"github.com/egress-manager/egress-manager/internal/system"
)

type xrayRunnerStep struct {
	name     string
	contains []string
	err      error
}

type xrayScriptedRunner struct {
	t       *testing.T
	steps   []xrayRunnerStep
	current int
}

func (runner *xrayScriptedRunner) Run(_ context.Context, command system.Command) (system.Result, error) {
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

func TestFragmentExecutorValidatesInstallsRestartsVerifiesAndCommits(t *testing.T) {
	t.Parallel()
	installation, plan := executableFragmentPlan(t)
	runner := &xrayScriptedRunner{t: t, steps: []xrayRunnerStep{
		{name: "xray", contains: []string{"run -test -confdir"}},
		{name: "systemctl", contains: []string{"restart xray.service"}},
		{name: "xray", contains: []string{"run -test -confdir"}},
		{name: "systemctl", contains: []string{"is-active --quiet xray.service"}},
	}}
	store := xrayExecutorStore(t)
	executor := FragmentExecutor{Runner: runner, Journal: store, Protector: xrayExecutorProtector(t), Installation: installation}
	response, err := executor.Execute(context.Background(), "xray_success", plan)
	if err != nil {
		t.Fatal(err)
	}
	if response.State != domain.TransactionCommitted || response.CandidateHash != plan.Review.CandidateHash {
		t.Fatalf("response = %#v", response)
	}
	installed, err := os.ReadFile(installation.ManagedPath)
	if err != nil || !bytes.Equal(installed, plan.Candidate()) {
		t.Fatalf("installed fragment mismatch, err = %v", err)
	}
	operation, err := store.Operation(context.Background(), "xray_success")
	if err != nil || operation.State != domain.TransactionCommitted || strings.Contains(operation.PreviousSnapshot, "in-a") || strings.Contains(operation.CandidateConfig, "in-a") {
		t.Fatalf("operation = %#v, err = %v", operation, err)
	}
}

func TestFragmentExecutorRejectsStalePlanBeforeJournal(t *testing.T) {
	t.Parallel()
	installation, plan := executableFragmentPlan(t)
	writeTestFile(t, installation.ConfigPath, `{"inbounds":[{"tag":"changed"}],"outbounds":[{"tag":"direct"}]}`)
	store := xrayExecutorStore(t)
	_, err := (FragmentExecutor{Runner: &xrayScriptedRunner{t: t}, Journal: store, Protector: xrayExecutorProtector(t), Installation: installation}).Execute(context.Background(), "xray_stale", plan)
	if !errors.Is(err, ErrXrayStateChanged) {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, loadErr := store.Operation(context.Background(), "xray_stale"); !errors.Is(loadErr, database.ErrNotFound) {
		t.Fatalf("stale operation was journaled: %v", loadErr)
	}
}

func TestFragmentExecutorRestartFailureRestoresPreviousAbsence(t *testing.T) {
	t.Parallel()
	installation, plan := executableFragmentPlan(t)
	runner := &xrayScriptedRunner{t: t, steps: []xrayRunnerStep{
		{name: "xray"},
		{name: "systemctl", err: errors.New("restart failed")},
		{name: "xray"},
		{name: "systemctl", contains: []string{"restart xray.service"}},
		{name: "xray"},
		{name: "systemctl", contains: []string{"is-active --quiet xray.service"}},
	}}
	store := xrayExecutorStore(t)
	_, err := (FragmentExecutor{Runner: runner, Journal: store, Protector: xrayExecutorProtector(t), Installation: installation}).Execute(context.Background(), "xray_rollback", plan)
	if err == nil || !strings.Contains(err.Error(), "restart failed") {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, statErr := os.Stat(installation.ManagedPath); !os.IsNotExist(statErr) {
		t.Fatalf("fragment remains after rollback: %v", statErr)
	}
	operation, _ := store.Operation(context.Background(), "xray_rollback")
	if operation.State != domain.TransactionRolledBack || operation.FailureDetail != "restart_failed" {
		t.Fatalf("operation = %#v", operation)
	}
}

func TestFragmentExecutorRecoversInterruptedApply(t *testing.T) {
	t.Parallel()
	installation, plan := executableFragmentPlan(t)
	writeTestFile(t, installation.ManagedPath, string(plan.Candidate()))
	store := xrayExecutorStore(t)
	protector := xrayExecutorProtector(t)
	executor := FragmentExecutor{Runner: &xrayScriptedRunner{t: t, steps: []xrayRunnerStep{
		{name: "xray"}, {name: "systemctl"}, {name: "xray"}, {name: "systemctl"},
	}}, Journal: store, Protector: protector, Installation: installation}
	snapshot := fragmentRecoverySnapshot{Exists: false, ForeignHash: plan.Review.ForeignStateHash}
	snapshotBytes, _ := jsonMarshal(snapshot)
	protectedSnapshot, err := executor.protect("xray_recover", "snapshot", snapshotBytes)
	if err != nil {
		t.Fatal(err)
	}
	protectedCandidate, err := executor.protect("xray_recover", "candidate", plan.Candidate())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	operation := domain.Transaction{ID: "xray_recover", Operation: "xray_fragment_apply", State: domain.TransactionPrepared, RequestedChange: "apply 1 native Xray route bindings", PreviousSnapshot: protectedSnapshot, CandidateConfig: protectedCandidate, CreatedAt: now, UpdatedAt: now}
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
	if _, err := os.Stat(installation.ManagedPath); !os.IsNotExist(err) {
		t.Fatalf("interrupted fragment remains: %v", err)
	}
	loaded, _ := store.Operation(context.Background(), operation.ID)
	if loaded.State != domain.TransactionRolledBack || loaded.FailureDetail != "interrupted_operation" {
		t.Fatalf("operation = %#v", loaded)
	}
}

func executableFragmentPlan(t *testing.T) (Installation, FragmentExecutionPlan) {
	t.Helper()
	root := t.TempDir()
	config := filepath.Join(root, "config.json")
	writeTestFile(t, config, `{"inbounds":[{"tag":"in-a"}],"outbounds":[{"tag":"direct"}],"routing":{"rules":[]}}`)
	installation := tempManagedInstallation(root, config)
	installation.XrayExecutable = "xray"
	snapshot, err := SnapshotConfdir(installation)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildFragmentPlan(snapshot.InstallationWithEffectiveTags(installation), []Binding{{ID: "native_route", InboundTag: "in-a", OutboundTag: "direct", Enabled: true}}, snapshot.Foreign, snapshot.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	return installation, plan
}

func xrayExecutorStore(t *testing.T) *database.Store {
	t.Helper()
	connection, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "xray.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return database.NewStore(connection)
}

func xrayExecutorProtector(t *testing.T) *secrets.Protector {
	t.Helper()
	protector, err := secrets.NewProtector([32]byte{8, 7, 6, 5}, bytes.NewReader(bytes.Repeat([]byte{4}, 4096)))
	if err != nil {
		t.Fatal(err)
	}
	return protector
}

func jsonMarshal(value any) ([]byte, error) { return json.Marshal(value) }
