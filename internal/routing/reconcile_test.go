package routing

import (
	"bytes"
	"net/netip"
	"strconv"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

func TestReconciliationPreservesAppliedStateAndRejectsForeignCollisions(t *testing.T) {
	state, _ := ParseState(nil, false)
	plan, err := BuildPlan(testRoutingSettings(), []domain.Route{testRoute()}, []domain.Outbound{testRoutingOutbound("primary", "198.51.100.20")}, testRoutingHost(), nil, state)
	if err != nil {
		t.Fatal(err)
	}
	state, _ = ParseState(plan.Candidate(), true)
	replayed, err := BuildReconciliationPlan(plan.Candidate(), testRoutingHost(), nil)
	if err != nil || !bytes.Equal(replayed.Candidate(), plan.Candidate()) || replayed.Plan.StateHash != state.Hash {
		t.Fatalf("replay error=%v", err)
	}
	intent := state.Routes[0]
	reservedHost := testRoutingHost()
	reservedHost.PolicyRules = append(reservedHost.PolicyRules, inventory.PolicyRule{Priority: 100, Protocol: "boot", Table: strconv.FormatUint(uint64(intent.RoutingTable), 10)})
	replanned, err := BuildPlan(testRoutingSettings(), []domain.Route{testRoute()}, []domain.Outbound{testRoutingOutbound("primary", "198.51.100.20")}, reservedHost, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	newState, _ := ParseState(replanned.Candidate(), true)
	if newState.Routes[0].RoutingTable == intent.RoutingTable {
		t.Fatal("foreign policy reference to an empty table was ignored")
	}
	for _, test := range []struct {
		name   string
		mutate func(*inventory.Inventory)
	}{
		{"foreign IPv6 table", func(host *inventory.Inventory) {
			host.Routes = append(host.Routes, inventory.Route{Destination: "2001:db8::/32", Protocol: "boot", Table: strconv.FormatUint(uint64(intent.RoutingTable), 10)})
		}},
		{"foreign priority", func(host *inventory.Inventory) {
			host.PolicyRules = append(host.PolicyRules, inventory.PolicyRule{Priority: int(intent.RulePriority), Protocol: "boot", Table: "main"})
		}},
		{"foreign table reference", func(host *inventory.Inventory) {
			host.PolicyRules = append(host.PolicyRules, inventory.PolicyRule{Priority: 1, Protocol: "boot", Table: strconv.FormatUint(uint64(intent.RoutingTable), 10)})
		}},
		{"incomplete IPv6", func(host *inventory.Inventory) { host.Warnings = []string{"IPv6 route inventory unavailable"} }},
		{"down ingress", func(host *inventory.Inventory) { host.Interfaces[2].State = "down" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := testRoutingHost()
			test.mutate(&host)
			if _, err := BuildReconciliationPlan(plan.Candidate(), host, nil); err == nil {
				t.Fatal("unsafe reconciliation accepted")
			}
		})
	}
	if _, err := BuildReconciliationPlan(plan.Candidate(), testRoutingHost(), []netip.Prefix{netip.MustParsePrefix("10.8.0.0/24")}); err == nil {
		t.Fatal("new management protection ignored")
	}
}
