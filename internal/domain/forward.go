package domain

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

type TransportProtocol string

const (
	ProtocolTCP TransportProtocol = "tcp"
	ProtocolUDP TransportProtocol = "udp"
)

func (protocol TransportProtocol) Validate() error {
	if protocol != ProtocolTCP && protocol != ProtocolUDP {
		return fmt.Errorf("unsupported transport protocol %q", protocol)
	}
	return nil
}

type PortRange struct {
	From uint16 `json:"from"`
	To   uint16 `json:"to"`
}

func (portRange PortRange) Validate() error {
	if portRange.From == 0 || portRange.To == 0 || portRange.From > portRange.To {
		return fmt.Errorf("port range must satisfy 1 <= from <= to <= 65535")
	}
	return nil
}

func (portRange PortRange) Contains(port uint16) bool {
	return port >= portRange.From && port <= portRange.To
}

type PortForward struct {
	ID              ID                  `json:"id"`
	Name            string              `json:"name"`
	Protocols       []TransportProtocol `json:"protocols"`
	ListenAddress   netip.Addr          `json:"listen_address"`
	ListenPorts     []PortRange         `json:"listen_ports,omitempty"`
	AllPortsExcept  []PortRange         `json:"all_ports_except,omitempty"`
	RemoteAddress   netip.Addr          `json:"remote_address"`
	RemotePortStart uint16              `json:"remote_port_start,omitempty"`
	SourceCIDRs     []netip.Prefix      `json:"source_cidrs,omitempty"`
	Enabled         bool                `json:"enabled"`
}

func (forward PortForward) Validate() error {
	errs := []error{
		forward.ID.Validate("port forward id"),
		validateDisplayName("port forward name", forward.Name),
	}

	if len(forward.Protocols) == 0 {
		errs = append(errs, fmt.Errorf("at least one protocol is required"))
	}
	for index, protocol := range forward.Protocols {
		errs = append(errs, protocol.Validate())
		if slices.Contains(forward.Protocols[:index], protocol) {
			errs = append(errs, fmt.Errorf("duplicate protocol %q", protocol))
		}
	}

	if !forward.ListenAddress.IsValid() || forward.ListenAddress.IsUnspecified() {
		errs = append(errs, fmt.Errorf("listen address must be a specific valid IP address"))
	}
	if !forward.RemoteAddress.IsValid() || forward.RemoteAddress.IsUnspecified() {
		errs = append(errs, fmt.Errorf("remote address must be a specific valid IP address"))
	}
	if forward.ListenAddress.IsValid() && forward.RemoteAddress.IsValid() && forward.ListenAddress.BitLen() != forward.RemoteAddress.BitLen() {
		errs = append(errs, fmt.Errorf("listen and remote address families must match"))
	}

	if len(forward.ListenPorts) == 0 && len(forward.AllPortsExcept) == 0 {
		errs = append(errs, fmt.Errorf("listen ports or all-ports-except mode is required"))
	}
	if len(forward.ListenPorts) > 0 && len(forward.AllPortsExcept) > 0 {
		errs = append(errs, fmt.Errorf("listen ports and all-ports-except mode are mutually exclusive"))
	}
	for _, portRange := range append(slices.Clone(forward.ListenPorts), forward.AllPortsExcept...) {
		errs = append(errs, portRange.Validate())
	}

	if forward.RemotePortStart != 0 && len(forward.ListenPorts) != 1 {
		errs = append(errs, fmt.Errorf("port remapping requires exactly one listen range"))
	}
	if forward.RemotePortStart != 0 && len(forward.ListenPorts) == 1 {
		width := uint32(forward.ListenPorts[0].To) - uint32(forward.ListenPorts[0].From)
		if uint32(forward.RemotePortStart)+width > 65535 {
			errs = append(errs, fmt.Errorf("remapped port range exceeds 65535"))
		}
	}

	for _, prefix := range forward.SourceCIDRs {
		if !prefix.IsValid() || prefix != prefix.Masked() {
			errs = append(errs, fmt.Errorf("source CIDR %q is not canonical", prefix))
		}
		if prefix.IsValid() && forward.ListenAddress.IsValid() && prefix.Addr().BitLen() != forward.ListenAddress.BitLen() {
			errs = append(errs, fmt.Errorf("source CIDR %q does not match listen address family", prefix))
		}
	}

	return errorsFrom(errs)
}

type FirewallAction string

const (
	FirewallAccept FirewallAction = "accept"
	FirewallDrop   FirewallAction = "drop"
	FirewallReject FirewallAction = "reject"
)

type FirewallRule struct {
	ID               ID                `json:"id"`
	Table            string            `json:"table"`
	Chain            string            `json:"chain"`
	Protocol         TransportProtocol `json:"protocol"`
	SourceCIDR       *netip.Prefix     `json:"source_cidr,omitempty"`
	DestinationPorts []PortRange       `json:"destination_ports,omitempty"`
	Action           FirewallAction    `json:"action"`
	Comment          string            `json:"comment,omitempty"`
	Enabled          bool              `json:"enabled"`
}

func (rule FirewallRule) Validate() error {
	errs := []error{
		rule.ID.Validate("firewall rule id"),
		validateConfigName("firewall table", rule.Table),
		validateConfigName("firewall chain", rule.Chain),
		rule.Protocol.Validate(),
	}
	if !strings.HasPrefix(strings.ToLower(rule.Table), "egm_") || !strings.HasPrefix(strings.ToLower(rule.Chain), "egm_") {
		errs = append(errs, fmt.Errorf("firewall table and chain must use the egm_ ownership prefix"))
	}
	if rule.Action != FirewallAccept && rule.Action != FirewallDrop && rule.Action != FirewallReject {
		errs = append(errs, fmt.Errorf("unsupported firewall action %q", rule.Action))
	}
	if rule.SourceCIDR != nil && (!rule.SourceCIDR.IsValid() || *rule.SourceCIDR != rule.SourceCIDR.Masked()) {
		errs = append(errs, fmt.Errorf("firewall source CIDR must be canonical"))
	}
	for _, portRange := range rule.DestinationPorts {
		errs = append(errs, portRange.Validate())
	}
	if len(rule.Comment) > 128 {
		errs = append(errs, fmt.Errorf("firewall comment must not exceed 128 bytes"))
	}
	return errorsFrom(errs)
}
