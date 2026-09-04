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
		t.Fatal("Validate() accepted implicit failure, DNS, and IPv6 policies")
	}

	route.FailurePolicy = FailureBlock
	route.DNSPolicy = DNSFollowOutbound
	route.IPv6Policy = IPv6Block
	if err := route.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
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
