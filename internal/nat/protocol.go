package nat

import "github.com/egress-manager/egress-manager/internal/domain"

type PlanRequest struct {
	Family   AddressFamily        `json:"family"`
	Forwards []domain.PortForward `json:"forwards"`
}

type CounterRequest struct {
	Family AddressFamily `json:"family"`
}

type ApplyRequest struct {
	TransactionID   domain.ID            `json:"transaction_id"`
	RequestedChange string               `json:"requested_change"`
	Family          AddressFamily        `json:"family"`
	Forwards        []domain.PortForward `json:"forwards"`
}

type ApplyResponse struct {
	TransactionID domain.ID               `json:"transaction_id"`
	State         domain.TransactionState `json:"state"`
}
