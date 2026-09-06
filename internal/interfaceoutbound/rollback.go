package interfaceoutbound

import (
	"context"
	"fmt"
	"reflect"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func (executor Executor) RollbackCommitted(ctx context.Context, operation domain.Transaction) error {
	if err := executor.validate(); err != nil {
		return err
	}
	if operation.Operation != "interface_outbound_apply" || operation.State != domain.TransactionCommitted {
		return fmt.Errorf("interface outbound operation is not eligible for rollback")
	}
	previous, err := executor.open(operation.ID, "snapshot", operation.PreviousSnapshot)
	if err != nil {
		return err
	}
	candidate, err := executor.open(operation.ID, "candidate", operation.CandidateConfig)
	if err != nil {
		return err
	}
	state, err := InspectState(executor.StatePath, executor.RuntimeDirectory)
	if err != nil {
		return err
	}
	current, err := executor.snapshot(state)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, candidate) {
		return ErrStateChanged
	}
	if err := executor.verifyEntries(ctx, candidate.Configs); err != nil {
		return ErrStateChanged
	}
	if err := executor.transition(ctx, operation.ID, domain.TransactionCommitted, domain.TransactionRollingBack, "operator_requested"); err != nil {
		return err
	}
	if err := executor.restore(ctx, previous, candidate); err != nil {
		return fmt.Errorf("restore committed interface outbound snapshot: %w", err)
	}
	return executor.transition(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, "operator_requested")
}
