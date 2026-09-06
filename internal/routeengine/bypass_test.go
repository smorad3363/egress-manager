package routeengine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

func TestBypassDisablesOnlyOwnedRuntimeAndPreservesDesiredState(t *testing.T) {
	t.Parallel()
	plan := coordinatedTestPlan(t)
	store, _ := routeEngineStore(t)
	directory := t.TempDir()
	runner := &routeEngineRunner{t: t}
	executor := Executor{Runner: runner, Journal: store, Protector: routeEngineProtector(t), SingBoxConfigPath: filepath.Join(directory, "sing-box.json"), RoutingStatePath: filepath.Join(directory, "routing.json"), InterfaceStatePath: filepath.Join(directory, "interfaces", "state.json"), InterfaceRuntimeDirectory: filepath.Join(directory, "interfaces")}
	ctx := context.Background()
	if _, err := executor.Execute(ctx, "route_before_bypass", plan); err != nil {
		t.Fatal(err)
	}
	routingBefore, err := os.ReadFile(executor.RoutingStatePath)
	if err != nil {
		t.Fatal(err)
	}
	singBoxBefore, err := os.ReadFile(executor.SingBoxConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Bypass(ctx, "emergency_bypass")
	if err != nil {
		t.Fatal(err)
	}
	if response.State != domain.TransactionCommitted || response.AlreadyActive || runner.nftInstalled || runner.ipv4Installed || runner.ipv6Installed {
		t.Fatalf("response=%#v runtime=%t/%t/%t", response, runner.nftInstalled, runner.ipv4Installed, runner.ipv6Installed)
	}
	operation, err := store.Operation(ctx, "emergency_bypass")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(operation.PreviousSnapshot+operation.CandidateConfig+operation.RequestedChange, "user:pass") {
		t.Fatal("bypass journal exposed protected sing-box content")
	}
	status, err := executor.BypassStatus()
	if err != nil || !status.Active || status.OperationID != "emergency_bypass" {
		t.Fatalf("status=%#v error=%v", status, err)
	}
	routingAfter, _ := os.ReadFile(executor.RoutingStatePath)
	singBoxAfter, _ := os.ReadFile(executor.SingBoxConfigPath)
	if string(routingAfter) != string(routingBefore) || string(singBoxAfter) != string(singBoxBefore) {
		t.Fatal("bypass changed persistent desired/applied configuration")
	}
	response, err = executor.Bypass(ctx, "emergency_bypass_again")
	if err != nil {
		t.Fatal(err)
	}
	if !response.AlreadyActive || response.State != domain.TransactionCommitted {
		t.Fatalf("second response=%#v", response)
	}
	if _, err := store.Operation(ctx, "emergency_bypass_again"); err == nil {
		t.Fatal("idempotent bypass created another journal transaction")
	}
}

func TestResumeAppliedExitsBypassAndRestoresRuntime(t *testing.T) {
	t.Parallel()
	plan := coordinatedTestPlan(t)
	store, _ := routeEngineStore(t)
	directory := t.TempDir()
	runner := &routeEngineRunner{t: t}
	executor := Executor{Runner: runner, Journal: store, Protector: routeEngineProtector(t), SingBoxConfigPath: filepath.Join(directory, "sing-box.json"), RoutingStatePath: filepath.Join(directory, "routing.json"), InterfaceStatePath: filepath.Join(directory, "interfaces", "state.json"), InterfaceRuntimeDirectory: filepath.Join(directory, "interfaces")}
	ctx := context.Background()
	if _, err := executor.Execute(ctx, "route_before_resume", plan); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Bypass(ctx, "bypass_before_resume"); err != nil {
		t.Fatal(err)
	}
	host := inventory.Inventory{Interfaces: []inventory.Interface{{Name: "tun0", State: "up", MTU: 1500, Addresses: []inventory.Address{{Family: "inet", CIDR: "10.8.0.1/24", Scope: "global"}}}}}
	if err := executor.ResumeApplied(ctx, "resume_after_bypass", host, nil); err != nil {
		t.Fatal(err)
	}
	status, err := executor.BypassStatus()
	if err != nil || status.Active || !runner.nftInstalled || !runner.ipv4Installed || !runner.ipv6Installed {
		t.Fatalf("status=%#v runtime=%t/%t/%t error=%v", status, runner.nftInstalled, runner.ipv4Installed, runner.ipv6Installed, err)
	}
}

func TestBypassFailureRestoresPreviousRuntime(t *testing.T) {
	t.Parallel()
	plan := coordinatedTestPlan(t)
	store, _ := routeEngineStore(t)
	directory := t.TempDir()
	runner := &routeEngineRunner{t: t}
	executor := Executor{Runner: runner, Journal: store, Protector: routeEngineProtector(t), SingBoxConfigPath: filepath.Join(directory, "sing-box.json"), RoutingStatePath: filepath.Join(directory, "routing.json"), InterfaceStatePath: filepath.Join(directory, "interfaces", "state.json"), InterfaceRuntimeDirectory: filepath.Join(directory, "interfaces")}
	ctx := context.Background()
	if _, err := executor.Execute(ctx, "route_before_interruption", plan); err != nil {
		t.Fatal(err)
	}
	runner.failNFTApply = true
	if _, err := executor.Bypass(ctx, "interrupted_bypass"); err == nil {
		t.Fatal("Bypass() accepted interrupted nftables removal")
	}
	operation, err := store.Operation(ctx, "interrupted_bypass")
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != domain.TransactionRolledBack || !runner.nftInstalled || !runner.ipv4Installed || !runner.ipv6Installed {
		t.Fatalf("rolled-back operation=%#v runtime=%t/%t/%t", operation, runner.nftInstalled, runner.ipv4Installed, runner.ipv6Installed)
	}
	status, err := executor.BypassStatus()
	if err != nil || status.Active {
		t.Fatalf("status=%#v error=%v", status, err)
	}
}

func TestRecoverCompletesBypassInterruptedByCrash(t *testing.T) {
	t.Parallel()
	plan := coordinatedTestPlan(t)
	store, _ := routeEngineStore(t)
	directory := t.TempDir()
	runner := &routeEngineRunner{t: t}
	executor := Executor{Runner: runner, Journal: store, Protector: routeEngineProtector(t), SingBoxConfigPath: filepath.Join(directory, "sing-box.json"), RoutingStatePath: filepath.Join(directory, "routing.json"), InterfaceStatePath: filepath.Join(directory, "interfaces", "state.json"), InterfaceRuntimeDirectory: filepath.Join(directory, "interfaces")}
	ctx := context.Background()
	if _, err := executor.Execute(ctx, "route_before_crash", plan); err != nil {
		t.Fatal(err)
	}
	singBoxSnapshot, err := inspectOwnedFile(executor.SingBoxConfigPath, func(content []byte, exists bool) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	routingSnapshot, err := inspectOwnedFile(executor.RoutingStatePath, func(content []byte, exists bool) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	nftSnapshot, err := executor.inspectNFT(ctx)
	if err != nil {
		t.Fatal(err)
	}
	protected, err := executor.protectSnapshot("crashed_bypass", singBoxSnapshot, routingSnapshot, nftSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	protectedJSON, _ := json.Marshal(protected)
	candidateJSON, _ := json.Marshal(bypassCandidate{Scope: "egress-routing"})
	now := executor.now()
	operation := domain.Transaction{ID: "crashed_bypass", Operation: "route_engine_bypass", State: domain.TransactionPrepared, RequestedChange: "disable Egress Manager-owned routing and interception", PreviousSnapshot: string(protectedJSON), CandidateConfig: string(candidateJSON), CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionOperation(ctx, operation.ID, domain.TransactionPrepared, domain.TransactionValidated, now, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionOperation(ctx, operation.ID, domain.TransactionValidated, domain.TransactionApplying, now, ""); err != nil {
		t.Fatal(err)
	}
	if err := executor.activateBypass(operation.ID, now, false); err != nil {
		t.Fatal(err)
	}
	runner.ipv4Installed = false
	if err := executor.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	operation, err = store.Operation(ctx, "crashed_bypass")
	if err != nil {
		t.Fatal(err)
	}
	status, statusErr := executor.BypassStatus()
	if operation.State != domain.TransactionCommitted || statusErr != nil || !status.Active || runner.nftInstalled || runner.ipv4Installed || runner.ipv6Installed {
		t.Fatalf("operation=%#v status=%#v error=%v", operation, status, statusErr)
	}
}

func TestBypassStateRejectsCorruptionAndCanBeDeactivated(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	executor := Executor{RoutingStatePath: filepath.Join(directory, "routing.json")}
	if err := os.WriteFile(executor.BypassPath(), []byte(`{"schema":"egress-manager/bypass/v1","active":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.BypassStatus(); err == nil {
		t.Fatal("BypassStatus() accepted corrupt state")
	}
	if err := os.Remove(executor.BypassPath()); err != nil {
		t.Fatal(err)
	}
	if err := executor.activateBypass("bypass_state", executor.now(), false); err != nil {
		t.Fatal(err)
	}
	if err := executor.DeactivateBypass(); err != nil {
		t.Fatal(err)
	}
	status, err := executor.BypassStatus()
	if err != nil || status.Active {
		t.Fatalf("status=%#v error=%v", status, err)
	}
}
