package routeengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	managedInterface "github.com/egress-manager/egress-manager/internal/interfaceoutbound"
	"github.com/egress-manager/egress-manager/internal/routing"
	"github.com/egress-manager/egress-manager/internal/secrets"
	"github.com/egress-manager/egress-manager/internal/singbox"
	"github.com/egress-manager/egress-manager/internal/system"
)

const (
	ownedSingBoxService = "egress-manager-sing-box.service"
	maximumOwnedFile    = 128 << 10
)

var ErrStateChanged = errors.New("owned route-engine state changed after planning")

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
	Runner                    system.Runner
	Journal                   Journal
	Protector                 JournalProtector
	SingBoxConfigPath         string
	RoutingStatePath          string
	InterfaceStatePath        string
	InterfaceRuntimeDirectory string
	Timeout                   time.Duration
	Now                       func() time.Time
}

type ApplyResponse struct {
	TransactionID         domain.ID               `json:"transaction_id"`
	State                 domain.TransactionState `json:"state"`
	CombinedCandidateHash string                  `json:"combined_candidate_hash"`
}

type fileSnapshot struct {
	Exists  bool   `json:"exists"`
	Content []byte `json:"content,omitempty"`
}

type protectedSnapshot struct {
	SingBox secrets.Envelope `json:"sing_box"`
	Routing secrets.Envelope `json:"routing"`
	NFT     secrets.Envelope `json:"nft"`
}

