package nat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/system"
)

func InspectIPTablesState(ctx context.Context, runner system.Runner, family AddressFamily, timeout time.Duration) (IPTablesState, error) {
	if runner == nil {
		return IPTablesState{}, fmt.Errorf("iptables inspection runner is required")
	}
	_, save, _, err := iptablesExecutables(family)
	if err != nil {
		return IPTablesState{}, err
	}
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 2 * time.Second
	}
	natOutput, err := runIPTablesCommand(ctx, runner, timeout, system.Command{Name: save, Args: []string{"-t", "nat"}})
	if err != nil {
		return IPTablesState{}, fmt.Errorf("inspect iptables nat table: %w", err)
	}
	filterOutput, err := runIPTablesCommand(ctx, runner, timeout, system.Command{Name: save, Args: []string{"-t", "filter"}})
	if err != nil {
		return IPTablesState{}, fmt.Errorf("inspect iptables filter table: %w", err)
	}
	return ParseIPTablesState(natOutput, filterOutput)
}

func iptablesStateHash(state IPTablesState) (string, error) {
	encoded, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (executor Executor) executeIPTables(ctx context.Context, id domain.ID, requestedChange string, plan Plan) error {
	if executor.Runner == nil || executor.Journal == nil {
		return fmt.Errorf("iptables executor dependencies are required")
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
	state, err := InspectIPTablesState(ctx, executor.Runner, plan.Family, timeout)
	if err != nil {
		return err
	}
	stateHash, err := iptablesStateHash(state)
	if err != nil {
		return err
	}
	if stateHash != plan.StateHash {
		return ErrHostStateChanged
	}
	snapshot, err := json.Marshal(iptablesRecoverySnapshot{Family: plan.Family, State: state})
	if err != nil {
		return fmt.Errorf("encode iptables snapshot: %w", err)
	}
	protectedSnapshot, err := executor.protectJournal(id, "snapshot", snapshot)
	if err != nil {
		return err
	}
	protectedCandidate, err := executor.protectJournal(id, "candidate", []byte(plan.Candidate))
	if err != nil {
		return err
	}
	now := executor.now()
	operation := domain.Transaction{
		ID: id, Operation: "nat_apply_iptables", State: domain.TransactionPrepared,
		RequestedChange: requestedChange, PreviousSnapshot: protectedSnapshot, CandidateConfig: protectedCandidate,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := executor.Journal.CreateOperation(ctx, operation); err != nil {
		return fmt.Errorf("journal prepared iptables operation: %w", err)
	}
	if err := executor.runIPTablesRestore(ctx, plan.Family, true, plan.Candidate); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionPrepared, domain.TransactionFailed, executor.now(), "native_validation_failed")
		return fmt.Errorf("validate iptables candidate: %w", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionPrepared, domain.TransactionValidated, ""); err != nil {
		return err
	}
	if err := executor.transition(ctx, id, domain.TransactionValidated, domain.TransactionApplying, ""); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionValidated, domain.TransactionFailed, executor.now(), "journal_transition_failed")
		return err
	}
	if err := executor.runIPTablesRestore(ctx, plan.Family, false, plan.Candidate); err != nil {
		rollbackErr := executor.rollbackIPTables(ctx, operation, plan.Family, state, domain.TransactionApplying, "apply_failed")
		return errors.Join(fmt.Errorf("apply iptables candidate: %w", err), rollbackErr)
	}
	if err := executor.transition(ctx, id, domain.TransactionApplying, domain.TransactionVerifying, ""); err != nil {
		rollbackErr := executor.rollbackIPTables(ctx, operation, plan.Family, state, domain.TransactionApplying, "journal_transition_failed")
		return errors.Join(err, rollbackErr)
	}
	if err := executor.verifyIPTables(ctx, plan); err != nil {
		rollbackErr := executor.rollbackIPTables(ctx, operation, plan.Family, state, domain.TransactionVerifying, "verification_failed")
		return errors.Join(fmt.Errorf("verify iptables operation: %w", err), rollbackErr)
	}
	if err := executor.transition(ctx, id, domain.TransactionVerifying, domain.TransactionCommitted, ""); err != nil {
		rollbackErr := executor.rollbackIPTables(ctx, operation, plan.Family, state, domain.TransactionVerifying, "journal_transition_failed")
		return errors.Join(err, rollbackErr)
	}
	return nil
}

type iptablesRecoverySnapshot struct {
	Family AddressFamily `json:"family"`
	State  IPTablesState `json:"state"`
}

