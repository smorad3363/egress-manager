package routing

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func TestVerifyPolicyRuntimeRejectsDrift(t *testing.T) {
	t.Parallel()
	state, _ := ParseState(nil, false)
	plan, err := BuildPlan(testRoutingSettings(), []domain.Route{testRoute()}, []domain.Outbound{testRoutingOutbound("primary", "198.51.100.20")}, testRoutingHost(), nil, state)
	if err != nil {
		t.Fatal(err)
	}
	state, err = ParseState(plan.Candidate(), true)
	if err != nil {
		t.Fatal(err)
	}
	intent := state.Routes[0]
	for _, ipv4 := range []bool{true, false} {
		rule := map[string]any{"priority": intent.RulePriority, "src": "all", "table": intent.RoutingTable, "protocol": "242", "iif": intent.IngressInterface}
		routes := []map[string]any{{"type": "blackhole", "dst": "default", "protocol": 242, "metric": 1024, "flags": []string{}}}
		if ipv4 {
			rule["src"] = intent.Source.Subnet.String()
			delete(rule, "iif")
			routes = []map[string]any{{"dst": "default", "dev": intent.TunnelInterface, "protocol": "242", "scope": "link"}}
			for _, address := range intent.BypassAddresses {
				if address.Is4() {
					routes = append(routes, map[string]any{"type": "throw", "dst": netip.PrefixFrom(address, 32).String(), "protocol": "242"})
				}
			}
		}
		check := func(want bool) {
			t.Helper()
			rulesJSON, _ := json.Marshal([]map[string]any{rule})
			routesJSON, _ := json.Marshal(routes)
			if err := VerifyPolicyRuntime(intent, ipv4, rulesJSON, routesJSON); (err == nil) != want {
				t.Fatalf("ipv4=%v want=%v error=%v", ipv4, want, err)
			}
		}
		check(true)
		if ipv4 {
			rule["src"] = intent.Source.Subnet.Addr().String()
			rule["srclen"] = intent.Source.Subnet.Bits()
			check(true)
			delete(rule, "srclen")
			rule["src"] = intent.Source.Subnet.String()
		} else {
			routes[0]["dev"] = "lo"
			check(true)
		}
		rule["table"] = intent.RoutingTable + 1
		check(false)
		rule["table"] = intent.RoutingTable
		rule["fwmark"] = "0x1"
		check(false)
		delete(rule, "fwmark")
		rule["src"] = "192.0.2.0/24"
		check(false)
		if ipv4 {
			rule["src"] = intent.Source.Subnet.String()
		} else {
			rule["src"] = "all"
		}
		routes[0]["gateway"] = "192.0.2.1"
		check(false)
		delete(routes[0], "gateway")
		routes[0]["protocol"] = "boot"
		check(false)
		routes[0]["protocol"] = "242"
		routes[0]["flags"] = []string{"linkdown"}
		check(false)
		routes[0]["flags"] = []string{}
		routes = append(routes, routes[0])
		check(false)
		routes = routes[:len(routes)-1]
		check(true)
		rulesJSON, _ := json.Marshal([]map[string]any{rule, rule})
		routesJSON, _ := json.Marshal(routes)
		if VerifyPolicyRuntime(intent, ipv4, rulesJSON, routesJSON) == nil {
			t.Fatal("duplicate policy rule accepted")
		}
		if VerifyPolicyRuntime(intent, ipv4, []byte(`[]`), routesJSON) == nil {
			t.Fatal("missing policy rule accepted")
		}
	}
}