func (executor Executor) Execute(ctx context.Context, id domain.ID, plan Plan) (ApplyResponse, error) {
	if err := executor.validate(); err != nil {
		return ApplyResponse{}, err
	}
	interfaceState, err := managedInterface.InspectState(executor.InterfaceStatePath, executor.InterfaceRuntimeDirectory)
	if err != nil {
		return ApplyResponse{}, err
	}
	if interfaceState.Hash != plan.Review.InterfaceStateHash {
		return ApplyResponse{}, ErrStateChanged
	}
	validatedPlan, err := BuildPlan(plan.singBox, plan.routing, plan.native, interfaceState)
	if err != nil {
		return ApplyResponse{}, err
	}
	if !reflect.DeepEqual(validatedPlan.Review, plan.Review) {
		return ApplyResponse{}, fmt.Errorf("coordinated route-engine review does not match its candidates")
	}
	singBoxSnapshot, err := inspectOwnedFile(executor.SingBoxConfigPath, func(content []byte, exists bool) error {
		_, parseErr := singbox.ParseState(content, exists)
		return parseErr
	})
	if err != nil {
		return ApplyResponse{}, err
	}
	routingSnapshot, err := inspectOwnedFile(executor.RoutingStatePath, func(content []byte, exists bool) error {
		_, parseErr := routing.ParseState(content, exists)
		return parseErr
	})
	if err != nil {
		return ApplyResponse{}, err
	}
	singBoxState, _ := singbox.ParseState(singBoxSnapshot.Content, singBoxSnapshot.Exists)
	routingState, _ := routing.ParseState(routingSnapshot.Content, routingSnapshot.Exists)
	if singBoxState.Hash != plan.Review.SingBoxStateHash || routingState.Hash != plan.Review.RoutingStateHash {
		return ApplyResponse{}, ErrStateChanged
	}
	nftSnapshot, err := executor.inspectNFT(ctx)
	if err != nil {
		return ApplyResponse{}, err
	}
	if nftSnapshot.Exists != plan.native.TableExisted {
		return ApplyResponse{}, ErrStateChanged
	}
	protected, err := executor.protectSnapshot(id, singBoxSnapshot, routingSnapshot, nftSnapshot)
	if err != nil {
		return ApplyResponse{}, err
	}
	protectedJSON, err := json.Marshal(protected)
	if err != nil {
		return ApplyResponse{}, fmt.Errorf("encode protected route-engine snapshot: %w", err)
	}
	candidateEnvelope, err := executor.Protector.Seal(journalContext(id, "candidate-routing"), plan.routing.Candidate())
	if err != nil {
		return ApplyResponse{}, fmt.Errorf("protect routing candidate: %w", err)
	}
	candidateJSON, err := json.Marshal(candidateEnvelope)
	if err != nil {
		return ApplyResponse{}, fmt.Errorf("encode protected routing candidate: %w", err)
	}
	now := executor.now()
	operation := domain.Transaction{
		ID: id, Operation: "route_engine_apply", State: domain.TransactionPrepared,
		RequestedChange:  fmt.Sprintf("apply %d protected egress routes; combined candidate %s", plan.Review.EnabledRoutes, plan.Review.CombinedCandidateHash),
		PreviousSnapshot: string(protectedJSON), CandidateConfig: string(candidateJSON), CreatedAt: now, UpdatedAt: now,
	}
	if err := executor.Journal.CreateOperation(ctx, operation); err != nil {
		return ApplyResponse{}, fmt.Errorf("journal prepared route-engine operation: %w", err)
	}
	temporary, err := writeTemporary(executor.SingBoxConfigPath, plan.singBox.Candidate())
	if err != nil {
		executor.failPrepared(ctx, id, "candidate_write_failed")
		return ApplyResponse{}, err
	}
	defer os.Remove(temporary)
	if err := executor.run(ctx, system.Command{Name: "sing-box", Args: []string{"check", "-c", temporary}}); err != nil {
		executor.failPrepared(ctx, id, "native_validation_failed")
		return ApplyResponse{}, fmt.Errorf("validate routed sing-box candidate: %w", err)
	}
	if err := executor.run(ctx, system.Command{Name: "nft", Args: []string{"--check", "--file", "-"}, Stdin: plan.native.NFTCandidate()}); err != nil {
		executor.failPrepared(ctx, id, "native_validation_failed")
		return ApplyResponse{}, fmt.Errorf("validate route leak-control candidate: %w", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionPrepared, domain.TransactionValidated, ""); err != nil {
		return ApplyResponse{}, err
	}
	if err := executor.transition(ctx, id, domain.TransactionValidated, domain.TransactionApplying, ""); err != nil {
		executor.failValidated(ctx, id)
		return ApplyResponse{}, err
	}
	if err := executor.run(ctx, system.Command{Name: "nft", Args: []string{"--file", "-"}, Stdin: plan.native.NFTCandidate()}); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, plan.routing.Candidate(), domain.TransactionApplying, "nft_apply_failed", err)
	}
	if err := installTemporary(temporary, executor.SingBoxConfigPath); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, plan.routing.Candidate(), domain.TransactionApplying, "singbox_install_failed", err)
	}
	if err := executor.run(ctx, system.Command{Name: "systemctl", Args: []string{"restart", ownedSingBoxService}}); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, plan.routing.Candidate(), domain.TransactionApplying, "singbox_restart_failed", err)
	}
	if err := executor.verifyTUNs(ctx, plan.routing.Candidate()); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, plan.routing.Candidate(), domain.TransactionApplying, "tun_verification_failed", err)
	}
	if err := executor.replaceIP(ctx, routingSnapshot.Content, routingSnapshot.Exists, plan); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, plan.routing.Candidate(), domain.TransactionApplying, "iproute_apply_failed", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionApplying, domain.TransactionVerifying, ""); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, plan.routing.Candidate(), domain.TransactionApplying, "journal_transition_failed", err)
	}
	if err := executor.verify(ctx, plan); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, plan.routing.Candidate(), domain.TransactionVerifying, "verification_failed", err)
	}
	if err := atomicWrite(executor.RoutingStatePath, plan.routing.Candidate()); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, plan.routing.Candidate(), domain.TransactionVerifying, "state_commit_failed", err)
	}
	if err := executor.transition(ctx, id, domain.TransactionVerifying, domain.TransactionCommitted, ""); err != nil {
		return ApplyResponse{}, executor.failWithRollback(ctx, operation, plan.routing.Candidate(), domain.TransactionVerifying, "journal_transition_failed", err)
	}
	return ApplyResponse{TransactionID: id, State: domain.TransactionCommitted, CombinedCandidateHash: plan.Review.CombinedCandidateHash}, nil
}

