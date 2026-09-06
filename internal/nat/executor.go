package nat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/secrets"
	"github.com/egress-manager/egress-manager/internal/system"
)

var ErrHostStateChanged = errors.New("host firewall state changed after planning")

type Journal interface {
	CreateOperation(context.Context, domain.Transaction) error
	TransitionOperation(context.Context, domain.ID, domain.TransactionState, domain.TransactionState, time.Time, string) error
	UnfinishedOperations(context.Context, int) ([]domain.Transaction, error)
}

type Verifier interface {
	Verify(context.Context, Plan) error
}

type JournalProtector interface {
	Seal(string, []byte) (secrets.Envelope, error)
	Open(string, secrets.Envelope) ([]byte, error)
}

type Executor struct {
	Runner    system.Runner
	Journal   Journal
	Verifier  Verifier
	Protector JournalProtector
	Timeout   time.Duration
	Now       func() time.Time
}

func InspectOwnedTable(ctx context.Context, runner system.Runner, family AddressFamily, timeout time.Duration) (bool, error) {
	if runner == nil {
		return false, fmt.Errorf("NAT inspection runner is required")
	}
	nativeFamily, table, err := family.nativeName()
	if err != nil {
		return false, err
	}
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 2 * time.Second
	}
	return inspectOwnedTablePresence(ctx, runner, timeout, nativeFamily, table)
}

func (executor Executor) Execute(ctx context.Context, id domain.ID, requestedChange string, plan Plan) error {
	if plan.Engine == "iptables" {
		return executor.executeIPTables(ctx, id, requestedChange, plan)
	}
	if executor.Runner == nil || executor.Journal == nil || executor.Verifier == nil {
		return fmt.Errorf("NAT executor dependencies are required")
	}
	if executor.Protector == nil {
		return fmt.Errorf("NAT journal protector is required")
	}
	if err := validateExecutablePlan(plan); err != nil {
		return err
	}
	if requestedChange == "" {
		return fmt.Errorf("requested change is required")
	}
	timeout := executor.timeout()
	snapshot, exists, err := executor.inspect(ctx, timeout, plan)
	if err != nil {
		return err
	}
	if exists != plan.TableExisted {
		return ErrHostStateChanged
	}
	if !exists {
		snapshot = "absent"
	}
	protectedSnapshot, err := executor.protectJournal(id, "snapshot", []byte(snapshot))
	if err != nil {
		return err
	}
	protectedCandidate, err := executor.protectJournal(id, "candidate", []byte(plan.Candidate))
	if err != nil {
		return err
	}
	now := executor.now()
	operation := domain.Transaction{
		ID: id, Operation: "nat_apply", State: domain.TransactionPrepared,
		RequestedChange: requestedChange, PreviousSnapshot: protectedSnapshot, CandidateConfig: protectedCandidate,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := executor.Journal.CreateOperation(ctx, operation); err != nil {
		return fmt.Errorf("journal prepared NAT operation: %w", err)
	}
	if err := executor.runNFT(ctx, timeout, true, plan.Candidate); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionPrepared, domain.TransactionFailed, executor.now(), "native_validation_failed")
		return fmt.Errorf("validate nftables candidate: %w", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionPrepared, domain.TransactionValidated, ""); err != nil {
		return err
	}
	if err := executor.transition(ctx, id, domain.TransactionValidated, domain.TransactionApplying, ""); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionValidated, domain.TransactionFailed, executor.now(), "journal_transition_failed")
		return err
	}
	if err := executor.runNFT(ctx, timeout, false, plan.Candidate); err != nil {
		rollbackErr := executor.rollback(ctx, operation, domain.TransactionApplying, "apply_failed")
		return errors.Join(fmt.Errorf("apply nftables candidate: %w", err), rollbackErr)
	}
	if err := executor.transition(ctx, id, domain.TransactionApplying, domain.TransactionVerifying, ""); err != nil {
		rollbackErr := executor.rollback(ctx, operation, domain.TransactionApplying, "journal_transition_failed")
		return errors.Join(err, rollbackErr)
	}
	if err := executor.Verifier.Verify(ctx, plan); err != nil {
		rollbackErr := executor.rollback(ctx, operation, domain.TransactionVerifying, "verification_failed")
		return errors.Join(fmt.Errorf("verify NAT operation: %w", err), rollbackErr)
	}
	if err := executor.transition(ctx, id, domain.TransactionVerifying, domain.TransactionCommitted, ""); err != nil {
		rollbackErr := executor.rollback(ctx, operation, domain.TransactionVerifying, "journal_transition_failed")
		return errors.Join(err, rollbackErr)
	}
	return nil
}

