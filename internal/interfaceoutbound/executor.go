package interfaceoutbound

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
	Runner           system.Runner
	Journal          Journal
	Protector        JournalProtector
	StatePath        string
	RuntimeDirectory string
	Timeout          time.Duration
	Now              func() time.Time
}

type ApplyResponse struct {
	TransactionID domain.ID               `json:"transaction_id"`
	State         domain.TransactionState `json:"state"`
	CandidateHash string                  `json:"candidate_hash"`
}

type runtimeConfigSnapshot struct {
	Entry   StateEntry `json:"entry"`
	Content []byte     `json:"content"`
}

type runtimeSnapshot struct {
	StateExists bool                    `json:"state_exists"`
	State       []byte                  `json:"state,omitempty"`
	Configs     []runtimeConfigSnapshot `json:"configs"`
}

func (executor Executor) Execute(ctx context.Context, id domain.ID, plan ExecutionPlan) (ApplyResponse, error) {
	if err := executor.validate(); err != nil {
		return ApplyResponse{}, err
	}
	if err := validateExecutionPlan(plan); err != nil {
		return ApplyResponse{}, err
	}
	state, err := InspectState(executor.StatePath, executor.RuntimeDirectory)
	if err != nil {
		return ApplyResponse{}, err
	}
	if state.Hash != plan.Review.StateHash {
		return ApplyResponse{}, ErrStateChanged
	}
	previous, err := executor.snapshot(state)
	if err != nil {
		return ApplyResponse{}, err
	}
	candidate := snapshotFromPlan(plan)
	protectedPrevious, err := executor.protect(id, "snapshot", previous)
	if err != nil {
		return ApplyResponse{}, err
	}
	protectedCandidate, err := executor.protect(id, "candidate", candidate)
	if err != nil {
		return ApplyResponse{}, err
	}
	now := executor.now()
	operation := domain.Transaction{
		ID: id, Operation: "interface_outbound_apply", State: domain.TransactionPrepared,
		RequestedChange:  fmt.Sprintf("apply %d enabled native interface outbounds; candidate %s", plan.Review.EnabledOutbounds, plan.Review.CandidateHash),
		PreviousSnapshot: protectedPrevious, CandidateConfig: protectedCandidate, CreatedAt: now, UpdatedAt: now,
	}
	if err := executor.Journal.CreateOperation(ctx, operation); err != nil {
		return ApplyResponse{}, fmt.Errorf("journal prepared interface outbound operation: %w", err)
	}
	if err := executor.validateNative(ctx, plan.entries); err != nil {
		executor.failPrepared(ctx, id, "native_validation_failed")
		return ApplyResponse{}, err
	}
	if err := executor.transition(ctx, id, domain.TransactionPrepared, domain.TransactionValidated, ""); err != nil {
		return ApplyResponse{}, err
	}
	if err := executor.transition(ctx, id, domain.TransactionValidated, domain.TransactionApplying, ""); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionValidated, domain.TransactionFailed, executor.now(), "journal_transition_failed")
		return ApplyResponse{}, err
	}
	if err := executor.apply(ctx, state, plan); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, candidate, domain.TransactionApplying, "native_apply_failed", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionApplying, domain.TransactionVerifying, ""); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, candidate, domain.TransactionApplying, "journal_transition_failed", err)
	}
	if err := executor.verifyEntries(ctx, candidate.Configs); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, candidate, domain.TransactionVerifying, "verification_failed", err)
	}
	verified, err := InspectState(executor.StatePath, executor.RuntimeDirectory)
	if err != nil || verified.Hash != plan.Review.CandidateHash {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, candidate, domain.TransactionVerifying, "state_verification_failed", errors.Join(err, ErrStateChanged))
	}
	if err := executor.transition(ctx, id, domain.TransactionVerifying, domain.TransactionCommitted, ""); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, candidate, domain.TransactionVerifying, "journal_transition_failed", err)
	}
	return ApplyResponse{TransactionID: id, State: domain.TransactionCommitted, CandidateHash: plan.Review.CandidateHash}, nil
}

