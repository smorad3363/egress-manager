package routeengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/routing"
	"github.com/egress-manager/egress-manager/internal/singbox"
	"github.com/egress-manager/egress-manager/internal/system"
)

const bypassSchema = "egress-manager/bypass/v1"

type BypassStatus struct {
	Active      bool      `json:"active"`
	OperationID domain.ID `json:"operation_id,omitempty"`
	ActivatedAt time.Time `json:"activated_at,omitempty"`
}

type BypassResponse struct {
	TransactionID domain.ID               `json:"transaction_id"`
	State         domain.TransactionState `json:"state"`
	AlreadyActive bool                    `json:"already_active"`
}

type BypassActiveError struct{}

func (BypassActiveError) Error() string        { return "emergency bypass is active" }
func (BypassActiveError) IPCErrorCode() string { return "bypass_active" }

type bypassDocument struct {
	Schema      string    `json:"schema"`
	Active      bool      `json:"active"`
	OperationID domain.ID `json:"operation_id"`
	ActivatedAt time.Time `json:"activated_at"`
}

type bypassCandidate struct {
	Scope                string `json:"scope"`
	PreviousBypassActive bool   `json:"previous_bypass_active"`
}

func InspectBypassState(path string) (BypassStatus, error) {
	snapshot, err := inspectOwnedFile(path, func(content []byte, exists bool) error {
		_, parseErr := parseBypassDocument(content, exists)
		return parseErr
	})
	if err != nil {
		return BypassStatus{}, err
	}
	if !snapshot.Exists {
		return BypassStatus{}, nil
	}
	document, err := parseBypassDocument(snapshot.Content, true)
	if err != nil {
		return BypassStatus{}, err
	}
	return BypassStatus{Active: true, OperationID: document.OperationID, ActivatedAt: document.ActivatedAt}, nil
}

func (executor Executor) BypassPath() string {
	if executor.BypassStatePath != "" {
		return executor.BypassStatePath
	}
	return filepath.Join(filepath.Dir(executor.RoutingStatePath), "bypass.json")
}

func (executor Executor) BypassStatus() (BypassStatus, error) {
	return InspectBypassState(executor.BypassPath())
}

func (executor Executor) DeactivateBypass() error {
	return removeOwnedFile(executor.BypassPath(), func(content []byte, exists bool) error {
		_, err := parseBypassDocument(content, exists)
		return err
	})
}

