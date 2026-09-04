package xray

import "github.com/egress-manager/egress-manager/internal/domain"

type FragmentApplyRequest struct {
	TransactionID             domain.ID `json:"transaction_id"`
	ExpectedForeignStateHash  string    `json:"expected_foreign_state_hash"`
	ExpectedFragmentStateHash string    `json:"expected_fragment_state_hash"`
	ExpectedCandidateHash     string    `json:"expected_candidate_hash"`
}
