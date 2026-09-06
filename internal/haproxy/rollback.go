package haproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/secrets"
	"github.com/egress-manager/egress-manager/internal/system"
)

func (executor Executor) RollbackCommitted(ctx context.Context, operation domain.Transaction) error {
	if err := executor.validate(); err != nil {
		return err
	}
	if executor.Protector == nil || operation.Operation != "haproxy_apply" || operation.State != domain.TransactionCommitted {
		return fmt.Errorf("HAProxy operation is not eligible for rollback")
	}
	var snapshotEnvelope secrets.Envelope
	if err := json.Unmarshal([]byte(operation.PreviousSnapshot), &snapshotEnvelope); err != nil || len(snapshotEnvelope.Nonce) == 0 || len(snapshotEnvelope.Ciphertext) == 0 {
		return fmt.Errorf("HAProxy operation is not eligible for rollback")
	}
	snapshot, err := executor.openProtectedSnapshot(operation, snapshotEnvelope)
	if err != nil {
		return err
	}
	candidate, err := executor.openProtectedCandidate(operation)
	if err != nil {
		return err
	}
	current, state, err := inspectConfig(executor.ConfigPath)
	if err != nil || !state.Exists || !bytes.Equal(current, candidate) {
		return ErrStateChanged
	}
	if _, err := executor.verify(ctx); err != nil {
		return ErrStateChanged
	}
	if err := executor.validateSnapshotNative(ctx, snapshot); err != nil {
		return err
	}
	if err := executor.transition(ctx, operation.ID, domain.TransactionCommitted, domain.TransactionRollingBack, "operator_requested"); err != nil {
		return err
	}
	if err := executor.restore(ctx, snapshot); err != nil {
		return fmt.Errorf("restore committed HAProxy snapshot: %w", err)
	}
	return executor.transition(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, "operator_requested")
}

func (executor Executor) openProtectedCandidate(operation domain.Transaction) ([]byte, error) {
	var envelope secrets.Envelope
	if err := json.Unmarshal([]byte(operation.CandidateConfig), &envelope); err != nil || len(envelope.Nonce) == 0 || len(envelope.Ciphertext) == 0 {
		return nil, fmt.Errorf("decode protected HAProxy candidate")
	}
	candidate, err := executor.Protector.Open(haproxyJournalContext(operation.ID, "candidate"), envelope)
	if err != nil {
		return nil, fmt.Errorf("authenticate protected HAProxy candidate: %w", err)
	}
	if _, err := ParseState(candidate, true); err != nil {
		return nil, err
	}
	return candidate, nil
}

func (executor Executor) validateSnapshotNative(ctx context.Context, snapshot recoverySnapshot) error {
	if !snapshot.Exists {
		return nil
	}
	temporary, err := writeTemporaryConfig(executor.ConfigPath, snapshot.Content)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := executor.run(ctx, system.Command{Name: "haproxy", Args: []string{"-c", "-f", temporary}}); err != nil {
		return fmt.Errorf("validate HAProxy rollback snapshot: %w", err)
	}
	return nil
}