func (executor Executor) Bypass(ctx context.Context, id domain.ID) (BypassResponse, error) {
	if err := executor.validate(); err != nil {
		return BypassResponse{}, err
	}
	if err := id.Validate("transaction id"); err != nil {
		return BypassResponse{}, err
	}
	status, err := executor.BypassStatus()
	if err != nil {
		return BypassResponse{}, err
	}
	routingSnapshot, err := inspectOwnedFile(executor.RoutingStatePath, func(content []byte, exists bool) error {
		_, parseErr := routing.ParseState(content, exists)
		return parseErr
	})
	if err != nil {
		return BypassResponse{}, err
	}
	singBoxSnapshot, err := inspectOwnedFile(executor.SingBoxConfigPath, func(content []byte, exists bool) error {
		_, parseErr := singbox.ParseState(content, exists)
		return parseErr
	})
	if err != nil {
		return BypassResponse{}, err
	}
	nftSnapshot, err := executor.inspectNFT(ctx)
	if err != nil {
		return BypassResponse{}, err
	}
	if status.Active {
		if err := executor.verifyBypass(ctx, routingSnapshot); err == nil {
			return BypassResponse{TransactionID: status.OperationID, State: domain.TransactionCommitted, AlreadyActive: true}, nil
		}
	}
	protected, err := executor.protectSnapshot(id, singBoxSnapshot, routingSnapshot, nftSnapshot)
	if err != nil {
		return BypassResponse{}, err
	}
	protectedJSON, err := json.Marshal(protected)
	if err != nil {
		return BypassResponse{}, fmt.Errorf("encode protected bypass snapshot: %w", err)
	}
	now := executor.now()
	candidateJSON, err := json.Marshal(bypassCandidate{Scope: "egress-routing", PreviousBypassActive: status.Active})
	if err != nil {
		return BypassResponse{}, fmt.Errorf("encode bypass candidate: %w", err)
	}
	operation := domain.Transaction{
		ID: id, Operation: "route_engine_bypass", State: domain.TransactionPrepared,
		RequestedChange:  "disable Egress Manager-owned routing and interception",
		PreviousSnapshot: string(protectedJSON), CandidateConfig: string(candidateJSON),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := executor.Journal.CreateOperation(ctx, operation); err != nil {
		return BypassResponse{}, fmt.Errorf("journal prepared bypass operation: %w", err)
	}
	if nftSnapshot.Exists {
		if err := executor.run(ctx, system.Command{Name: "nft", Args: []string{"--check", "--file", "-"}, Stdin: bypassNFTCandidate()}); err != nil {
			executor.failPrepared(ctx, id, "native_validation_failed")
			return BypassResponse{}, fmt.Errorf("validate bypass candidate: %w", err)
		}
	}
	if err := executor.transition(ctx, id, domain.TransactionPrepared, domain.TransactionValidated, ""); err != nil {
		return BypassResponse{}, err
	}
	if err := executor.transition(ctx, id, domain.TransactionValidated, domain.TransactionApplying, ""); err != nil {
		executor.failValidated(ctx, id)
		return BypassResponse{}, err
	}
	if err := executor.activateBypass(id, now, status.Active); err != nil {
		cause := fmt.Errorf("persist bypass state: %w", err)
		return BypassResponse{}, executor.rollbackBypass(ctx, operation, routingSnapshot, nftSnapshot, domain.TransactionApplying, status.Active, cause)
	}
	if err := executor.applyBypass(ctx, routingSnapshot, nftSnapshot.Exists); err != nil {
		return BypassResponse{}, executor.rollbackBypass(ctx, operation, routingSnapshot, nftSnapshot, domain.TransactionApplying, status.Active, err)
	}
	if err := executor.transition(ctx, id, domain.TransactionApplying, domain.TransactionVerifying, ""); err != nil {
		return BypassResponse{}, executor.rollbackBypass(ctx, operation, routingSnapshot, nftSnapshot, domain.TransactionApplying, status.Active, err)
	}
	if err := executor.verifyBypass(ctx, routingSnapshot); err != nil {
		return BypassResponse{}, executor.rollbackBypass(ctx, operation, routingSnapshot, nftSnapshot, domain.TransactionVerifying, status.Active, err)
	}
	if err := executor.transition(ctx, id, domain.TransactionVerifying, domain.TransactionCommitted, ""); err != nil {
		return BypassResponse{}, executor.rollbackBypass(ctx, operation, routingSnapshot, nftSnapshot, domain.TransactionVerifying, status.Active, err)
	}
	return BypassResponse{TransactionID: id, State: domain.TransactionCommitted, AlreadyActive: status.Active}, nil
}

func (executor Executor) recoverBypass(ctx context.Context, operation domain.Transaction) error {
	switch operation.State {
	case domain.TransactionPrepared, domain.TransactionValidated:
		return executor.Journal.TransitionOperation(ctx, operation.ID, operation.State, domain.TransactionFailed, executor.now(), "interrupted_before_apply")
	case domain.TransactionApplying, domain.TransactionVerifying:
		snapshot, err := executor.openSnapshot(operation)
		if err != nil {
			_ = executor.Journal.TransitionOperation(ctx, operation.ID, operation.State, domain.TransactionFailed, executor.now(), "snapshot_authentication_failed")
			return err
		}
		if err := executor.activateBypass(operation.ID, operation.CreatedAt, false); err != nil {
			return err
		}
		if err := executor.applyBypass(ctx, snapshot.Routing, snapshot.NFT.Exists); err != nil {
			return err
		}
		if operation.State == domain.TransactionApplying {
			if err := executor.transition(ctx, operation.ID, domain.TransactionApplying, domain.TransactionVerifying, "interrupted_operation"); err != nil {
				return err
			}
		}
		if err := executor.verifyBypass(ctx, snapshot.Routing); err != nil {
			return err
		}
		return executor.transition(ctx, operation.ID, domain.TransactionVerifying, domain.TransactionCommitted, "interrupted_operation")
	case domain.TransactionRollingBack:
		snapshot, err := executor.openSnapshot(operation)
		if err != nil {
			_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "snapshot_authentication_failed")
			return err
		}
		candidate, err := openBypassCandidate(operation.CandidateConfig)
		if err != nil {
			_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "candidate_validation_failed")
			return err
		}
		if candidate.PreviousBypassActive {
			err = executor.applyBypass(ctx, snapshot.Routing, snapshot.NFT.Exists)
			if err == nil {
				err = executor.verifyBypass(ctx, snapshot.Routing)
			}
		} else {
			err = executor.restoreBypassRuntime(ctx, snapshot.Routing, snapshot.NFT)
			if err == nil {
				err = executor.DeactivateBypass()
			}
		}
		if err != nil {
			_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "rollback_failed")
			return err
		}
		return executor.transition(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, "interrupted_operation")
	default:
		return fmt.Errorf("unsupported bypass recovery state %s", operation.State)
	}
}

