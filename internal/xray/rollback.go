package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/secrets"
)

func (executor FragmentExecutor) RollbackCommitted(ctx context.Context, operation domain.Transaction) error {
	if err := executor.validate(); err != nil {
		return err
	}
	if operation.Operation != "xray_fragment_apply" || operation.State != domain.TransactionCommitted {
		return fmt.Errorf("Xray operation is not eligible for rollback")
	}
	previous, err := executor.openSnapshot(operation)
	if err != nil {
		return err
	}
	candidate, candidateExists, err := executor.openCandidate(operation)
	if err != nil {
		return err
	}
	current, err := SnapshotConfdir(executor.Installation)
	if err != nil {
		return err
	}
	if current.Foreign.Hash != previous.ForeignHash || current.Fragment.Exists != candidateExists || !bytes.Equal(current.fragmentData, candidate) {
		return ErrXrayStateChanged
	}
	if err := executor.verify(ctx, current.Foreign.Hash, current.Fragment.Hash); err != nil {
		return ErrXrayStateChanged
	}
	if err := executor.validateEffectiveCandidate(ctx, current, previous.Content, previous.Exists); err != nil {
		return fmt.Errorf("validate previous Xray snapshot: %w", err)
	}
	if err := executor.transition(ctx, operation.ID, domain.TransactionCommitted, domain.TransactionRollingBack, "operator_requested"); err != nil {
		return err
	}
	if err := executor.restore(ctx, previous); err != nil {
		return fmt.Errorf("restore committed Xray snapshot: %w", err)
	}
	return executor.transition(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, "operator_requested")
}

func (executor FragmentExecutor) openCandidate(operation domain.Transaction) ([]byte, bool, error) {
	var envelope secrets.Envelope
	if err := json.Unmarshal([]byte(operation.CandidateConfig), &envelope); err != nil {
		return nil, false, fmt.Errorf("decode protected Xray candidate")
	}
	candidate, err := executor.Protector.Open(xrayJournalContext(operation.ID, "candidate"), envelope)
	if err != nil {
		return nil, false, fmt.Errorf("authenticate protected Xray candidate: %w", err)
	}
	exists := len(candidate) != 0
	if _, err := ParseFragmentState(candidate, exists); err != nil {
		return nil, false, err
	}
	return candidate, exists, nil
}
