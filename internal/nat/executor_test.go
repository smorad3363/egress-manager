package nat

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/system"
)

type runnerStep struct {
	command string
	stdin   string
	result  system.Result
	err     error
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
	actual := command.Name + " " + strings.Join(command.Args, " ")
	if actual != step.command {
		runner.t.Fatalf("command = %q, want %q", actual, step.command)
	}
	if step.stdin != "" && !strings.Contains(string(command.Stdin), step.stdin) {
		runner.t.Fatalf("stdin omitted %q:\n%s", step.stdin, command.Stdin)
	}
	return step.result, step.err
}

func (runner *scriptedRunner) assertDone() {
	runner.t.Helper()
	if runner.current != len(runner.steps) {
		runner.t.Fatalf("executed %d commands, want %d", runner.current, len(runner.steps))
	}
}

type fakeVerifier struct {
	err   error
	calls int
}

func (verifier *fakeVerifier) Verify(context.Context, Plan) error {
	verifier.calls++
	return verifier.err
}

func testExecutorStore(t *testing.T) *database.Store {
	t.Helper()
	connection, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "nat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return database.NewStore(connection)
}

func executableTestPlan(t *testing.T) Plan {
	t.Helper()
	forward := testForward()
	forward.RemotePortStart = 0
	plan, err := BuildNFTPlan(IPv4, []domain.PortForward{forward}, nil, testPolicy(), false)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestExecutorCommitsValidatedVerifiedPlan(t *testing.T) {
	t.Parallel()

	store := testExecutorStore(t)
	plan := executableTestPlan(t)
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{command: "nft list tables", result: system.Result{ExitCode: 0}},
		{command: "nft --check --file -", stdin: "table ip egm_nat4", result: system.Result{ExitCode: 0}},
		{command: "nft --file -", stdin: "table ip egm_nat4", result: system.Result{ExitCode: 0}},
	}}
	verifier := &fakeVerifier{}
	executor := Executor{Runner: runner, Journal: store, Verifier: verifier, Now: func() time.Time { return time.Unix(1_700_000_000, 0) }}
	if err := executor.Execute(context.Background(), "nat_success", `{}`, plan); err != nil {
		t.Fatal(err)
	}
	operation, err := store.Operation(context.Background(), "nat_success")
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != domain.TransactionCommitted || verifier.calls != 1 {
		t.Fatalf("state = %s, verifier calls = %d", operation.State, verifier.calls)
	}
	runner.assertDone()
}

func TestExecutorRollsBackVerificationFailure(t *testing.T) {
	t.Parallel()

	store := testExecutorStore(t)
	plan := executableTestPlan(t)
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{command: "nft list tables", result: system.Result{ExitCode: 0}},
		{command: "nft --check --file -", result: system.Result{ExitCode: 0}},
		{command: "nft --file -", result: system.Result{ExitCode: 0}},
		{command: "nft list tables", result: system.Result{Stdout: []byte("table ip egm_nat4\n"), ExitCode: 0}},
		{command: "nft list table ip egm_nat4", result: system.Result{Stdout: []byte("table ip egm_nat4 {}\n"), ExitCode: 0}},
		{command: "nft --check --file -", stdin: "delete table ip egm_nat4", result: system.Result{ExitCode: 0}},
		{command: "nft --file -", stdin: "delete table ip egm_nat4", result: system.Result{ExitCode: 0}},
	}}
	verifier := &fakeVerifier{err: errors.New("remote unreachable")}
	executor := Executor{Runner: runner, Journal: store, Verifier: verifier}
	if err := executor.Execute(context.Background(), "nat_rollback", `{}`, plan); err == nil || !strings.Contains(err.Error(), "remote unreachable") {
		t.Fatalf("Execute() error = %v", err)
	}
	operation, err := store.Operation(context.Background(), "nat_rollback")
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != domain.TransactionRolledBack || operation.FailureDetail != "verification_failed" {
		t.Fatalf("operation = %#v", operation)
	}
	runner.assertDone()
}

func TestExecutorNativeValidationFailureDoesNotApply(t *testing.T) {
	t.Parallel()

	store := testExecutorStore(t)
	plan := executableTestPlan(t)
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{command: "nft list tables", result: system.Result{ExitCode: 0}},
		{command: "nft --check --file -", result: system.Result{ExitCode: 1}, err: errors.New("invalid candidate")},
	}}
	executor := Executor{Runner: runner, Journal: store, Verifier: &fakeVerifier{}}
	if err := executor.Execute(context.Background(), "nat_invalid", `{}`, plan); err == nil {
		t.Fatal("Execute() accepted failed native validation")
	}
	operation, err := store.Operation(context.Background(), "nat_invalid")
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != domain.TransactionFailed || operation.FailureDetail != "native_validation_failed" {
		t.Fatalf("operation = %#v", operation)
	}
	runner.assertDone()
}

func TestExecutorRecoversInterruptedApplyAfterRestart(t *testing.T) {
	t.Parallel()

	store := testExecutorStore(t)
	plan := executableTestPlan(t)
	now := time.Now().UTC().Truncate(time.Second)
	operation := domain.Transaction{
		ID: "nat_restart", Operation: "nat_apply", State: domain.TransactionPrepared,
		RequestedChange: `{}`, PreviousSnapshot: "absent", CandidateConfig: plan.Candidate,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionOperation(context.Background(), operation.ID, domain.TransactionPrepared, domain.TransactionValidated, now, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionOperation(context.Background(), operation.ID, domain.TransactionValidated, domain.TransactionApplying, now, ""); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{command: "nft list tables", result: system.Result{Stdout: []byte("table ip egm_nat4\n"), ExitCode: 0}},
		{command: "nft list table ip egm_nat4", result: system.Result{Stdout: []byte("table ip egm_nat4 {}\n"), ExitCode: 0}},
		{command: "nft --check --file -", stdin: "delete table ip egm_nat4", result: system.Result{ExitCode: 0}},
		{command: "nft --file -", stdin: "delete table ip egm_nat4", result: system.Result{ExitCode: 0}},
	}}
	executor := Executor{Runner: runner, Journal: store}
	if err := executor.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Operation(context.Background(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != domain.TransactionRolledBack || loaded.FailureDetail != "interrupted_operation" {
		t.Fatalf("recovered operation = %#v", loaded)
	}
	runner.assertDone()
}

func TestSystemVerifierRejectsUnreachableRemote(t *testing.T) {
	t.Parallel()

	plan := executableTestPlan(t)
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{command: "nft list table ip egm_nat4", result: system.Result{ExitCode: 0}},
		{command: "ip route get 10.10.0.5", result: system.Result{ExitCode: 2}, err: fmt.Errorf("unreachable")},
	}}
	verifier := SystemVerifier{Runner: runner}
	if err := verifier.Verify(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("Verify() error = %v", err)
	}
	runner.assertDone()
}
