package main

import (
	"context"
	"fmt"
	"net/netip"
	"os"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/nat"
	"github.com/egress-manager/egress-manager/internal/system"
)

func main() {
	if os.Getenv("EGRESS_IPTABLES_ROLLBACK") == "1" {
		current, err := nat.InspectIPTablesState(context.Background(), system.ExecRunner{}, nat.IPv4, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Print(nat.BuildIPTablesRollback(nat.IPTablesState{NatRules: []string{}, FilterRules: []string{}}, current))
		return
	}
	forwards := []domain.PortForward{
		{ID: "lab_tcp", Name: "Lab TCP", Protocols: []domain.TransportProtocol{domain.ProtocolTCP}, ListenAddress: netip.MustParseAddr("10.203.1.1"), ListenPorts: []domain.PortRange{{From: 19080, To: 19080}}, RemoteAddress: netip.MustParseAddr("10.203.2.2"), RemotePortStart: 8080, SourceCIDRs: []netip.Prefix{netip.MustParsePrefix("10.203.1.0/24")}, Enabled: true},
		{ID: "lab_udp", Name: "Lab UDP", Protocols: []domain.TransportProtocol{domain.ProtocolUDP}, ListenAddress: netip.MustParseAddr("10.203.1.1"), ListenPorts: []domain.PortRange{{From: 19053, To: 19053}}, RemoteAddress: netip.MustParseAddr("10.203.2.2"), RemotePortStart: 5353, Enabled: true},
	}
	state := nat.IPTablesState{NatRules: []string{}, FilterRules: []string{}}
	var err error
	if os.Getenv("EGRESS_IPTABLES_INSPECT") == "1" {
		state, err = nat.InspectIPTablesState(context.Background(), system.ExecRunner{}, nat.IPv4, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	plan, err := nat.BuildIPTablesPlan(nat.IPv4, forwards, nil, nat.SafetyPolicy{SSHPorts: []uint16{22}, PanelPort: 443}, state)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(plan.Candidate)
}
