// Package routeengine coordinates sing-box and kernel routing as one transaction.
package routeengine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/egress-manager/egress-manager/internal/domain"
	managedInterface "github.com/egress-manager/egress-manager/internal/interfaceoutbound"
	"github.com/egress-manager/egress-manager/internal/routing"
	"github.com/egress-manager/egress-manager/internal/singbox"
)

type Review struct {
	Engine                string           `json:"engine"`
	SingBoxStateHash      string           `json:"sing_box_state_hash"`
	RoutingStateHash      string           `json:"routing_state_hash"`
	InterfaceStateHash    string           `json:"interface_state_hash"`
	SingBoxCandidateHash  string           `json:"sing_box_candidate_hash"`
	RoutingCandidateHash  string           `json:"routing_candidate_hash"`
	NativeCandidateHash   string           `json:"native_candidate_hash"`
	CombinedCandidateHash string           `json:"combined_candidate_hash"`
	EnabledOutbounds      int              `json:"enabled_outbounds"`
	EnabledRoutes         int              `json:"enabled_routes"`
	SingBoxActions        []singbox.Action `json:"sing_box_actions"`
	RoutingActions        []routing.Action `json:"routing_actions"`
	NativeActions         []routing.Action `json:"native_actions"`
}

type Plan struct {
	Review  Review
	singBox singbox.ExecutionPlan
	routing routing.ExecutionPlan
	native  routing.NativePlan
}

func BuildPlan(singBox singbox.ExecutionPlan, desired routing.ExecutionPlan, native routing.NativePlan, interfaceState managedInterface.State) (Plan, error) {
	if singBox.Plan.Engine != "sing-box" || singBox.Plan.CandidateHash != hash(singBox.Candidate()) {
		return Plan{}, fmt.Errorf("coordinated sing-box plan is invalid")
	}
	if desired.Plan.Engine != "policy-routing" || desired.Plan.CandidateHash != hash(desired.Candidate()) {
		return Plan{}, fmt.Errorf("coordinated routing plan is invalid")
	}
	if native.Engine != "nftables+iproute2" || native.StateHash != desired.Plan.StateHash || native.EnabledRoutes != desired.Plan.EnabledRoutes {
		return Plan{}, fmt.Errorf("coordinated native plan does not match routing desired state")
	}
	nativeContent := append(append(append(append([]byte{}, desired.Candidate()...), native.NFTCandidate()...), native.IPv4Batch()...), native.IPv6Batch()...)
	if native.CandidateHash != hash(nativeContent) {
		return Plan{}, fmt.Errorf("coordinated native candidate hash is invalid")
	}
	parsedDesired, err := routing.ParseState(desired.Candidate(), true)
	if err != nil {
		return Plan{}, fmt.Errorf("coordinated routing candidate is not project-owned: %w", err)
	}
	singBoxRoutes, err := validateInterfaceBindings(parsedDesired.Routes, interfaceState)
	if err != nil {
		return Plan{}, err
	}
	if singBox.Plan.RoutedRoutes != singBoxRoutes {
		return Plan{}, fmt.Errorf("coordinated sing-box route count does not match routing desired state")
	}
	if _, err := singbox.ParseState(singBox.Candidate(), true); err != nil {
		return Plan{}, fmt.Errorf("coordinated sing-box candidate is not project-owned: %w", err)
	}
	combined := combinedHash(singBox.Candidate(), desired.Candidate(), native.NFTCandidate(), native.IPv4Batch(), native.IPv6Batch(), []byte(interfaceState.Hash))
	review := Review{
		Engine: "coordinated-egress-routing", SingBoxStateHash: singBox.Plan.StateHash, RoutingStateHash: desired.Plan.StateHash, InterfaceStateHash: interfaceState.Hash,
		SingBoxCandidateHash: singBox.Plan.CandidateHash, RoutingCandidateHash: desired.Plan.CandidateHash,
		NativeCandidateHash: native.CandidateHash, CombinedCandidateHash: combined,
		EnabledOutbounds: singBox.Plan.EnabledOutbounds, EnabledRoutes: desired.Plan.EnabledRoutes,
		SingBoxActions: append([]singbox.Action{}, singBox.Plan.Actions...), RoutingActions: append([]routing.Action{}, desired.Plan.Actions...), NativeActions: append([]routing.Action{}, native.Actions...),
	}
	return Plan{Review: review, singBox: singBox, routing: desired, native: native}, nil
}

func validateInterfaceBindings(intents []routing.RouteIntent, state managedInterface.State) (int, error) {
	if len(state.Hash) != sha256.Size*2 {
		return 0, fmt.Errorf("coordinated interface outbound state is required")
	}
	if _, err := hex.DecodeString(state.Hash); err != nil {
		return 0, fmt.Errorf("coordinated interface outbound state hash is invalid")
	}
	owned := make(map[domain.ID]string, len(state.Entries))
	interfaces := make(map[string]struct{}, len(state.Entries))
	for _, entry := range state.Entries {
		expected, err := managedInterface.InterfaceName(entry.Kind, entry.ID)
		if err != nil || entry.InterfaceName != expected {
			return 0, fmt.Errorf("coordinated interface outbound state contains an invalid entry")
		}
		if _, exists := owned[entry.ID]; exists {
			return 0, fmt.Errorf("coordinated interface outbound state contains duplicate IDs")
		}
		if _, exists := interfaces[entry.InterfaceName]; exists {
			return 0, fmt.Errorf("coordinated interface outbound state contains duplicate interfaces")
		}
		owned[entry.ID] = entry.InterfaceName
		interfaces[entry.InterfaceName] = struct{}{}
	}
	singBoxRoutes := 0
	for _, intent := range intents {
		if intent.OutboundAdapter == domain.OutboundAdapterSingBox {
			singBoxRoutes++
			continue
		}
		interfaceName, exists := owned[intent.SelectedOutboundID]
		if !exists || interfaceName != intent.TunnelInterface {
			return 0, fmt.Errorf("coordinated native route %q does not match interface outbound state", intent.ID)
		}
	}
	return singBoxRoutes, nil
}

func hash(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func combinedHash(parts ...[]byte) string {
	digest := sha256.New()
	for _, part := range parts {
		length := []byte(fmt.Sprintf("%d\x00", len(part)))
		_, _ = digest.Write(length)
		_, _ = digest.Write(part)
	}
	return hex.EncodeToString(digest.Sum(nil))
}
