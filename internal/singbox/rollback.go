package singbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/secrets"
)

func (executor Executor) RollbackCommitted(ctx context.Context, operation domain.Transaction) error {
	if err := executor.validate(); err != nil {
		return err
	}
	if operation.Operation != "singbox_apply" || operation.State != domain.TransactionCommitted {
		return fmt.Errorf("sing-box operation is not eligible for rollback")
	}
	snapshot, err := executor.openSnapshot(operation)
	if err != nil {
		return err
	}
	candidate, err := executor.openCandidate(operation)
	if err != nil {
		return err
	}
	current, exists, err := inspectOwnedConfiguration(executor.ConfigPath)
	if err != nil {
		return err
	}
	if !exists || !bytes.Equal(current, candidate) {
		return ErrStateChanged
	}
	if err := executor.verify(ctx); err != nil {
		return ErrStateChanged
	}
	if err := executor.transition(ctx, operation.ID, domain.TransactionCommitted, domain.TransactionRollingBack, "operator_requested"); err != nil {
		return err
	}
	if err := executor.restore(ctx, snapshot); err != nil {
		return fmt.Errorf("restore committed sing-box snapshot: %w", err)
	}
	return executor.transition(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, "operator_requested")
}

func (executor Executor) openCandidate(operation domain.Transaction) ([]byte, error) {
	var envelope secrets.Envelope
	if err := json.Unmarshal([]byte(operation.CandidateConfig), &envelope); err != nil {
		return nil, fmt.Errorf("decode protected sing-box candidate")
	}
	candidate, err := executor.Protector.Open(journalContext(operation.ID, "candidate"), envelope)
	if err != nil {
		return nil, fmt.Errorf("authenticate protected sing-box candidate: %w", err)
	}
	if _, err := ParseState(candidate, true); err != nil {
		return nil, err
	}
	return candidate, nil
}