func (executor Executor) Recover(ctx context.Context) error {
	if err := executor.validate(); err != nil {
		return err
	}
	operations, err := executor.Journal.UnfinishedOperations(ctx, 100)
	if err != nil {
		return fmt.Errorf("read unfinished route-engine operations: %w", err)
	}
	var recoveryErrors []error
	for _, operation := range operations {
		err = nil
		if operation.Operation != "route_engine_apply" {
			continue
		}
		switch operation.State {
		case domain.TransactionPrepared, domain.TransactionValidated:
			err = executor.Journal.TransitionOperation(ctx, operation.ID, operation.State, domain.TransactionFailed, executor.now(), "interrupted_before_apply")
		case domain.TransactionApplying, domain.TransactionVerifying, domain.TransactionRollingBack:
			snapshot, openErr := executor.openSnapshot(operation)
			candidate, candidateErr := executor.openCandidate(operation)
			if openErr != nil || candidateErr != nil {
				_ = executor.Journal.TransitionOperation(ctx, operation.ID, operation.State, domain.TransactionFailed, executor.now(), "snapshot_authentication_failed")
				err = errors.Join(openErr, candidateErr)
				break
			}
			from := operation.State
			if from != domain.TransactionRollingBack {
				if transitionErr := executor.Journal.TransitionOperation(ctx, operation.ID, from, domain.TransactionRollingBack, executor.now(), "interrupted_operation"); transitionErr != nil {
					err = transitionErr
					break
				}
			}
			err = executor.restore(ctx, candidate, snapshot)
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

func (executor Executor) failWithRollback(ctx context.Context, operation domain.Transaction, candidate []byte, from domain.TransactionState, detail string, cause error) error {
	if err := executor.Journal.TransitionOperation(ctx, operation.ID, from, domain.TransactionRollingBack, executor.now(), detail); err != nil {
		return errors.Join(cause, fmt.Errorf("journal route-engine rollback: %w", err))
	}
	snapshot, err := executor.openSnapshot(operation)
	if err != nil {
		_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "snapshot_authentication_failed")
		return errors.Join(cause, err)
	}
	if err := executor.restore(ctx, candidate, snapshot); err != nil {
		_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "rollback_failed")
		return errors.Join(cause, err)
	}
	if err := executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, executor.now(), detail); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (executor Executor) restore(ctx context.Context, candidate []byte, snapshot routeEngineSnapshot) error {
	candidateState, err := routing.ParseState(candidate, true)
	if err != nil {
		return fmt.Errorf("validate rollback routing candidate: %w", err)
	}
	if err := executor.removeOwnedIP(ctx, candidateState.Routes); err != nil {
		return fmt.Errorf("remove candidate policy routing: %w", err)
	}
	if err := executor.restoreSingBox(ctx, snapshot.SingBox); err != nil {
		return err
	}
	if snapshot.Routing.Exists {
		previous, parseErr := routing.ParseState(snapshot.Routing.Content, true)
		if parseErr != nil {
			return parseErr
		}
		if err := executor.verifyTUNs(ctx, snapshot.Routing.Content); err != nil && len(previous.Routes) != 0 {
			return err
		}
		ipv4, ipv6, batchErr := routing.BuildIPBatches(previous.Routes)
		if batchErr != nil {
			return batchErr
		}
		if len(ipv4) != 0 {
			if err := executor.run(ctx, system.Command{Name: "ip", Args: []string{"-4", "-batch", "-"}, Stdin: ipv4}); err != nil {
				return err
			}
		}
		if len(ipv6) != 0 {
			if err := executor.run(ctx, system.Command{Name: "ip", Args: []string{"-6", "-batch", "-"}, Stdin: ipv6}); err != nil {
				return err
			}
		}
	}
	if err := executor.restoreNFT(ctx, snapshot.NFT); err != nil {
		return err
	}
	return restoreOwnedFile(executor.RoutingStatePath, snapshot.Routing, func(content []byte, exists bool) error {
		_, err := routing.ParseState(content, exists)
		return err
	})
}

func (executor Executor) restoreSingBox(ctx context.Context, snapshot fileSnapshot) error {
	if !snapshot.Exists {
		if err := executor.run(ctx, system.Command{Name: "systemctl", Args: []string{"stop", ownedSingBoxService}}); err != nil {
			return err
		}
		return removeOwnedFile(executor.SingBoxConfigPath, func(content []byte, exists bool) error {
			_, err := singbox.ParseState(content, exists)
			return err
		})
	}
	temporary, err := writeTemporary(executor.SingBoxConfigPath, snapshot.Content)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := executor.run(ctx, system.Command{Name: "sing-box", Args: []string{"check", "-c", temporary}}); err != nil {
		return err
	}
	if err := installTemporary(temporary, executor.SingBoxConfigPath); err != nil {
		return err
	}
	return executor.run(ctx, system.Command{Name: "systemctl", Args: []string{"restart", ownedSingBoxService}})
}

func (executor Executor) restoreNFT(ctx context.Context, snapshot fileSnapshot) error {
	current, err := executor.inspectNFT(ctx)
	if err != nil {
		return err
	}
	var candidate bytes.Buffer
	if current.Exists {
		candidate.WriteString("delete table inet egm_egress\n")
	}
	if snapshot.Exists {
		candidate.Write(snapshot.Content)
	}
	if candidate.Len() == 0 {
		return nil
	}
	if err := executor.run(ctx, system.Command{Name: "nft", Args: []string{"--check", "--file", "-"}, Stdin: candidate.Bytes()}); err != nil {
		return err
	}
	return executor.run(ctx, system.Command{Name: "nft", Args: []string{"--file", "-"}, Stdin: candidate.Bytes()})
}

type routeEngineSnapshot struct {
	SingBox fileSnapshot
	Routing fileSnapshot
	NFT     fileSnapshot
}

func (executor Executor) openSnapshot(operation domain.Transaction) (routeEngineSnapshot, error) {
	var protected protectedSnapshot
	if err := decodeStrict([]byte(operation.PreviousSnapshot), &protected); err != nil {
		return routeEngineSnapshot{}, fmt.Errorf("decode protected route-engine snapshot")
	}
	items := []struct {
		purpose  string
		envelope secrets.Envelope
	}{
		{"snapshot-singbox", protected.SingBox},
		{"snapshot-routing", protected.Routing},
		{"snapshot-nft", protected.NFT},
	}
	decoded := make([]fileSnapshot, 3)
	for index, item := range items {
		plaintext, err := executor.Protector.Open(journalContext(operation.ID, item.purpose), item.envelope)
		if err != nil {
			return routeEngineSnapshot{}, fmt.Errorf("authenticate protected route-engine snapshot: %w", err)
		}
		if bytes.Equal(plaintext, []byte("absent\x00")) {
			decoded[index] = fileSnapshot{}
		} else {
			decoded[index] = fileSnapshot{Exists: true, Content: plaintext}
		}
	}
	if _, err := singbox.ParseState(decoded[0].Content, decoded[0].Exists); err != nil {
		return routeEngineSnapshot{}, err
	}
	if _, err := routing.ParseState(decoded[1].Content, decoded[1].Exists); err != nil {
		return routeEngineSnapshot{}, err
	}
	if decoded[2].Exists && (len(decoded[2].Content) == 0 || len(decoded[2].Content) > maximumOwnedFile || !bytes.Contains(decoded[2].Content, []byte("table inet egm_egress"))) {
		return routeEngineSnapshot{}, fmt.Errorf("protected nftables snapshot is invalid")
	}
	return routeEngineSnapshot{SingBox: decoded[0], Routing: decoded[1], NFT: decoded[2]}, nil
}

func (executor Executor) openCandidate(operation domain.Transaction) ([]byte, error) {
	var envelope secrets.Envelope
	if err := decodeStrict([]byte(operation.CandidateConfig), &envelope); err != nil {
		return nil, fmt.Errorf("decode protected routing candidate")
	}
	candidate, err := executor.Protector.Open(journalContext(operation.ID, "candidate-routing"), envelope)
	if err != nil {
		return nil, fmt.Errorf("authenticate protected routing candidate: %w", err)
	}
	if _, err := routing.ParseState(candidate, true); err != nil {
		return nil, err
	}
	return candidate, nil
}

func (executor Executor) replaceIP(ctx context.Context, previous []byte, previousExists bool, plan Plan) error {
	if previousExists {
		state, err := routing.ParseState(previous, true)
		if err != nil {
			return err
		}
		if err := executor.removeOwnedIP(ctx, state.Routes); err != nil {
			return err
		}
	}
	if err := executor.run(ctx, system.Command{Name: "ip", Args: []string{"-4", "-batch", "-"}, Stdin: plan.native.IPv4Batch()}); err != nil {
		return err
	}
	return executor.run(ctx, system.Command{Name: "ip", Args: []string{"-6", "-batch", "-"}, Stdin: plan.native.IPv6Batch()})
}

func (executor Executor) verify(ctx context.Context, plan Plan) error {
	if err := executor.run(ctx, system.Command{Name: "sing-box", Args: []string{"check", "-c", executor.SingBoxConfigPath}}); err != nil {
		return err
	}
	if err := executor.run(ctx, system.Command{Name: "systemctl", Args: []string{"is-active", "--quiet", ownedSingBoxService}}); err != nil {
		return err
	}
	nft, err := executor.inspectNFT(ctx)
	if err != nil {
		return err
	}
	if !nft.Exists {
		return fmt.Errorf("owned route leak-control table is missing")
	}
	state, err := routing.ParseState(plan.routing.Candidate(), true)
	if err != nil {
		return err
	}
	for _, intent := range state.Routes {
		if err := executor.verifyTUN(ctx, intent.TunnelInterface); err != nil {
			return err
		}
		for _, family := range []string{"-4", "-6"} {
			rules, err := executor.output(ctx, system.Command{Name: "ip", Args: []string{"-j", family, "rule", "show", "priority", strconv.FormatUint(uint64(intent.RulePriority), 10)}})
			ownedRules, rulesErr := inspectOwnedProtocol(rules)
			if err != nil || rulesErr != nil || !ownedRules {
				return fmt.Errorf("owned %s policy rule for route %q is missing", family, intent.ID)
			}
			routes, err := executor.output(ctx, system.Command{Name: "ip", Args: []string{"-j", family, "route", "show", "table", strconv.FormatUint(uint64(intent.RoutingTable), 10)}})
			ownedRoutes, routesErr := inspectOwnedProtocol(routes)
			if err != nil || routesErr != nil || !ownedRoutes {
				return fmt.Errorf("owned %s route table for route %q is missing", family, intent.ID)
			}
		}
	}
	return nil
}

func (executor Executor) verifyTUNs(ctx context.Context, candidate []byte) error {
	state, err := routing.ParseState(candidate, true)
	if err != nil {
		return err
	}
	for _, intent := range state.Routes {
		if err := executor.verifyTUN(ctx, intent.TunnelInterface); err != nil {
			return err
		}
	}
	return nil
}

func (executor Executor) verifyTUN(ctx context.Context, name string) error {
	output, err := executor.output(ctx, system.Command{Name: "ip", Args: []string{"-j", "link", "show", "dev", name}})
	var links []struct {
		Name string `json:"ifname"`
	}
	if err != nil || json.Unmarshal(output, &links) != nil || len(links) != 1 || links[0].Name != name {
		return fmt.Errorf("owned TUN %q is missing", name)
	}
	return nil
}

func inspectOwnedProtocol(output []byte) (bool, error) {
	var entries []struct {
		Protocol json.RawMessage `json:"protocol"`
	}
	if err := json.Unmarshal(output, &entries); err != nil || entries == nil {
		return false, fmt.Errorf("invalid policy routing inventory")
	}
	for _, entry := range entries {
		var protocol string
		if json.Unmarshal(entry.Protocol, &protocol) == nil && protocol == routing.OwnedRouteProtocol {
			return true, nil
		}
		var number uint32
		if json.Unmarshal(entry.Protocol, &number) == nil && strconv.FormatUint(uint64(number), 10) == routing.OwnedRouteProtocol {
			return true, nil
		}
	}
	return false, nil
}

func (executor Executor) removeOwnedIP(ctx context.Context, intents []routing.RouteIntent) error {
	for _, intent := range intents {
		for _, family := range []string{"-4", "-6"} {
			priority := strconv.FormatUint(uint64(intent.RulePriority), 10)
			table := strconv.FormatUint(uint64(intent.RoutingTable), 10)
			_, _ = executor.output(ctx, system.Command{Name: "ip", Args: []string{family, "rule", "delete", "priority", priority, "table", table, "protocol", routing.OwnedRouteProtocol}})
			_, _ = executor.output(ctx, system.Command{Name: "ip", Args: []string{family, "route", "flush", "table", table, "proto", routing.OwnedRouteProtocol}})
			rules, err := executor.output(ctx, system.Command{Name: "ip", Args: []string{"-j", family, "rule", "show", "priority", priority}})
			if err != nil {
				return err
			}
			ownedRules, err := inspectOwnedProtocol(rules)
			if err != nil {
				return err
			}
			if ownedRules {
				return fmt.Errorf("owned %s policy rule at priority %s remains after removal", family, priority)
			}
			routes, routeErr := executor.output(ctx, system.Command{Name: "ip", Args: []string{"-j", family, "route", "show", "table", table}})
			if routeErr != nil {
				// iproute2 can report an absent FIB table after its last route
				// is removed. Preserve the existing empty-table handling.
				continue
			}
			ownedRoutes, err := inspectOwnedProtocol(routes)
			if err != nil {
				return err
			}
			if ownedRoutes {
				return fmt.Errorf("owned %s routes in table %s remain after removal", family, table)
			}
		}
	}
	return nil
}

func (executor Executor) inspectNFT(ctx context.Context) (fileSnapshot, error) {
	tables, err := executor.output(ctx, system.Command{Name: "nft", Args: []string{"list", "tables"}})
	if err != nil {
		return fileSnapshot{}, err
	}
	exists := false
	for _, line := range strings.Split(string(tables), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "table" && fields[1] == routing.OwnedNFTFamily && fields[2] == routing.OwnedNFTTable {
			exists = true
			break
		}
	}
	if !exists {
		return fileSnapshot{}, nil
	}
	content, err := executor.output(ctx, system.Command{Name: "nft", Args: []string{"list", "table", routing.OwnedNFTFamily, routing.OwnedNFTTable}})
	if err != nil {
		return fileSnapshot{}, err
	}
	if len(content) == 0 || len(content) > maximumOwnedFile {
		return fileSnapshot{}, fmt.Errorf("owned nftables snapshot is empty or oversized")
	}
	return fileSnapshot{Exists: true, Content: content}, nil
}

func inspectOwnedFile(path string, validate func([]byte, bool) error) (fileSnapshot, error) {
	if !filepath.IsAbs(path) {
		return fileSnapshot{}, fmt.Errorf("owned state path must be absolute")
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return fileSnapshot{}, validate(nil, false)
	}
	if err != nil {
		return fileSnapshot{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > maximumOwnedFile {
		return fileSnapshot{}, fmt.Errorf("owned state is not a bounded regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fileSnapshot{}, err
	}
	if err := validate(content, true); err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{Exists: true, Content: content}, nil
}

func (executor Executor) protectSnapshot(id domain.ID, snapshots ...fileSnapshot) (protectedSnapshot, error) {
	if len(snapshots) != 3 {
		return protectedSnapshot{}, fmt.Errorf("three route-engine snapshots are required")
	}
	envelopes := make([]secrets.Envelope, 3)
	for index, item := range snapshots {
		plaintext := item.Content
		if !item.Exists {
			plaintext = []byte("absent\x00")
		}
		var err error
		envelopes[index], err = executor.Protector.Seal(journalContext(id, []string{"snapshot-singbox", "snapshot-routing", "snapshot-nft"}[index]), plaintext)
		if err != nil {
			return protectedSnapshot{}, err
		}
	}
	return protectedSnapshot{SingBox: envelopes[0], Routing: envelopes[1], NFT: envelopes[2]}, nil
}

func (executor Executor) validate() error {
	if executor.Runner == nil || executor.Journal == nil || executor.Protector == nil || !filepath.IsAbs(executor.SingBoxConfigPath) || !filepath.IsAbs(executor.RoutingStatePath) || !filepath.IsAbs(executor.InterfaceStatePath) || !filepath.IsAbs(executor.InterfaceRuntimeDirectory) || filepath.Clean(executor.SingBoxConfigPath) == filepath.Clean(executor.RoutingStatePath) {
		return fmt.Errorf("route-engine executor dependencies and absolute owned paths are required")
	}
	interfaceRelative, err := filepath.Rel(filepath.Clean(executor.InterfaceRuntimeDirectory), filepath.Clean(executor.InterfaceStatePath))
	if err != nil || interfaceRelative == "." || interfaceRelative == ".." || strings.HasPrefix(interfaceRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(interfaceRelative) {
		return fmt.Errorf("route-engine interface state must be inside its runtime directory")
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
		return fmt.Errorf("journal route-engine transition %s to %s: %w", from, to, err)
	}
	return nil
}

func (executor Executor) failPrepared(ctx context.Context, id domain.ID, detail string) {
	_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionPrepared, domain.TransactionFailed, executor.now(), detail)
}

func (executor Executor) failValidated(ctx context.Context, id domain.ID) {
	_ = executor.Journal.TransitionOperation(ctx, id, domain.TransactionValidated, domain.TransactionFailed, executor.now(), "journal_transition_failed")
}

func (executor Executor) timeout() time.Duration {
	if executor.Timeout <= 0 || executor.Timeout > 30*time.Second {
		return 10 * time.Second
	}
	return executor.Timeout
}

func (executor Executor) now() time.Time {
	if executor.Now != nil {
		return executor.Now().UTC().Truncate(time.Second)
	}
	return time.Now().UTC().Truncate(time.Second)
}

func journalContext(id domain.ID, purpose string) string {
	return "route-engine/" + string(id) + "/" + purpose + "/v1"
}

func writeTemporary(path string, content []byte) (string, error) {
	if len(content) == 0 || len(content) > maximumOwnedFile || !filepath.IsAbs(path) {
		return "", fmt.Errorf("owned configuration target or content is invalid")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(directory, ".egress-route-engine-*")
	if err != nil {
		return "", err
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

func installTemporary(temporary, destination string) error {
	if filepath.Dir(temporary) != filepath.Dir(destination) {
		return fmt.Errorf("temporary and destination paths must share a directory")
	}
	if err := os.Rename(temporary, destination); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func atomicWrite(path string, content []byte) error {
	temporary, err := writeTemporary(path, content)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	return installTemporary(temporary, path)
}

func restoreOwnedFile(path string, snapshot fileSnapshot, validate func([]byte, bool) error) error {
	if err := validate(snapshot.Content, snapshot.Exists); err != nil {
		return err
	}
	if snapshot.Exists {
		return atomicWrite(path, snapshot.Content)
	}
	return removeOwnedFile(path, validate)
}

func removeOwnedFile(path string, validate func([]byte, bool) error) error {
	current, err := inspectOwnedFile(path, validate)
	if err != nil {
		return err
	}
	if !current.Exists {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func decodeStrict(input []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("document contains trailing data")
	}
	return nil
}
