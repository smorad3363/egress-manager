package domain

import (
	"fmt"
	"net/netip"
)

const MaximumHAProxyBackendsPerFrontend = 64

type BalanceAlgorithm string

const (
	BalanceRoundRobin BalanceAlgorithm = "roundrobin"
	BalanceLeastConn  BalanceAlgorithm = "leastconn"
)

type HAProxyFrontend struct {
	ID         ID               `json:"id"`
	Name       string           `json:"name"`
	Bind       netip.Addr       `json:"bind"`
	Port       uint16           `json:"port"`
	BackendIDs []ID             `json:"backend_ids"`
	Algorithm  BalanceAlgorithm `json:"algorithm"`
	Enabled    bool             `json:"enabled"`
}

func (frontend HAProxyFrontend) Validate() error {
	errs := []error{
		frontend.ID.Validate("HAProxy frontend id"),
		validateDisplayName("HAProxy frontend name", frontend.Name),
	}
	if !frontend.Bind.IsValid() {
		errs = append(errs, fmt.Errorf("HAProxy bind address is invalid"))
	}
	if frontend.Port == 0 {
		errs = append(errs, fmt.Errorf("HAProxy frontend port must be between 1 and 65535"))
	}
	if len(frontend.BackendIDs) == 0 {
		errs = append(errs, fmt.Errorf("HAProxy frontend requires at least one backend"))
	}
	if len(frontend.BackendIDs) > MaximumHAProxyBackendsPerFrontend {
		errs = append(errs, fmt.Errorf("HAProxy frontend must not reference more than %d backends", MaximumHAProxyBackendsPerFrontend))
	}
	seen := make(map[ID]struct{}, len(frontend.BackendIDs))
	for _, id := range frontend.BackendIDs {
		errs = append(errs, id.Validate("HAProxy backend id"))
		if _, exists := seen[id]; exists {
			errs = append(errs, fmt.Errorf("duplicate HAProxy backend id %q", id))
		}
		seen[id] = struct{}{}
	}
	if frontend.Algorithm != BalanceRoundRobin && frontend.Algorithm != BalanceLeastConn {
		errs = append(errs, fmt.Errorf("unsupported HAProxy balance algorithm %q", frontend.Algorithm))
	}
	return errorsFrom(errs)
}

type HAProxyBackend struct {
	ID          ID       `json:"id"`
	Name        string   `json:"name"`
	Server      Endpoint `json:"server"`
	Weight      uint16   `json:"weight"`
	Backup      bool     `json:"backup"`
	HealthCheck bool     `json:"health_check"`
	Health      Health   `json:"health"`
	Enabled     bool     `json:"enabled"`
}

func (backend HAProxyBackend) Validate() error {
	var weightError error
	if backend.Weight == 0 || backend.Weight > 256 {
		weightError = fmt.Errorf("HAProxy backend weight must be between 1 and 256")
	}
	return joinErrors(
		backend.ID.Validate("HAProxy backend id"),
		validateDisplayName("HAProxy backend name", backend.Name),
		backend.Server.Validate(),
		weightError,
		backend.Health.Validate(),
	)
}