func (executor Executor) Recover(ctx context.Context) error {
	if err := executor.validate(); err != nil {
		return err
	}
	operations, err := executor.Journal.UnfinishedOperations(ctx, 100)
	if err != nil {
		return fmt.Errorf("read unfinished interface outbound operations: %w", err)
	}
	var recoveryErrors []error
	for _, operation := range operations {
		if operation.Operation != "interface_outbound_apply" {
			continue
		}
		previous, previousErr := executor.open(operation.ID, "snapshot", operation.PreviousSnapshot)
		candidate, candidateErr := executor.open(operation.ID, "candidate", operation.CandidateConfig)
		if previousErr != nil || candidateErr != nil {
			recoveryErrors = append(recoveryErrors, errors.Join(previousErr, candidateErr))
			continue
		}
		switch operation.State {
		case domain.TransactionPrepared, domain.TransactionValidated:
			err = executor.Journal.TransitionOperation(ctx, operation.ID, operation.State, domain.TransactionFailed, executor.now(), "interrupted_before_apply")
		case domain.TransactionApplying, domain.TransactionVerifying:
			err = executor.rollback(ctx, operation, previous, candidate, operation.State, "interrupted_operation")
		case domain.TransactionRollingBack:
			err = executor.restore(ctx, previous, candidate)
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

func (executor Executor) validateNative(ctx context.Context, entries []candidateEntry) error {
	if err := executor.prepareRuntimeDirectory(); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(executor.RuntimeDirectory, ".validate-")
	if err != nil {
		return fmt.Errorf("create interface outbound validation directory: %w", err)
	}
	defer os.RemoveAll(temporary)
	if err := os.Chmod(temporary, 0o700); err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(temporary, filepath.Base(runtimeConfigPath(temporary, entry.state)))
		if err := os.WriteFile(path, entry.config, 0o600); err != nil {
			return fmt.Errorf("write interface outbound validation config: %w", err)
		}
		var command system.Command
		switch entry.state.Kind {
		case domain.OutboundWireGuard:
			command = system.Command{Name: "wg-quick", Args: []string{"strip", path}}
		case domain.OutboundOpenVPN:
			command = system.Command{Name: "openvpn", Args: []string{"--config", path, "--show-tls"}}
		default:
			return fmt.Errorf("unsupported native interface outbound validation kind")
		}
		if err := executor.run(ctx, command); err != nil {
			return fmt.Errorf("validate %s interface outbound: %w", entry.state.Kind, err)
		}
	}
	return nil
}

func (executor Executor) apply(ctx context.Context, state State, plan ExecutionPlan) error {
	current := make(map[domain.ID]StateEntry, len(state.Entries))
	for _, entry := range state.Entries {
		current[entry.ID] = entry
	}
	desired := make(map[domain.ID]candidateEntry, len(plan.entries))
	for _, entry := range plan.entries {
		desired[entry.state.ID] = entry
	}
	for _, entry := range state.Entries {
		candidate, exists := desired[entry.ID]
		if !exists || !equalStateEntry(entry, candidate.state) {
			if err := executor.stopEntry(ctx, entry, true); err != nil {
				return err
			}
		}
	}
	for _, entry := range plan.entries {
		existing, exists := current[entry.state.ID]
		if exists && equalStateEntry(existing, entry.state) {
			if err := executor.verifyEntry(ctx, runtimeConfigSnapshot{Entry: entry.state, Content: entry.config}); err != nil {
				return err
			}
			continue
		}
		if err := executor.writeRuntimeConfig(entry.state, entry.config); err != nil {
			return err
		}
		if err := executor.startEntry(ctx, entry.state); err != nil {
			return err
		}
	}
	if len(plan.candidate) == 0 {
		return executor.removeStateFile()
	}
	return atomicWrite(executor.StatePath, plan.candidate, 0o600)
}

func (executor Executor) startEntry(ctx context.Context, entry StateEntry) error {
	path := runtimeConfigPath(executor.RuntimeDirectory, entry)
	switch entry.Kind {
	case domain.OutboundWireGuard:
		if err := executor.run(ctx, system.Command{Name: "ip", Args: []string{"link", "add", "dev", entry.InterfaceName, "type", "wireguard"}}); err != nil {
			return fmt.Errorf("create WireGuard interface: %w", err)
		}
		if err := executor.run(ctx, system.Command{Name: "wg", Args: []string{"setconf", entry.InterfaceName, path}}); err != nil {
			return err
		}
		for _, address := range entry.Addresses {
			if err := executor.run(ctx, system.Command{Name: "ip", Args: []string{"address", "add", address, "dev", entry.InterfaceName}}); err != nil {
				return err
			}
		}
		if entry.MTU != 0 {
			if err := executor.run(ctx, system.Command{Name: "ip", Args: []string{"link", "set", "dev", entry.InterfaceName, "mtu", fmt.Sprint(entry.MTU)}}); err != nil {
				return err
			}
		}
		return executor.run(ctx, system.Command{Name: "ip", Args: []string{"link", "set", "dev", entry.InterfaceName, "up"}})
	case domain.OutboundOpenVPN:
		return executor.run(ctx, system.Command{Name: "systemd-run", Args: []string{
			"--unit", entry.ServiceName, "--collect", "--property=Type=notify", "--property=PrivateTmp=yes", "--property=ProtectSystem=strict", "--property=ProtectHome=yes", "--property=NoNewPrivileges=yes",
			"--property=DeviceAllow=/dev/net/tun rw", "--property=CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW CAP_IPC_LOCK CAP_DAC_OVERRIDE",
			"--", "openvpn", "--suppress-timestamps", "--nobind", "--config", path,
		}})
	default:
		return fmt.Errorf("unsupported interface outbound kind")
	}
}

func (executor Executor) stopEntry(ctx context.Context, entry StateEntry, removeConfig bool) error {
	switch entry.Kind {
	case domain.OutboundWireGuard:
		exists, kind, err := executor.inspectLink(ctx, entry.InterfaceName)
		if err != nil {
			return err
		}
		if exists {
			if kind != "wireguard" {
				return fmt.Errorf("owned WireGuard name now belongs to a foreign interface")
			}
			if err := executor.run(ctx, system.Command{Name: "ip", Args: []string{"link", "delete", "dev", entry.InterfaceName}}); err != nil {
				return err
			}
		}
	case domain.OutboundOpenVPN:
		if err := executor.stopService(ctx, entry.ServiceName); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported interface outbound kind")
	}
	if removeConfig {
		return executor.removeRuntimeConfig(entry)
	}
	return nil
}

func (executor Executor) stopService(ctx context.Context, service string) error {
	commandContext, cancel := context.WithTimeout(ctx, executor.timeout())
	defer cancel()
	result, err := executor.Runner.Run(commandContext, system.Command{Name: "systemctl", Args: []string{"stop", service}})
	if result.ExitCode == 0 || result.ExitCode == 5 {
		return nil
	}
	return errors.Join(err, fmt.Errorf("systemctl exited with code %d", result.ExitCode))
}

func (executor Executor) verifyEntries(ctx context.Context, configs []runtimeConfigSnapshot) error {
	for _, entry := range configs {
		if err := executor.verifyEntry(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

func (executor Executor) verifyEntry(ctx context.Context, snapshot runtimeConfigSnapshot) error {
	content, exists, err := inspectOwnedFile(runtimeConfigPath(executor.RuntimeDirectory, snapshot.Entry), MaximumImportBytes)
	if err != nil || !exists || hashBytes(content) != snapshot.Entry.ConfigHash || hashBytes(snapshot.Content) != snapshot.Entry.ConfigHash {
		return errors.Join(err, fmt.Errorf("interface outbound runtime configuration verification failed"))
	}
	exists, kind, err := executor.inspectLink(ctx, snapshot.Entry.InterfaceName)
	if err != nil || !exists {
		return errors.Join(err, fmt.Errorf("interface outbound link is unavailable"))
	}
	switch snapshot.Entry.Kind {
	case domain.OutboundWireGuard:
		if kind != "wireguard" {
			return fmt.Errorf("WireGuard interface kind verification failed")
		}
		return executor.run(ctx, system.Command{Name: "wg", Args: []string{"show", snapshot.Entry.InterfaceName, "latest-handshakes"}})
	case domain.OutboundOpenVPN:
		return executor.run(ctx, system.Command{Name: "systemctl", Args: []string{"is-active", "--quiet", snapshot.Entry.ServiceName}})
	default:
		return fmt.Errorf("unsupported interface outbound kind")
	}
}

func (executor Executor) inspectLink(ctx context.Context, name string) (bool, string, error) {
	commandContext, cancel := context.WithTimeout(ctx, executor.timeout())
	defer cancel()
	result, err := executor.Runner.Run(commandContext, system.Command{Name: "ip", Args: []string{"-json", "-details", "link", "show", "dev", name}})
	if result.ExitCode != 0 {
		if result.ExitCode == 1 || result.ExitCode == 255 {
			return false, "", nil
		}
		return false, "", errors.Join(err, fmt.Errorf("ip exited with code %d", result.ExitCode))
	}
	var links []struct {
		Name     string `json:"ifname"`
		LinkInfo struct {
			Kind string `json:"info_kind"`
		} `json:"linkinfo"`
	}
	if err := json.Unmarshal(result.Stdout, &links); err != nil || len(links) != 1 || links[0].Name != name {
		return false, "", fmt.Errorf("decode interface outbound link state")
	}
	return true, links[0].LinkInfo.Kind, nil
}

func (executor Executor) snapshot(state State) (runtimeSnapshot, error) {
	content, exists, err := inspectOwnedFile(executor.StatePath, maximumStateBytes)
	if err != nil || exists != state.Exists {
		return runtimeSnapshot{}, errors.Join(err, ErrStateChanged)
	}
	snapshot := runtimeSnapshot{StateExists: exists, State: content, Configs: []runtimeConfigSnapshot{}}
	for _, entry := range state.Entries {
		config, present := state.configs[entry.ID]
		if !present || hashBytes(config) != entry.ConfigHash {
			return runtimeSnapshot{}, ErrStateChanged
		}
		snapshot.Configs = append(snapshot.Configs, runtimeConfigSnapshot{Entry: entry, Content: append([]byte{}, config...)})
	}
	return snapshot, nil
}

func snapshotFromPlan(plan ExecutionPlan) runtimeSnapshot {
	snapshot := runtimeSnapshot{StateExists: len(plan.candidate) > 0, State: plan.Candidate(), Configs: make([]runtimeConfigSnapshot, 0, len(plan.entries))}
	for _, entry := range plan.entries {
		snapshot.Configs = append(snapshot.Configs, runtimeConfigSnapshot{Entry: entry.state, Content: append([]byte{}, entry.config...)})
	}
	return snapshot
}

func (executor Executor) restore(ctx context.Context, previous, candidate runtimeSnapshot) error {
	for _, config := range candidate.Configs {
		if err := executor.stopEntry(ctx, config.Entry, true); err != nil {
			return err
		}
	}
	for _, config := range previous.Configs {
		if err := executor.writeRuntimeConfig(config.Entry, config.Content); err != nil {
			return err
		}
		if err := executor.startEntry(ctx, config.Entry); err != nil {
			return err
		}
	}
	if previous.StateExists {
		if err := atomicWrite(executor.StatePath, previous.State, 0o600); err != nil {
			return err
		}
	} else if err := executor.removeStateFile(); err != nil {
		return err
	}
	return executor.verifyEntries(ctx, previous.Configs)
}

func (executor Executor) rollback(ctx context.Context, operation domain.Transaction, previous, candidate runtimeSnapshot, from domain.TransactionState, detail string) error {
	if err := executor.Journal.TransitionOperation(ctx, operation.ID, from, domain.TransactionRollingBack, executor.now(), detail); err != nil {
		return fmt.Errorf("journal interface outbound rollback: %w", err)
	}
	if err := executor.restore(ctx, previous, candidate); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "rollback_failed")
		return fmt.Errorf("restore interface outbound snapshot: %w", err)
	}
	return executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, executor.now(), detail)
}

func (executor Executor) failWithRollback(ctx context.Context, operation domain.Transaction, candidate runtimeSnapshot, from domain.TransactionState, detail string, cause error) error {
	previous, err := executor.open(operation.ID, "snapshot", operation.PreviousSnapshot)
	if err != nil {
		_ = executor.Journal.TransitionOperation(ctx, operation.ID, from, domain.TransactionFailed, executor.now(), "snapshot_authentication_failed")
		return errors.Join(cause, err)
	}
	return errors.Join(cause, executor.rollback(ctx, operation, previous, candidate, from, detail))
}

func (executor Executor) protect(id domain.ID, purpose string, snapshot runtimeSnapshot) (string, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("encode interface outbound %s: %w", purpose, err)
	}
	envelope, err := executor.Protector.Seal(journalContext(id, purpose), encoded)
	if err != nil {
		return "", fmt.Errorf("protect interface outbound %s: %w", purpose, err)
	}
	protected, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode protected interface outbound %s: %w", purpose, err)
	}
	return string(protected), nil
}

