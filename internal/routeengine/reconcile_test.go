package routeengine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egress-manager/egress-manager/internal/inventory"
)

func TestReconcileAppliedRestoresLostRuntimeAndSecondRunDoesNotApply(t *testing.T) {
	t.Parallel()
	plan := coordinatedTestPlan(t)
	store, _ := routeEngineStore(t)
	directory := t.TempDir()
	runner := &routeEngineRunner{t: t}
	executor := Executor{Runner: runner, Journal: store, Protector: routeEngineProtector(t), SingBoxConfigPath: filepath.Join(directory, "sing-box.json"), RoutingStatePath: filepath.Join(directory, "routing.json"), InterfaceStatePath: filepath.Join(directory, "interfaces", "state.json"), InterfaceRuntimeDirectory: filepath.Join(directory, "interfaces")}
	ctx := context.Background()
	if _, err := executor.Execute(ctx, "before_restart", plan); err != nil {
		t.Fatal(err)
	}
	runner.nftInstalled, runner.ipv4Installed, runner.ipv6Installed = false, false, false
	host := inventory.Inventory{Interfaces: []inventory.Interface{{Name: "tun0", State: "up", MTU: 1500, Addresses: []inventory.Address{{Family: "inet", CIDR: "10.8.0.1/24", Scope: "global"}}}}}
	if err := executor.ReconcileApplied(ctx, "after_restart", host, nil); err != nil {
		t.Fatal(err)
	}
	if !runner.nftInstalled || !runner.ipv4Installed || !runner.ipv6Installed {
		t.Fatal("runtime did not converge")
	}
	if err := executor.ReconcileApplied(ctx, "second_reconcile", host, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Operation(ctx, "second_reconcile"); err == nil {
		t.Fatal("healthy second reconciliation created an apply transaction")
	}
	// After a reboot the old NFT snapshot is absent. A failed reconciliation
	// must restore protection from persistent policy, not remove the kill switch.
	runner.nftInstalled, runner.ipv4Installed, runner.ipv6Installed = false, false, false
	runner.failIPv6 = true
	if err := executor.ReconcileApplied(ctx, "failed_restart", host, nil); err == nil {
		t.Fatal("injected runtime failure was accepted")
	}
	if !runner.nftInstalled || !runner.ipv4Installed || !runner.ipv6Installed {
		t.Fatal("rollback lost persistent routing or leak protection")
	}
	runner.nftInstalled, runner.ipv4Installed, runner.ipv6Installed = false, false, false
	runner.failNFTApply = true
	runner.requireNFTForIP = true
	if err := executor.ReconcileApplied(ctx, "failed_nft_restart", host, nil); err == nil {
		t.Fatal("injected nft failure was accepted")
	}
	if !runner.nftInstalled || !runner.ipv4Installed || !runner.ipv6Installed {
		t.Fatal("rollback did not restore leak protection before policy routing")
	}
}