func (executor Executor) rollbackBypass(ctx context.Context, operation domain.Transaction, routingSnapshot, nftSnapshot fileSnapshot, from domain.TransactionState, previousActive bool, cause error) error {
	if err := executor.transition(ctx, operation.ID, from, domain.TransactionRollingBack, "bypass_failed"); err != nil {
		return errors.Join(cause, err)
	}
	var rollbackErr error
	if previousActive {
		rollbackErr = executor.applyBypass(ctx, routingSnapshot, nftSnapshot.Exists)
		if rollbackErr == nil {
			rollbackErr = executor.verifyBypass(ctx, routingSnapshot)
		}
	} else {
		rollbackErr = executor.restoreBypassRuntime(ctx, routingSnapshot, nftSnapshot)
		if rollbackErr == nil {
			rollbackErr = executor.DeactivateBypass()
		}
	}
	if rollbackErr != nil {
		_ = executor.Journal.TransitionOperation(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionFailed, executor.now(), "rollback_failed")
		return errors.Join(cause, rollbackErr)
	}
	if err := executor.transition(ctx, operation.ID, domain.TransactionRollingBack, domain.TransactionRolledBack, "bypass_failed"); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (executor Executor) restoreBypassRuntime(ctx context.Context, routingSnapshot, nftSnapshot fileSnapshot) error {
	state, err := routing.ParseState(routingSnapshot.Content, routingSnapshot.Exists)
	if err != nil {
		return err
	}
	if len(state.Routes) != 0 {
		if err := executor.verifyTUNs(ctx, routingSnapshot.Content); err != nil {
			return err
		}
	}
	if !nftSnapshot.Exists && len(state.Routes) != 0 {
		current, err := executor.inspectNFT(ctx)
		if err != nil {
			return err
		}
		protection, err := routing.BuildRecoveryNFT(state.Routes, current.Exists)
		if err != nil {
			return err
		}
		if err := executor.run(ctx, system.Command{Name: "nft", Args: []string{"--check", "--file", "-"}, Stdin: protection}); err != nil {
			return err
		}
		if err := executor.run(ctx, system.Command{Name: "nft", Args: []string{"--file", "-"}, Stdin: protection}); err != nil {
			return err
		}
	} else if err := executor.restoreNFT(ctx, nftSnapshot); err != nil {
		return err
	}
	ipv4, ipv6, err := routing.BuildIPBatches(state.Routes)
	if err != nil {
		return err
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
	return nil
}

func openBypassCandidate(content string) (bypassCandidate, error) {
	var candidate bypassCandidate
	if err := decodeStrict([]byte(content), &candidate); err != nil || candidate.Scope != "egress-routing" {
		return bypassCandidate{}, fmt.Errorf("bypass candidate is invalid")
	}
	return candidate, nil
}

func (executor Executor) applyBypass(ctx context.Context, routingSnapshot fileSnapshot, removeNFT bool) error {
	state, err := routing.ParseState(routingSnapshot.Content, routingSnapshot.Exists)
	if err != nil {
		return err
	}
	if err := executor.removeOwnedIP(ctx, state.Routes); err != nil {
		return fmt.Errorf("remove owned policy routing: %w", err)
	}
	current, err := executor.inspectNFT(ctx)
	if err != nil {
		return err
	}
	if removeNFT || current.Exists {
		if !current.Exists {
			return nil
		}
		if err := executor.run(ctx, system.Command{Name: "nft", Args: []string{"--file", "-"}, Stdin: bypassNFTCandidate()}); err != nil {
			return fmt.Errorf("remove owned interception table: %w", err)
		}
	}
	return nil
}

func (executor Executor) verifyBypass(ctx context.Context, routingSnapshot fileSnapshot) error {
	status, err := executor.BypassStatus()
	if err != nil || !status.Active {
		return fmt.Errorf("bypass state is not active")
	}
	state, err := routing.ParseState(routingSnapshot.Content, routingSnapshot.Exists)
	if err != nil {
		return err
	}
	if err := executor.verifyOwnedIPAbsent(ctx, state.Routes); err != nil {
		return err
	}
	nft, err := executor.inspectNFT(ctx)
	if err != nil {
		return err
	}
	if nft.Exists {
		return fmt.Errorf("owned interception table remains after bypass")
	}
	return nil
}

func (executor Executor) activateBypass(id domain.ID, at time.Time, preserve bool) error {
	if preserve {
		return nil
	}
	document := bypassDocument{Schema: bypassSchema, Active: true, OperationID: id, ActivatedAt: at.UTC().Truncate(time.Second)}
	content, err := json.Marshal(document)
	if err != nil {
		return err
	}
	return atomicWrite(executor.BypassPath(), append(content, '\n'))
}

func parseBypassDocument(content []byte, exists bool) (bypassDocument, error) {
	if !exists {
		return bypassDocument{}, nil
	}
	var document bypassDocument
	if err := decodeStrict(content, &document); err != nil {
		return bypassDocument{}, fmt.Errorf("decode bypass state")
	}
	if document.Schema != bypassSchema || !document.Active || document.ActivatedAt.IsZero() || document.OperationID.Validate("bypass operation id") != nil {
		return bypassDocument{}, fmt.Errorf("bypass state is invalid")
	}
	return document, nil
}

func bypassNFTCandidate() []byte {
	return []byte("delete table inet egm_egress\n")
}
