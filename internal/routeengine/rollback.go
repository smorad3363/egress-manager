package routeengine

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/routing"
)

func (executor Executor) RollbackCommitted(ctx context.Context, operation domain.Transaction, host inventory.Inventory, protected []netip.Prefix) error {
	if err := executor.validate(); err != nil {
		return err
	}
	if operation.State != domain.TransactionCommitted || (operation.Operation != "route_engine_apply" && operation.Operation != "route_engine_bypass") {
		return fmt.Errorf("route-engine operation is not eligible for rollback")
	}
	if operation.Operation == "route_engine_bypass" {
		return executor.rollbackCommittedBypass(ctx, operation, host, protected)
	}
	bypass, err := executor.BypassStatus()
	if err != nil {
		return err
	}
	if bypass.Active {
		return BypassActiveError{}
	}
	snapshot, err := executor.openSnapshot(operation)
	if err != nil {
		return err
	}
	candidate, err := executor.openCandidate(operation)
	if err != nil {
		return err
	}
	current, err := inspectOwnedFile(executor.RoutingStatePath, func(content []byte, exists bool) error {
		_, parseErr := routing.ParseState(content, exists)
		return parseErr
	})
	if err != nil {
		return err
	}
	if !current.Exists || !bytes.Equal(current.Content, candidate) {
		return ErrStateChanged
	}
	currentPlan, exists, err := executor.BuildAppliedPlan(ctx, host, protected)
	if err != nil {
		return err
	}
	if !exists || executor.VerifyRuntime(ctx, currentPlan) != nil {
		return ErrStateChanged
	}
	if snapshot.Routing.Exists {
		if _, err := routing.BuildReconciliationPlan(snapshot.Routing.Content, host, protected); err != nil {
			return fmt.Errorf("validate previous routing preconditions: %w", err)
		}
	}
	if err := executor.transition(ctx, operation.ID, domain.TransactionCommitted, domain.TransactionRollingBack, "operator_requested"); err != nil {
		return err
	}
	if err := executor.restore(ctx, candidate, snapshot); err != nil {
		return fmt.Errorf("restore committed route-engine snapshot: %w", err)
	}
	return executor.transition(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, "operator_requested")
}

func (executor Executor) rollbackCommittedBypass(ctx context.Context, operation domain.Transaction, host inventory.Inventory, protected []netip.Prefix) error {
	status, err := executor.BypassStatus()
	if err != nil {
		return err
	}
	if !status.Active || status.OperationID != operation.ID {
		return ErrStateChanged
	}
	snapshot, err := executor.openSnapshot(operation)
	if err != nil {
		return err
	}
	if err := executor.verifyBypass(ctx, snapshot.Routing); err != nil {
		return ErrStateChanged
	}
	if snapshot.Routing.Exists {
		if _, err := routing.BuildReconciliationPlan(snapshot.Routing.Content, host, protected); err != nil {
			return fmt.Errorf("validate previous routing preconditions: %w", err)
		}
	}
	if err := executor.transition(ctx, operation.ID, domain.TransactionCommitted, domain.TransactionRollingBack, "operator_requested"); err != nil {
		return err
	}
	if err := executor.restoreBypassRuntime(ctx, snapshot.Routing, snapshot.NFT); err != nil {
		return fmt.Errorf("restore committed bypass snapshot: %w", err)
	}
	if err := executor.DeactivateBypass(); err != nil {
		return err
	}
	return executor.transition(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, "operator_requested")
}
