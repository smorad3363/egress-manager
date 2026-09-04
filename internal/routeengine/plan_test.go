package routeengine

import (
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
	managedInterface "github.com/egress-manager/egress-manager/internal/interfaceoutbound"
	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/routing"
	"github.com/egress-manager/egress-manager/internal/singbox"
)

func coordinatedTestPlan(t *testing.T) Plan {
	t.Helper()
	imports, err := singbox.ParseImport("socks5://user:pass@203.0.113.30:1080#Route")
	if err != nil {
		t.Fatal(err)
	}
	outbound := imports[0].Outbound
	route := domain.Route{
		ID: "vpn_clients", Name: "VPN clients", Source: domain.RouteSource{Kind: domain.RouteSourceInterface, Interface: "tun0"},
		OutboundID: outbound.ID, FailurePolicy: domain.FailureBlock, DNSPolicy: domain.DNSFollowOutbound,
		DNSServers: []netip.Addr{netip.MustParseAddr("1.1.1.1")}, IPv4Policy: domain.IPv4FollowOutbound,
		IPv6Policy: domain.IPv6Block, KillSwitch: true, MTU: 1400, TCPMSS: 1360, Enabled: true,
	}
	host := inventory.Inventory{Interfaces: []inventory.Interface{{Name: "tun0", State: "up", MTU: 1500, Addresses: []inventory.Address{{Family: "inet", CIDR: "10.8.0.1/24", Scope: "global"}}}}}
	routingState, _ := routing.ParseState(nil, false)
	desired, err := routing.BuildPlan(routing.Settings{TableBase: 20000, RulePriorityBase: 21000}, []domain.Route{route}, []domain.Outbound{outbound}, host, nil, routingState)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := routing.ParseState(desired.Candidate(), true)
	if err != nil {
		t.Fatal(err)
	}
	singBoxState, _ := singbox.ParseState(nil, false)
	singBoxPlan, err := singbox.BuildRoutedPlan([]domain.Outbound{outbound}, map[domain.ID][]byte{outbound.ID: imports[0].CredentialDocument}, parsed.Routes, singBoxState)
	if err != nil {
		t.Fatal(err)
	}
	native, err := routing.BuildNativePlan(desired, false)
	if err != nil {
		t.Fatal(err)
	}
	interfaceState, _ := managedInterface.ParseState(nil, false)
	plan, err := BuildPlan(singBoxPlan, desired, native, interfaceState)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestBuildPlanBindsAllReviewedCandidates(t *testing.T) {
	t.Parallel()
	plan := coordinatedTestPlan(t)
	if plan.Review.Engine != "coordinated-egress-routing" || plan.Review.EnabledRoutes != 1 || plan.Review.CombinedCandidateHash == "" {
		t.Fatalf("review = %#v", plan.Review)
	}
	mutated := plan.singBox
	candidate := mutated.Candidate()
	candidate[len(candidate)-2] ^= 1
	digest := sha256.Sum256(candidate)
	mutated.Plan.CandidateHash = hex.EncodeToString(digest[:])
	interfaceState, _ := managedInterface.ParseState(nil, false)
	if _, err := BuildPlan(mutated, plan.routing, plan.native, interfaceState); err == nil {
		t.Fatal("coordinator accepted a malformed mutated candidate")
	}
}

func TestBuildPlanBindsNativeRouteToExactInterfaceState(t *testing.T) {
	t.Parallel()
	interfaceName, err := managedInterface.InterfaceName(domain.OutboundWireGuard, "native_wg")
	if err != nil {
		t.Fatal(err)
	}
	outbound := domain.Outbound{
		ID: "native_wg", Name: "Native WireGuard", Adapter: domain.OutboundAdapterInterface, Type: domain.OutboundWireGuard,
		Server: domain.Endpoint{Host: "203.0.113.30", Port: 51820}, Capabilities: domain.Capabilities{TCP: true, UDP: true},
		Health: domain.UnknownOutboundHealth(), Enabled: true, SecretMetadata: []string{"private_key"},
	}
	route := domain.Route{
		ID: "vpn_clients", Name: "VPN clients", Source: domain.RouteSource{Kind: domain.RouteSourceInterface, Interface: "tun0"},
		OutboundID: outbound.ID, FailurePolicy: domain.FailureBlock, DNSPolicy: domain.DNSBlock,
		IPv4Policy: domain.IPv4FollowOutbound, IPv6Policy: domain.IPv6Block, KillSwitch: true, Enabled: true,
	}
	host := inventory.Inventory{Interfaces: []inventory.Interface{{Name: "tun0", State: "up", MTU: 1500}, {Name: interfaceName, State: "up", MTU: 1420}}}
	routingState, _ := routing.ParseState(nil, false)
	desired, err := routing.BuildPlan(routing.Settings{TableBase: 20000, RulePriorityBase: 21000, InterfaceOutbounds: map[domain.ID]string{outbound.ID: interfaceName}}, []domain.Route{route}, []domain.Outbound{outbound}, host, nil, routingState)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := routing.ParseState(desired.Candidate(), true)
	if err != nil {
		t.Fatal(err)
	}
	singBoxState, _ := singbox.ParseState(nil, false)
	singBoxPlan, err := singbox.BuildRoutedPlan([]domain.Outbound{outbound}, nil, parsed.Routes, singBoxState)
	if err != nil {
		t.Fatal(err)
	}
	native, err := routing.BuildNativePlan(desired, false)
	if err != nil {
		t.Fatal(err)
	}
	interfaceState := managedInterface.State{Exists: true, Hash: strings.Repeat("a", 64), Entries: []managedInterface.StateEntry{{ID: outbound.ID, Kind: outbound.Type, InterfaceName: interfaceName, ConfigHash: strings.Repeat("b", 64), Addresses: []string{"10.0.0.2/32"}}}}
	plan, err := BuildPlan(singBoxPlan, desired, native, interfaceState)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Review.InterfaceStateHash != interfaceState.Hash || singBoxPlan.Plan.RoutedRoutes != 0 || plan.Review.EnabledRoutes != 1 {
		t.Fatalf("native coordinated review = %#v", plan.Review)
	}
	interfaceState.Entries = nil
	if _, err := BuildPlan(singBoxPlan, desired, native, interfaceState); err == nil {
		t.Fatal("coordinator accepted native route without matching interface state")
	}
}
