// Package routeengine coordinates sing-box and kernel routing as one transaction.
package routeengine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/egress-manager/egress-manager/internal/routing"
	"github.com/egress-manager/egress-manager/internal/singbox"
)

type Review struct {
	Engine                string           `json:"engine"`
	SingBoxStateHash      string           `json:"sing_box_state_hash"`
	RoutingStateHash      string           `json:"routing_state_hash"`
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

func BuildPlan(singBox singbox.ExecutionPlan, desired routing.ExecutionPlan, native routing.NativePlan) (Plan, error) {
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
	if singBox.Plan.RoutedRoutes != desired.Plan.EnabledRoutes {
		return Plan{}, fmt.Errorf("coordinated sing-box route count does not match routing desired state")
	}
	if _, err := singbox.ParseState(singBox.Candidate(), true); err != nil {
		return Plan{}, fmt.Errorf("coordinated sing-box candidate is not project-owned: %w", err)
	}
	if _, err := routing.ParseState(desired.Candidate(), true); err != nil {
		return Plan{}, fmt.Errorf("coordinated routing candidate is not project-owned: %w", err)
	}
	combined := combinedHash(singBox.Candidate(), desired.Candidate(), native.NFTCandidate(), native.IPv4Batch(), native.IPv6Batch())
	review := Review{
		Engine: "coordinated-egress-routing", SingBoxStateHash: singBox.Plan.StateHash, RoutingStateHash: desired.Plan.StateHash,
		SingBoxCandidateHash: singBox.Plan.CandidateHash, RoutingCandidateHash: desired.Plan.CandidateHash,
		NativeCandidateHash: native.CandidateHash, CombinedCandidateHash: combined,
		EnabledOutbounds: singBox.Plan.EnabledOutbounds, EnabledRoutes: desired.Plan.EnabledRoutes,
		SingBoxActions: append([]singbox.Action{}, singBox.Plan.Actions...), RoutingActions: append([]routing.Action{}, desired.Plan.Actions...), NativeActions: append([]routing.Action{}, native.Actions...),
	}
	return Plan{Review: review, singBox: singBox, routing: desired, native: native}, nil
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
