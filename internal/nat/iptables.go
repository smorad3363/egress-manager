package nat

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

const (
	ipChainPrerouting  = "EGM_PREROUTING"
	ipChainPostrouting = "EGM_POSTROUTING"
	ipChainForward     = "EGM_FORWARD"
)

type IPTablesState struct {
	PreroutingChain   bool     `json:"prerouting_chain"`
	PostroutingChain  bool     `json:"postrouting_chain"`
	ForwardChain      bool     `json:"forward_chain"`
	PreroutingAnchor  bool     `json:"prerouting_anchor"`
	PostroutingAnchor bool     `json:"postrouting_anchor"`
	ForwardAnchor     bool     `json:"forward_anchor"`
	NatRules          []string `json:"nat_rules"`
	FilterRules       []string `json:"filter_rules"`
}

func ParseIPTablesState(natSave, filterSave []byte) (IPTablesState, error) {
	state := IPTablesState{NatRules: []string{}, FilterRules: []string{}}
	if err := parseIPTablesTable(string(natSave), "nat", &state); err != nil {
		return IPTablesState{}, err
	}
	if err := parseIPTablesTable(string(filterSave), "filter", &state); err != nil {
		return IPTablesState{}, err
	}
	return state, nil
}

func parseIPTablesTable(input, table string, state *IPTablesState) error {
	for _, rawLine := range strings.Split(input, "\n") {
		line := strings.TrimSpace(rawLine)
		switch {
		case strings.HasPrefix(line, ":"+ipChainPrerouting+" "):
			if table != "nat" {
				return fmt.Errorf("owned prerouting chain appears outside nat table")
			}
			state.PreroutingChain = true
		case strings.HasPrefix(line, ":"+ipChainPostrouting+" "):
			if table != "nat" {
				return fmt.Errorf("owned postrouting chain appears outside nat table")
			}
			state.PostroutingChain = true
		case strings.HasPrefix(line, ":"+ipChainForward+" "):
			if table != "filter" {
				return fmt.Errorf("owned forward chain appears outside filter table")
			}
			state.ForwardChain = true
		case strings.HasPrefix(line, "-A "+ipChainPrerouting+" "), strings.HasPrefix(line, "-A "+ipChainPostrouting+" "):
			if table != "nat" || !ownedIPTablesRule(line) {
				return fmt.Errorf("iptables owned-chain collision in %s", table)
			}
			state.NatRules = append(state.NatRules, line)
		case strings.HasPrefix(line, "-A "+ipChainForward+" "):
			if table != "filter" || !ownedIPTablesRule(line) {
				return fmt.Errorf("iptables owned-chain collision in %s", table)
			}
			state.FilterRules = append(state.FilterRules, line)
		case jumpsTo(line, ipChainPrerouting):
			if table != "nat" || !isAnchor(line, "PREROUTING", ipChainPrerouting, "egm_anchor_prerouting") {
				return fmt.Errorf("foreign jump targets %s", ipChainPrerouting)
			}
			if state.PreroutingAnchor {
				return fmt.Errorf("duplicate owned anchor targets %s", ipChainPrerouting)
			}
			state.PreroutingAnchor = true
		case jumpsTo(line, ipChainPostrouting):
			if table != "nat" || !isAnchor(line, "POSTROUTING", ipChainPostrouting, "egm_anchor_postrouting") {
				return fmt.Errorf("foreign jump targets %s", ipChainPostrouting)
			}
			if state.PostroutingAnchor {
				return fmt.Errorf("duplicate owned anchor targets %s", ipChainPostrouting)
			}
			state.PostroutingAnchor = true
		case jumpsTo(line, ipChainForward):
			if table != "filter" || !isAnchor(line, "FORWARD", ipChainForward, "egm_anchor_forward") {
				return fmt.Errorf("foreign jump targets %s", ipChainForward)
			}
			if state.ForwardAnchor {
				return fmt.Errorf("duplicate owned anchor targets %s", ipChainForward)
			}
			state.ForwardAnchor = true
		}
	}
	if state.PreroutingAnchor && !state.PreroutingChain || state.PostroutingAnchor && !state.PostroutingChain || state.ForwardAnchor && !state.ForwardChain {
		return fmt.Errorf("iptables anchor targets a missing owned chain")
	}
	return nil
}

