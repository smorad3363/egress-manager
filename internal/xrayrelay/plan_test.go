package xrayrelay

import (
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

func TestBuildPlanRequiresEnabledXrayOutbound(t *testing.T) {
	parsed, err := ParseImport(syntheticVLESSURI)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := ParseState(nil, false)
	relay := domain.Relay{ID: "relay", Name: "Relay", ListenAddress: "0.0.0.0", ListenPort: 6111, Network: domain.RelayTCP, Destination: domain.Endpoint{Host: "91.107.220.12", Port: 6111}, OutboundID: parsed.Outbound.ID, Enabled: true}
	disabled := parsed.Outbound
	disabled.Enabled = false
	if _, err := BuildPlan(Settings{}, []domain.Relay{relay}, []domain.Outbound{disabled}, map[domain.ID][]byte{parsed.Outbound.ID: parsed.CredentialDocument}, nil, state); err == nil {
		t.Fatal("disabled outbound was accepted")
	}
	wrong := parsed.Outbound
	wrong.Adapter = domain.OutboundAdapterSingBox
	if _, err := BuildPlan(Settings{}, []domain.Relay{relay}, []domain.Outbound{wrong}, map[domain.ID][]byte{parsed.Outbound.ID: parsed.CredentialDocument}, nil, state); err == nil {
		t.Fatal("wrong adapter outbound was accepted")
	}
}
