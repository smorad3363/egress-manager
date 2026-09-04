package haproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/system"
)

var ErrStateChanged = errors.New("owned HAProxy state changed after planning")

type Journal interface {
	CreateOperation(context.Context, domain.Transaction) error
	TransitionOperation(context.Context, domain.ID, domain.TransactionState, domain.TransactionState, time.Time, string) error
	UnfinishedOperations(context.Context, int) ([]domain.Transaction, error)
}

type Runtime interface {
	Snapshot(context.Context) (RuntimeSnapshot, error)
}

type Executor struct {
	Runner     system.Runner
	Journal    Journal
	Runtime    Runtime
	ConfigPath string
	PIDPath    string
	Timeout    time.Duration
	Now        func() time.Time
}

type ApplyResponse struct {
	TransactionID domain.ID               `json:"transaction_id"`
	State         domain.TransactionState `json:"state"`
	Runtime       RuntimeSnapshot         `json:"runtime"`
}

type recoverySnapshot struct {
	Exists  bool   `json:"exists"`
	Content []byte `json:"content"`
}

func (executor Executor) Execute(ctx context.Context, id domain.ID, requestedChange string, plan Plan) (ApplyResponse, error) {
	if err := executor.validate(); err != nil {
		return ApplyResponse{}, err
	}
	if err := validateExecutablePlan(plan); err != nil {
		return ApplyResponse{}, err
	}
	if requestedChange == "" {
		return ApplyResponse{}, fmt.Errorf("requested HAProxy change is required")
	}
	content, state, err := inspectConfig(executor.ConfigPath)
	if err != nil {
		return ApplyResponse{}, err
	}
	if state.Hash != plan.StateHash {
		return ApplyResponse{}, ErrStateChanged
	}
	previousSnapshot := recoverySnapshot{Exists: state.Exists, Content: content}
	snapshotJSON, err := json.Marshal(previousSnapshot)
	if err != nil {
		return ApplyResponse{}, fmt.Errorf("encode HAProxy snapshot: %w", err)
	}
	now := executor.now()
	operation := domain.Transaction{ID: id, Operation: "haproxy_apply", State: domain.TransactionPrepared, RequestedChange: requestedChange, PreviousSnapshot: string(snapshotJSON), CandidateConfig: plan.Candidate, CreatedAt: now, UpdatedAt: now}
	if err := executor.Journal.CreateOperation(ctx, operation); err != nil {
		return ApplyResponse{}, fmt.Errorf("journal prepared HAProxy operation: %w", err)
	}
	temporaryPath, err := writeTemporaryConfig(executor.ConfigPath, []byte(plan.Candidate))
	if err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionPrepared, domain.TransactionFailed, executor.now(), "candidate_write_failed")
		return ApplyResponse{}, err
	}
	defer os.Remove(temporaryPath)
	if err := executor.run(ctx, system.Command{Name: "haproxy", Args: []string{"-c", "-f", temporaryPath}}); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionPrepared, domain.TransactionFailed, executor.now(), "native_validation_failed")
		return ApplyResponse{}, fmt.Errorf("validate HAProxy candidate: %w", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionPrepared, domain.TransactionValidated, ""); err != nil {
		return ApplyResponse{}, err
	}
	oldPID, oldPIDExists, err := readPID(executor.PIDPath)
	if err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionValidated, domain.TransactionFailed, executor.now(), "pid_inspection_failed")
		return ApplyResponse{}, err
	}
	if err := executor.transition(ctx, id, domain.TransactionValidated, domain.TransactionApplying, ""); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionValidated, domain.TransactionFailed, executor.now(), "journal_transition_failed")
		return ApplyResponse{}, err
	}
	if err := installTemporaryConfig(temporaryPath, executor.ConfigPath); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, previousSnapshot, domain.TransactionApplying, "config_install_failed", err)
	}
	if err := executor.reload(ctx, oldPID, oldPIDExists); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, previousSnapshot, domain.TransactionApplying, "reload_failed", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionApplying, domain.TransactionVerifying, ""); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, previousSnapshot, domain.TransactionApplying, "journal_transition_failed", err)
	}
	runtime, err := executor.verify(ctx)
	if err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, previousSnapshot, domain.TransactionVerifying, "verification_failed", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionVerifying, domain.TransactionCommitted, ""); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, previousSnapshot, domain.TransactionVerifying, "journal_transition_failed", err)
	}
	return ApplyResponse{TransactionID: id, State: domain.TransactionCommitted, Runtime: runtime}, nil
}

func (executor Executor) Recover(ctx context.Context) error {
	if err := executor.validate(); err != nil {
		return err
	}
	operations, err := executor.Journal.UnfinishedOperations(ctx, 100)
	if err != nil {
		return fmt.Errorf("read unfinished HAProxy operations: %w", err)
	}
	var recoveryErrors []error
	for _, operation := range operations {
		if operation.Operation != "haproxy_apply" {
			continue
		}
		var snapshot recoverySnapshot
		if err := json.Unmarshal([]byte(operation.PreviousSnapshot), &snapshot); err != nil {
			recoveryErrors = append(recoveryErrors, err)
			continue
		}
		if _, err := ParseState(snapshot.Content, snapshot.Exists); err != nil {
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

func (executor Executor) failWithRollback(ctx context.Context, operation domain.Transaction, previous recoverySnapshot, from domain.TransactionState, detail string, cause error) error {
	return errors.Join(cause, executor.rollback(ctx, operation, previous, from, detail))
}

func (executor Executor) rollback(ctx context.Context, operation domain.Transaction, previous recoverySnapshot, from domain.TransactionState, detail string) error {
	if err := executor.Journal.TransitionOperation(ctx, operation.ID, from, domain.TransactionRollingBack, executor.now(), detail); err != nil {
		return fmt.Errorf("journal HAProxy rollback: %w", err)
	}
	if err := executor.restore(ctx, previous); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "rollback_failed")
		return fmt.Errorf("restore HAProxy snapshot: %w", err)
	}
	return executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, executor.now(), detail)
}