func ownedIPTablesRule(line string) bool {
	return strings.Contains(line, "--comment \"egm_") || strings.Contains(line, "--comment egm_")
}

func jumpsTo(line, chain string) bool {
	return strings.HasPrefix(line, "-A ") && strings.Contains(line, " -j "+chain)
}

func isAnchor(line, builtin, target, comment string) bool {
	return strings.HasPrefix(line, "-A "+builtin+" ") && hasIPTablesComment(line, comment) && strings.HasSuffix(line, "-j "+target)
}

func hasIPTablesComment(line, comment string) bool {
	fields := strings.Fields(line)
	for index, field := range fields {
		if field == "--comment" && index+1 < len(fields) && strings.Trim(fields[index+1], `"`) == comment {
			return true
		}
	}
	return false
}

func BuildIPTablesPlan(family AddressFamily, desired []domain.PortForward, listeners []inventory.Listener, policy SafetyPolicy, state IPTablesState) (Plan, error) {
	if state.NatRules == nil || state.FilterRules == nil {
		return Plan{}, fmt.Errorf("iptables state must contain initialized rule collections")
	}
	normalized, err := prepareForwards(family, desired, listeners, policy)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		Engine: "iptables", Family: family,
		OwnedTable:   "nat/EGM_PREROUTING nat/EGM_POSTROUTING filter/EGM_FORWARD",
		TableExisted: state.PreroutingChain || state.PostroutingChain || state.ForwardChain,
		Targets:      []VerificationTarget{}, Actions: []Action{},
	}
	plan.StateHash, err = iptablesStateHash(state)
	if err != nil {
		return Plan{}, fmt.Errorf("hash iptables state: %w", err)
	}
	if plan.TableExisted {
		plan.Actions = append(plan.Actions, Action{Kind: "replace", Resource: plan.OwnedTable, Summary: "Replace only Egress Manager-owned iptables chains."})
	} else {
		plan.Actions = append(plan.Actions, Action{Kind: "create", Resource: plan.OwnedTable, Summary: "Create Egress Manager-owned iptables chains and anchors."})
	}
	for _, forward := range normalized {
		if !forward.Enabled {
			continue
		}
		plan.EnabledRules++
		plan.Targets = appendVerificationTarget(plan.Targets, forward.RemoteAddress.String())
		plan.Actions = append(plan.Actions, Action{Kind: "forward", Resource: string(forward.ID), Summary: "Install owned DNAT, forwarding, and masquerade rules."})
	}
	plan.Candidate = renderIPTables(normalized, state)
	if len(plan.Candidate) > maximumCandidateBytes {
		return Plan{}, fmt.Errorf("iptables candidate exceeds %d bytes", maximumCandidateBytes)
	}
	return plan, nil
}

