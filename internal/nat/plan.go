// Package nat plans and applies project-owned NAT port forwarding.
package nat

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

const (
	MaximumForwards       = 16
	maximumCandidateBytes = 20 << 10
)

type AddressFamily string

const (
	IPv4 AddressFamily = "ipv4"
	IPv6 AddressFamily = "ipv6"
)

func (family AddressFamily) nativeName() (string, string, error) {
	switch family {
	case IPv4:
		return "ip", "egm_nat4", nil
	case IPv6:
		return "ip6", "egm_nat6", nil
	default:
		return "", "", fmt.Errorf("unsupported address family %q", family)
	}
}

type SafetyPolicy struct {
	SSHPorts               []uint16
	PanelPort              uint16
	ProtectedLocalPrefixes []netip.Prefix
}

func (policy SafetyPolicy) Validate() error {
	var errs []error
	if policy.PanelPort == 0 {
		errs = append(errs, fmt.Errorf("panel port is required"))
	}
	if len(policy.SSHPorts) == 0 {
		errs = append(errs, fmt.Errorf("at least one SSH port is required"))
	}
	seen := map[uint16]struct{}{}
	for _, port := range append(append([]uint16{}, policy.SSHPorts...), policy.PanelPort) {
		if port == 0 {
			errs = append(errs, fmt.Errorf("protected ports must be nonzero"))
		}
		if _, exists := seen[port]; exists {
			errs = append(errs, fmt.Errorf("duplicate protected port %d", port))
		}
		seen[port] = struct{}{}
	}
	for _, prefix := range policy.ProtectedLocalPrefixes {
		if !prefix.IsValid() || prefix != prefix.Masked() {
			errs = append(errs, fmt.Errorf("protected local prefixes must be canonical"))
		}
	}
	return errors.Join(errs...)
}

func (policy SafetyPolicy) protectedPorts() []uint16 {
	ports := append(append([]uint16{}, policy.SSHPorts...), policy.PanelPort)
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
	return ports
}

type Action struct {
	Kind     string `json:"kind"`
	Resource string `json:"resource"`
	Summary  string `json:"summary"`
}

type Plan struct {
	Engine       string               `json:"engine"`
	Family       AddressFamily        `json:"family"`
	OwnedTable   string               `json:"owned_table"`
	TableExisted bool                 `json:"table_existed"`
	EnabledRules int                  `json:"enabled_rules"`
	Targets      []VerificationTarget `json:"verification_targets"`
	Actions      []Action             `json:"actions"`
	Candidate    string               `json:"candidate"`
}

type VerificationTarget struct {
	Address string `json:"address"`
}

func BuildNFTPlan(family AddressFamily, desired []domain.PortForward, listeners []inventory.Listener, policy SafetyPolicy, tableExisted bool) (Plan, error) {
	nativeFamily, table, err := family.nativeName()
	if err != nil {
		return Plan{}, err
	}
	if err := policy.Validate(); err != nil {
		return Plan{}, fmt.Errorf("validate NAT safety policy: %w", err)
	}
	if len(desired) > MaximumForwards {
		return Plan{}, fmt.Errorf("NAT configuration exceeds %d forwards", MaximumForwards)
	}

	normalized := make([]domain.PortForward, 0, len(desired))
	for _, forward := range desired {
		if err := forward.Validate(); err != nil {
			return Plan{}, fmt.Errorf("validate port forward %q: %w", forward.ID, err)
		}
		if family == IPv4 && !forward.ListenAddress.Is4() || family == IPv6 && !forward.ListenAddress.Is6() {
			continue
		}
		forward = normalizeForward(forward)
		if err := enforceSafety(forward, listeners, policy); err != nil {
			return Plan{}, err
		}
		normalized = append(normalized, forward)
	}
	if err := rejectDuplicates(normalized); err != nil {
		return Plan{}, err
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ID < normalized[j].ID })

	plan := Plan{
		Engine:       "nftables",
		Family:       family,
		OwnedTable:   nativeFamily + " " + table,
		TableExisted: tableExisted,
		Targets:      []VerificationTarget{},
		Actions:      []Action{},
	}
	if tableExisted {
		plan.Actions = append(plan.Actions, Action{Kind: "replace", Resource: plan.OwnedTable, Summary: "Replace only the project-owned NAT table."})
	} else {
		plan.Actions = append(plan.Actions, Action{Kind: "create", Resource: plan.OwnedTable, Summary: "Create the project-owned NAT table."})
	}
	for _, forward := range normalized {
		if forward.Enabled {
			plan.EnabledRules++
			plan.Targets = appendVerificationTarget(plan.Targets, forward.RemoteAddress.String())
			plan.Actions = append(plan.Actions, Action{Kind: "forward", Resource: string(forward.ID), Summary: "Install owned DNAT, forwarding, and masquerade rules."})
		}
	}
	plan.Candidate = renderNFT(nativeFamily, table, normalized, tableExisted)
	if len(plan.Candidate) > maximumCandidateBytes {
		return Plan{}, fmt.Errorf("nftables candidate exceeds %d bytes", maximumCandidateBytes)
	}
	return plan, nil
}

func appendVerificationTarget(targets []VerificationTarget, address string) []VerificationTarget {
	for _, target := range targets {
		if target.Address == address {
			return targets
		}
	}
	return append(targets, VerificationTarget{Address: address})
}

