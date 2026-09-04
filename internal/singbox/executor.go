package singbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/secrets"
	"github.com/egress-manager/egress-manager/internal/system"
)

const ownedServiceName = "egress-manager-sing-box.service"

var ErrStateChanged = errors.New("owned sing-box state changed after planning")

type Journal interface {
	CreateOperation(context.Context, domain.Transaction) error
	TransitionOperation(context.Context, domain.ID, domain.TransactionState, domain.TransactionState, time.Time, string) error
	UnfinishedOperations(context.Context, int) ([]domain.Transaction, error)
}

type JournalProtector interface {
	Seal(string, []byte) (secrets.Envelope, error)
	Open(string, secrets.Envelope) ([]byte, error)
}

type Executor struct {
	Runner     system.Runner
	Journal    Journal
	Protector  JournalProtector
	ConfigPath string
	Timeout    time.Duration
	Now        func() time.Time
}

type ApplyResponse struct {
	TransactionID domain.ID               `json:"transaction_id"`
	State         domain.TransactionState `json:"state"`
	CandidateHash string                  `json:"candidate_hash"`
}

type recoverySnapshot struct {
	Exists  bool   `json:"exists"`
	Content []byte `json:"content"`
}

func (executor Executor) Execute(ctx context.Context, id domain.ID, plan ExecutionPlan) (ApplyResponse, error) {
	if err := executor.validate(); err != nil {
		return ApplyResponse{}, err
	}
	if err := validateExecutionPlan(plan); err != nil {
		return ApplyResponse{}, err
	}
	content, exists, err := inspectOwnedConfiguration(executor.ConfigPath)
	if err != nil {
		return ApplyResponse{}, err
	}
	state, err := ParseState(content, exists)
	if err != nil {
		return ApplyResponse{}, err
	}
	if state.Hash != plan.Plan.StateHash {
		return ApplyResponse{}, ErrStateChanged
	}
	snapshotDocument, err := json.Marshal(recoverySnapshot{Exists: exists, Content: content})
	if err != nil {
		return ApplyResponse{}, fmt.Errorf("encode sing-box recovery snapshot: %w", err)
	}
	protectedSnapshot, err := executor.protectJournal(id, "snapshot", snapshotDocument)
	if err != nil {
		return ApplyResponse{}, err
	}
	protectedCandidate, err := executor.protectJournal(id, "candidate", plan.candidate)
	if err != nil {
		return ApplyResponse{}, err
	}
	now := executor.now()
	operation := domain.Transaction{
		ID: id, Operation: "singbox_apply", State: domain.TransactionPrepared,
		RequestedChange:  fmt.Sprintf("apply %d enabled sing-box outbounds", plan.Plan.EnabledOutbounds),
		PreviousSnapshot: protectedSnapshot, CandidateConfig: protectedCandidate, CreatedAt: now, UpdatedAt: now,
	}
	if err := executor.Journal.CreateOperation(ctx, operation); err != nil {
		return ApplyResponse{}, fmt.Errorf("journal prepared sing-box operation: %w", err)
	}
	temporary, err := writeTemporaryConfiguration(executor.ConfigPath, plan.candidate)
	if err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionPrepared, domain.TransactionFailed, executor.now(), "candidate_write_failed")
		return ApplyResponse{}, err
	}
	defer os.Remove(temporary)
	if err := executor.run(ctx, system.Command{Name: "sing-box", Args: []string{"check", "-c", temporary}}); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionPrepared, domain.TransactionFailed, executor.now(), "native_validation_failed")
		return ApplyResponse{}, fmt.Errorf("validate sing-box candidate: %w", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionPrepared, domain.TransactionValidated, ""); err != nil {
		return ApplyResponse{}, err
	}
	if err := executor.transition(ctx, id, domain.TransactionValidated, domain.TransactionApplying, ""); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionValidated, domain.TransactionFailed, executor.now(), "journal_transition_failed")
		return ApplyResponse{}, err
	}
	if err := installTemporaryConfiguration(temporary, executor.ConfigPath); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, domain.TransactionApplying, "config_install_failed", err)
	}
	if err := executor.restart(ctx); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, domain.TransactionApplying, "restart_failed", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionApplying, domain.TransactionVerifying, ""); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, domain.TransactionApplying, "journal_transition_failed", err)
	}
	if err := executor.verify(ctx); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, domain.TransactionVerifying, "verification_failed", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionVerifying, domain.TransactionCommitted, ""); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, domain.TransactionVerifying, "journal_transition_failed", err)
	}
	return ApplyResponse{TransactionID: id, State: domain.TransactionCommitted, CandidateHash: plan.Plan.CandidateHash}, nil
}

