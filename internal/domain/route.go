package domain

import (
	"fmt"
	"net/netip"
)

type RouteSourceKind string

const (
	RouteSourceInterface   RouteSourceKind = "interface"
	RouteSourceSubnet      RouteSourceKind = "subnet"
	RouteSourceListener    RouteSourceKind = "listener"
	RouteSourceXrayInbound RouteSourceKind = "xray_inbound"
)

type RouteSource struct {
	Kind      RouteSourceKind `json:"kind"`
	Interface string          `json:"interface,omitempty"`
	Subnet    netip.Prefix    `json:"subnet,omitempty"`
	Listener  uint16          `json:"listener,omitempty"`
	XrayTag   string          `json:"xray_tag,omitempty"`
}

func (source RouteSource) Validate() error {
	unexpected := func(interfaceSet, subnetSet, listenerSet, xraySet bool) error {
		if interfaceSet || subnetSet || listenerSet || xraySet {
			return fmt.Errorf("route source contains fields for another source kind")
		}
		return nil
	}

	switch source.Kind {
	case RouteSourceInterface:
		return joinErrors(
			validateLinuxName("source interface", source.Interface),
			unexpected(false, source.Subnet.IsValid(), source.Listener != 0, source.XrayTag != ""),
		)
	case RouteSourceSubnet:
		if !source.Subnet.IsValid() || source.Subnet != source.Subnet.Masked() {
			return fmt.Errorf("source subnet must be a canonical CIDR")
		}
		return unexpected(source.Interface != "", false, source.Listener != 0, source.XrayTag != "")
	case RouteSourceListener:
		if source.Listener == 0 {
			return fmt.Errorf("source listener port must be between 1 and 65535")
		}
		return unexpected(source.Interface != "", source.Subnet.IsValid(), false, source.XrayTag != "")
	case RouteSourceXrayInbound:
		return joinErrors(
			validateConfigName("Xray inbound tag", source.XrayTag),
			unexpected(source.Interface != "", source.Subnet.IsValid(), source.Listener != 0, false),
		)
	default:
		return fmt.Errorf("unsupported route source kind %q", source.Kind)
	}
}

type FailurePolicy string

const (
	FailureBlock    FailurePolicy = "block"
	FailureFailover FailurePolicy = "failover"
	FailureDirect   FailurePolicy = "direct"
)

func (policy FailurePolicy) Validate() error {
	switch policy {
	case FailureBlock, FailureFailover, FailureDirect:
		return nil
	default:
		return fmt.Errorf("unsupported failure policy %q", policy)
	}
}

type DNSPolicy string

const (
	DNSFollowOutbound DNSPolicy = "follow_outbound"
	DNSSystem         DNSPolicy = "system"
	DNSBlock          DNSPolicy = "block"
)

func (policy DNSPolicy) Validate() error {
	switch policy {
	case DNSFollowOutbound, DNSSystem, DNSBlock:
		return nil
	default:
		return fmt.Errorf("unsupported DNS policy %q", policy)
	}
}

type IPv4Policy string

const (
	IPv4FollowOutbound IPv4Policy = "follow_outbound"
	IPv4Block          IPv4Policy = "block"
	IPv4Direct         IPv4Policy = "direct"
)

func (policy IPv4Policy) Validate() error {
	switch policy {
	case IPv4FollowOutbound, IPv4Block, IPv4Direct:
		return nil
	default:
		return fmt.Errorf("unsupported IPv4 policy %q", policy)
	}
}

type IPv6Policy string

const (
	IPv6FollowOutbound IPv6Policy = "follow_outbound"
	IPv6Block          IPv6Policy = "block"
	IPv6Direct         IPv6Policy = "direct"
)

func (policy IPv6Policy) Validate() error {
	switch policy {
	case IPv6FollowOutbound, IPv6Block, IPv6Direct:
		return nil
	default:
		return fmt.Errorf("unsupported IPv6 policy %q", policy)
	}
}

