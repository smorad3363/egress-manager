package domain

import (
	"net/netip"
	"testing"
)

func TestHAProxyModels(t *testing.T) {
	t.Parallel()

	backend := HAProxyBackend{
		ID:          "api-primary",
		Name:        "API Primary",
		Server:      Endpoint{Host: "10.20.0.10", Port: 443},
		Weight:      100,
		HealthCheck: true,
		Health:      Health{Status: HealthUnknown},
		Enabled:     true,
	}
	if err := backend.Validate(); err != nil {
		t.Fatalf("backend Validate() error = %v", err)
	}

	frontend := HAProxyFrontend{
		ID:         "api-listener",
		Name:       "API Listener",
		Bind:       netip.MustParseAddr("192.0.2.10"),
		Port:       443,
		BackendIDs: []ID{backend.ID},
		Algorithm:  BalanceRoundRobin,
		Enabled:    true,
	}
	if err := frontend.Validate(); err != nil {
		t.Fatalf("frontend Validate() error = %v", err)
	}
}

func TestHAProxyFrontendRejectsDuplicateBackends(t *testing.T) {
	t.Parallel()

	frontend := HAProxyFrontend{
		ID:         "api-listener",
		Name:       "API Listener",
		Bind:       netip.MustParseAddr("192.0.2.10"),
		Port:       443,
		BackendIDs: []ID{"api-primary", "api-primary"},
		Algorithm:  BalanceLeastConn,
	}
	if err := frontend.Validate(); err == nil {
		t.Fatal("Validate() accepted duplicate backends")
	}
}