func (executor Executor) open(id domain.ID, purpose, protected string) (runtimeSnapshot, error) {
	var envelope secrets.Envelope
	if err := json.Unmarshal([]byte(protected), &envelope); err != nil {
		return runtimeSnapshot{}, fmt.Errorf("decode protected interface outbound %s", purpose)
	}
	plaintext, err := executor.Protector.Open(journalContext(id, purpose), envelope)
	if err != nil {
		return runtimeSnapshot{}, fmt.Errorf("authenticate interface outbound %s: %w", purpose, err)
	}
	var snapshot runtimeSnapshot
	if err := json.Unmarshal(plaintext, &snapshot); err != nil {
		return runtimeSnapshot{}, fmt.Errorf("decode interface outbound %s", purpose)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return runtimeSnapshot{}, err
	}
	return snapshot, nil
}

func validateSnapshot(snapshot runtimeSnapshot) error {
	if !snapshot.StateExists {
		if len(snapshot.State) != 0 || len(snapshot.Configs) != 0 {
			return fmt.Errorf("absent interface outbound snapshot is invalid")
		}
		return nil
	}
	document, err := parseState(snapshot.State)
	if err != nil || len(document.Entries) != len(snapshot.Configs) {
		return fmt.Errorf("interface outbound snapshot state is invalid")
	}
	for index, config := range snapshot.Configs {
		if !equalStateEntry(document.Entries[index], config.Entry) || hashBytes(config.Content) != config.Entry.ConfigHash {
			return fmt.Errorf("interface outbound snapshot configuration is invalid")
		}
	}
	return nil
}