func (executor Executor) Recover(ctx context.Context) error {
	if executor.Runner == nil || executor.Journal == nil {
		return fmt.Errorf("NAT recovery dependencies are required")
	}
	operations, err := executor.Journal.UnfinishedOperations(ctx, 100)
	if err != nil {
		return fmt.Errorf("read unfinished NAT operations: %w", err)
	}
	var recoveryErrors []error
	for _, operation := range operations {
		if operation.Operation == "nat_apply_iptables" {
			if err := executor.recoverIPTables(ctx, operation); err != nil {
				recoveryErrors = append(recoveryErrors, err)
			}
			continue
		}
		if operation.Operation != "nat_apply" {
			continue
		}
		if _, _, _, err := executor.openNFTJournal(operation, true); err != nil {
			recoveryErrors = append(recoveryErrors, err)
			continue
		}
		switch operation.State {
		case domain.TransactionPrepared, domain.TransactionValidated:
			if err := executor.Journal.TransitionOperation(ctx, operation.ID, operation.State, domain.TransactionFailed, executor.now(), "interrupted_before_apply"); err != nil {
				recoveryErrors = append(recoveryErrors, err)
			}
		case domain.TransactionApplying, domain.TransactionVerifying:
			if err := executor.rollback(ctx, operation, operation.State, "interrupted_operation"); err != nil {
				recoveryErrors = append(recoveryErrors, err)
			}
		case domain.TransactionRollingBack:
			if err := executor.restoreSnapshot(ctx, operation); err != nil {
				_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "rollback_failed")
				recoveryErrors = append(recoveryErrors, err)
				continue
			}
			if err := executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, executor.now(), "interrupted_operation"); err != nil {
				recoveryErrors = append(recoveryErrors, err)
			}
		}
	}
	return errors.Join(recoveryErrors...)
}

func (executor Executor) rollback(ctx context.Context, operation domain.Transaction, from domain.TransactionState, detail string) error {
	if err := executor.Journal.TransitionOperation(ctx, operation.ID, from, domain.TransactionRollingBack, executor.now(), detail); err != nil {
		return fmt.Errorf("journal NAT rollback: %w", err)
	}
	if err := executor.restoreSnapshot(ctx, operation); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "rollback_failed")
		return fmt.Errorf("restore nftables snapshot: %w", err)
	}
	if err := executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, executor.now(), detail); err != nil {
		return fmt.Errorf("journal completed NAT rollback: %w", err)
	}
	return nil
}

func (executor Executor) restoreSnapshot(ctx context.Context, operation domain.Transaction) error {
	snapshot, candidate, _, err := executor.openNFTJournal(operation, true)
	if err != nil {
		return err
	}
	family, table, err := ownedTableFromCandidate(candidate)
	if err != nil {
		return err
	}
	plan := Plan{OwnedTable: family + " " + table}
	_, exists, inspectErr := executor.inspect(ctx, executor.timeout(), plan)
	if inspectErr != nil {
		return inspectErr
	}
	if snapshot == "absent" && !exists {
		return nil
	}
	var rollback strings.Builder
	if exists {
		fmt.Fprintf(&rollback, "delete table %s %s\n", family, table)
	}
	if snapshot != "absent" {
		rollback.WriteString(snapshot)
		if !strings.HasSuffix(snapshot, "\n") {
			rollback.WriteByte('\n')
		}
	}
	if rollback.Len() == 0 {
		return nil
	}
	if err := executor.runNFT(ctx, executor.timeout(), true, rollback.String()); err != nil {
		return fmt.Errorf("validate nftables rollback: %w", err)
	}
	return executor.runNFT(ctx, executor.timeout(), false, rollback.String())
}

