package xrayrelay

import (
	"bytes"
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

const ownedServiceName = "egress-manager-xray-relay.service"

var ErrStateChanged = errors.New("owned Xray relay state changed after planning")

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

type ApplyRequest struct {
	TransactionID         domain.ID `json:"transaction_id"`
	ExpectedStateHash     string    `json:"expected_state_hash"`
	ExpectedCandidateHash string    `json:"expected_candidate_hash"`
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

type protectedCandidate struct {
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
	current, err := InspectState(executor.ConfigPath)
	if err != nil {
		return ApplyResponse{}, err
	}
	if current.Hash != plan.Review.StateHash {
		return ApplyResponse{}, ErrStateChanged
	}
	var currentContent []byte
	if current.Exists {
		currentContent, err = os.ReadFile(executor.ConfigPath)
		if err != nil {
			return ApplyResponse{}, fmt.Errorf("read Xray relay recovery snapshot: %w", err)
		}
	}
	snapshotDocument, err := json.Marshal(recoverySnapshot{Exists: current.Exists, Content: currentContent})
	if err != nil {
		return ApplyResponse{}, fmt.Errorf("encode Xray relay recovery snapshot: %w", err)
	}
	candidateDocument, err := json.Marshal(protectedCandidate{Exists: plan.Review.CandidateExists, Content: plan.candidate})
	if err != nil {
		return ApplyResponse{}, fmt.Errorf("encode Xray relay candidate: %w", err)
	}
	sealedSnapshot, err := executor.protect(id, "snapshot", snapshotDocument)
	if err != nil {
		return ApplyResponse{}, err
	}
	sealedCandidate, err := executor.protect(id, "candidate", candidateDocument)
	if err != nil {
		return ApplyResponse{}, err
	}
	now := executor.now()
	operation := domain.Transaction{
		ID: id, Operation: "xray_relay_apply", State: domain.TransactionPrepared,
		RequestedChange:  fmt.Sprintf("apply %d enabled Xray listener relays", plan.Review.EnabledRelays),
		PreviousSnapshot: sealedSnapshot, CandidateConfig: sealedCandidate, CreatedAt: now, UpdatedAt: now,
	}
	if err := executor.Journal.CreateOperation(ctx, operation); err != nil {
		return ApplyResponse{}, fmt.Errorf("journal prepared Xray relay operation: %w", err)
	}
	if plan.Review.CandidateExists {
		if err := executor.validateCandidate(ctx, plan.candidate); err != nil {
			_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionPrepared, domain.TransactionFailed, executor.now(), "native_validation_failed")
			return ApplyResponse{}, fmt.Errorf("validate Xray relay candidate: %w", err)
		}
	}
	if err := executor.transition(ctx, id, domain.TransactionPrepared, domain.TransactionValidated, ""); err != nil {
		return ApplyResponse{}, err
	}
	latest, err := InspectState(executor.ConfigPath)
	if err != nil || latest.Hash != plan.Review.StateHash {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionValidated, domain.TransactionFailed, executor.now(), "state_changed_before_apply")
		return ApplyResponse{}, errors.Join(err, ErrStateChanged)
	}
	if err := executor.transition(ctx, id, domain.TransactionValidated, domain.TransactionApplying, ""); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionValidated, domain.TransactionFailed, executor.now(), "journal_transition_failed")
		return ApplyResponse{}, err
	}
	if err := executor.install(plan.Review.CandidateExists, plan.candidate); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, domain.TransactionApplying, "config_install_failed", err)
	}
	if err := executor.activate(ctx, plan.Review.CandidateExists); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, domain.TransactionApplying, "service_transition_failed", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionApplying, domain.TransactionVerifying, ""); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, domain.TransactionApplying, "journal_transition_failed", err)
	}
	if err := executor.verify(ctx, plan.Review.CandidateHash, plan.Review.CandidateExists); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, domain.TransactionVerifying, "verification_failed", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionVerifying, domain.TransactionCommitted, ""); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, domain.TransactionVerifying, "journal_transition_failed", err)
	}
	return ApplyResponse{TransactionID: id, State: domain.TransactionCommitted, CandidateHash: plan.Review.CandidateHash}, nil
}

