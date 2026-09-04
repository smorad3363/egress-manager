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
	ID            ID            `json:"id"`
	Name          string        `json:"name"`
	Source        RouteSource   `json:"source"`
	OutboundID    ID            `json:"outbound_id"`
	FailurePolicy FailurePolicy `json:"failure_policy"`
	DNSPolicy     DNSPolicy     `json:"dns_policy"`
	IPv6Policy    IPv6Policy    `json:"ipv6_policy"`
	Enabled       bool          `json:"enabled"`
}

func (route Route) Validate() error {
	return joinErrors(
		route.ID.Validate("route id"),
		validateDisplayName("route name", route.Name),
		route.Source.Validate(),
		route.OutboundID.Validate("outbound id"),
		route.FailurePolicy.Validate(),
		route.DNSPolicy.Validate(),
		route.IPv6Policy.Validate(),
	)
}
