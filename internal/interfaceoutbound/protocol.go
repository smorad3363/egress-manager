package interfaceoutbound

import "github.com/egress-manager/egress-manager/internal/domain"

type ImportRequest struct {
	Input string `json:"input"`
}

type ImportResponse struct {
	Outbound domain.Outbound `json:"outbound"`
}

type TestRequest struct {
	ID               domain.ID `json:"id,omitempty"`
	ExpectedRevision int64     `json:"expected_revision,omitempty"`
	Input            string    `json:"input,omitempty"`
}

type TestResponse struct {
	Outbound domain.Outbound       `json:"outbound"`
	Health   domain.OutboundHealth `json:"health"`
}

type ApplyRequest struct {
	TransactionID         domain.ID `json:"transaction_id"`
	ExpectedStateHash     string    `json:"expected_state_hash"`
	ExpectedCandidateHash string    `json:"expected_candidate_hash"`
}