func normalizeForward(forward domain.PortForward) domain.PortForward {
	forward.ListenAddress = forward.ListenAddress.Unmap()
	forward.RemoteAddress = forward.RemoteAddress.Unmap()
	forward.Protocols = append([]domain.TransportProtocol{}, forward.Protocols...)
	sort.Slice(forward.Protocols, func(i, j int) bool { return forward.Protocols[i] < forward.Protocols[j] })
	forward.ListenPorts = normalizeRanges(forward.ListenPorts)
	forward.AllPortsExcept = normalizeRanges(forward.AllPortsExcept)
	forward.SourceCIDRs = append([]netip.Prefix{}, forward.SourceCIDRs...)
	sort.Slice(forward.SourceCIDRs, func(i, j int) bool { return forward.SourceCIDRs[i].String() < forward.SourceCIDRs[j].String() })
	return forward
}

func normalizeRanges(input []domain.PortRange) []domain.PortRange {
	ranges := append([]domain.PortRange{}, input...)
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].From != ranges[j].From {
			return ranges[i].From < ranges[j].From
		}
		return ranges[i].To < ranges[j].To
	})
	merged := make([]domain.PortRange, 0, len(ranges))
	for _, current := range ranges {
		if len(merged) == 0 || uint32(current.From) > uint32(merged[len(merged)-1].To)+1 {
			merged = append(merged, current)
			continue
		}
		if current.To > merged[len(merged)-1].To {
			merged[len(merged)-1].To = current.To
		}
	}
	return merged
}

func enforceSafety(forward domain.PortForward, listeners []inventory.Listener, policy SafetyPolicy) error {
	if forward.ListenAddress.IsLoopback() || forward.RemoteAddress.IsLoopback() {
		return fmt.Errorf("port forward %q must not capture or target loopback", forward.ID)
	}
	for _, prefix := range policy.ProtectedLocalPrefixes {
		if prefix.Contains(forward.ListenAddress) {
			return fmt.Errorf("port forward %q captures a protected local management prefix", forward.ID)
		}
	}
	for _, protected := range policy.protectedPorts() {
		if matchesPort(forward, protected) {
			return fmt.Errorf("port forward %q captures protected management port %d", forward.ID, protected)
		}
		if len(forward.AllPortsExcept) > 0 && !rangesContain(forward.AllPortsExcept, protected) {
			return fmt.Errorf("port forward %q all-ports mode does not exclude protected management port %d", forward.ID, protected)
		}
	}
	if !forward.Enabled {
		return nil
	}
	for _, listener := range listeners {
		if !matchesPort(forward, listener.Port) {
			continue
		}
		for _, protocol := range forward.Protocols {
			if protocol != domain.TransportProtocol(strings.ToLower(listener.Protocol)) {
				continue
			}
			conflicts, err := inventory.DetectConflicts([]inventory.Binding{{Protocol: string(protocol), Address: forward.ListenAddress.String(), Port: listener.Port}}, []inventory.Listener{listener})
			if err != nil {
				return err
			}
			if len(conflicts) > 0 {
				return fmt.Errorf("port forward %q conflicts with %s listener on %s:%d", forward.ID, protocol, listener.Address, listener.Port)
			}
		}
	}
	return nil
}

func matchesPort(forward domain.PortForward, port uint16) bool {
	if len(forward.ListenPorts) > 0 {
		return rangesContain(forward.ListenPorts, port)
	}
	return !rangesContain(forward.AllPortsExcept, port)
}

func rangesContain(ranges []domain.PortRange, port uint16) bool {
	for _, portRange := range ranges {
		if portRange.Contains(port) {
			return true
		}
	}
	return false
}

func rejectDuplicates(forwards []domain.PortForward) error {
	for firstIndex, first := range forwards {
		if !first.Enabled {
			continue
		}
		for _, second := range forwards[firstIndex+1:] {
			if !second.Enabled || first.ListenAddress != second.ListenAddress || !sourcesOverlap(first.SourceCIDRs, second.SourceCIDRs) || !protocolsOverlap(first.Protocols, second.Protocols) {
				continue
			}
			if port, overlaps := rangesOverlap(allowedRanges(first), allowedRanges(second)); overlaps {
				return fmt.Errorf("port forwards %q and %q overlap on port %d", first.ID, second.ID, port)
			}
		}
	}
	return nil
}

func allowedRanges(forward domain.PortForward) []domain.PortRange {
	if len(forward.ListenPorts) > 0 {
		return forward.ListenPorts
	}
	allowed := make([]domain.PortRange, 0, len(forward.AllPortsExcept)+1)
	next := uint32(1)
	for _, excluded := range forward.AllPortsExcept {
		if next < uint32(excluded.From) {
			allowed = append(allowed, domain.PortRange{From: uint16(next), To: excluded.From - 1})
		}
		next = uint32(excluded.To) + 1
	}
	if next <= 65535 {
		allowed = append(allowed, domain.PortRange{From: uint16(next), To: 65535})
	}
	return allowed
}

func rangesOverlap(first, second []domain.PortRange) (uint16, bool) {
	for _, left := range first {
		for _, right := range second {
			start := left.From
			if right.From > start {
				start = right.From
			}
			if start <= left.To && start <= right.To {
				return start, true
			}
		}
	}
	return 0, false
}

func protocolsOverlap(first, second []domain.TransportProtocol) bool {
	for _, left := range first {
		for _, right := range second {
			if left == right {
				return true
			}
		}
	}
	return false
}

func sourcesOverlap(first, second []netip.Prefix) bool {
	if len(first) == 0 || len(second) == 0 {
		return true
	}
	for _, left := range first {
		for _, right := range second {
			if left.Contains(right.Addr()) || right.Contains(left.Addr()) {
				return true
			}
		}
	}
	return false
}