func (executor Executor) Recover(ctx context.Context) error {
	if err := executor.validate(); err != nil {
		return err
	}
	operations, err := executor.Journal.UnfinishedOperations(ctx, 100)
	if err != nil {
		return fmt.Errorf("read unfinished sing-box operations: %w", err)
	}
	var recoveryErrors []error
	for _, operation := range operations {
		if operation.Operation != "singbox_apply" {
			continue
		}
		snapshot, err := executor.openSnapshot(operation)
		if err != nil {
			recoveryErrors = append(recoveryErrors, err)
			continue
		}
		switch operation.State {
		case domain.TransactionPrepared, domain.TransactionValidated:
			err = executor.Journal.TransitionOperation(ctx, operation.ID, operation.State, domain.TransactionFailed, executor.now(), "interrupted_before_apply")
		case domain.TransactionApplying, domain.TransactionVerifying:
			err = executor.rollback(ctx, operation, snapshot, operation.State, "interrupted_operation")
		case domain.TransactionRollingBack:
			err = executor.restore(ctx, snapshot)
			if err == nil {
				err = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, executor.now(), "interrupted_operation")
			} else {
				_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "rollback_failed")
			}
		}
		if err != nil {
			recoveryErrors = append(recoveryErrors, err)
		}
	}
	return errors.Join(recoveryErrors...)
}

func (executor Executor) failWithRollback(ctx context.Context, operation domain.Transaction, from domain.TransactionState, detail string, cause error) error {
	snapshot, snapshotErr := executor.openSnapshot(operation)
	if snapshotErr != nil {
		_ = executor.Journal.TransitionOperation(ctx, operation.ID, from, domain.TransactionFailed, executor.now(), "snapshot_authentication_failed")
		return errors.Join(cause, snapshotErr)
	}
	return errors.Join(cause, executor.rollback(ctx, operation, snapshot, from, detail))
}

func (executor Executor) rollback(ctx context.Context, operation domain.Transaction, snapshot recoverySnapshot, from domain.TransactionState, detail string) error {
	if err := executor.Journal.TransitionOperation(ctx, operation.ID, from, domain.TransactionRollingBack, executor.now(), detail); err != nil {
		return fmt.Errorf("journal sing-box rollback: %w", err)
	}
	if err := executor.restore(ctx, snapshot); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "rollback_failed")
		return fmt.Errorf("restore sing-box snapshot: %w", err)
	}
	return executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, executor.now(), detail)
}

func (executor Executor) restore(ctx context.Context, snapshot recoverySnapshot) error {
	if _, err := ParseState(snapshot.Content, snapshot.Exists); err != nil {
		return fmt.Errorf("validate sing-box recovery snapshot: %w", err)
	}
	if !snapshot.Exists {
		if err := removeOwnedConfiguration(executor.ConfigPath); err != nil {
			return err
		}
		return executor.run(ctx, system.Command{Name: "systemctl", Args: []string{"stop", ownedServiceName}})
	}
	temporary, err := writeTemporaryConfiguration(executor.ConfigPath, snapshot.Content)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := executor.run(ctx, system.Command{Name: "sing-box", Args: []string{"check", "-c", temporary}}); err != nil {
		return err
	}
	if err := installTemporaryConfiguration(temporary, executor.ConfigPath); err != nil {
		return err
	}
	if err := executor.restart(ctx); err != nil {
		return err
	}
	return executor.verify(ctx)
}

func (executor Executor) restart(ctx context.Context) error {
	return executor.run(ctx, system.Command{Name: "systemctl", Args: []string{"restart", ownedServiceName}})
}

func (executor Executor) verify(ctx context.Context) error {
	if err := executor.run(ctx, system.Command{Name: "sing-box", Args: []string{"check", "-c", executor.ConfigPath}}); err != nil {
		return err
	}
	return executor.run(ctx, system.Command{Name: "systemctl", Args: []string{"is-active", "--quiet", ownedServiceName}})
}

