package main

import (
	"fmt"
	"net/netip"
	"os"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/nat"
)

func main() {
	forwards := []domain.PortForward{
		{
			ID: "lab_tcp", Name: "Lab TCP", Protocols: []domain.TransportProtocol{domain.ProtocolTCP},
			ListenAddress: netip.MustParseAddr("10.203.1.1"), ListenPorts: []domain.PortRange{{From: 19080, To: 19080}},
			RemoteAddress: netip.MustParseAddr("10.203.2.2"), RemotePortStart: 8080,
			SourceCIDRs: []netip.Prefix{netip.MustParsePrefix("10.203.1.0/24")}, Enabled: true,
		},
		{
			ID: "lab_udp", Name: "Lab UDP", Protocols: []domain.TransportProtocol{domain.ProtocolUDP},
			ListenAddress: netip.MustParseAddr("10.203.1.1"), ListenPorts: []domain.PortRange{{From: 19053, To: 19053}},
			RemoteAddress: netip.MustParseAddr("10.203.2.2"), RemotePortStart: 5353,
			SourceCIDRs: []netip.Prefix{netip.MustParsePrefix("10.203.1.0/24")}, Enabled: true,
		},
	}
	plan, err := nat.BuildNFTPlan(nat.IPv4, forwards, nil, nat.SafetyPolicy{SSHPorts: []uint16{22}, PanelPort: 43127}, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "NAT plan generation failed")
		os.Exit(1)
	}
	fmt.Print(plan.Candidate)
}