func (executor Executor) recoverIPTables(ctx context.Context, operation domain.Transaction) error {
	encoded, snapshotProtected, err := executor.openJournalField(operation.ID, "snapshot", operation.PreviousSnapshot, true)
	if err != nil {
		return err
	}
	candidate, candidateProtected, err := executor.openJournalField(operation.ID, "candidate", operation.CandidateConfig, true)
	if err != nil {
		return err
	}
	if snapshotProtected != candidateProtected {
		return fmt.Errorf("NAT journal protection is inconsistent")
	}
	var snapshot iptablesRecoverySnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return fmt.Errorf("decode interrupted iptables snapshot: %w", err)
	}
	if _, _, _, err := iptablesExecutables(snapshot.Family); err != nil {
		return err
	}
	if _, err := iptablesStateFromCandidate(candidate, snapshot.State); err != nil {
		return err
	}
	switch operation.State {
	case domain.TransactionPrepared, domain.TransactionValidated:
		return executor.Journal.TransitionOperation(ctx, operation.ID, operation.State, domain.TransactionFailed, executor.now(), "interrupted_before_apply")
	case domain.TransactionApplying, domain.TransactionVerifying:
		return executor.rollbackIPTables(ctx, operation, snapshot.Family, snapshot.State, operation.State, "interrupted_operation")
	case domain.TransactionRollingBack:
		if err := executor.restoreIPTablesSnapshot(ctx, snapshot.Family, snapshot.State); err != nil {
			_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "rollback_failed")
			return err
		}
		return executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, executor.now(), "interrupted_operation")
	default:
		return nil
	}
}

func (executor Executor) rollbackIPTables(ctx context.Context, operation domain.Transaction, family AddressFamily, previous IPTablesState, from domain.TransactionState, detail string) error {
	if err := executor.Journal.TransitionOperation(ctx, operation.ID, from, domain.TransactionRollingBack, executor.now(), detail); err != nil {
		return fmt.Errorf("journal iptables rollback: %w", err)
	}
	if err := executor.restoreIPTablesSnapshot(ctx, family, previous); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "rollback_failed")
		return err
	}
	if err := executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, executor.now(), detail); err != nil {
		return fmt.Errorf("journal completed iptables rollback: %w", err)
	}
	return nil
}

func (executor Executor) restoreIPTablesSnapshot(ctx context.Context, family AddressFamily, previous IPTablesState) error {
	current, err := InspectIPTablesState(ctx, executor.Runner, family, executor.timeout())
	if err != nil {
		return err
	}
	candidate := BuildIPTablesRollback(previous, current)
	if err := executor.runIPTablesRestore(ctx, family, true, candidate); err != nil {
		return fmt.Errorf("validate iptables rollback: %w", err)
	}
	if err := executor.runIPTablesRestore(ctx, family, false, candidate); err != nil {
		return fmt.Errorf("restore iptables snapshot: %w", err)
	}
	return nil
}

func BuildIPTablesRollback(previous, current IPTablesState) string {
	var output strings.Builder
	output.WriteString("*nat\n")
	if previous.PreroutingChain || current.PreroutingChain {
		output.WriteString(":" + ipChainPrerouting + " - [0:0]\n")
	}
	if previous.PostroutingChain || current.PostroutingChain {
		output.WriteString(":" + ipChainPostrouting + " - [0:0]\n")
	}
	writeAnchorRestore(&output, "PREROUTING", ipChainPrerouting, "egm_anchor_prerouting", previous.PreroutingAnchor, current.PreroutingAnchor)
	writeAnchorRestore(&output, "POSTROUTING", ipChainPostrouting, "egm_anchor_postrouting", previous.PostroutingAnchor, current.PostroutingAnchor)
	writeChainRestore(&output, ipChainPrerouting, previous.PreroutingChain, current.PreroutingChain, previous.NatRules)
	writeChainRestore(&output, ipChainPostrouting, previous.PostroutingChain, current.PostroutingChain, previous.NatRules)
	output.WriteString("COMMIT\n*filter\n")
	if previous.ForwardChain || current.ForwardChain {
		output.WriteString(":" + ipChainForward + " - [0:0]\n")
	}
	writeAnchorRestore(&output, "FORWARD", ipChainForward, "egm_anchor_forward", previous.ForwardAnchor, current.ForwardAnchor)
	writeChainRestore(&output, ipChainForward, previous.ForwardChain, current.ForwardChain, previous.FilterRules)
	output.WriteString("COMMIT\n")
	return output.String()
}

func writeAnchorRestore(output *strings.Builder, builtin, target, comment string, previous, current bool) {
	rule := "-m comment --comment \"" + comment + "\" -j " + target
	if current && !previous {
		output.WriteString("-D " + builtin + " " + rule + "\n")
	}
	if previous && !current {
		output.WriteString("-I " + builtin + " 1 " + rule + "\n")
	}
}

func writeChainRestore(output *strings.Builder, chain string, previous, current bool, rules []string) {
	if current {
		output.WriteString("-F " + chain + "\n")
	}
	if previous {
		for _, rule := range rules {
			if strings.HasPrefix(rule, "-A "+chain+" ") {
				output.WriteString(rule + "\n")
			}
		}
	} else if current {
		output.WriteString("-X " + chain + "\n")
	}
}

