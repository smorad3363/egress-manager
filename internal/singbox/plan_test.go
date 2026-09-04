package singbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/routing"
)

func TestBuildPlanIsDeterministicOwnedAndPubliclyRedacted(t *testing.T) {
	t.Parallel()
	imported, err := ParseImport(readFixture(t, "vless.uri"))
	if err != nil {
		t.Fatal(err)
	}
	outbound := imported[0].Outbound
	state, err := ParseState(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	credentials := map[domain.ID][]byte{outbound.ID: imported[0].CredentialDocument}
	first, err := BuildPlan([]domain.Outbound{outbound}, credentials, state)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPlan([]domain.Outbound{outbound}, credentials, state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Candidate(), second.Candidate()) || first.Plan.CandidateHash != second.Plan.CandidateHash {
		t.Fatal("sing-box plan is not deterministic")
	}
	if _, err := ParseState(first.Candidate(), true); err != nil {
		t.Fatal(err)
	}
	publicJSON, err := json.Marshal(first.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), "00000000-0000-4000") || strings.Contains(string(publicJSON), "credential") {
		t.Fatalf("public plan leaked credential data: %s", publicJSON)
	}
	if !strings.Contains(string(first.Candidate()), "00000000-0000-4000") {
		t.Fatal("private candidate did not contain required adapter credential")
	}
}

func TestBuildRoutedPlanComposesOwnedTUNAndLeakPolicies(t *testing.T) {
	t.Parallel()
	imports, err := ParseImport(readFixture(t, "vless.uri"))
	if err != nil {
		t.Fatal(err)
	}
	outbound := imports[0].Outbound
	state, _ := ParseState(nil, false)
	id := domain.ID("vpn_clients")
	digest := sha256.Sum256([]byte(id))
	intent := routing.RouteIntent{
		ID: id, Source: domain.RouteSource{Kind: domain.RouteSourceInterface, Interface: "tun0"}, IngressInterface: "tun0",
		TunnelInterface: "egm" + hex.EncodeToString(digest[:4]), RoutingTable: 20001, RulePriority: 21001,
		OutboundID: outbound.ID, OutboundAdapter: domain.OutboundAdapterSingBox, SelectedOutboundID: outbound.ID,
		FailurePolicy: domain.FailureBlock, DNSPolicy: domain.DNSFollowOutbound, DNSServers: []netip.Addr{netip.MustParseAddr("1.1.1.1")},
		IPv4Policy: domain.IPv4FollowOutbound, IPv6Policy: domain.IPv6Block, KillSwitch: true, MTU: 1400, TCPMSS: 1360,
		BypassAddresses: []netip.Addr{netip.MustParseAddr("198.51.100.20")},
		TunnelAddresses: []netip.Prefix{netip.MustParsePrefix("198.18.0.1/30"), netip.MustParsePrefix("fd45:474d::1/126")},
	}
	credentials := map[domain.ID][]byte{outbound.ID: imports[0].CredentialDocument}
	plan, err := BuildRoutedPlan([]domain.Outbound{outbound}, credentials, []routing.RouteIntent{intent}, state)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Plan.RoutedRoutes != 1 {
		t.Fatalf("routed routes = %d, want 1", plan.Plan.RoutedRoutes)
	}
	candidate := string(plan.Candidate())
	for _, fragment := range []string{`"type": "tun"`, `"auto_route": false`, `"stack": "system"`, `"auto_detect_interface": true`, `"server": "1.1.1.1"`, `"action": "hijack-dns"`, `"ip_version": 6`, `"action": "reject"`} {
		if !strings.Contains(candidate, fragment) {
			t.Fatalf("routed candidate omitted %q:\n%s", fragment, candidate)
		}
	}
	if strings.Contains(candidate, directRouteTag) {
		t.Fatal("block policy unexpectedly configured direct fallback")
	}
	if _, err := ParseState(plan.Candidate(), true); err != nil {
		t.Fatal(err)
	}

	intent.FailurePolicy = domain.FailureDirect
	intent.DNSPolicy = domain.DNSSystem
	intent.DNSServers = nil
	intent.IPv4Policy = domain.IPv4Direct
	intent.IPv6Policy = domain.IPv6Direct
	intent.KillSwitch = false
	intent.SelectedOutboundID = ""
	intent.UseDirect = true
	direct, err := BuildRoutedPlan([]domain.Outbound{outbound}, credentials, []routing.RouteIntent{intent}, state)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(direct.Candidate()), `"tag": "egm_direct_fallback"`) {
		t.Fatal("explicit direct policy omitted the owned direct outbound")
	}
}

func TestBuildPlanRejectsCredentialMismatchAndDuplicateIDs(t *testing.T) {
	t.Parallel()
	imports, err := ParseImport(readFixture(t, "trojan.uri"))
	if err != nil {
		t.Fatal(err)
	}
	outbound := imports[0].Outbound
	state, _ := ParseState(nil, false)
	credentials := map[domain.ID][]byte{outbound.ID: imports[0].CredentialDocument}
	mismatch := outbound
	mismatch.Server.Host = "other.example.com"
	if _, err := BuildPlan([]domain.Outbound{mismatch}, credentials, state); err == nil {
		t.Fatal("credential mismatch accepted")
	}
	if _, err := BuildPlan([]domain.Outbound{outbound, outbound}, credentials, state); err == nil {
		t.Fatal("duplicate outbound accepted")
	}
}

func TestBuildPlanIgnoresNativeInterfaceOutbounds(t *testing.T) {
	t.Parallel()
	state, _ := ParseState(nil, false)
	outbound := domain.Outbound{
		ID: "native_wg", Name: "Native WireGuard", Adapter: domain.OutboundAdapterInterface, Type: domain.OutboundWireGuard,
		Server: domain.Endpoint{Host: "vpn.example.com", Port: 51820}, Capabilities: domain.Capabilities{TCP: true, UDP: true},
		Health: domain.UnknownOutboundHealth(), Enabled: true, SecretMetadata: []string{"private_key"},
	}
	plan, err := BuildPlan([]domain.Outbound{outbound}, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Plan.EnabledOutbounds != 0 || strings.Contains(string(plan.Candidate()), "native_wg") {
		t.Fatalf("native interface outbound entered sing-box candidate: %s", plan.Candidate())
	}
}

func TestParseStateRejectsForeignConfiguration(t *testing.T) {
	t.Parallel()
	foreign := []byte(`{"$schema":"https://sing-box.sagernet.org/schema.json#egress-manager-owned","log":{"disabled":true},"outbounds":[{"type":"direct","tag":"foreign"}]}`)
	if _, err := ParseState(foreign, true); err == nil {
		t.Fatal("foreign sing-box configuration accepted")
	}
	if _, err := ParseState(nil, false); err != nil {
		t.Fatal(err)
	}
	foreignInbound := []byte(`{"$schema":"https://sing-box.sagernet.org/schema.json#egress-manager-owned","log":{"disabled":true},"inbounds":[{"type":"tun","tag":"foreign"}],"outbounds":[]}`)
	if _, err := ParseState(foreignInbound, true); err == nil {
		t.Fatal("foreign sing-box inbound accepted")
	}
}
