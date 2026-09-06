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

// BuildAppliedPlan reconstructs a read-only plan from the last applied files.
// It never reads pending database desired state.
func (executor Executor) BuildAppliedPlan(ctx context.Context, host inventory.Inventory, protected []netip.Prefix) (Plan, bool, error) {
	if err := executor.validate(); err != nil {
		return Plan{}, false, err
	}
	routeFile, err := inspectOwnedFile(executor.RoutingStatePath, func(content []byte, exists bool) error { _, err := routing.ParseState(content, exists); return err })
	if err != nil {
		return Plan{}, false, err
	}
	if !routeFile.Exists {
		return Plan{}, false, nil
	}
	singBoxFile, err := inspectOwnedFile(executor.SingBoxConfigPath, func(content []byte, exists bool) error { _, err := singbox.ParseState(content, exists); return err })
	if err != nil {
		return Plan{}, false, err
	}
	if !singBoxFile.Exists {
		return Plan{}, false, fmt.Errorf("applied route configuration requires its sing-box snapshot")
	}
	desired, err := routing.BuildReconciliationPlan(routeFile.Content, host, protected)
	if err != nil {
		return Plan{}, false, err
	}
	singBox, err := singbox.BuildReconciliationPlan(singBoxFile.Content)
	if err != nil {
		return Plan{}, false, err
	}
	interfaces, err := managedInterface.InspectState(executor.InterfaceStatePath, executor.InterfaceRuntimeDirectory)
	if err != nil {
		return Plan{}, false, err
	}
	table, err := executor.inspectNFT(ctx)
	if err != nil {
		return Plan{}, false, err
	}
	native, err := routing.BuildNativePlan(desired, table.Exists)
	if err != nil {
		return Plan{}, false, err
	}
	plan, err := BuildPlan(singBox, desired, native, interfaces)
	if err != nil {
		return Plan{}, false, err
	}
	return plan, true, nil
}

// ReconcileApplied restores only a previously applied route configuration. The
// caller must hold the global mutation lease and finish journal recovery first.
func (executor Executor) ReconcileApplied(ctx context.Context, id domain.ID, host inventory.Inventory, protected []netip.Prefix) error {
	plan, exists, err := executor.BuildAppliedPlan(ctx, host, protected)
	if err != nil || !exists {
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

// ResumeApplied deliberately exits emergency bypass and restores the last
// applied configuration. The caller must hold the global mutation lease.
func (executor Executor) ResumeApplied(ctx context.Context, id domain.ID, host inventory.Inventory, protected []netip.Prefix) error {
	status, err := executor.BypassStatus()
	if err != nil {
		return err
	}
	plan, exists, err := executor.BuildAppliedPlan(ctx, host, protected)
	if err != nil {
		return err
	}
	if !exists {
		if status.Active {
			return executor.DeactivateBypass()
		}
		return nil
	}
	if status.Active {
		if err := executor.DeactivateBypass(); err != nil {
			return err
		}
	}
	if executor.VerifyRuntime(ctx, plan) == nil {
		return nil
	}
	if _, err := executor.Execute(ctx, id, plan); err != nil {
		return err
	}
	return executor.VerifyRuntime(ctx, plan)
}