func (executor Executor) restore(ctx context.Context, snapshot recoverySnapshot) error {
	if _, err := ParseState(snapshot.Content, snapshot.Exists); err != nil {
		return fmt.Errorf("validate HAProxy recovery snapshot: %w", err)
	}
	currentPID, currentPIDExists, err := readPID(executor.PIDPath)
	if err != nil {
		return err
	}
	if snapshot.Exists {
		temporary, err := writeTemporaryConfig(executor.ConfigPath, snapshot.Content)
		if err != nil {
			return err
		}
		defer os.Remove(temporary)
		if err := executor.run(ctx, system.Command{Name: "haproxy", Args: []string{"-c", "-f", temporary}}); err != nil {
			return err
		}
		if err := installTemporaryConfig(temporary, executor.ConfigPath); err != nil {
			return err
		}
		if err := executor.reload(ctx, currentPID, currentPIDExists); err != nil {
			return err
		}
		_, err = executor.verify(ctx)
		return err
	}
	if err := removeOwnedConfig(executor.ConfigPath); err != nil {
		return err
	}
	if currentPIDExists {
		return executor.run(ctx, system.Command{Name: "kill", Args: []string{"--signal", "TERM", "--", strconv.FormatUint(currentPID, 10)}})
	}
	return nil
}

func (executor Executor) reload(ctx context.Context, oldPID uint64, oldPIDExists bool) error {
	args := []string{"-D", "-W", "-f", executor.ConfigPath, "-p", executor.PIDPath}
	if oldPIDExists {
		args = append(args, "-sf", strconv.FormatUint(oldPID, 10))
	}
	return executor.run(ctx, system.Command{Name: "haproxy", Args: args})
}

func (executor Executor) verify(ctx context.Context) (RuntimeSnapshot, error) {
	deadline := time.Now().Add(executor.timeout())
	var lastErr error
	for {
		snapshot, err := executor.Runtime.Snapshot(ctx)
		if err == nil && snapshot.Info.PID > 0 {
			return snapshot, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return RuntimeSnapshot{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return RuntimeSnapshot{}, fmt.Errorf("verify HAProxy Runtime API: %w", lastErr)
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

func (executor Executor) validate() error {
	if executor.Runner == nil || executor.Journal == nil || executor.Runtime == nil {
		return fmt.Errorf("HAProxy executor dependencies are required")
	}
	if !filepath.IsAbs(executor.ConfigPath) || !filepath.IsAbs(executor.PIDPath) || filepath.Clean(executor.ConfigPath) == filepath.Clean(executor.PIDPath) {
		return fmt.Errorf("distinct absolute HAProxy config and PID paths are required")
	}
	return nil
}

func validateExecutablePlan(plan Plan) error {
	if plan.Engine != "haproxy" || plan.StateHash == "" || len(plan.Candidate) == 0 || len(plan.Candidate) > maximumCandidate || !strings.HasPrefix(plan.Candidate, "# Egress Manager owned HAProxy configuration\n") || strings.IndexByte(plan.Candidate, 0) >= 0 {
		return fmt.Errorf("HAProxy plan is invalid")
	}
	return nil
}

func inspectConfig(path string) ([]byte, State, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		state, stateErr := ParseState(nil, false)
		return nil, state, stateErr
	}
	if err != nil {
		return nil, State{}, fmt.Errorf("inspect owned HAProxy configuration: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > maximumCandidate {
		return nil, State{}, fmt.Errorf("owned HAProxy configuration is not a bounded regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, State{}, fmt.Errorf("read owned HAProxy configuration: %w", err)
	}
	if !strings.HasPrefix(string(content), "# Egress Manager owned HAProxy configuration\n") {
		return nil, State{}, fmt.Errorf("HAProxy configuration path contains foreign data")
	}
	state, err := ParseState(content, true)
	return content, state, err
}

func InspectState(path string) (State, error) {
	_, state, err := inspectConfig(path)
	return state, err
}

func writeTemporaryConfig(path string, content []byte) (string, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create HAProxy configuration directory: %w", err)
	}
	file, err := os.CreateTemp(directory, ".egress-haproxy-*.cfg")
	if err != nil {
		return "", fmt.Errorf("create temporary HAProxy configuration: %w", err)
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

func installTemporaryConfig(temporary, destination string) error {
	if filepath.Dir(temporary) != filepath.Dir(destination) {
		return fmt.Errorf("HAProxy temporary and destination paths must share a directory")
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("atomically install HAProxy configuration: %w", err)
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func removeOwnedConfig(path string) error {
	_, state, err := inspectConfig(path)
	if os.IsNotExist(err) || err == nil && !state.Exists {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove owned HAProxy configuration: %w", err)
	}
	return nil
}

func readPID(path string) (uint64, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > 32 {
		return 0, false, fmt.Errorf("HAProxy PID file is invalid")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, false, err
	}
	pid, err := strconv.ParseUint(strings.TrimSpace(string(content)), 10, 31)
	if err != nil || pid == 0 {
		return 0, false, fmt.Errorf("HAProxy PID file is invalid")
	}
	return pid, true, nil
}

func (executor Executor) transition(ctx context.Context, id domain.ID, from, to domain.TransactionState, detail string) error {
	if err := executor.Journal.TransitionOperation(ctx, id, from, to, executor.now(), detail); err != nil {
		return fmt.Errorf("journal HAProxy transition %s to %s: %w", from, to, err)
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
