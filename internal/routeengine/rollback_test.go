package routeengine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

func TestRollbackCommittedRestoresAuthenticatedRouteSnapshot(t *testing.T) {
	t.Parallel()
	plan := coordinatedTestPlan(t)
	store, _ := routeEngineStore(t)
	directory := t.TempDir()
	runner := &routeEngineRunner{t: t}
	executor := Executor{Runner: runner, Journal: store, Protector: routeEngineProtector(t), SingBoxConfigPath: filepath.Join(directory, "sing-box.json"), RoutingStatePath: filepath.Join(directory, "routing.json"), InterfaceStatePath: filepath.Join(directory, "interfaces", "state.json"), InterfaceRuntimeDirectory: filepath.Join(directory, "interfaces")}
	ctx := context.Background()
	if _, err := executor.Execute(ctx, "rollback_route", plan); err != nil {
		t.Fatal(err)
	}
	operation, err := store.Operation(ctx, "rollback_route")
	if err != nil {
		t.Fatal(err)
	}
	host := inventory.Inventory{Interfaces: []inventory.Interface{{Name: "tun0", State: "up", MTU: 1500, Addresses: []inventory.Address{{Family: "inet", CIDR: "10.8.0.1/24", Scope: "global"}}}}}
	if err := executor.RollbackCommitted(ctx, operation, host, nil); err != nil {
		t.Fatal(err)
	}
	operation, err = store.Operation(ctx, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != domain.TransactionRolledBack || runner.nftInstalled || runner.ipv4Installed || runner.ipv6Installed {
		t.Fatalf("operation=%#v runtime=%t/%t/%t", operation, runner.nftInstalled, runner.ipv4Installed, runner.ipv6Installed)
	}
	if _, err := os.Stat(executor.RoutingStatePath); !os.IsNotExist(err) {
		t.Fatalf("routing snapshot was not restored to absence: %v", err)
	}
}

func TestRollbackCommittedRejectsChangedCurrentStateBeforeMutation(t *testing.T) {
	t.Parallel()
	plan := coordinatedTestPlan(t)
	store, _ := routeEngineStore(t)
	directory := t.TempDir()
	runner := &routeEngineRunner{t: t}
	executor := Executor{Runner: runner, Journal: store, Protector: routeEngineProtector(t), SingBoxConfigPath: filepath.Join(directory, "sing-box.json"), RoutingStatePath: filepath.Join(directory, "routing.json"), InterfaceStatePath: filepath.Join(directory, "interfaces", "state.json"), InterfaceRuntimeDirectory: filepath.Join(directory, "interfaces")}
	ctx := context.Background()
	if _, err := executor.Execute(ctx, "stale_rollback_route", plan); err != nil {
		t.Fatal(err)
	}
	operation, err := store.Operation(ctx, "stale_rollback_route")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executor.RoutingStatePath, []byte(`{"schema":"egress-manager/routing/v1","routes":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := executor.RollbackCommitted(ctx, operation, inventory.Inventory{}, nil); err == nil {
		t.Fatal("RollbackCommitted() accepted changed current state")
	}
	operation, err = store.Operation(ctx, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != domain.TransactionCommitted || !runner.nftInstalled || !runner.ipv4Installed || !runner.ipv6Installed {
		t.Fatalf("operation or runtime changed: %#v", operation)
	}
}

func TestRollbackCommittedBypassRestoresOwnedRuntime(t *testing.T) {
	t.Parallel()
	plan := coordinatedTestPlan(t)
	store, _ := routeEngineStore(t)
	directory := t.TempDir()
	runner := &routeEngineRunner{t: t}
	executor := Executor{Runner: runner, Journal: store, Protector: routeEngineProtector(t), SingBoxConfigPath: filepath.Join(directory, "sing-box.json"), RoutingStatePath: filepath.Join(directory, "routing.json"), InterfaceStatePath: filepath.Join(directory, "interfaces", "state.json"), InterfaceRuntimeDirectory: filepath.Join(directory, "interfaces")}
	ctx := context.Background()
	if _, err := executor.Execute(ctx, "route_before_bypass_rollback", plan); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Bypass(ctx, "rollback_bypass"); err != nil {
		t.Fatal(err)
	}
	operation, err := store.Operation(ctx, "rollback_bypass")
	if err != nil {
		t.Fatal(err)
	}
	host := inventory.Inventory{Interfaces: []inventory.Interface{{Name: "tun0", State: "up", MTU: 1500, Addresses: []inventory.Address{{Family: "inet", CIDR: "10.8.0.1/24", Scope: "global"}}}}}
	if err := executor.RollbackCommitted(ctx, operation, host, nil); err != nil {
		t.Fatal(err)
	}
	status, err := executor.BypassStatus()
	if err != nil || status.Active || !runner.nftInstalled || !runner.ipv4Installed || !runner.ipv6Installed {
		t.Fatalf("status=%#v runtime=%t/%t/%t error=%v", status, runner.nftInstalled, runner.ipv4Installed, runner.ipv6Installed, err)
	}
}