func (executor Executor) Recover(ctx context.Context) error {
	if err := executor.validate(); err != nil {
		return err
	}
	operations, err := executor.Journal.UnfinishedOperations(ctx, 100)
	if err != nil {
		return fmt.Errorf("read unfinished Xray relay operations: %w", err)
	}
	var recoveryErrors []error
	for _, operation := range operations {
		if operation.Operation != "xray_relay_apply" {
			continue
		}
		snapshot, openErr := executor.openSnapshot(operation)
		if openErr != nil {
			recoveryErrors = append(recoveryErrors, openErr)
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

func (executor Executor) RollbackCommitted(ctx context.Context, operation domain.Transaction) error {
	if err := executor.validate(); err != nil {
		return err
	}
	if operation.Operation != "xray_relay_apply" || operation.State != domain.TransactionCommitted {
		return fmt.Errorf("Xray relay operation is not eligible for rollback")
	}
	snapshot, err := executor.openSnapshot(operation)
	if err != nil {
		return err
	}
	candidate, err := executor.openCandidate(operation)
	if err != nil {
		return err
	}
	current, err := InspectState(executor.ConfigPath)
	if err != nil || current.Exists != candidate.Exists || current.Hash != hashState(candidate.Content, candidate.Exists) {
		return ErrStateChanged
	}
	if candidate.Exists {
		content, readErr := os.ReadFile(executor.ConfigPath)
		if readErr != nil || !bytes.Equal(content, candidate.Content) {
			return ErrStateChanged
		}
	}
	if err := executor.verify(ctx, current.Hash, current.Exists); err != nil {
		return ErrStateChanged
	}
	if err := executor.transition(ctx, operation.ID, domain.TransactionCommitted, domain.TransactionRollingBack, "operator_requested"); err != nil {
		return err
	}
	if err := executor.restore(ctx, snapshot); err != nil {
		return fmt.Errorf("restore committed Xray relay snapshot: %w", err)
	}
	return executor.transition(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, "operator_requested")
}

func (executor Executor) VerifyRuntime(ctx context.Context) error {
	state, err := InspectState(executor.ConfigPath)
	if err != nil {
		return err
	}
	return executor.verify(ctx, state.Hash, state.Exists)
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
		return fmt.Errorf("journal Xray relay rollback: %w", err)
	}
	if err := executor.restore(ctx, snapshot); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "rollback_failed")
		return fmt.Errorf("restore Xray relay snapshot: %w", err)
	}
	return executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, executor.now(), detail)
}

func (executor Executor) restore(ctx context.Context, snapshot recoverySnapshot) error {
	if _, err := ParseState(snapshot.Content, snapshot.Exists); err != nil {
		return fmt.Errorf("validate Xray relay recovery snapshot: %w", err)
	}
	if snapshot.Exists {
		if err := executor.validateCandidate(ctx, snapshot.Content); err != nil {
			return err
		}
	}
	if err := executor.install(snapshot.Exists, snapshot.Content); err != nil {
		return err
	}
	if err := executor.activate(ctx, snapshot.Exists); err != nil {
		return err
	}
	return executor.verify(ctx, hashState(snapshot.Content, snapshot.Exists), snapshot.Exists)
}

func (executor Executor) validateCandidate(ctx context.Context, content []byte) error {
	if _, err := ParseState(content, true); err != nil {
		return err
	}
	temporary, err := executor.writeTemporary(content)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	return executor.run(ctx, system.Command{Name: "xray", Args: []string{"run", "-test", "-config", temporary}})
}

func (executor Executor) install(exists bool, content []byte) error {
	if !exists {
		if err := os.Remove(executor.ConfigPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove Xray relay configuration: %w", err)
		}
		return syncDirectory(filepath.Dir(executor.ConfigPath))
	}
	temporary, err := executor.writeTemporary(content)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.Rename(temporary, executor.ConfigPath); err != nil {
		return fmt.Errorf("atomically install Xray relay configuration: %w", err)
	}
	return syncDirectory(filepath.Dir(executor.ConfigPath))
}

