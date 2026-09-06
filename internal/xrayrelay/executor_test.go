package xrayrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
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

type relayRunnerStep struct {
	name     string
	contains []string
	exitCode int
	err      error
}

type relayScriptedRunner struct {
	t       *testing.T
	steps   []relayRunnerStep
	current int
}

func (runner *relayScriptedRunner) Run(_ context.Context, command system.Command) (system.Result, error) {
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
	exitCode := step.exitCode
	if step.err != nil && exitCode == 0 {
		exitCode = 1
	}
	return system.Result{ExitCode: exitCode}, step.err
}

func relayExecutorStore(t *testing.T) *database.Store {
	t.Helper()
	connection, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "relay-executor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return database.NewStore(connection)
}

func relayExecutorProtector(t *testing.T) *secrets.Protector {
	t.Helper()
	protector, err := secrets.NewProtector([32]byte{9, 8, 7, 6}, bytes.NewReader(bytes.Repeat([]byte{0x44}, 8192)))
	if err != nil {
		t.Fatal(err)
	}
	return protector
}

func relayExecutionPlan(t *testing.T, state State) ExecutionPlan {
	t.Helper()
	parsed, err := ParseImport(syntheticVLESSURI)
	if err != nil {
		t.Fatal(err)
	}
	relay := domain.Relay{
		ID: "ssh_gateway", Name: "SSH gateway", ListenAddress: "127.0.0.1", ListenPort: 6111, Network: domain.RelayTCP,
		Destination: domain.Endpoint{Host: "91.107.220.12", Port: 6111}, OutboundID: parsed.Outbound.ID,
		SourceCIDRs: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, Enabled: true,
	}
	plan, err := BuildPlan(Settings{ProtectedPorts: []uint16{22, 47604}}, []domain.Relay{relay}, []domain.Outbound{parsed.Outbound}, map[domain.ID][]byte{parsed.Outbound.ID: parsed.CredentialDocument}, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestExecutorValidatesInstallsRestartsCommitsAndProtectsJournal(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "xray-relay.json")
	state, _ := ParseState(nil, false)
	plan := relayExecutionPlan(t, state)
	runner := &relayScriptedRunner{t: t, steps: []relayRunnerStep{
		{name: "xray", contains: []string{"run -test -config"}},
		{name: "systemctl", contains: []string{"restart " + ownedServiceName}},
		{name: "xray", contains: []string{"run -test -config"}},
		{name: "systemctl", contains: []string{"is-active --quiet " + ownedServiceName}},
	}}
	store := relayExecutorStore(t)
	executor := Executor{Runner: runner, Journal: store, Protector: relayExecutorProtector(t), ConfigPath: configPath}
	response, err := executor.Execute(context.Background(), "relay_success", plan)
	if err != nil {
		t.Fatal(err)
	}
	if response.State != domain.TransactionCommitted || response.CandidateHash != plan.Review.CandidateHash {
		t.Fatalf("response = %#v", response)
	}
	installed, err := os.ReadFile(configPath)
	if err != nil || !bytes.Equal(installed, plan.Candidate()) {
		t.Fatalf("installed candidate mismatch, error = %v", err)
	}
	operation, err := store.Operation(context.Background(), "relay_success")
	if err != nil || operation.State != domain.TransactionCommitted {
		t.Fatalf("operation = %#v, error = %v", operation, err)
	}
	for _, value := range []string{operation.RequestedChange, operation.PreviousSnapshot, operation.CandidateConfig, operation.FailureDetail} {
		for _, secret := range []string{"11111111-2222-4333-8444-555555555555", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "0123456789abcdef"} {
			if strings.Contains(value, secret) {
				t.Fatalf("operation journal leaked credential %q", secret)
			}
		}
	}
	if runner.current != len(runner.steps) {
		t.Fatalf("runner consumed %d of %d steps", runner.current, len(runner.steps))
	}
}

func TestExecutorRejectsStaleOwnedStateBeforeMutation(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "xray-relay.json")
	state, _ := ParseState(nil, false)
	plan := relayExecutionPlan(t, state)
	if err := os.WriteFile(configPath, plan.Candidate(), 0o600); err != nil {
		t.Fatal(err)
	}
	store := relayExecutorStore(t)
	_, err := (Executor{Runner: &relayScriptedRunner{t: t}, Journal: store, Protector: relayExecutorProtector(t), ConfigPath: configPath}).Execute(context.Background(), "relay_stale", plan)
	if !errors.Is(err, ErrStateChanged) {
		t.Fatalf("Execute() error = %v, want ErrStateChanged", err)
	}
	if _, err := store.Operation(context.Background(), "relay_stale"); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("stale execution created operation: %v", err)
	}
}

