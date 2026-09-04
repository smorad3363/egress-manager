package domain

import (
	"net/netip"
	"testing"
)

func TestRouteRequiresExplicitPolicies(t *testing.T) {
	t.Parallel()

	route := Route{
		ID:         "vpn-clients",
		Name:       "VPN Clients",
		Source:     RouteSource{Kind: RouteSourceSubnet, Subnet: netip.MustParsePrefix("10.8.0.0/24")},
		OutboundID: "vless-de",
		Enabled:    true,
	}
	if err := route.Validate(); err == nil {
		t.Fatal("Validate() accepted implicit failure, DNS, IPv4, and IPv6 policies")
	}

	route.FailurePolicy = FailureBlock
	route.DNSPolicy = DNSFollowOutbound
	route.IPv4Policy = IPv4FollowOutbound
	route.IPv6Policy = IPv6Block
	route.KillSwitch = true
	if err := route.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestRouteRequiresExplicitDistinctFallbackAndSafeMTU(t *testing.T) {
	t.Parallel()

	route := Route{
		ID: "vpn-clients", Name: "VPN Clients", Source: RouteSource{Kind: RouteSourceInterface, Interface: "tun0"},
		OutboundID: "vless-de", FailurePolicy: FailureFailover, DNSPolicy: DNSFollowOutbound,
		IPv4Policy: IPv4FollowOutbound, IPv6Policy: IPv6Block, KillSwitch: true, MTU: 1400, TCPMSS: 1360, Enabled: true,
	}
	if err := route.Validate(); err == nil {
		t.Fatal("Validate() accepted failover without a fallback outbound")
	}
	route.FallbackOutboundID = route.OutboundID
	if err := route.Validate(); err == nil {
		t.Fatal("Validate() accepted the primary outbound as its own fallback")
	}
	route.FallbackOutboundID = "vless-nl"
	if err := route.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	route.TCPMSS = route.MTU
	if err := route.Validate(); err == nil {
		t.Fatal("Validate() accepted TCP MSS equal to MTU")
	}
}

func TestRouteRejectsContradictoryFailureAndKillSwitchPolicy(t *testing.T) {
	t.Parallel()
	route := Route{
		ID: "vpn-clients", Name: "VPN Clients", Source: RouteSource{Kind: RouteSourceInterface, Interface: "tun0"},
		OutboundID: "vless-de", FailurePolicy: FailureDirect, DNSPolicy: DNSSystem,
		IPv4Policy: IPv4Direct, IPv6Policy: IPv6Direct, KillSwitch: true, Enabled: true,
	}
	if err := route.Validate(); err == nil {
		t.Fatal("Validate() accepted a kill switch with direct fallback")
	}
	route.KillSwitch = false
	if err := route.Validate(); err != nil {
		t.Fatalf("explicit direct policy error = %v", err)
	}
}

func TestRouteRejectsNonCanonicalSubnet(t *testing.T) {
	t.Parallel()

	route := Route{
		ID:            "vpn-clients",
		Name:          "VPN Clients",
		Source:        RouteSource{Kind: RouteSourceSubnet, Subnet: netip.MustParsePrefix("10.8.0.3/24")},
		OutboundID:    "vless-de",
		FailurePolicy: FailureBlock,
		DNSPolicy:     DNSFollowOutbound,
		IPv4Policy:    IPv4FollowOutbound,
		IPv6Policy:    IPv6Block,
	}
	if err := route.Validate(); err == nil {
		t.Fatal("Validate() accepted a non-canonical subnet")
	}
}

func TestInterfaceSourceRejectsUnsafeName(t *testing.T) {
	t.Parallel()

	source := RouteSource{Kind: RouteSourceInterface, Interface: "tun0;reboot"}
	if err := source.Validate(); err == nil {
		t.Fatal("Validate() accepted an unsafe interface name")
	}
}

func TestRouteSourceRejectsFieldsFromAnotherKind(t *testing.T) {
	t.Parallel()

	source := RouteSource{
		Kind:      RouteSourceInterface,
		Interface: "tun0",
		Subnet:    netip.MustParsePrefix("10.8.0.0/24"),
	}
	if err := source.Validate(); err == nil {
		t.Fatal("Validate() accepted an ambiguous route source")
	}
}