func validateExecutionPlan(plan ExecutionPlan) error {
	if plan.Review.Engine != "native-interface-outbounds" || plan.Review.StateHash == "" || plan.Review.CandidateHash == "" || plan.Review.CandidateExists != (len(plan.candidate) > 0) || plan.Review.EnabledOutbounds != len(plan.entries) {
		return fmt.Errorf("interface outbound execution plan is invalid")
	}
	if plan.Review.CandidateHash != hashState(plan.candidate, len(plan.candidate) > 0, plan.entries) {
		return fmt.Errorf("interface outbound candidate hash does not match plan")
	}
	if len(plan.candidate) == 0 {
		if len(plan.entries) != 0 {
			return fmt.Errorf("empty interface outbound candidate has entries")
		}
		return nil
	}
	document, err := parseState(plan.candidate)
	if err != nil || len(document.Entries) != len(plan.entries) {
		return fmt.Errorf("interface outbound candidate is not project-owned")
	}
	for index, entry := range plan.entries {
		if !equalStateEntry(document.Entries[index], entry.state) || hashBytes(entry.config) != entry.state.ConfigHash {
			return fmt.Errorf("interface outbound candidate configuration mismatch")
		}
	}
	return nil
}

func (executor Executor) writeRuntimeConfig(entry StateEntry, content []byte) error {
	if hashBytes(content) != entry.ConfigHash {
		return fmt.Errorf("interface outbound runtime configuration hash mismatch")
	}
	if err := executor.prepareRuntimeDirectory(); err != nil {
		return err
	}
	return atomicWrite(runtimeConfigPath(executor.RuntimeDirectory, entry), content, 0o600)
}

