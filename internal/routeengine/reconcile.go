package routeengine

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/egress-manager/egress-manager/internal/domain"
	managedInterface "github.com/egress-manager/egress-manager/internal/interfaceoutbound"
	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/routing"
	"github.com/egress-manager/egress-manager/internal/singbox"
)

// ReconcileApplied restores only a previously applied route configuration. The
// caller must hold the global mutation lease and finish journal recovery first.
func (executor Executor) ReconcileApplied(ctx context.Context, id domain.ID, host inventory.Inventory, protected []netip.Prefix) error {
	if err := executor.validate(); err != nil {
		return err
	}
	routeFile, err := inspectOwnedFile(executor.RoutingStatePath, func(content []byte, exists bool) error { _, err := routing.ParseState(content, exists); return err })
	if err != nil {
		return err
	}
	if !routeFile.Exists {
		return nil
	}
	singBoxFile, err := inspectOwnedFile(executor.SingBoxConfigPath, func(content []byte, exists bool) error { _, err := singbox.ParseState(content, exists); return err })
	if err != nil {
		return err
	}
	if !singBoxFile.Exists {
		return fmt.Errorf("applied route configuration requires its sing-box snapshot")
	}
	desired, err := routing.BuildReconciliationPlan(routeFile.Content, host, protected)
	if err != nil {
		return err
	}
	singBox, err := singbox.BuildReconciliationPlan(singBoxFile.Content)
	if err != nil {
		return err
	}
	interfaces, err := managedInterface.InspectState(executor.InterfaceStatePath, executor.InterfaceRuntimeDirectory)
	if err != nil {
		return err
	}
	table, err := executor.inspectNFT(ctx)
	if err != nil {
		return err
	}
	native, err := routing.BuildNativePlan(desired, table.Exists)
	if err != nil {
		return err
	}
	plan, err := BuildPlan(singBox, desired, native, interfaces)
	if err != nil {
		return err
	}
	if executor.VerifyRuntime(ctx, plan) == nil {
		return nil
	}
	if _, err := executor.Execute(ctx, id, plan); err != nil {
		return err
	}
	// Verify the full leak-control expressions, not just the table's existence.
	return executor.VerifyRuntime(ctx, plan)
}