func (executor Executor) writeTemporary(content []byte) (string, error) {
	if len(content) == 0 || len(content) > MaximumConfigurationBytes {
		return "", fmt.Errorf("Xray relay candidate has invalid size")
	}
	directory := filepath.Dir(executor.ConfigPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create Xray relay configuration directory: %w", err)
	}
	file, err := os.CreateTemp(directory, ".egress-xray-relay-*.json")
	if err != nil {
		return "", fmt.Errorf("create temporary Xray relay configuration: %w", err)
	}
	path := file.Name()
	failed := true
	defer func() {
		if failed {
			_ = os.Remove(path)
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
	return path, nil
}

func (executor Executor) activate(ctx context.Context, exists bool) error {
	if !exists {
		return executor.run(ctx, system.Command{Name: "systemctl", Args: []string{"stop", ownedServiceName}})
	}
	return executor.run(ctx, system.Command{Name: "systemctl", Args: []string{"restart", ownedServiceName}})
}

func (executor Executor) verify(ctx context.Context, expectedHash string, exists bool) error {
	current, err := InspectState(executor.ConfigPath)
	if err != nil {
		return err
	}
	if current.Exists != exists || current.Hash != expectedHash {
		return ErrStateChanged
	}
	if !exists {
		result, runErr := executor.runResult(ctx, system.Command{Name: "systemctl", Args: []string{"is-active", "--quiet", ownedServiceName}})
		if runErr == nil && result.ExitCode == 0 {
			return fmt.Errorf("Xray relay service is active without desired listeners")
		}
		return nil
	}
	content, err := os.ReadFile(executor.ConfigPath)
	if err != nil {
		return err
	}
	if err := executor.validateCandidate(ctx, content); err != nil {
		return err
	}
	return executor.run(ctx, system.Command{Name: "systemctl", Args: []string{"is-active", "--quiet", ownedServiceName}})
}

func (executor Executor) protect(id domain.ID, purpose string, plaintext []byte) (string, error) {
	envelope, err := executor.Protector.Seal(journalContext(id, purpose), plaintext)
	if err != nil {
		return "", fmt.Errorf("protect Xray relay journal %s: %w", purpose, err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode protected Xray relay journal %s: %w", purpose, err)
	}
	return string(encoded), nil
}

func (executor Executor) openSnapshot(operation domain.Transaction) (recoverySnapshot, error) {
	plaintext, err := executor.openProtected(operation.ID, "snapshot", operation.PreviousSnapshot)
	if err != nil {
		return recoverySnapshot{}, err
	}
	var snapshot recoverySnapshot
	if err := json.Unmarshal(plaintext, &snapshot); err != nil {
		return recoverySnapshot{}, fmt.Errorf("decode Xray relay recovery snapshot")
	}
	if _, err := ParseState(snapshot.Content, snapshot.Exists); err != nil {
		return recoverySnapshot{}, err
	}
	return snapshot, nil
}

func (executor Executor) openCandidate(operation domain.Transaction) (protectedCandidate, error) {
	plaintext, err := executor.openProtected(operation.ID, "candidate", operation.CandidateConfig)
	if err != nil {
		return protectedCandidate{}, err
	}
	var candidate protectedCandidate
	if err := json.Unmarshal(plaintext, &candidate); err != nil {
		return protectedCandidate{}, fmt.Errorf("decode protected Xray relay candidate")
	}
	if _, err := ParseState(candidate.Content, candidate.Exists); err != nil {
		return protectedCandidate{}, err
	}
	return candidate, nil
}

func (executor Executor) openProtected(id domain.ID, purpose, encoded string) ([]byte, error) {
	var envelope secrets.Envelope
	if err := json.Unmarshal([]byte(encoded), &envelope); err != nil {
		return nil, fmt.Errorf("decode protected Xray relay %s", purpose)
	}
	plaintext, err := executor.Protector.Open(journalContext(id, purpose), envelope)
	if err != nil {
		return nil, fmt.Errorf("authenticate protected Xray relay %s: %w", purpose, err)
	}
	return plaintext, nil
}

func validateExecutionPlan(plan ExecutionPlan) error {
	if plan.Review.Engine != "xray-relay" || plan.Review.StateHash == "" || plan.Review.CandidateHash == "" {
		return fmt.Errorf("Xray relay execution plan is invalid")
	}
	if plan.Review.CandidateExists {
		if len(plan.candidate) == 0 || len(plan.candidate) > MaximumConfigurationBytes || hashState(plan.candidate, true) != plan.Review.CandidateHash {
			return fmt.Errorf("Xray relay candidate hash does not match plan")
		}
		if _, err := ParseState(plan.candidate, true); err != nil {
			return err
		}
	} else if len(plan.candidate) != 0 || hashState(nil, false) != plan.Review.CandidateHash {
		return fmt.Errorf("Xray relay absent candidate does not match plan")
	}
	return nil
}

func (executor Executor) transition(ctx context.Context, id domain.ID, from, to domain.TransactionState, detail string) error {
	if err := executor.Journal.TransitionOperation(ctx, id, from, to, executor.now(), detail); err != nil {
		return fmt.Errorf("journal Xray relay transition %s to %s: %w", from, to, err)
	}
	return nil
}

func (executor Executor) validate() error {
	if executor.Runner == nil || executor.Journal == nil || executor.Protector == nil {
		return fmt.Errorf("Xray relay executor dependencies are required")
	}
	if !filepath.IsAbs(executor.ConfigPath) || strings.TrimSpace(executor.ConfigPath) != executor.ConfigPath {
		return fmt.Errorf("absolute Xray relay configuration path is required")
	}
	return nil
}

func (executor Executor) run(ctx context.Context, command system.Command) error {
	result, err := executor.runResult(ctx, command)
	if err != nil || result.ExitCode != 0 {
		return errors.Join(err, fmt.Errorf("%s exited with code %d", command.Name, result.ExitCode))
	}
	return nil
}

func (executor Executor) runResult(ctx context.Context, command system.Command) (system.Result, error) {
	commandContext, cancel := context.WithTimeout(ctx, executor.timeout())
	defer cancel()
	return executor.Runner.Run(commandContext, command)
}

func (executor Executor) timeout() time.Duration {
	if executor.Timeout <= 0 || executor.Timeout > 30*time.Second {
		return 5 * time.Second
	}
	return executor.Timeout
}

func (executor Executor) now() time.Time {
	if executor.Now != nil {
		return executor.Now().UTC()
	}
	return time.Now().UTC()
}

func journalContext(id domain.ID, purpose string) string {
	return "xray-relay-transaction:" + string(id) + ":" + purpose
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
