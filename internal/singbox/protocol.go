package singbox

import (
	"github.com/egress-manager/egress-manager/internal/domain"
)

type ImportRequest struct {
	Input string `json:"input"`
}

type ImportResponse struct {
	Outbounds []domain.Outbound `json:"outbounds"`
}

type TestRequest struct {
	ID               domain.ID `json:"id,omitempty"`
	ExpectedRevision int64     `json:"expected_revision,omitempty"`
	Input            string    `json:"input,omitempty"`
}

type TestResult struct {
	Outbound domain.Outbound       `json:"outbound"`
	Health   domain.OutboundHealth `json:"health"`
}

type TestResponse struct {
	Results []TestResult `json:"results"`
}

type ApplyRequest struct {
	TransactionID         domain.ID `json:"transaction_id"`
	ExpectedStateHash     string    `json:"expected_state_hash"`
	ExpectedCandidateHash string    `json:"expected_candidate_hash"`
}
