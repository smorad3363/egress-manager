package nat

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

func testPolicy() SafetyPolicy {
	return SafetyPolicy{SSHPorts: []uint16{22}, PanelPort: 43127}
}

func testForward() domain.PortForward {
	return domain.PortForward{
		ID:              "web_forward",
		Name:            "Web forward",
		Protocols:       []domain.TransportProtocol{domain.ProtocolUDP, domain.ProtocolTCP},
		ListenAddress:   netip.MustParseAddr("203.0.113.10"),
		ListenPorts:     []domain.PortRange{{From: 8443, To: 8446}},
		RemoteAddress:   netip.MustParseAddr("10.10.0.5"),
		RemotePortStart: 9443,
		SourceCIDRs:     []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")},
		Enabled:         true,
	}
}

func TestBuildNFTPlanIsOwnedDeterministicAndNormalized(t *testing.T) {
	t.Parallel()

	forward := testForward()
	plan, err := BuildNFTPlan(IPv4, []domain.PortForward{forward}, nil, testPolicy(), true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.OwnedTable != "ip egm_nat4" || plan.EnabledRules != 1 || len(plan.Actions) != 2 {
		t.Fatalf("plan = %#v", plan)
	}
	for _, fragment := range []string{
		"delete table ip egm_nat4",
		"table ip egm_nat4 {",
		"ip daddr 203.0.113.10 tcp dport 8443-8446 counter dnat to 10.10.0.5:9443-9446 comment \"egm_pf_web_forward\"",
		"ct original daddr 203.0.113.10 ip daddr 10.10.0.5 ip saddr 198.51.100.0/24 udp dport 9443-9446 counter accept comment \"egm_pf_web_forward\"",
		"ct original daddr 203.0.113.10 ip daddr 10.10.0.5 tcp dport 9443-9446 counter drop comment \"egm_pf_web_forward_source_drop\"",
		"counter masquerade comment \"egm_pf_web_forward\"",
	} {
		if !strings.Contains(plan.Candidate, fragment) {
			t.Fatalf("candidate omitted %q:\n%s", fragment, plan.Candidate)
		}
	}
	if strings.Contains(plan.Candidate, "flush ruleset") || strings.Contains(plan.Candidate, "iptables -F") {
		t.Fatalf("candidate contains a global firewall operation:\n%s", plan.Candidate)
	}

	second, err := BuildNFTPlan(IPv4, []domain.PortForward{forward}, nil, testPolicy(), true)
	if err != nil {
		t.Fatal(err)
	}
	if second.Candidate != plan.Candidate {
		t.Fatal("planner output is not deterministic")
	}
}

func TestBuildNFTPlanRejectsProtectedPortsAndUnsafeAllPorts(t *testing.T) {
	t.Parallel()

	explicit := testForward()
	explicit.ListenPorts = []domain.PortRange{{From: 22, To: 22}}
	explicit.RemotePortStart = 0
	if _, err := BuildNFTPlan(IPv4, []domain.PortForward{explicit}, nil, testPolicy(), false); err == nil || !strings.Contains(err.Error(), "protected management port 22") {
		t.Fatalf("protected SSH error = %v", err)
	}

	allPorts := testForward()
	allPorts.ListenPorts = nil
	allPorts.RemotePortStart = 0
	allPorts.AllPortsExcept = []domain.PortRange{{From: 22, To: 22}}
	if _, err := BuildNFTPlan(IPv4, []domain.PortForward{allPorts}, nil, testPolicy(), false); err == nil || !strings.Contains(err.Error(), "43127") {
		t.Fatalf("unprotected panel error = %v", err)
	}
	allPorts.AllPortsExcept = append(allPorts.AllPortsExcept, domain.PortRange{From: 43127, To: 43127})
	plan, err := BuildNFTPlan(IPv4, []domain.PortForward{allPorts}, nil, testPolicy(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Candidate, "tcp dport != { 22, 43127 }") {
		t.Fatalf("all-ports candidate =\n%s", plan.Candidate)
	}
}

func TestBuildNFTPlanRejectsDuplicateAndListenerConflicts(t *testing.T) {
	t.Parallel()

	first := testForward()
	first.RemotePortStart = 0
	second := first
	second.ID = "other_forward"
	second.Name = "Other forward"
	second.ListenPorts = []domain.PortRange{{From: 8446, To: 8500}}
	if _, err := BuildNFTPlan(IPv4, []domain.PortForward{first, second}, nil, testPolicy(), false); err == nil || !strings.Contains(err.Error(), "overlap on port 8446") {
		t.Fatalf("duplicate error = %v", err)
	}

	listeners := []inventory.Listener{{Protocol: "tcp", Address: "0.0.0.0", Port: 8444, Process: "haproxy"}}
	if _, err := BuildNFTPlan(IPv4, []domain.PortForward{first}, listeners, testPolicy(), false); err == nil || !strings.Contains(err.Error(), "conflicts with tcp listener") {
		t.Fatalf("listener conflict error = %v", err)
	}

	second.SourceCIDRs = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	if _, err := BuildNFTPlan(IPv4, []domain.PortForward{first, second}, nil, testPolicy(), false); err != nil {
		t.Fatalf("disjoint source CIDRs conflicted: %v", err)
	}
}

func TestBuildNFTPlanRejectsInvalidCIDRRangeLoopbackAndProtectedPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*domain.PortForward, *SafetyPolicy)
	}{
		{name: "invalid range", mutate: func(forward *domain.PortForward, _ *SafetyPolicy) {
			forward.ListenPorts = []domain.PortRange{{From: 9000, To: 8000}}
			forward.RemotePortStart = 0
		}},
		{name: "noncanonical source", mutate: func(forward *domain.PortForward, _ *SafetyPolicy) {
			forward.SourceCIDRs = []netip.Prefix{netip.MustParsePrefix("198.51.100.9/24")}
		}},
		{name: "loopback destination", mutate: func(forward *domain.PortForward, _ *SafetyPolicy) {
			forward.RemoteAddress = netip.MustParseAddr("127.0.0.1")
		}},
		{name: "protected local prefix", mutate: func(_ *domain.PortForward, policy *SafetyPolicy) {
			policy.ProtectedLocalPrefixes = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forward := testForward()
			policy := testPolicy()
			test.mutate(&forward, &policy)
			if _, err := BuildNFTPlan(IPv4, []domain.PortForward{forward}, nil, policy, false); err == nil {
				t.Fatal("unsafe plan was accepted")
			}
		})
	}
}

func TestBuildNFTPlanOmitsDisabledRulesAndRendersIPv6(t *testing.T) {
	t.Parallel()

	disabled := testForward()
	disabled.Enabled = false
	disabled.RemotePortStart = 0
	plan, err := BuildNFTPlan(IPv4, []domain.PortForward{disabled}, nil, testPolicy(), false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Candidate, "egm_pf_web_forward") || plan.EnabledRules != 0 {
		t.Fatalf("disabled rule rendered:\n%s", plan.Candidate)
	}

	ipv6 := testForward()
	ipv6.ListenAddress = netip.MustParseAddr("2001:db8::10")
	ipv6.RemoteAddress = netip.MustParseAddr("2001:db8:1::5")
	ipv6.SourceCIDRs = []netip.Prefix{netip.MustParsePrefix("2001:db8:2::/64")}
	plan, err = BuildNFTPlan(IPv6, []domain.PortForward{ipv6}, nil, testPolicy(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Candidate, "table ip6 egm_nat6") || !strings.Contains(plan.Candidate, "dnat to [2001:db8:1::5]:9443-9446") {
		t.Fatalf("IPv6 candidate =\n%s", plan.Candidate)
	}
}