func (executor Executor) protectJournal(id domain.ID, purpose string, plaintext []byte) (string, error) {
	envelope, err := executor.Protector.Seal(journalContext(id, purpose), plaintext)
	if err != nil {
		return "", fmt.Errorf("protect sing-box journal %s: %w", purpose, err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode protected sing-box journal %s: %w", purpose, err)
	}
	return string(encoded), nil
}

func (executor Executor) openSnapshot(operation domain.Transaction) (recoverySnapshot, error) {
	var envelope secrets.Envelope
	if err := json.Unmarshal([]byte(operation.PreviousSnapshot), &envelope); err != nil {
		return recoverySnapshot{}, fmt.Errorf("decode protected sing-box recovery snapshot")
	}
	plaintext, err := executor.Protector.Open(journalContext(operation.ID, "snapshot"), envelope)
	if err != nil {
		return recoverySnapshot{}, fmt.Errorf("authenticate protected sing-box recovery snapshot: %w", err)
	}
	var snapshot recoverySnapshot
	if err := json.Unmarshal(plaintext, &snapshot); err != nil {
		return recoverySnapshot{}, fmt.Errorf("decode sing-box recovery snapshot")
	}
	if _, err := ParseState(snapshot.Content, snapshot.Exists); err != nil {
		return recoverySnapshot{}, err
	}
	return snapshot, nil
}

func validateExecutionPlan(plan ExecutionPlan) error {
	if plan.Plan.Engine != "sing-box" || plan.Plan.StateHash == "" || plan.Plan.CandidateHash == "" || len(plan.candidate) == 0 || len(plan.candidate) > maximumCandidate {
		return fmt.Errorf("sing-box execution plan is invalid")
	}
	if hashBytes(plan.candidate) != plan.Plan.CandidateHash {
		return fmt.Errorf("sing-box candidate hash does not match plan")
	}
	if _, err := ParseState(plan.candidate, true); err != nil {
		return fmt.Errorf("sing-box candidate is not project-owned: %w", err)
	}
	return nil
}

func writeTemporaryConfiguration(path string, content []byte) (string, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create sing-box configuration directory: %w", err)
	}
	file, err := os.CreateTemp(directory, ".egress-sing-box-*.json")
	if err != nil {
		return "", fmt.Errorf("create temporary sing-box configuration: %w", err)
	}
	temporary := file.Name()
	failed := true
	defer func() {
		if failed {
			_ = os.Remove(temporary)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return "", err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	failed = false
	return temporary, nil
}

func installTemporaryConfiguration(temporary, destination string) error {
	if filepath.Dir(temporary) != filepath.Dir(destination) {
		return fmt.Errorf("sing-box temporary and destination paths must share a directory")
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("atomically install sing-box configuration: %w", err)
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func removeOwnedConfiguration(path string) error {
	_, exists, err := inspectOwnedConfiguration(path)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove owned sing-box configuration: %w", err)
	}
	return nil
}

func (executor Executor) run(ctx context.Context, command system.Command) error {
	commandContext, cancel := context.WithTimeout(ctx, executor.timeout())
	defer cancel()
	result, err := executor.Runner.Run(commandContext, command)
	if err != nil || result.ExitCode != 0 {
		return errors.Join(err, fmt.Errorf("%s exited with code %d", command.Name, result.ExitCode))
	}
	return nil
}

func (executor Executor) transition(ctx context.Context, id domain.ID, from, to domain.TransactionState, detail string) error {
	if err := executor.Journal.TransitionOperation(ctx, id, from, to, executor.now(), detail); err != nil {
		return fmt.Errorf("journal sing-box transition %s to %s: %w", from, to, err)
	}
	return nil
}

func (executor Executor) validate() error {
	if executor.Runner == nil || executor.Journal == nil || executor.Protector == nil {
		return fmt.Errorf("sing-box executor dependencies are required")
	}
	if !filepath.IsAbs(executor.ConfigPath) || strings.TrimSpace(executor.ConfigPath) != executor.ConfigPath {
		return fmt.Errorf("absolute sing-box configuration path is required")
	}
	return nil
}

func (executor Executor) now() time.Time {
	if executor.Now != nil {
		return executor.Now().UTC()
	}
	return time.Now().UTC()
}

func (executor Executor) timeout() time.Duration {
	if executor.Timeout <= 0 || executor.Timeout > 30*time.Second {
		return 5 * time.Second
	}
	return executor.Timeout
}

func journalContext(id domain.ID, purpose string) string {
	return "singbox-transaction:" + string(id) + ":" + purpose
}
