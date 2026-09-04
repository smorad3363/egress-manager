package routing

import (
	"bytes"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

func TestBuildNativePlanRendersOwnedLeakControlsAndIPBatches(t *testing.T) {
	t.Parallel()
	state, _ := ParseState(nil, false)
	desired, err := BuildPlan(testRoutingSettings(), []domain.Route{testRoute()}, []domain.Outbound{testRoutingOutbound("primary", "203.0.113.30")}, testRoutingHost(), nil, state)
	if err != nil {
		t.Fatal(err)
	}
	first, err := BuildNativePlan(desired, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildNativePlan(desired, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Engine != "nftables+iproute2" || first.EnabledRoutes != 1 || first.CandidateHash == "" || !bytes.Equal(first.NFTCandidate(), second.NFTCandidate()) || first.CandidateHash != second.CandidateHash {
		t.Fatalf("native plan = %#v", first)
	}
	nft := string(first.NFTCandidate())
	for _, fragment := range []string{"table inet egm_egress", `iifname "tun0" ip saddr 10.8.0.0/24`, `oifname "`, "tcp option maxseg size set 1360", "counter drop", "dns_input"} {
		if !strings.Contains(nft, fragment) {
			t.Fatalf("nft candidate omitted %q:\n%s", fragment, nft)
		}
	}
	ipv4 := string(first.IPv4Batch())
	for _, fragment := range []string{"route replace throw 203.0.113.30/32", "route replace default dev ", "proto 242", "rule add priority ", "from 10.8.0.0/24"} {
		if !strings.Contains(ipv4, fragment) {
			t.Fatalf("IPv4 batch omitted %q:\n%s", fragment, ipv4)
		}
	}
	ipv6 := string(first.IPv6Batch())
	if !strings.Contains(ipv6, "route replace blackhole default") || !strings.Contains(ipv6, "iif tun0") {
		t.Fatalf("IPv6 block batch = %s", ipv6)
	}
	replacement, err := BuildNativePlan(desired, true)
	if err != nil || !strings.HasPrefix(string(replacement.NFTCandidate()), "delete table inet egm_egress\n") {
		t.Fatalf("replacement candidate = %q, error = %v", replacement.NFTCandidate(), err)
	}
}

func TestPlannerReusesVerifiedOwnedResourcesButNotForeignCollisions(t *testing.T) {
	t.Parallel()
	absent, _ := ParseState(nil, false)
	route := testRoute()
	outbounds := []domain.Outbound{testRoutingOutbound("primary", "203.0.113.30")}
	first, err := BuildPlan(testRoutingSettings(), []domain.Route{route}, outbounds, testRoutingHost(), nil, absent)
	if err != nil {
		t.Fatal(err)
	}
	owned, err := ParseState(first.Candidate(), true)
	if err != nil {
		t.Fatal(err)
	}
	intent := owned.Routes[0]
	host := testRoutingHost()
	host.Interfaces = append(host.Interfaces, interfaceForIntent(intent))
	host.Routes = append(host.Routes, routeForIntent(intent, OwnedRouteProtocol))
	host.PolicyRules = append(host.PolicyRules, policyRuleForIntent(intent, OwnedRouteProtocol))
	replanned, err := BuildPlan(testRoutingSettings(), []domain.Route{route}, outbounds, host, nil, owned)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := ParseState(replanned.Candidate(), true)
	if next.Routes[0].RoutingTable != intent.RoutingTable || next.Routes[0].RulePriority != intent.RulePriority || next.Routes[0].TunnelInterface != intent.TunnelInterface {
		t.Fatal("planner did not preserve verified owned resources")
	}

	host.Routes[len(host.Routes)-1].Protocol = "static"
	foreign, err := BuildPlan(testRoutingSettings(), []domain.Route{route}, outbounds, host, nil, owned)
	if err != nil {
		t.Fatal(err)
	}
	foreignState, _ := ParseState(foreign.Candidate(), true)
	if foreignState.Routes[0].RoutingTable == intent.RoutingTable {
		t.Fatal("planner reused a routing table occupied by foreign state")
	}
}

func interfaceForIntent(intent RouteIntent) inventory.Interface {
	addresses := make([]inventory.Address, len(intent.TunnelAddresses))
	for index, prefix := range intent.TunnelAddresses {
		family := "inet6"
		if prefix.Addr().Is4() {
			family = "inet"
		}
		addresses[index] = inventory.Address{Family: family, CIDR: prefix.String(), Scope: "global"}
	}
	return inventory.Interface{Name: intent.TunnelInterface, State: "up", MTU: 1400, Addresses: addresses}
}

func routeForIntent(intent RouteIntent, protocol string) inventory.Route {
	return inventory.Route{Destination: "default", Interface: intent.TunnelInterface, Protocol: protocol, Table: strconv.FormatUint(uint64(intent.RoutingTable), 10), Default: true}
}

func policyRuleForIntent(intent RouteIntent, protocol string) inventory.PolicyRule {
	return inventory.PolicyRule{Priority: int(intent.RulePriority), Protocol: protocol, Source: netip.MustParsePrefix("10.8.0.0/24").String(), Table: strconv.FormatUint(uint64(intent.RoutingTable), 10)}
}
