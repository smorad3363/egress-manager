package haproxy

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

func testSettings() Settings {
	return Settings{RuntimeSocket: "/run/egress-manager/haproxy-runtime.sock", PIDFile: "/run/egress-manager/haproxy.pid", MaxConnections: 4096, ConnectTimeout: 5 * time.Second, ClientTimeout: 30 * time.Second, ServerTimeout: 30 * time.Second, ProtectedPorts: []uint16{22, 43127}}
}

func testHAProxyDesired() ([]domain.HAProxyFrontend, []domain.HAProxyBackend) {
	backends := []domain.HAProxyBackend{
		{ID: "api_backup", Name: "API Backup", Server: domain.Endpoint{Host: "10.20.0.11", Port: 8080}, Weight: 50, Backup: true, HealthCheck: true, Health: domain.Health{Status: domain.HealthUnknown}, Enabled: true},
		{ID: "api_primary", Name: "API Primary", Server: domain.Endpoint{Host: "10.20.0.10", Port: 8080}, Weight: 100, HealthCheck: true, Health: domain.Health{Status: domain.HealthUnknown}, Enabled: true},
	}
	frontends := []domain.HAProxyFrontend{{ID: "public_api", Name: "Public API", Bind: netip.MustParseAddr("192.0.2.10"), Port: 443, BackendIDs: []domain.ID{"api_primary", "api_backup"}, Algorithm: domain.BalanceLeastConn, Enabled: true}}
	return frontends, backends
}

func TestBuildPlanRendersDeterministicOwnedTCPConfiguration(t *testing.T) {
	t.Parallel()
	frontends, backends := testHAProxyDesired()
	state, err := ParseState(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(testSettings(), frontends, backends, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"stats socket /run/egress-manager/haproxy-runtime.sock mode 660 level operator",
		"frontend egm_fe_public_api",
		"bind 192.0.2.10:443",
		"balance leastconn",
		"server egm_srv_api_primary 10.20.0.10:8080 weight 100 check",
		"server egm_srv_api_backup 10.20.0.11:8080 weight 50 check backup",
	} {
		if !strings.Contains(plan.Candidate, expected) {
			t.Fatalf("candidate omitted %q:\n%s", expected, plan.Candidate)
		}
	}
	if strings.Index(plan.Candidate, "egm_srv_api_primary") > strings.Index(plan.Candidate, "egm_srv_api_backup") {
		t.Fatal("frontend backend order was not preserved")
	}
	if plan.EnabledFrontends != 1 || plan.EnabledBackends != 2 || plan.StateHash != state.Hash {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestBuildPlanRejectsMissingDisabledConflictingAndProtectedInputs(t *testing.T) {
	t.Parallel()
	frontends, backends := testHAProxyDesired()
	state, _ := ParseState(nil, false)
	missing := append([]domain.HAProxyFrontend{}, frontends...)
	missing[0].BackendIDs = []domain.ID{"missing"}
	if _, err := BuildPlan(testSettings(), missing, backends, nil, state); err == nil || !strings.Contains(err.Error(), "missing backend") {
		t.Fatalf("missing backend error = %v", err)
	}
	disabled := append([]domain.HAProxyBackend{}, backends...)
	disabled[0].Enabled = false
	disabled[1].Enabled = false
	if _, err := BuildPlan(testSettings(), frontends, disabled, nil, state); err == nil || !strings.Contains(err.Error(), "no enabled backend") {
		t.Fatalf("disabled backend error = %v", err)
	}
	listeners := []inventory.Listener{{Protocol: "tcp", Address: "0.0.0.0", Port: 443, Process: "nginx"}}
	if _, err := BuildPlan(testSettings(), frontends, backends, listeners, state); err == nil || !strings.Contains(err.Error(), "conflicts with listener") {
		t.Fatalf("listener error = %v", err)
	}
	protected := testSettings()
	protected.ProtectedPorts = append(protected.ProtectedPorts, 443)
	if _, err := BuildPlan(protected, frontends, backends, nil, state); err == nil || !strings.Contains(err.Error(), "protected port") {
		t.Fatalf("protected error = %v", err)
	}
}

func TestParseStateAllowsOnlyKnownOwnedListenerToReconcile(t *testing.T) {
	t.Parallel()
	content := []byte(`# Egress Manager owned HAProxy configuration
global
  stats socket /run/egress-manager/haproxy-runtime.sock mode 660 level operator
defaults
  mode tcp
frontend egm_fe_public_api
  bind 192.0.2.10:443
  default_backend egm_pool_public_api
backend egm_pool_public_api
  server egm_srv_api_primary 10.20.0.10:8080
`)
	state, err := ParseState(content, true)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Exists || len(state.Bindings) != 1 || state.Bindings[0].Port != 443 {
		t.Fatalf("state = %#v", state)
	}
	frontends, backends := testHAProxyDesired()
	ownedListener := []inventory.Listener{{Protocol: "tcp", Address: "192.0.2.10", Port: 443, Process: "haproxy"}}
	if _, err := BuildPlan(testSettings(), frontends, backends, ownedListener, state); err != nil {
		t.Fatalf("owned reconcile failed: %v", err)
	}
	foreignHAProxy := []inventory.Listener{{Protocol: "tcp", Address: "0.0.0.0", Port: 443, Process: "haproxy"}}
	if _, err := BuildPlan(testSettings(), frontends, backends, foreignHAProxy, state); err == nil {
		t.Fatal("foreign HAProxy listener was ignored")
	}
}

func TestDisabledBackendRemainsVisibleToRuntimeAPI(t *testing.T) {
	t.Parallel()
	frontends, backends := testHAProxyDesired()
	backends[1].Enabled = false
	state, _ := ParseState(nil, false)
	plan, err := BuildPlan(testSettings(), frontends, backends, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Candidate, "egm_srv_api_primary 10.20.0.10:8080 weight 100 check disabled") {
		t.Fatalf("disabled server omitted:\n%s", plan.Candidate)
	}
}