func TestExecutorNativeValidationFailureDoesNotInstall(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "xray-relay.json")
	state, _ := ParseState(nil, false)
	plan := relayExecutionPlan(t, state)
	runner := &relayScriptedRunner{t: t, steps: []relayRunnerStep{{name: "xray", contains: []string{"run -test -config"}, err: errors.New("invalid Xray candidate")}}}
	store := relayExecutorStore(t)
	_, err := (Executor{Runner: runner, Journal: store, Protector: relayExecutorProtector(t), ConfigPath: configPath}).Execute(context.Background(), "relay_invalid", plan)
	if err == nil || !strings.Contains(err.Error(), "invalid Xray candidate") {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("candidate installed after validation failure: %v", statErr)
	}
	operation, _ := store.Operation(context.Background(), "relay_invalid")
	if operation.State != domain.TransactionFailed || operation.FailureDetail != "native_validation_failed" {
		t.Fatalf("operation = %#v", operation)
	}
}

func TestExecutorRestartFailureRollsBackInitialApply(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "xray-relay.json")
	state, _ := ParseState(nil, false)
	plan := relayExecutionPlan(t, state)
	runner := &relayScriptedRunner{t: t, steps: []relayRunnerStep{
		{name: "xray", contains: []string{"run -test -config"}},
		{name: "systemctl", contains: []string{"restart " + ownedServiceName}, err: errors.New("restart failed")},
		{name: "systemctl", contains: []string{"stop " + ownedServiceName}},
		{name: "systemctl", contains: []string{"is-active --quiet " + ownedServiceName}, exitCode: 3, err: errors.New("inactive")},
	}}
	store := relayExecutorStore(t)
	_, err := (Executor{Runner: runner, Journal: store, Protector: relayExecutorProtector(t), ConfigPath: configPath}).Execute(context.Background(), "relay_rollback", plan)
	if err == nil || !strings.Contains(err.Error(), "restart failed") {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("candidate remains after rollback: %v", statErr)
	}
	operation, _ := store.Operation(context.Background(), "relay_rollback")
	if operation.State != domain.TransactionRolledBack || operation.FailureDetail != "restart_failed" {
		t.Fatalf("operation = %#v", operation)
	}
}

func TestExecutorRecoversInterruptedInitialApply(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "xray-relay.json")
	state, _ := ParseState(nil, false)
	plan := relayExecutionPlan(t, state)
	if err := os.WriteFile(configPath, plan.Candidate(), 0o600); err != nil {
		t.Fatal(err)
	}
	store := relayExecutorStore(t)
	protector := relayExecutorProtector(t)
	runner := &relayScriptedRunner{t: t, steps: []relayRunnerStep{
		{name: "systemctl", contains: []string{"stop " + ownedServiceName}},
		{name: "systemctl", contains: []string{"is-active --quiet " + ownedServiceName}, exitCode: 3, err: errors.New("inactive")},
	}}
	executor := Executor{Runner: runner, Journal: store, Protector: protector, ConfigPath: configPath}
	snapshot, err := executor.protect("relay_recover", "snapshot", []byte(`{"exists":false,"content":null}`))
	if err != nil {
		t.Fatal(err)
	}
	candidateDocument, err := json.Marshal(protectedCandidate{Exists: true, Content: plan.Candidate()})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := executor.protect("relay_recover", "candidate", candidateDocument)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	operation := domain.Transaction{ID: "relay_recover", Operation: "xray_relay_apply", State: domain.TransactionPrepared, RequestedChange: "apply 1 enabled Xray listener relays", PreviousSnapshot: snapshot, CandidateConfig: candidate, CreatedAt: now, UpdatedAt: now}
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

func TestExecutorManualRollbackRestoresCommittedSnapshot(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "xray-relay.json")
	state, _ := ParseState(nil, false)
	plan := relayExecutionPlan(t, state)
	runner := &relayScriptedRunner{t: t, steps: []relayRunnerStep{
		{name: "xray", contains: []string{"run -test -config"}},
		{name: "systemctl", contains: []string{"restart " + ownedServiceName}},
		{name: "xray", contains: []string{"run -test -config"}},
		{name: "systemctl", contains: []string{"is-active --quiet " + ownedServiceName}},
		{name: "xray", contains: []string{"run -test -config"}},
		{name: "systemctl", contains: []string{"is-active --quiet " + ownedServiceName}},
		{name: "systemctl", contains: []string{"stop " + ownedServiceName}},
		{name: "systemctl", contains: []string{"is-active --quiet " + ownedServiceName}, exitCode: 3, err: errors.New("inactive")},
	}}
	store := relayExecutorStore(t)
	executor := Executor{Runner: runner, Journal: store, Protector: relayExecutorProtector(t), ConfigPath: configPath}
	if _, err := executor.Execute(context.Background(), "relay_manual", plan); err != nil {
		t.Fatal(err)
	}
	operation, err := store.Operation(context.Background(), "relay_manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.RollbackCommitted(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("committed candidate remains after rollback: %v", err)
	}
	rolledBack, _ := store.Operation(context.Background(), operation.ID)
	if rolledBack.State != domain.TransactionRolledBack || rolledBack.FailureDetail != "operator_requested" {
		t.Fatalf("operation = %#v", rolledBack)
	}
}
