package nat

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func TestBuildIPTablesPlanUsesOnlyOwnedChainsAndStableComments(t *testing.T) {
	t.Parallel()
	forward := testForward()
	plan, err := BuildIPTablesPlan(IPv4, []domain.PortForward{forward}, nil, SafetyPolicy{SSHPorts: []uint16{22}, PanelPort: 443}, IPTablesState{NatRules: []string{}, FilterRules: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`-A EGM_PREROUTING -d 203.0.113.10 -p tcp --dport 8443:8446 -m comment --comment "egm_pf_web_forward_dnat" -j DNAT --to-destination 10.10.0.5:9443-9446`,
		`-A EGM_FORWARD -d 10.10.0.5 -p udp --dport 9443:9446 -m conntrack --ctorigdst 203.0.113.10 -s 198.51.100.0/24 -m comment --comment "egm_pf_web_forward_allow" -j ACCEPT`,
		`-A EGM_FORWARD -d 10.10.0.5 -p tcp --dport 9443:9446 -m conntrack --ctorigdst 203.0.113.10 -m comment --comment "egm_pf_web_forward_source_drop" -j DROP`,
		`-I PREROUTING 1 -m comment --comment "egm_anchor_prerouting" -j EGM_PREROUTING`,
	} {
		if !strings.Contains(plan.Candidate, expected) {
			t.Fatalf("candidate omitted %q:\n%s", expected, plan.Candidate)
		}
	}
	for _, forbidden := range []string{"iptables -F", "iptables -t nat -F", "flush ruleset", "-F PREROUTING", "-F FORWARD"} {
		if strings.Contains(plan.Candidate, forbidden) {
			t.Fatalf("candidate contains forbidden operation %q", forbidden)
		}
	}
}

func TestBuildIPTablesAllPortsExceptExpandsSafeAllowedRanges(t *testing.T) {
	t.Parallel()
	forward := testForward()
	forward.ListenPorts = nil
	forward.RemotePortStart = 0
	forward.SourceCIDRs = nil
	forward.AllPortsExcept = []domain.PortRange{{From: 22, To: 22}, {From: 443, To: 443}, {From: 8443, To: 8443}}
	plan, err := BuildIPTablesPlan(IPv4, []domain.PortForward{forward}, nil, SafetyPolicy{SSHPorts: []uint16{22}, PanelPort: 443}, IPTablesState{NatRules: []string{}, FilterRules: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Candidate, "--dport 22 ") || strings.Contains(plan.Candidate, "--dport 443 ") || strings.Contains(plan.Candidate, "--dport 8443 ") {
		t.Fatalf("candidate captured an excluded port:\n%s", plan.Candidate)
	}
	for _, expected := range []string{"--dport 1:21", "--dport 23:442", "--dport 444:8442", "--dport 8444:65535"} {
		if !strings.Contains(plan.Candidate, expected) {
			t.Fatalf("candidate omitted allowed complement %q", expected)
		}
	}
}

func TestParseIPTablesStatePreservesOwnedRulesAndRejectsCollisions(t *testing.T) {
	t.Parallel()
	natSave := `*nat
:PREROUTING ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
:EGM_PREROUTING - [0:0]
:EGM_POSTROUTING - [0:0]
-A PREROUTING -m comment --comment "egm_anchor_prerouting" -j EGM_PREROUTING
-A POSTROUTING -m comment --comment "egm_anchor_postrouting" -j EGM_POSTROUTING
-A EGM_PREROUTING -d 203.0.113.10/32 -p tcp -m tcp --dport 443 -m comment --comment "egm_pf_old_dnat" -j DNAT --to-destination 10.0.0.2:443
COMMIT`
	filterSave := `*filter
:FORWARD ACCEPT [0:0]
:EGM_FORWARD - [0:0]
-A FORWARD -m comment --comment "egm_anchor_forward" -j EGM_FORWARD
-A EGM_FORWARD -m comment --comment "egm_established" -j ACCEPT
COMMIT`
	state, err := ParseIPTablesState([]byte(natSave), []byte(filterSave))
	if err != nil {
		t.Fatal(err)
	}
	if !state.PreroutingAnchor || !state.PostroutingAnchor || !state.ForwardAnchor || len(state.NatRules) != 1 || len(state.FilterRules) != 1 {
		t.Fatalf("state = %#v", state)
	}
	if _, err := ParseIPTablesState([]byte(strings.Replace(natSave, `--comment "egm_pf_old_dnat"`, `--comment "somebody_else"`, 1)), []byte(filterSave)); err == nil {
		t.Fatal("ParseIPTablesState() accepted a foreign rule in an owned chain")
	}
	if _, err := ParseIPTablesState([]byte(strings.Replace(natSave, `--comment "egm_anchor_prerouting"`, `--comment "foreign_anchor"`, 1)), []byte(filterSave)); err == nil {
		t.Fatal("ParseIPTablesState() accepted a foreign jump to an owned chain")
	}
}

func TestIPTablesPlannerStillEnforcesSharedSafetyPolicy(t *testing.T) {
	t.Parallel()
	forward := testForward()
	forward.ListenAddress = netip.MustParseAddr("127.0.0.1")
	if _, err := BuildIPTablesPlan(IPv4, []domain.PortForward{forward}, nil, testPolicy(), IPTablesState{NatRules: []string{}, FilterRules: []string{}}); err == nil {
		t.Fatal("BuildIPTablesPlan() accepted loopback capture")
	}
}
