package xray

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/secrets"
	"github.com/egress-manager/egress-manager/internal/system"
)

var ErrXrayStateChanged = errors.New("Xray configuration changed after planning")

type TransactionJournal interface {
	CreateOperation(context.Context, domain.Transaction) error
	TransitionOperation(context.Context, domain.ID, domain.TransactionState, domain.TransactionState, time.Time, string) error
	UnfinishedOperations(context.Context, int) ([]domain.Transaction, error)
}

type TransactionProtector interface {
	Seal(string, []byte) (secrets.Envelope, error)
	Open(string, secrets.Envelope) ([]byte, error)
}

type FragmentExecutor struct {
	Runner       system.Runner
	Journal      TransactionJournal
	Protector    TransactionProtector
	Installation Installation
	Timeout      time.Duration
	Now          func() time.Time
}

type FragmentApplyResponse struct {
	TransactionID domain.ID               `json:"transaction_id"`
	State         domain.TransactionState `json:"state"`
	CandidateHash string                  `json:"candidate_hash"`
}

type fragmentRecoverySnapshot struct {
	Exists      bool   `json:"exists"`
	Content     []byte `json:"content"`
	ForeignHash string `json:"foreign_hash"`
}

func (executor FragmentExecutor) Execute(ctx context.Context, id domain.ID, plan FragmentExecutionPlan) (FragmentApplyResponse, error) {
	if err := executor.validate(); err != nil {
		return FragmentApplyResponse{}, err
	}
	if err := validateFragmentExecutionPlan(executor.Installation, plan); err != nil {
		return FragmentApplyResponse{}, err
	}
	current, err := executor.currentState(plan.Review)
	if err != nil {
		return FragmentApplyResponse{}, err
	}
	snapshotDocument, err := json.Marshal(fragmentRecoverySnapshot{Exists: current.Fragment.Exists, Content: current.fragmentData, ForeignHash: current.Foreign.Hash})
	if err != nil {
		return FragmentApplyResponse{}, fmt.Errorf("encode Xray recovery snapshot: %w", err)
	}
	protectedSnapshot, err := executor.protect(id, "snapshot", snapshotDocument)
	if err != nil {
		return FragmentApplyResponse{}, err
	}
	protectedCandidate, err := executor.protect(id, "candidate", plan.candidate)
	if err != nil {
		return FragmentApplyResponse{}, err
	}
	now := executor.now()
	operation := domain.Transaction{
		ID: id, Operation: "xray_fragment_apply", State: domain.TransactionPrepared,
		RequestedChange:  fmt.Sprintf("apply %d native Xray route bindings", plan.Review.EnabledBindings),
		PreviousSnapshot: protectedSnapshot, CandidateConfig: protectedCandidate, CreatedAt: now, UpdatedAt: now,
	}
	if err := executor.Journal.CreateOperation(ctx, operation); err != nil {
		return FragmentApplyResponse{}, fmt.Errorf("journal prepared Xray operation: %w", err)
	}
	if err := executor.validateEffectiveCandidate(ctx, current, plan.candidate, plan.Review.CandidateExists); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionPrepared, domain.TransactionFailed, executor.now(), "native_validation_failed")
		return FragmentApplyResponse{}, fmt.Errorf("validate Xray candidate: %w", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionPrepared, domain.TransactionValidated, ""); err != nil {
		return FragmentApplyResponse{}, err
	}
	if _, err := executor.currentState(plan.Review); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionValidated, domain.TransactionFailed, executor.now(), "external_change_before_apply")
		return FragmentApplyResponse{}, err
	}
	if err := executor.transition(ctx, id, domain.TransactionValidated, domain.TransactionApplying, ""); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionValidated, domain.TransactionFailed, executor.now(), "journal_transition_failed")
		return FragmentApplyResponse{}, err
	}
	if err := installFragment(executor.Installation.ManagedPath, plan.candidate, plan.Review.CandidateExists, plan.Review.FragmentStateHash); err != nil {
		return FragmentApplyResponse{}, executor.failWithRollback(ctx, operation, domain.TransactionApplying, "fragment_install_failed", err)
	}
	if err := executor.restart(ctx); err != nil {
		return FragmentApplyResponse{}, executor.failWithRollback(ctx, operation, domain.TransactionApplying, "restart_failed", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionApplying, domain.TransactionVerifying, ""); err != nil {
		return FragmentApplyResponse{}, executor.failWithRollback(ctx, operation, domain.TransactionApplying, "journal_transition_failed", err)
	}
	if err := executor.verify(ctx, plan.Review.ForeignStateHash, plan.Review.CandidateHash); err != nil {
		return FragmentApplyResponse{}, executor.failWithRollback(ctx, operation, domain.TransactionVerifying, "verification_failed", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionVerifying, domain.TransactionCommitted, ""); err != nil {
		return FragmentApplyResponse{}, executor.failWithRollback(ctx, operation, domain.TransactionVerifying, "journal_transition_failed", err)
	}
	return FragmentApplyResponse{TransactionID: id, State: domain.TransactionCommitted, CandidateHash: plan.Review.CandidateHash}, nil
}

func (executor FragmentExecutor) Recover(ctx context.Context) error {
	if err := executor.validate(); err != nil {
		return err
	}
	operations, err := executor.Journal.UnfinishedOperations(ctx, 100)
	if err != nil {
		return fmt.Errorf("read unfinished Xray operations: %w", err)
	}
	var recoveryErrors []error
	for _, operation := range operations {
		if operation.Operation != "xray_fragment_apply" {
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
			if err == nil || errors.Is(err, ErrXrayStateChanged) {
				detail := "interrupted_operation"
				if errors.Is(err, ErrXrayStateChanged) {
					detail += "_external_change_detected"
				}
				transitionErr := executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, executor.now(), detail)
				err = errors.Join(err, transitionErr)
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

func (executor FragmentExecutor) currentState(review FragmentReview) (ConfdirSnapshot, error) {
	snapshot, err := SnapshotConfdir(executor.Installation)
	if err != nil {
		return ConfdirSnapshot{}, err
	}
	if snapshot.Foreign.Hash != review.ForeignStateHash || snapshot.Fragment.Hash != review.FragmentStateHash {
		return ConfdirSnapshot{}, ErrXrayStateChanged
	}
	return snapshot, nil
}

func (executor FragmentExecutor) validateEffectiveCandidate(ctx context.Context, snapshot ConfdirSnapshot, candidate []byte, candidateExists bool) error {
	directory, err := materializeConfdir(snapshot.foreignFiles, candidate, candidateExists)
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	return executor.run(ctx, system.Command{Name: executor.Installation.XrayExecutable, Args: []string{"run", "-test", "-confdir", directory}, Dir: executor.Installation.ConfigRoot})
}

func (executor FragmentExecutor) verify(ctx context.Context, foreignHash, candidateHash string) error {
	snapshot, err := SnapshotConfdir(executor.Installation)
	if err != nil {
		return err
	}
	if snapshot.Foreign.Hash != foreignHash || snapshot.Fragment.Hash != candidateHash {
		return ErrXrayStateChanged
	}
	if err := executor.validateEffectiveCandidate(ctx, snapshot, snapshot.fragmentData, snapshot.Fragment.Exists); err != nil {
		return err
	}
	return executor.run(ctx, system.Command{Name: "systemctl", Args: []string{"is-active", "--quiet", executor.Installation.ServiceName}})
}

func (executor FragmentExecutor) failWithRollback(ctx context.Context, operation domain.Transaction, from domain.TransactionState, detail string, cause error) error {
	snapshot, snapshotErr := executor.openSnapshot(operation)
	if snapshotErr != nil {
		_ = executor.Journal.TransitionOperation(ctx, operation.ID, from, domain.TransactionFailed, executor.now(), "snapshot_authentication_failed")
		return errors.Join(cause, snapshotErr)
	}
	return errors.Join(cause, executor.rollback(ctx, operation, snapshot, from, detail))
}

func (executor FragmentExecutor) rollback(ctx context.Context, operation domain.Transaction, snapshot fragmentRecoverySnapshot, from domain.TransactionState, detail string) error {
	if err := executor.Journal.TransitionOperation(ctx, operation.ID, from, domain.TransactionRollingBack, executor.now(), detail); err != nil {
		return fmt.Errorf("journal Xray rollback: %w", err)
	}
	if err := executor.restore(ctx, snapshot); err != nil && !errors.Is(err, ErrXrayStateChanged) {
		_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "rollback_failed")
		return fmt.Errorf("restore Xray snapshot: %w", err)
	} else if errors.Is(err, ErrXrayStateChanged) {
		transitionErr := executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, executor.now(), detail+"_external_change_detected")
		return errors.Join(err, transitionErr)
	}
	return executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, executor.now(), detail)
}

func (executor FragmentExecutor) restore(ctx context.Context, recovery fragmentRecoverySnapshot) error {
	if _, err := ParseFragmentState(recovery.Content, recovery.Exists); err != nil {
		return fmt.Errorf("validate Xray recovery snapshot: %w", err)
	}
	current, err := SnapshotConfdir(executor.Installation)
	if err != nil {
		return err
	}
	changeErr := error(nil)
	if current.Foreign.Hash != recovery.ForeignHash {
		changeErr = ErrXrayStateChanged
	}
	if err := executor.validateEffectiveCandidate(ctx, current, recovery.Content, recovery.Exists); err != nil {
		return errors.Join(changeErr, err)
	}
	if err := installFragment(executor.Installation.ManagedPath, recovery.Content, recovery.Exists, current.Fragment.Hash); err != nil {
		return errors.Join(changeErr, err)
	}
	if err := executor.restart(ctx); err != nil {
		return errors.Join(changeErr, err)
	}
	expectedHash := hashFragmentState(recovery.Content, recovery.Exists)
	return errors.Join(changeErr, executor.verify(ctx, current.Foreign.Hash, expectedHash))
}

func validateFragmentExecutionPlan(installation Installation, plan FragmentExecutionPlan) error {
	if plan.Review.Engine != "xray" || plan.Review.ServiceName != installation.ServiceName || filepath.Clean(plan.Review.ManagedPath) != filepath.Clean(installation.ManagedPath) || plan.Review.ForeignStateHash == "" || plan.Review.FragmentStateHash == "" || plan.Review.CandidateHash == "" {
		return fmt.Errorf("Xray fragment execution plan is invalid")
	}
	if plan.Review.CandidateExists {
		if len(plan.candidate) == 0 || len(plan.candidate) > MaximumConfigurationBytes || hashFragmentState(plan.candidate, true) != plan.Review.CandidateHash {
			return fmt.Errorf("Xray fragment candidate hash does not match plan")
		}
		if _, err := ParseFragmentState(plan.candidate, true); err != nil {
			return fmt.Errorf("Xray candidate is not project-owned: %w", err)
		}
	} else if len(plan.candidate) != 0 || hashFragmentState(nil, false) != plan.Review.CandidateHash {
		return fmt.Errorf("Xray absent candidate does not match plan")
	}
	return nil
}

func materializeConfdir(files []confdirFile, candidate []byte, candidateExists bool) (string, error) {
	directory, err := os.MkdirTemp("", "egress-xray-validation-")
	if err != nil {
		return "", fmt.Errorf("create private Xray validation directory: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(directory)
		}
	}()
	for _, file := range files {
		if filepath.Base(file.name) != file.name || !xrayConfigName(file.name) {
			return "", fmt.Errorf("invalid Xray validation filename")
		}
		if err := os.WriteFile(filepath.Join(directory, file.name), file.content, 0o600); err != nil {
			return "", fmt.Errorf("write private Xray validation input: %w", err)
		}
	}
	if candidateExists {
		if err := os.WriteFile(filepath.Join(directory, managedFragmentName), candidate, 0o600); err != nil {
			return "", fmt.Errorf("write private Xray validation candidate: %w", err)
		}
	}
	failed = false
	return directory, nil
}

func installFragment(path string, content []byte, exists bool, expectedStateHash string) error {
	if expectedStateHash == "" {
		return fmt.Errorf("expected Xray fragment state hash is required")
	}
	if !exists {
		current, err := inspectFragmentPath(path)
		if err != nil {
			return err
		}
		if current.Hash != expectedStateHash {
			return ErrXrayStateChanged
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove owned Xray fragment: %w", err)
		}
		return syncDirectory(filepath.Dir(path))
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".egress-xray-*.json")
	if err != nil {
		return fmt.Errorf("create temporary Xray fragment: %w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	current, err := inspectFragmentPath(path)
	if err != nil {
		return err
	}
	if current.Hash != expectedStateHash {
		return ErrXrayStateChanged
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("atomically install Xray fragment: %w", err)
	}
	return syncDirectory(filepath.Dir(path))
}

func inspectFragmentPath(path string) (FragmentState, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return ParseFragmentState(nil, false)
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > MaximumConfigurationBytes {
		return FragmentState{}, fmt.Errorf("owned Xray fragment path is unsafe")
	}
	content, err := readStableRegular(path)
	if err != nil {
		return FragmentState{}, err
	}
	return ParseFragmentState(content, true)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (executor FragmentExecutor) restart(ctx context.Context) error {
	return executor.run(ctx, system.Command{Name: "systemctl", Args: []string{"restart", executor.Installation.ServiceName}})
}

func (executor FragmentExecutor) run(ctx context.Context, command system.Command) error {
	commandContext, cancel := context.WithTimeout(ctx, executor.timeout())
	defer cancel()
	result, err := executor.Runner.Run(commandContext, command)
	if err != nil || result.ExitCode != 0 {
		return errors.Join(err, fmt.Errorf("%s exited with code %d", command.Name, result.ExitCode))
	}
	return nil
}

func (executor FragmentExecutor) transition(ctx context.Context, id domain.ID, from, to domain.TransactionState, detail string) error {
	if err := executor.Journal.TransitionOperation(ctx, id, from, to, executor.now(), detail); err != nil {
		return fmt.Errorf("journal Xray transition %s to %s: %w", from, to, err)
	}
	return nil
}

func (executor FragmentExecutor) protect(id domain.ID, purpose string, plaintext []byte) (string, error) {
	envelope, err := executor.Protector.Seal(xrayJournalContext(id, purpose), plaintext)
	if err != nil {
		return "", fmt.Errorf("protect Xray journal %s: %w", purpose, err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode protected Xray journal %s: %w", purpose, err)
	}
	return string(encoded), nil
}

func (executor FragmentExecutor) openSnapshot(operation domain.Transaction) (fragmentRecoverySnapshot, error) {
	var envelope secrets.Envelope
	if err := json.Unmarshal([]byte(operation.PreviousSnapshot), &envelope); err != nil {
		return fragmentRecoverySnapshot{}, fmt.Errorf("decode protected Xray recovery snapshot")
	}
	plaintext, err := executor.Protector.Open(xrayJournalContext(operation.ID, "snapshot"), envelope)
	if err != nil {
		return fragmentRecoverySnapshot{}, fmt.Errorf("authenticate protected Xray recovery snapshot: %w", err)
	}
	var snapshot fragmentRecoverySnapshot
	if err := json.Unmarshal(plaintext, &snapshot); err != nil || snapshot.ForeignHash == "" {
		return fragmentRecoverySnapshot{}, fmt.Errorf("decode Xray recovery snapshot")
	}
	if _, err := ParseFragmentState(snapshot.Content, snapshot.Exists); err != nil {
		return fragmentRecoverySnapshot{}, err
	}
	return snapshot, nil
}

func (executor FragmentExecutor) validate() error {
	if executor.Runner == nil || executor.Journal == nil || executor.Protector == nil {
		return fmt.Errorf("Xray executor dependencies are required")
	}
	if err := validateManagedInstallation(executor.Installation); err != nil {
		return err
	}
	if executor.Installation.ServiceName != "xray.service" || (executor.Installation.XrayExecutable != "xray" && executor.Installation.XrayExecutable != "/usr/local/bin/xray") {
		return fmt.Errorf("Xray executor requires a fixed standalone service and executable")
	}
	return nil
}

func (executor FragmentExecutor) now() time.Time {
	if executor.Now != nil {
		return executor.Now().UTC()
	}
	return time.Now().UTC()
}

func (executor FragmentExecutor) timeout() time.Duration {
	if executor.Timeout <= 0 || executor.Timeout > 30*time.Second {
		return 5 * time.Second
	}
	return executor.Timeout
}

func xrayJournalContext(id domain.ID, purpose string) string {
	return "xray-transaction:" + string(id) + ":" + purpose
}
