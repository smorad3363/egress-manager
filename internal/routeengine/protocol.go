package routeengine

import "github.com/egress-manager/egress-manager/internal/domain"

type ApplyRequest struct {
	TransactionID             domain.ID `json:"transaction_id"`
	ExpectedSingBoxStateHash  string    `json:"expected_sing_box_state_hash"`
	ExpectedRoutingStateHash  string    `json:"expected_routing_state_hash"`
	ExpectedSingBoxCandidate  string    `json:"expected_sing_box_candidate_hash"`
	ExpectedRoutingCandidate  string    `json:"expected_routing_candidate_hash"`
	ExpectedNativeCandidate   string    `json:"expected_native_candidate_hash"`
	ExpectedCombinedCandidate string    `json:"expected_combined_candidate_hash"`
}
