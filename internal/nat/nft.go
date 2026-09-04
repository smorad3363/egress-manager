package nat

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func renderNFT(family, table string, forwards []domain.PortForward, tableExisted bool) string {
	var output strings.Builder
	if tableExisted {
		fmt.Fprintf(&output, "delete table %s %s\n", family, table)
	}
	fmt.Fprintf(&output, "table %s %s {\n", family, table)
	output.WriteString("  comment \"Egress Manager owned NAT table\"\n")
	output.WriteString("  chain prerouting {\n    type nat hook prerouting priority dstnat; policy accept;\n")
	for _, forward := range forwards {
		if !forward.Enabled {
			continue
		}
		for _, protocol := range forward.Protocols {
			fmt.Fprintf(&output, "    %s counter dnat to %s comment \"egm_pf_%s_dnat\"\n", nftMatch(forward, protocol, false, false), nftDestination(forward), forward.ID)
		}
	}
	output.WriteString("  }\n")
	output.WriteString("  chain forward {\n    type filter hook forward priority filter; policy accept;\n")
	output.WriteString("    ct state established,related counter accept comment \"egm_established\"\n")
	for _, forward := range forwards {
		if !forward.Enabled {
			continue
		}
		for _, protocol := range forward.Protocols {
			fmt.Fprintf(&output, "    %s counter accept comment \"egm_pf_%s_allow\"\n", nftMatch(forward, protocol, true, true), forward.ID)
		}
	}
	for _, forward := range forwards {
		if !forward.Enabled || len(forward.SourceCIDRs) == 0 {
			continue
		}
		for _, protocol := range forward.Protocols {
			fmt.Fprintf(&output, "    %s counter drop comment \"egm_pf_%s_source_drop\"\n", nftMatch(forward, protocol, true, false), forward.ID)
		}
	}
	output.WriteString("  }\n")
	output.WriteString("  chain postrouting {\n    type nat hook postrouting priority srcnat; policy accept;\n")
	for _, forward := range forwards {
		if !forward.Enabled {
			continue
		}
		for _, protocol := range forward.Protocols {
			fmt.Fprintf(&output, "    %s counter masquerade comment \"egm_pf_%s_masquerade\"\n", nftMatch(forward, protocol, true, true), forward.ID)
		}
	}
	output.WriteString("  }\n}\n")
	return output.String()
}

func nftMatch(forward domain.PortForward, protocol domain.TransportProtocol, translated, includeSource bool) string {
	familyKeyword := "ip"
	if forward.ListenAddress.Is6() {
		familyKeyword = "ip6"
	}
	address := forward.ListenAddress
	ports := forward.ListenPorts
	if translated {
		address = forward.RemoteAddress
		if forward.RemotePortStart != 0 {
			width := uint32(forward.ListenPorts[0].To) - uint32(forward.ListenPorts[0].From)
			ports = []domain.PortRange{{From: forward.RemotePortStart, To: uint16(uint32(forward.RemotePortStart) + width)}}
		}
	}
	parts := []string{familyKeyword + " daddr " + address.String()}
	if translated {
		parts = append([]string{"ct original daddr " + forward.ListenAddress.String()}, parts...)
	}
	if includeSource && len(forward.SourceCIDRs) > 0 {
		parts = append(parts, familyKeyword+" saddr "+formatPrefixes(forward.SourceCIDRs))
	}
	if len(forward.AllPortsExcept) > 0 {
		parts = append(parts, string(protocol)+" dport != "+formatRanges(forward.AllPortsExcept))
	} else {
		parts = append(parts, string(protocol)+" dport "+formatRanges(ports))
	}
	return strings.Join(parts, " ")
}

func nftDestination(forward domain.PortForward) string {
	address := forward.RemoteAddress.String()
	if forward.RemotePortStart == 0 {
		return address
	}
	if forward.RemoteAddress.Is6() {
		address = "[" + address + "]"
	}
	width := uint32(forward.ListenPorts[0].To) - uint32(forward.ListenPorts[0].From)
	ports := domain.PortRange{From: forward.RemotePortStart, To: uint16(uint32(forward.RemotePortStart) + width)}
	return address + ":" + formatRange(ports)
}

func formatPrefixes(prefixes []netip.Prefix) string {
	if len(prefixes) == 1 {
		return prefixes[0].String()
	}
	values := make([]string, len(prefixes))
	for index, prefix := range prefixes {
		values[index] = prefix.String()
	}
	return "{ " + strings.Join(values, ", ") + " }"
}

func formatRanges(ranges []domain.PortRange) string {
	if len(ranges) == 1 {
		return formatRange(ranges[0])
	}
	values := make([]string, len(ranges))
	for index, portRange := range ranges {
		values[index] = formatRange(portRange)
	}
	return "{ " + strings.Join(values, ", ") + " }"
}

func formatRange(portRange domain.PortRange) string {
	if portRange.From == portRange.To {
		return strconv.Itoa(int(portRange.From))
	}
	return strconv.Itoa(int(portRange.From)) + "-" + strconv.Itoa(int(portRange.To))
}
