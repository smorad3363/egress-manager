package xrayrelay

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

func TestBuildPlanCreatesFixedDestinationRelay(t *testing.T) {
	parsed, err := ParseImport(syntheticVLESSURI)
	if err != nil {
		t.Fatal(err)
	}
	relay := domain.Relay{
		ID: "ssh_gateway", Name: "SSH gateway", ListenAddress: "0.0.0.0", ListenPort: 6111, Network: domain.RelayTCP,
		Destination: domain.Endpoint{Host: "91.107.220.12", Port: 6111}, OutboundID: parsed.Outbound.ID,
		SourceCIDRs: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, Enabled: true,
	}
	state, err := ParseState(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(Settings{ProtectedPorts: []uint16{22, 47604}}, []domain.Relay{relay}, []domain.Outbound{parsed.Outbound}, map[domain.ID][]byte{parsed.Outbound.ID: parsed.CredentialDocument}, nil, state)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if !plan.Review.CandidateExists || plan.Review.EnabledRelays != 1 || plan.Review.EnabledOutbounds != 1 {
		t.Fatalf("unexpected review: %#v", plan.Review)
	}
	candidate := string(plan.Candidate())
	for _, expected := range []string{"dokodemo-door", "91.107.220.12", `"port": 6111`, "egm_relay_in_ssh_gateway", "egm_out_", "198.51.100.0/24", "egm_block"} {
		if !strings.Contains(candidate, expected) {
			t.Fatalf("candidate does not contain %q: %s", expected, candidate)
		}
	}
	public, err := json.Marshal(plan.Review)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"11111111-2222-4333-8444-555555555555", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "0123456789abcdef"} {
		if strings.Contains(string(public), secret) {
			t.Fatalf("public plan leaked credential %q", secret)
		}
	}
}

func TestBuildPlanRejectsProtectedAndForeignListeners(t *testing.T) {
	parsed, err := ParseImport(syntheticVLESSURI)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := ParseState(nil, false)
	base := domain.Relay{ID: "relay", Name: "Relay", ListenAddress: "0.0.0.0", ListenPort: 6111, Network: domain.RelayTCP, Destination: domain.Endpoint{Host: "91.107.220.12", Port: 6111}, OutboundID: parsed.Outbound.ID, Enabled: true}
	credentials := map[domain.ID][]byte{parsed.Outbound.ID: parsed.CredentialDocument}
	if _, err := BuildPlan(Settings{ProtectedPorts: []uint16{6111}}, []domain.Relay{base}, []domain.Outbound{parsed.Outbound}, credentials, nil, state); err == nil {
		t.Fatal("protected listener was accepted")
	}
	listeners := []inventory.Listener{{Protocol: "tcp", Address: "0.0.0.0", Port: 6111, Process: "sshd", PID: 12}}
	if _, err := BuildPlan(Settings{}, []domain.Relay{base}, []domain.Outbound{parsed.Outbound}, credentials, listeners, state); err == nil {
		t.Fatal("foreign listener conflict was accepted")
	}
}

func TestBuildPlanRejectsOverlappingDesiredListeners(t *testing.T) {
	parsed, err := ParseImport(syntheticVLESSURI)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := ParseState(nil, false)
	left := domain.Relay{ID: "left", Name: "Left", ListenAddress: "0.0.0.0", ListenPort: 6111, Network: domain.RelayTCPUDP, Destination: domain.Endpoint{Host: "91.107.220.12", Port: 6111}, OutboundID: parsed.Outbound.ID, Enabled: true}
	right := left
	right.ID = "right"
	right.Name = "Right"
	right.ListenAddress = "213.176.120.172"
	right.Network = domain.RelayTCP
	if _, err := BuildPlan(Settings{}, []domain.Relay{left, right}, []domain.Outbound{parsed.Outbound}, map[domain.ID][]byte{parsed.Outbound.ID: parsed.CredentialDocument}, nil, state); err == nil {
		t.Fatal("overlapping desired listeners were accepted")
	}
}