func (executor Executor) removeRuntimeConfig(entry StateEntry) error {
	path := runtimeConfigPath(executor.RuntimeDirectory, entry)
	content, exists, err := inspectOwnedFile(path, MaximumImportBytes)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if hashBytes(content) != entry.ConfigHash {
		return ErrStateChanged
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove interface outbound runtime configuration: %w", err)
	}
	return nil
}

func (executor Executor) removeStateFile() error {
	_, exists, err := inspectOwnedFile(executor.StatePath, maximumStateBytes)
	if err != nil || !exists {
		return err
	}
	if err := os.Remove(executor.StatePath); err != nil {
		return fmt.Errorf("remove interface outbound state: %w", err)
	}
	return nil
}

func atomicWrite(path string, content []byte, mode os.FileMode) error {
	if !filepath.IsAbs(path) || len(content) == 0 {
		return fmt.Errorf("absolute interface outbound destination and non-empty content are required")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create interface outbound directory: %w", err)
	}
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("interface outbound destination is unsafe")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := os.CreateTemp(directory, ".egress-interface-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(mode); err != nil {
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
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("atomically install interface outbound file: %w", err)
	}
	return nil
}

func (executor Executor) prepareRuntimeDirectory() error {
	if err := os.MkdirAll(executor.RuntimeDirectory, 0o700); err != nil {
		return fmt.Errorf("create interface outbound runtime directory: %w", err)
	}
	info, err := os.Lstat(executor.RuntimeDirectory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.Join(err, fmt.Errorf("interface outbound runtime directory is unsafe"))
	}
	return nil
}

