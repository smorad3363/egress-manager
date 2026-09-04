package domain

import (
	"net/netip"
	"testing"
)

func validPortForward() PortForward {
	return PortForward{
		ID:            "web-forward",
		Name:          "Web Forward",
		Protocols:     []TransportProtocol{ProtocolTCP, ProtocolUDP},
		ListenAddress: netip.MustParseAddr("203.0.113.10"),
		ListenPorts:   []PortRange{{From: 8443, To: 8445}},
		RemoteAddress: netip.MustParseAddr("10.10.0.5"),
		SourceCIDRs:   []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")},
		Enabled:       true,
	}
}

func TestPortForwardValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*PortForward)
	}{
		{name: "valid", mutate: func(*PortForward) {}},
		{name: "invalid range", mutate: func(forward *PortForward) { forward.ListenPorts = []PortRange{{From: 90, To: 80}} }},
		{name: "duplicate protocol", mutate: func(forward *PortForward) { forward.Protocols = []TransportProtocol{ProtocolTCP, ProtocolTCP} }},
		{name: "conflicting all ports mode", mutate: func(forward *PortForward) { forward.AllPortsExcept = []PortRange{{From: 22, To: 22}} }},
		{name: "remap overflow", mutate: func(forward *PortForward) { forward.RemotePortStart = 65534 }},
		{name: "address family mismatch", mutate: func(forward *PortForward) { forward.RemoteAddress = netip.MustParseAddr("2001:db8::1") }},
		{name: "noncanonical source", mutate: func(forward *PortForward) {
			forward.SourceCIDRs = []netip.Prefix{netip.MustParsePrefix("198.51.100.9/24")}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			forward := validPortForward()
			test.mutate(&forward)
			err := forward.Validate()
			if test.name == "valid" && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if test.name != "valid" && err == nil {
				t.Fatal("Validate() expected an error")
			}
		})
	}
}

func TestAllPortsExceptMode(t *testing.T) {
	t.Parallel()

	forward := validPortForward()
	forward.ListenPorts = nil
	forward.AllPortsExcept = []PortRange{{From: 22, To: 22}, {From: 8443, To: 8443}}
	if err := forward.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestFirewallRuleRequiresOwnedNamespace(t *testing.T) {
	t.Parallel()

	rule := FirewallRule{
		ID:               "allow-web",
		Table:            "filter",
		Chain:            "INPUT",
		Protocol:         ProtocolTCP,
		DestinationPorts: []PortRange{{From: 443, To: 443}},
		Action:           FirewallAccept,
	}
	if err := rule.Validate(); err == nil {
		t.Fatal("Validate() accepted a foreign firewall namespace")
	}

	rule.Table = "egm_filter"
	rule.Chain = "EGM_INPUT"
	if err := rule.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