func (executor Executor) runIPTablesRestore(ctx context.Context, family AddressFamily, check bool, candidate string) error {
	_, _, restore, err := iptablesExecutables(family)
	if err != nil {
		return err
	}
	args := []string{"--noflush"}
	if check {
		args = []string{"--test", "--noflush"}
	}
	_, err = runIPTablesCommand(ctx, executor.Runner, executor.timeout(), system.Command{Name: restore, Args: args, Stdin: []byte(candidate)})
	return err
}

func (executor Executor) verifyIPTables(ctx context.Context, plan Plan) error {
	command, _, _, err := iptablesExecutables(plan.Family)
	if err != nil {
		return err
	}
	checks := []system.Command{
		{Name: command, Args: []string{"-t", "nat", "-C", "PREROUTING", "-m", "comment", "--comment", "egm_anchor_prerouting", "-j", ipChainPrerouting}},
		{Name: command, Args: []string{"-t", "nat", "-C", "POSTROUTING", "-m", "comment", "--comment", "egm_anchor_postrouting", "-j", ipChainPostrouting}},
		{Name: command, Args: []string{"-t", "filter", "-C", "FORWARD", "-m", "comment", "--comment", "egm_anchor_forward", "-j", ipChainForward}},
	}
	for _, check := range checks {
		if _, err := runIPTablesCommand(ctx, executor.Runner, executor.timeout(), check); err != nil {
			return err
		}
	}
	for _, target := range plan.Targets {
		if _, err := runIPTablesCommand(ctx, executor.Runner, executor.timeout(), system.Command{Name: "ip", Args: []string{"route", "get", target.Address}}); err != nil {
			return fmt.Errorf("remote destination %s is unreachable: %w", target.Address, err)
		}
	}
	return nil
}

func iptablesExecutables(family AddressFamily) (string, string, string, error) {
	switch family {
	case IPv4:
		return "iptables", "iptables-save", "iptables-restore", nil
	case IPv6:
		return "ip6tables", "ip6tables-save", "ip6tables-restore", nil
	default:
		return "", "", "", fmt.Errorf("unsupported address family %q", family)
	}
}

func runIPTablesCommand(ctx context.Context, runner system.Runner, timeout time.Duration, command system.Command) ([]byte, error) {
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := runner.Run(commandContext, command)
	if err != nil || result.ExitCode != 0 {
		return nil, errors.Join(err, fmt.Errorf("%s exited with code %d", command.Name, result.ExitCode))
	}
	return result.Stdout, nil
}

type IPTablesCounterReader struct {
	Runner  system.Runner
	Timeout time.Duration
}

func (reader IPTablesCounterReader) Read(ctx context.Context, family AddressFamily) (CounterSnapshot, error) {
	if reader.Runner == nil {
		return CounterSnapshot{}, fmt.Errorf("iptables counter reader runner is required")
	}
	_, save, _, err := iptablesExecutables(family)
	if err != nil {
		return CounterSnapshot{}, err
	}
	timeout := reader.Timeout
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 2 * time.Second
	}
	output, err := runIPTablesCommand(ctx, reader.Runner, timeout, system.Command{Name: save, Args: []string{"-c", "-t", "filter"}})
	if err != nil {
		return CounterSnapshot{}, err
	}
	items, err := parseIPTablesCounters(output)
	if err != nil {
		return CounterSnapshot{}, err
	}
	return CounterSnapshot{Family: family, Items: items}, nil
}

func parseIPTablesCounters(input []byte) ([]ForwardCounters, error) {
	items := map[domain.ID]*ForwardCounters{}
	for _, line := range strings.Split(string(input), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "-A "+ipChainForward+" ") {
			continue
		}
		close := strings.Index(line, "]")
		if !strings.HasPrefix(line, "[") || close < 2 {
			continue
		}
		values := strings.Split(line[1:close], ":")
		if len(values) != 2 {
			return nil, fmt.Errorf("invalid iptables counter prefix")
		}
		packets, packetErr := strconv.ParseUint(values[0], 10, 64)
		bytes, byteErr := strconv.ParseUint(values[1], 10, 64)
		if packetErr != nil || byteErr != nil {
			return nil, fmt.Errorf("invalid iptables counter value")
		}
		comment := ""
		fields := strings.Fields(line)
		for index, field := range fields {
			if field == "--comment" && index+1 < len(fields) {
				comment = strings.Trim(fields[index+1], `"`)
				break
			}
		}
		id, kind, ok := counterComment(comment)
		if !ok {
			continue
		}
		item := items[id]
		if item == nil {
			item = &ForwardCounters{ForwardID: id}
			items[id] = item
		}
		if kind == "allow" {
			item.AcceptedPackets += packets
			item.AcceptedBytes += bytes
		} else {
			item.DroppedPackets += packets
			item.DroppedBytes += bytes
		}
	}
	ordered := make([]ForwardCounters, 0, len(items))
	for _, item := range items {
		ordered = append(ordered, *item)
	}
	sortCounters(ordered)
	return ordered, nil
}

func sortCounters(items []ForwardCounters) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].ForwardID < items[j-1].ForwardID; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
