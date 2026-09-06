package domain

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

const MaximumRelaySourceCIDRs = 32

type RelayNetwork string

const (
	RelayTCP    RelayNetwork = "tcp"
	RelayUDP    RelayNetwork = "udp"
	RelayTCPUDP RelayNetwork = "tcp,udp"
)

var relayNetworks = []RelayNetwork{RelayTCP, RelayUDP, RelayTCPUDP}

func (network RelayNetwork) Validate() error {
	if !slices.Contains(relayNetworks, network) {
		return fmt.Errorf("unsupported relay network %q", network)
	}
	return nil
}

func (network RelayNetwork) RequiresTCP() bool {
	return network == RelayTCP || network == RelayTCPUDP
}

func (network RelayNetwork) RequiresUDP() bool {
	return network == RelayUDP || network == RelayTCPUDP
}

type Relay struct {
	ID            ID             `json:"id"`
	Name          string         `json:"name"`
	ListenAddress string         `json:"listen_address"`
	ListenPort    uint16         `json:"listen_port"`
	Network       RelayNetwork   `json:"network"`
	Destination   Endpoint       `json:"destination"`
	OutboundID    ID             `json:"outbound_id"`
	SourceCIDRs   []netip.Prefix `json:"source_cidrs,omitempty"`
	Enabled       bool           `json:"enabled"`
}

func (relay Relay) Validate() error {
	var errs []error
	address, err := netip.ParseAddr(relay.ListenAddress)
	if err != nil || !address.IsValid() || address.String() != relay.ListenAddress {
		errs = append(errs, fmt.Errorf("relay listen_address must be a canonical IP address"))
	}
	if relay.ListenPort == 0 {
		errs = append(errs, fmt.Errorf("relay listen_port must be between 1 and 65535"))
	}
	if len(relay.SourceCIDRs) > MaximumRelaySourceCIDRs {
		errs = append(errs, fmt.Errorf("relay must not contain more than %d source CIDRs", MaximumRelaySourceCIDRs))
	}
	seen := make(map[string]struct{}, len(relay.SourceCIDRs))
	for _, prefix := range relay.SourceCIDRs {
		if !prefix.IsValid() || prefix != prefix.Masked() {
			errs = append(errs, fmt.Errorf("relay source CIDRs must be canonical prefixes"))
			continue
		}
		key := prefix.String()
		if _, exists := seen[key]; exists {
			errs = append(errs, fmt.Errorf("duplicate relay source CIDR %q", key))
		}
		seen[key] = struct{}{}
	}
	if strings.TrimSpace(relay.ListenAddress) != relay.ListenAddress {
		errs = append(errs, fmt.Errorf("relay listen_address must not contain surrounding whitespace"))
	}
	return joinErrors(
		relay.ID.Validate("relay id"),
		validateDisplayName("relay name", relay.Name),
		relay.Network.Validate(),
		relay.Destination.Validate(),
		relay.OutboundID.Validate("relay outbound id"),
		errorsFrom(errs),
	)
}