func renderIPTables(forwards []domain.PortForward, state IPTablesState) string {
	var natRules, filterRules []string
	filterRules = append(filterRules, `-A EGM_FORWARD -m conntrack --ctstate ESTABLISHED,RELATED -m comment --comment "egm_established" -j ACCEPT`)
	for _, forward := range forwards {
		if !forward.Enabled {
			continue
		}
		for _, protocol := range forward.Protocols {
			for _, portRange := range allowedRanges(forward) {
				natRules = append(natRules, iptablesDNATRule(forward, protocol, portRange))
				translated := translatedRange(forward, portRange)
				if len(forward.SourceCIDRs) == 0 {
					filterRules = append(filterRules, iptablesForwardRule(forward, protocol, translated, netip.Prefix{}, "ACCEPT", "allow"))
				} else {
					for _, prefix := range forward.SourceCIDRs {
						filterRules = append(filterRules, iptablesForwardRule(forward, protocol, translated, prefix, "ACCEPT", "allow"))
					}
					filterRules = append(filterRules, iptablesForwardRule(forward, protocol, translated, netip.Prefix{}, "DROP", "source_drop"))
				}
				natRules = append(natRules, iptablesMasqueradeRule(forward, protocol, translated))
			}
		}
	}
	var output strings.Builder
	output.WriteString("*nat\n")
	output.WriteString(":" + ipChainPrerouting + " - [0:0]\n:" + ipChainPostrouting + " - [0:0]\n")
	if state.PreroutingChain {
		output.WriteString("-F " + ipChainPrerouting + "\n")
	}
	if state.PostroutingChain {
		output.WriteString("-F " + ipChainPostrouting + "\n")
	}
	for _, rule := range natRules {
		output.WriteString(rule + "\n")
	}
	if !state.PreroutingAnchor {
		output.WriteString(`-I PREROUTING 1 -m comment --comment "egm_anchor_prerouting" -j EGM_PREROUTING` + "\n")
	}
	if !state.PostroutingAnchor {
		output.WriteString(`-I POSTROUTING 1 -m comment --comment "egm_anchor_postrouting" -j EGM_POSTROUTING` + "\n")
	}
	output.WriteString("COMMIT\n*filter\n:" + ipChainForward + " - [0:0]\n")
	if state.ForwardChain {
		output.WriteString("-F " + ipChainForward + "\n")
	}
	for _, rule := range filterRules {
		output.WriteString(rule + "\n")
	}
	if !state.ForwardAnchor {
		output.WriteString(`-I FORWARD 1 -m comment --comment "egm_anchor_forward" -j EGM_FORWARD` + "\n")
	}
	output.WriteString("COMMIT\n")
	return output.String()
}

func iptablesDNATRule(forward domain.PortForward, protocol domain.TransportProtocol, ports domain.PortRange) string {
	return fmt.Sprintf(`-A %s -d %s -p %s --dport %s -m comment --comment "egm_pf_%s_dnat" -j DNAT --to-destination %s`, ipChainPrerouting, forward.ListenAddress, protocol, iptablesRange(ports), forward.ID, iptablesDestination(forward, ports))
}

func iptablesForwardRule(forward domain.PortForward, protocol domain.TransportProtocol, ports domain.PortRange, source netip.Prefix, verdict, kind string) string {
	parts := []string{"-A", ipChainForward, "-d", forward.RemoteAddress.String(), "-p", string(protocol), "--dport", iptablesRange(ports), "-m", "conntrack", "--ctorigdst", forward.ListenAddress.String()}
	if source.IsValid() {
		parts = append(parts, "-s", source.String())
	}
	parts = append(parts, "-m", "comment", "--comment", `"egm_pf_`+string(forward.ID)+"_"+kind+`"`, "-j", verdict)
	return strings.Join(parts, " ")
}

func iptablesMasqueradeRule(forward domain.PortForward, protocol domain.TransportProtocol, ports domain.PortRange) string {
	return fmt.Sprintf(`-A %s -d %s -p %s --dport %s -m conntrack --ctorigdst %s -m comment --comment "egm_pf_%s_masquerade" -j MASQUERADE`, ipChainPostrouting, forward.RemoteAddress, protocol, iptablesRange(ports), forward.ListenAddress, forward.ID)
}

func translatedRange(forward domain.PortForward, original domain.PortRange) domain.PortRange {
	if forward.RemotePortStart == 0 {
		return original
	}
	offset := uint32(original.From) - uint32(forward.ListenPorts[0].From)
	from := uint16(uint32(forward.RemotePortStart) + offset)
	return domain.PortRange{From: from, To: uint16(uint32(from) + uint32(original.To) - uint32(original.From))}
}

func iptablesDestination(forward domain.PortForward, original domain.PortRange) string {
	address := forward.RemoteAddress.String()
	if forward.RemoteAddress.Is6() {
		address = "[" + address + "]"
	}
	if forward.RemotePortStart == 0 {
		return address
	}
	return address + ":" + strings.ReplaceAll(iptablesRange(translatedRange(forward, original)), ":", "-")
}

func iptablesRange(portRange domain.PortRange) string {
	if portRange.From == portRange.To {
		return strconv.Itoa(int(portRange.From))
	}
	return strconv.Itoa(int(portRange.From)) + ":" + strconv.Itoa(int(portRange.To))
}

func sortedIPTablesRules(rules []string) []string {
	copyOfRules := append([]string{}, rules...)
	sort.Strings(copyOfRules)
	return copyOfRules
}