func TestBuildPlanRequiresPresentEnabledCapableXrayOutbound(t *testing.T) {
	parsed, err := ParseImport(syntheticVLESSURI)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := ParseState(nil, false)
	relay := domain.Relay{ID: "relay", Name: "Relay", ListenAddress: "0.0.0.0", ListenPort: 6111, Network: domain.RelayTCP, Destination: domain.Endpoint{Host: "91.107.220.12", Port: 6111}, OutboundID: parsed.Outbound.ID, Enabled: true}
	credentials := map[domain.ID][]byte{parsed.Outbound.ID: parsed.CredentialDocument}
	if _, err := BuildPlan(Settings{}, []domain.Relay{relay}, nil, credentials, nil, state); err == nil || !strings.Contains(err.Error(), "missing or disabled") {
		t.Fatalf("missing outbound error = %v", err)
	}
	disabled := parsed.Outbound
	disabled.Enabled = false
	if _, err := BuildPlan(Settings{}, []domain.Relay{relay}, []domain.Outbound{disabled}, credentials, nil, state); err == nil {
		t.Fatal("disabled outbound was accepted")
	}
	wrong := parsed.Outbound
	wrong.Adapter = domain.OutboundAdapterSingBox
	if _, err := BuildPlan(Settings{}, []domain.Relay{relay}, []domain.Outbound{wrong}, credentials, nil, state); err == nil {
		t.Fatal("wrong adapter outbound was accepted")
	}
	udpRelay := relay
	udpRelay.Network = domain.RelayUDP
	tcpOnly := parsed.Outbound
	tcpOnly.Capabilities.UDP = false
	if _, err := BuildPlan(Settings{}, []domain.Relay{udpRelay}, []domain.Outbound{tcpOnly}, credentials, nil, state); err == nil || !strings.Contains(err.Error(), "capability") {
		t.Fatalf("capability mismatch error = %v", err)
	}
}

func TestBuildPlanIsDeterministicAcrossInputOrder(t *testing.T) {
	first, err := ParseImport(syntheticVLESSURI)
	if err != nil {
		t.Fatal(err)
	}
	second := first.Outbound
	second.ID = "second_xray"
	second.Name = "Second Xray"
	second.Server = domain.Endpoint{Host: "example.net", Port: 2026}
	var secondDocument CredentialDocument
	if err := json.Unmarshal(first.CredentialDocument, &secondDocument); err != nil {
		t.Fatal(err)
	}
	settings := secondDocument.Outbound["settings"].(map[string]any)
	vnext := settings["vnext"].([]any)
	vnext[0].(map[string]any)["address"] = second.Server.Host
	vnext[0].(map[string]any)["port"] = second.Server.Port
	secondCredential, err := json.Marshal(secondDocument)
	if err != nil {
		t.Fatal(err)
	}
	left := domain.Relay{ID: "alpha", Name: "Alpha", ListenAddress: "127.0.0.1", ListenPort: 6111, Network: domain.RelayTCP, Destination: domain.Endpoint{Host: "192.0.2.10", Port: 22}, OutboundID: first.Outbound.ID, Enabled: true}
	right := domain.Relay{ID: "beta", Name: "Beta", ListenAddress: "127.0.0.1", ListenPort: 6112, Network: domain.RelayTCP, Destination: domain.Endpoint{Host: "192.0.2.11", Port: 22}, OutboundID: second.ID, Enabled: true}
	state, _ := ParseState(nil, false)
	credentials := map[domain.ID][]byte{first.Outbound.ID: first.CredentialDocument, second.ID: secondCredential}
	forward, err := BuildPlan(Settings{}, []domain.Relay{left, right}, []domain.Outbound{first.Outbound, second}, credentials, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := BuildPlan(Settings{}, []domain.Relay{right, left}, []domain.Outbound{second, first.Outbound}, credentials, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	if forward.Review.CandidateHash != reversed.Review.CandidateHash || !bytes.Equal(forward.Candidate(), reversed.Candidate()) {
		t.Fatalf("candidate depends on input order:\nforward=%s\nreversed=%s", forward.Candidate(), reversed.Candidate())
	}
}