func (executor Executor) inspect(ctx context.Context, timeout time.Duration, plan Plan) (string, bool, error) {
	parts := strings.Fields(plan.OwnedTable)
	if len(parts) != 2 || (parts[0] != "ip" && parts[0] != "ip6") || (parts[1] != "egm_nat4" && parts[1] != "egm_nat6") {
		return "", false, fmt.Errorf("invalid owned NAT table")
	}
	present, err := inspectOwnedTablePresence(ctx, executor.Runner, timeout, parts[0], parts[1])
	if err != nil {
		return "", false, err
	}
	if !present {
		return "", false, nil
	}
	snapshotContext, snapshotCancel := context.WithTimeout(ctx, timeout)
	defer snapshotCancel()
	result, err := executor.Runner.Run(snapshotContext, system.Command{Name: "nft", Args: []string{"list", "table", parts[0], parts[1]}})
	if err != nil || result.ExitCode != 0 {
		return "", false, errors.Join(err, fmt.Errorf("snapshot owned NAT table exited with code %d", result.ExitCode))
	}
	if len(bytes.TrimSpace(result.Stdout)) == 0 {
		return "", false, fmt.Errorf("owned NAT table snapshot is empty")
	}
	return string(result.Stdout), true, nil
}

func inspectOwnedTablePresence(ctx context.Context, runner system.Runner, timeout time.Duration, family, table string) (bool, error) {
	probeContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := runner.Run(probeContext, system.Command{Name: "nft", Args: []string{"list", "tables"}})
	if err != nil || result.ExitCode != 0 {
		return false, errors.Join(err, fmt.Errorf("list nftables tables exited with code %d", result.ExitCode))
	}
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "table" && fields[1] == family && fields[2] == table {
			return true, nil
		}
	}
	return false, nil
}

func (executor Executor) runNFT(ctx context.Context, timeout time.Duration, check bool, candidate string) error {
	arguments := []string{"--file", "-"}
	if check {
		arguments = []string{"--check", "--file", "-"}
	}
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := executor.Runner.Run(commandContext, system.Command{Name: "nft", Args: arguments, Stdin: []byte(candidate)})
	if err != nil || result.ExitCode != 0 {
		return errors.Join(err, fmt.Errorf("nft exited with code %d", result.ExitCode))
	}
	return nil
}

func (executor Executor) transition(ctx context.Context, id domain.ID, from, to domain.TransactionState, detail string) error {
	if err := executor.Journal.TransitionOperation(ctx, id, from, to, executor.now(), detail); err != nil {
		return fmt.Errorf("journal NAT transition %s to %s: %w", from, to, err)
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

func validateExecutablePlan(plan Plan) error {
	if len(plan.Candidate) == 0 || len(plan.Candidate) > 1<<20 || strings.Contains(strings.ToLower(plan.Candidate), "flush ruleset") {
		return fmt.Errorf("NAT candidate is empty, oversized, or globally destructive")
	}
	if plan.Engine == "iptables" {
		if plan.OwnedTable != "nat/EGM_PREROUTING nat/EGM_POSTROUTING filter/EGM_FORWARD" || plan.StateHash == "" || strings.Contains(plan.Candidate, "-F PREROUTING") || strings.Contains(plan.Candidate, "-F POSTROUTING") || strings.Contains(plan.Candidate, "-F FORWARD") {
			return fmt.Errorf("iptables plan metadata or ownership boundary is invalid")
		}
		return nil
	}
	family, table, err := ownedTableFromCandidate(plan.Candidate)
	if err != nil {
		return err
	}
	if plan.Engine != "nftables" || plan.OwnedTable != family+" "+table {
		return fmt.Errorf("NAT plan metadata does not match candidate")
	}
	return nil
}

func ownedTableFromCandidate(candidate string) (string, string, error) {
	for _, line := range strings.Split(candidate, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "table" && (fields[1] == "ip" || fields[1] == "ip6") && (fields[2] == "egm_nat4" || fields[2] == "egm_nat6") {
			return fields[1], fields[2], nil
		}
	}
	return "", "", fmt.Errorf("candidate does not define an owned NAT table")
}
