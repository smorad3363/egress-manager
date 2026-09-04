package main

import (
	"fmt"
	"net/netip"
	"os"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	managed "github.com/egress-manager/egress-manager/internal/haproxy"
)

func main() {
	backends := []domain.HAProxyBackend{
		{ID: "lab_primary", Name: "Lab Primary", Server: domain.Endpoint{Host: "10.203.2.2", Port: 18081}, Weight: 100, HealthCheck: true, Health: domain.Health{Status: domain.HealthUnknown}, Enabled: true},
		{ID: "lab_backup", Name: "Lab Backup", Server: domain.Endpoint{Host: "10.203.2.2", Port: 18082}, Weight: 50, Backup: true, HealthCheck: true, Health: domain.Health{Status: domain.HealthUnknown}, Enabled: true},
	}
	frontends := []domain.HAProxyFrontend{{ID: "lab_proxy", Name: "Lab Proxy", Bind: netip.MustParseAddr("10.203.1.1"), Port: 18080, BackendIDs: []domain.ID{"lab_primary", "lab_backup"}, Algorithm: domain.BalanceRoundRobin, Enabled: true}}
	state, err := managed.ParseState(nil, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	plan, err := managed.BuildPlan(managed.Settings{RuntimeSocket: "/tmp/egm-haproxy-runtime.sock", PIDFile: "/tmp/egm-haproxy.pid", MaxConnections: 4096, ConnectTimeout: time.Second, ClientTimeout: 5 * time.Second, ServerTimeout: 5 * time.Second, ProtectedPorts: []uint16{22, 443}}, frontends, backends, nil, state)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(plan.Candidate)
}