func (executor Executor) run(ctx context.Context, command system.Command) error {
	_, err := executor.output(ctx, command)
	return err
}

func (executor Executor) output(ctx context.Context, command system.Command) ([]byte, error) {
	commandContext, cancel := context.WithTimeout(ctx, executor.timeout())
	defer cancel()
	result, err := executor.Runner.Run(commandContext, command)
	if err != nil || result.ExitCode != 0 {
		return nil, errors.Join(err, fmt.Errorf("%s exited with code %d", command.Name, result.ExitCode))
	}
	return result.Stdout, nil
}

func (executor Executor) transition(ctx context.Context, id domain.ID, from, to domain.TransactionState, detail string) error {
	if err := executor.Journal.TransitionOperation(ctx, id, from, to, executor.now(), detail); err != nil {
		return fmt.Errorf("journal interface outbound transition %s to %s: %w", from, to, err)
	}
	return nil
}

func (executor Executor) failPrepared(ctx context.Context, id domain.ID, detail string) {
	_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionPrepared, domain.TransactionFailed, executor.now(), detail)
}

func (executor Executor) validate() error {
	if executor.Runner == nil || executor.Journal == nil || executor.Protector == nil {
		return fmt.Errorf("interface outbound executor dependencies are required")
	}
	if !filepath.IsAbs(executor.StatePath) || !filepath.IsAbs(executor.RuntimeDirectory) || filepath.Clean(executor.StatePath) == filepath.Clean(executor.RuntimeDirectory) || filepath.Clean(executor.StatePath) == filepath.Clean(filepath.Dir(executor.StatePath)) {
		return fmt.Errorf("distinct absolute interface outbound paths are required")
	}
	if filepath.Clean(filepath.Dir(executor.StatePath)) != filepath.Clean(executor.RuntimeDirectory) {
		return fmt.Errorf("interface outbound state must be inside the runtime directory")
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
		return 10 * time.Second
	}
	return executor.Timeout
}

func journalContext(id domain.ID, purpose string) string {
	return "interface-outbound-transaction:" + string(id) + ":" + purpose
}
