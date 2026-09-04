package haproxy

import (
	"github.com/egress-manager/egress-manager/internal/domain"
)

type PlanRequest struct {
	Frontends []domain.HAProxyFrontend `json:"frontends"`
	Backends  []domain.HAProxyBackend  `json:"backends"`
}

type ApplyRequest struct {
	TransactionID   domain.ID                `json:"transaction_id"`
	RequestedChange string                   `json:"requested_change"`
	Frontends       []domain.HAProxyFrontend `json:"frontends"`
	Backends        []domain.HAProxyBackend  `json:"backends"`
}
