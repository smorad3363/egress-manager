package routing

import (
	"encoding/json"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

func testRoutingSettings() Settings {
	return Settings{TableBase: 20000, RulePriorityBase: 21000, ProtectedLocalPrefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.9/32")}}
}

func testRoutingHost() inventory.Inventory {
	return inventory.Inventory{Interfaces: []inventory.Interface{
		{Name: "lo", State: "unknown", MTU: 65536, Addresses: []inventory.Address{{Family: "inet", CIDR: "127.0.0.1/8", Scope: "host"}}},
		{Name: "eth0", State: "up", MTU: 1500, Addresses: []inventory.Address{{Family: "inet", CIDR: "192.0.2.9/24", Scope: "global"}}},
		{Name: "tun0", State: "up", MTU: 1500, Addresses: []inventory.Address{{Family: "inet", CIDR: "10.8.0.1/24", Scope: "global"}, {Family: "inet6", CIDR: "2001:db8:8::1/64", Scope: "global"}}},
	}, Routes: []inventory.Route{{Destination: "default", Gateway: "192.0.2.1", Interface: "eth0", Table: "main", Default: true}}}
}

func testRoutingOutbound(id domain.ID, host string) domain.Outbound {
	return domain.Outbound{ID: id, Name: string(id), Adapter: domain.OutboundAdapterSingBox, Type: domain.OutboundSOCKS5, Server: domain.Endpoint{Host: host, Port: 1080}, Capabilities: domain.Capabilities{TCP: true, UDP: true}, Health: domain.UnknownOutboundHealth(), Enabled: true}
}

func testRoute() domain.Route {
	return domain.Route{
		ID: "vpn_clients", Name: "VPN clients", Source: domain.RouteSource{Kind: domain.RouteSourceSubnet, Subnet: netip.MustParsePrefix("10.8.0.0/24")},
		OutboundID: "primary", FailurePolicy: domain.FailureBlock, DNSPolicy: domain.DNSFollowOutbound,
		DNSServers: []netip.Addr{netip.MustParseAddr("1.1.1.1")},
		IPv4Policy: domain.IPv4FollowOutbound, IPv6Policy: domain.IPv6Block, KillSwitch: true, MTU: 1400, TCPMSS: 1360, Enabled: true,
	}
}

func TestBuildPlanIsDeterministicOwnedAndIncludesLeakControls(t *testing.T) {
	t.Parallel()
	state, err := ParseState(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	route := testRoute()
	outbounds := []domain.Outbound{testRoutingOutbound("primary", "edge.example.com")}
	resolved := ResolvedEndpoints{"primary": {netip.MustParseAddr("2001:db8::20"), netip.MustParseAddr("198.51.100.20"), netip.MustParseAddr("198.51.100.20")}}
	plan, err := BuildPlan(testRoutingSettings(), []domain.Route{route}, outbounds, testRoutingHost(), resolved, state)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Plan.Engine != "policy-routing" || plan.Plan.EnabledRoutes != 1 || len(plan.Plan.Actions) != 4 || plan.Plan.StateHash != state.Hash || plan.Plan.CandidateHash == "" {
		t.Fatalf("plan = %#v", plan.Plan)
	}
	candidate := string(plan.Candidate())
	for _, fragment := range []string{`"schema": "egress-manager/routing/v1"`, `"ingress_interface": "tun0"`, `"failure_policy": "block"`, `"dns_policy": "follow_outbound"`, `"ipv4_policy": "follow_outbound"`, `"ipv6_policy": "block"`, `"kill_switch": true`, `"198.51.100.20"`, `"2001:db8::20"`} {
		if !strings.Contains(candidate, fragment) {
			t.Fatalf("candidate omitted %q:\n%s", fragment, candidate)
		}
	}
	second, err := BuildPlan(testRoutingSettings(), []domain.Route{route}, outbounds, testRoutingHost(), resolved, state)
	if err != nil || string(second.Candidate()) != candidate || second.Plan.CandidateHash != plan.Plan.CandidateHash {
		t.Fatalf("planner is not deterministic: error = %v", err)
	}
	parsed, err := ParseState(plan.Candidate(), true)
	if err != nil || !parsed.Exists || parsed.Hash == state.Hash {
		t.Fatalf("parsed state = %#v, error = %v", parsed, err)
	}
}

func TestBuildPlanSupportsExplicitFailoverAndOmitsDisabledRoutes(t *testing.T) {
	t.Parallel()
	state, _ := ParseState(nil, false)
	route := testRoute()
	route.FailurePolicy = domain.FailureFailover
	route.FallbackOutboundID = "fallback"
	disabled := route
	disabled.ID = "disabled_route"
	disabled.Name = "Disabled route"
	disabled.Enabled = false
	plan, err := BuildPlan(testRoutingSettings(), []domain.Route{disabled, route}, []domain.Outbound{testRoutingOutbound("fallback", "203.0.113.31"), testRoutingOutbound("primary", "203.0.113.30")}, testRoutingHost(), nil, state)
	if err != nil {
		t.Fatal(err)
	}
	candidate := string(plan.Candidate())
	if plan.Plan.EnabledRoutes != 1 || !strings.Contains(candidate, `"fallback_outbound_id": "fallback"`) || strings.Contains(candidate, "disabled_route") {
		t.Fatalf("candidate = %s", candidate)
	}
}

func TestBuildPlanRejectsUnsafeSelectorsAndOutboundPaths(t *testing.T) {
	t.Parallel()
	state, _ := ParseState(nil, false)
	tests := []struct {
		name      string
		routes    []domain.Route
		outbounds []domain.Outbound
		host      inventory.Inventory
		resolved  ResolvedEndpoints
		settings  Settings
		want      string
	}{
		{name: "missing interface", routes: []domain.Route{func() domain.Route {
			value := testRoute()
			value.Source = domain.RouteSource{Kind: domain.RouteSourceInterface, Interface: "missing0"}
			return value
		}()}, outbounds: []domain.Outbound{testRoutingOutbound("primary", "203.0.113.30")}, host: testRoutingHost(), settings: testRoutingSettings(), want: "missing, down, or loopback"},
		{name: "protected source", routes: []domain.Route{func() domain.Route {
			value := testRoute()
			value.Source.Subnet = netip.MustParsePrefix("192.0.2.0/24")
			return value
		}()}, outbounds: []domain.Outbound{testRoutingOutbound("primary", "203.0.113.30")}, host: testRoutingHost(), settings: testRoutingSettings(), want: "protected management"},
		{name: "overlap", routes: []domain.Route{testRoute(), func() domain.Route {
			value := testRoute()
			value.ID = "nested_clients"
			value.Name = "Nested clients"
			value.Source.Subnet = netip.MustParsePrefix("10.8.0.0/25")
			return value
		}()}, outbounds: []domain.Outbound{testRoutingOutbound("primary", "203.0.113.30")}, host: testRoutingHost(), settings: testRoutingSettings(), want: "overlaps enabled route"},
		{name: "disabled outbound", routes: []domain.Route{testRoute()}, outbounds: []domain.Outbound{func() domain.Outbound {
			value := testRoutingOutbound("primary", "203.0.113.30")
			value.Enabled = false
			return value
		}()}, host: testRoutingHost(), settings: testRoutingSettings(), want: "missing or disabled"},
		{name: "unresolved hostname", routes: []domain.Route{testRoute()}, outbounds: []domain.Outbound{testRoutingOutbound("primary", "edge.example.com")}, host: testRoutingHost(), settings: testRoutingSettings(), want: "must resolve"},
		{name: "unsafe endpoint", routes: []domain.Route{testRoute()}, outbounds: []domain.Outbound{testRoutingOutbound("primary", "edge.example.com")}, host: testRoutingHost(), resolved: ResolvedEndpoints{"primary": {netip.MustParseAddr("127.0.0.1")}}, settings: testRoutingSettings(), want: "unsafe address"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildPlan(test.settings, test.routes, test.outbounds, test.host, test.resolved, state)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestBuildPlanSkipsForeignNumericTablesAndRejectsForeignState(t *testing.T) {
	t.Parallel()
	state, _ := ParseState(nil, false)
	host := testRoutingHost()
	first, err := BuildPlan(testRoutingSettings(), []domain.Route{testRoute()}, []domain.Outbound{testRoutingOutbound("primary", "203.0.113.30")}, host, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	var firstCandidate Candidate
	if err := json.Unmarshal(first.Candidate(), &firstCandidate); err != nil {
		t.Fatal(err)
	}
	host.Routes = append(host.Routes, inventory.Route{Destination: "default", Table: strconv.FormatUint(uint64(firstCandidate.Routes[0].RoutingTable), 10)})
	host.PolicyRules = append(host.PolicyRules, inventory.PolicyRule{Priority: int(firstCandidate.Routes[0].RulePriority), Source: "all", Table: "main"})
	second, err := BuildPlan(testRoutingSettings(), []domain.Route{testRoute()}, []domain.Outbound{testRoutingOutbound("primary", "203.0.113.30")}, host, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	var secondCandidate Candidate
	if err := json.Unmarshal(second.Candidate(), &secondCandidate); err != nil {
		t.Fatal(err)
	}
	if secondCandidate.Routes[0].RoutingTable == firstCandidate.Routes[0].RoutingTable || secondCandidate.Routes[0].RulePriority == firstCandidate.Routes[0].RulePriority {
		t.Fatal("planner reused a foreign routing table or policy priority")
	}
	if _, err := ParseState([]byte(`{"schema":"foreign","routes":[]}`), true); err == nil {
		t.Fatal("ParseState accepted foreign routing state")
	}
}