type Route struct {
	ID                 ID            `json:"id"`
	Name               string        `json:"name"`
	Source             RouteSource   `json:"source"`
	OutboundID         ID            `json:"outbound_id"`
	FallbackOutboundID ID            `json:"fallback_outbound_id,omitempty"`
	FailurePolicy      FailurePolicy `json:"failure_policy"`
	DNSPolicy          DNSPolicy     `json:"dns_policy"`
	DNSServers         []netip.Addr  `json:"dns_servers,omitempty"`
	IPv4Policy         IPv4Policy    `json:"ipv4_policy"`
	IPv6Policy         IPv6Policy    `json:"ipv6_policy"`
	KillSwitch         bool          `json:"kill_switch"`
	MTU                uint16        `json:"mtu,omitempty"`
	TCPMSS             uint16        `json:"tcp_mss,omitempty"`
	Enabled            bool          `json:"enabled"`
}

func (route Route) Validate() error {
	var fallbackError error
	if route.FailurePolicy == FailureFailover {
		if err := route.FallbackOutboundID.Validate("fallback outbound id"); err != nil {
			fallbackError = err
		} else if route.FallbackOutboundID == route.OutboundID {
			fallbackError = fmt.Errorf("fallback outbound must differ from primary outbound")
		}
	} else if route.FallbackOutboundID != "" {
		fallbackError = fmt.Errorf("fallback outbound is allowed only with failover policy")
	}
	var mtuError error
	if route.MTU != 0 && (route.MTU < 576 || route.MTU > 9000) {
		mtuError = fmt.Errorf("route MTU must be zero or between 576 and 9000")
	}
	var mssError error
	if route.TCPMSS != 0 {
		if route.TCPMSS < 536 || route.TCPMSS > 8960 {
			mssError = fmt.Errorf("route TCP MSS must be zero or between 536 and 8960")
		} else if route.MTU != 0 && route.TCPMSS >= route.MTU {
			mssError = fmt.Errorf("route TCP MSS must be smaller than MTU")
		}
	}
	var killSwitchError error
	if route.FailurePolicy == FailureDirect && route.KillSwitch {
		killSwitchError = fmt.Errorf("direct failure policy cannot enable the kill switch")
	}
	if (route.FailurePolicy == FailureBlock || route.FailurePolicy == FailureFailover) && !route.KillSwitch {
		killSwitchError = fmt.Errorf("block and failover policies require the kill switch")
	}
	var dnsServersError error
	if route.DNSPolicy == DNSFollowOutbound {
		if len(route.DNSServers) < 1 || len(route.DNSServers) > 4 {
			dnsServersError = fmt.Errorf("follow-outbound DNS policy requires between 1 and 4 explicit DNS servers")
		} else {
			seen := make(map[netip.Addr]struct{}, len(route.DNSServers))
			for _, address := range route.DNSServers {
				canonical := address.Unmap()
				if !address.IsValid() || address != canonical || address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() || address.IsLinkLocalUnicast() {
					dnsServersError = fmt.Errorf("DNS servers must be canonical unicast addresses")
					break
				}
				if _, exists := seen[address]; exists {
					dnsServersError = fmt.Errorf("DNS servers must not contain duplicates")
					break
				}
				seen[address] = struct{}{}
			}
		}
	} else if len(route.DNSServers) != 0 {
		dnsServersError = fmt.Errorf("explicit DNS servers are allowed only with follow-outbound DNS policy")
	}
	return joinErrors(
		route.ID.Validate("route id"),
		validateDisplayName("route name", route.Name),
		route.Source.Validate(),
		route.OutboundID.Validate("outbound id"),
		route.FailurePolicy.Validate(),
		fallbackError,
		route.DNSPolicy.Validate(),
		dnsServersError,
		route.IPv4Policy.Validate(),
		route.IPv6Policy.Validate(),
		mtuError,
		mssError,
		killSwitchError,
	)
}
