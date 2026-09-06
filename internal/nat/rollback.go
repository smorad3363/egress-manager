package nat

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func (executor Executor) RollbackCommitted(ctx context.Context, operation domain.Transaction) error {
	if executor.Runner == nil || executor.Journal == nil || executor.Protector == nil || operation.State != domain.TransactionCommitted {
		return fmt.Errorf("NAT operation is not eligible for rollback")
	}
	switch operation.Operation {
	case "nat_apply":
		return executor.rollbackCommittedNFT(ctx, operation)
	case "nat_apply_iptables":
		return executor.rollbackCommittedIPTables(ctx, operation)
	default:
		return fmt.Errorf("NAT operation is not eligible for rollback")
	}
}

func (executor Executor) rollbackCommittedNFT(ctx context.Context, operation domain.Transaction) error {
	snapshot, candidate, protected, err := executor.openNFTJournal(operation, false)
	if err != nil || !protected {
		return fmt.Errorf("NAT operation is not eligible for rollback: %w", err)
	}
	family, table, err := ownedTableFromCandidate(candidate)
	if err != nil {
		return err
	}
	current, exists, err := executor.inspect(ctx, executor.timeout(), Plan{OwnedTable: family + " " + table})
	if err != nil || !exists || canonicalNFTTable(current) != canonicalNFTTable(candidate) {
		return ErrHostStateChanged
	}
	rollback := nftRollbackCandidate(family, table, snapshot, true)
	if err := executor.runNFT(ctx, executor.timeout(), true, rollback); err != nil {
		return fmt.Errorf("validate committed nftables rollback: %w", err)
	}
	if err := executor.transition(ctx, operation.ID, domain.TransactionCommitted, domain.TransactionRollingBack, "operator_requested"); err != nil {
		return err
	}
	if err := executor.restoreSnapshot(ctx, operation); err != nil {
		return fmt.Errorf("restore committed nftables snapshot: %w", err)
	}
	return executor.transition(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, "operator_requested")
}

func (executor Executor) rollbackCommittedIPTables(ctx context.Context, operation domain.Transaction) error {
	encodedSnapshot, snapshotProtected, err := executor.openJournalField(operation.ID, "snapshot", operation.PreviousSnapshot, false)
	if err != nil || !snapshotProtected {
		return fmt.Errorf("NAT operation is not eligible for rollback: %w", err)
	}
	candidate, candidateProtected, err := executor.openJournalField(operation.ID, "candidate", operation.CandidateConfig, false)
	if err != nil || !candidateProtected {
		return fmt.Errorf("NAT operation is not eligible for rollback: %w", err)
	}
	var snapshot iptablesRecoverySnapshot
	if err := json.Unmarshal(encodedSnapshot, &snapshot); err != nil {
		return fmt.Errorf("decode protected iptables snapshot")
	}
	if _, _, _, err := iptablesExecutables(snapshot.Family); err != nil {
		return err
	}
	expected, err := iptablesStateFromCandidate(candidate, snapshot.State)
	if err != nil {
		return err
	}
	current, err := InspectIPTablesState(ctx, executor.Runner, snapshot.Family, executor.timeout())
	if err != nil || !reflect.DeepEqual(current, expected) {
		return ErrHostStateChanged
	}
	rollback := BuildIPTablesRollback(snapshot.State, current)
	if err := executor.runIPTablesRestore(ctx, snapshot.Family, true, rollback); err != nil {
		return fmt.Errorf("validate committed iptables rollback: %w", err)
	}
	if err := executor.transition(ctx, operation.ID, domain.TransactionCommitted, domain.TransactionRollingBack, "operator_requested"); err != nil {
		return err
	}
	if err := executor.restoreIPTablesSnapshot(ctx, snapshot.Family, snapshot.State); err != nil {
		return fmt.Errorf("restore committed iptables snapshot: %w", err)
	}
	return executor.transition(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, "operator_requested")
}

func nftRollbackCandidate(family, table, snapshot string, currentExists bool) string {
	var rollback strings.Builder
	if currentExists {
		fmt.Fprintf(&rollback, "delete table %s %s\n", family, table)
	}
	if snapshot != "absent" {
		rollback.WriteString(snapshot)
		if !strings.HasSuffix(snapshot, "\n") {
			rollback.WriteByte('\n')
		}
	}
	return rollback.String()
}

func canonicalNFTTable(value string) string {
	var result []string
	for _, raw := range strings.Split(value, "\n") {
		fields := strings.Fields(raw)
		if len(fields) == 0 || len(fields) >= 2 && fields[0] == "delete" && fields[1] == "table" {
			continue
		}
		for index := 0; index+4 < len(fields); index++ {
			if fields[index] == "counter" && fields[index+1] == "packets" && fields[index+3] == "bytes" {
				fields = append(fields[:index+1], fields[index+5:]...)
			}
		}
		result = append(result, strings.Join(fields, " "))
	}
	return strings.Join(result, "\n")
}

func iptablesStateFromCandidate(candidate []byte, previous IPTablesState) (IPTablesState, error) {
	marker := []byte("*filter\n")
	parts := strings.SplitN(string(candidate), string(marker), 2)
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "*nat\n") {
		return IPTablesState{}, fmt.Errorf("protected iptables candidate is invalid")
	}
	natPart := strings.ReplaceAll(parts[0], "-I PREROUTING 1 ", "-A PREROUTING ")
	natPart = strings.ReplaceAll(natPart, "-I POSTROUTING 1 ", "-A POSTROUTING ")
	filterPart := strings.ReplaceAll(parts[1], "-I FORWARD 1 ", "-A FORWARD ")
	state, err := ParseIPTablesState([]byte(natPart), append(marker, []byte(filterPart)...))
	if err != nil {
		return IPTablesState{}, err
	}
	state.PreroutingAnchor = state.PreroutingAnchor || previous.PreroutingAnchor
	state.PostroutingAnchor = state.PostroutingAnchor || previous.PostroutingAnchor
	state.ForwardAnchor = state.ForwardAnchor || previous.ForwardAnchor
	return state, nil
}
