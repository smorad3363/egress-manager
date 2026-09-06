package domain

import (
	"net/netip"
	"testing"
)

func TestRelayValidate(t *testing.T) {
	relay := Relay{
		ID:            "ssh_gateway",
		Name:          "SSH gateway",
		ListenAddress: "0.0.0.0",
		ListenPort:    6111,
		Network:       RelayTCP,
		Destination:   Endpoint{Host: "91.107.220.12", Port: 6111},
		OutboundID:    "mypcs",
		SourceCIDRs:   []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")},
		Enabled:       true,
	}
	if err := relay.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestRelayValidateRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Relay)
	}{
		{name: "listen address", edit: func(relay *Relay) { relay.ListenAddress = "example.com" }},
		{name: "listen port", edit: func(relay *Relay) { relay.ListenPort = 0 }},
		{name: "network", edit: func(relay *Relay) { relay.Network = "quic" }},
		{name: "destination", edit: func(relay *Relay) { relay.Destination.Port = 0 }},
		{name: "outbound", edit: func(relay *Relay) { relay.OutboundID = "" }},
		{name: "source prefix", edit: func(relay *Relay) { relay.SourceCIDRs = []netip.Prefix{netip.MustParsePrefix("198.51.100.3/24")} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			relay := Relay{ID: "ssh_gateway", Name: "SSH gateway", ListenAddress: "0.0.0.0", ListenPort: 6111, Network: RelayTCP, Destination: Endpoint{Host: "91.107.220.12", Port: 6111}, OutboundID: "mypcs", Enabled: true}
			test.edit(&relay)
			if err := relay.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}

func TestRelayNetworkCapabilities(t *testing.T) {
	if !RelayTCP.RequiresTCP() || RelayTCP.RequiresUDP() {
		t.Fatal("TCP capability mismatch")
	}
	if RelayUDP.RequiresTCP() || !RelayUDP.RequiresUDP() {
		t.Fatal("UDP capability mismatch")
	}
	if !RelayTCPUDP.RequiresTCP() || !RelayTCPUDP.RequiresUDP() {
		t.Fatal("TCP+UDP capability mismatch")
	}
}
