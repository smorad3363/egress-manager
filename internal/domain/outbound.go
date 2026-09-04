package domain

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strings"
)

var hostnamePattern = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*)$`)

type OutboundType string

const (
	OutboundVLESS       OutboundType = "vless"
	OutboundTrojan      OutboundType = "trojan"
	OutboundShadowsocks OutboundType = "shadowsocks"
	OutboundVMess       OutboundType = "vmess"
	OutboundHysteria2   OutboundType = "hysteria2"
	OutboundTUIC        OutboundType = "tuic"
	OutboundSOCKS5      OutboundType = "socks5"
	OutboundWireGuard   OutboundType = "wireguard"
)

var outboundTypes = []OutboundType{
	OutboundVLESS,
	OutboundTrojan,
	OutboundShadowsocks,
	OutboundVMess,
	OutboundHysteria2,
	OutboundTUIC,
	OutboundSOCKS5,
	OutboundWireGuard,
}

func (kind OutboundType) Validate() error {
	if !slices.Contains(outboundTypes, kind) {
		return fmt.Errorf("unsupported outbound type %q", kind)
	}
	return nil
}

type Endpoint struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
}

func (endpoint Endpoint) Validate() error {
	host := strings.TrimSpace(endpoint.Host)
	if host == "" || host != endpoint.Host || len(host) > 253 {
		return fmt.Errorf("endpoint host is invalid")
	}
	if _, err := netip.ParseAddr(host); err != nil && !hostnamePattern.MatchString(host) {
		return fmt.Errorf("endpoint host is not an IP address or hostname")
	}
	if endpoint.Port == 0 {
		return fmt.Errorf("endpoint port must be between 1 and 65535")
	}
	return nil
}

func (endpoint Endpoint) String() string {
	return net.JoinHostPort(endpoint.Host, fmt.Sprint(endpoint.Port))
}

type Capabilities struct {
	TCP bool `json:"tcp"`
	UDP bool `json:"udp"`
}

type Outbound struct {
	ID             ID           `json:"id"`
	Name           string       `json:"name"`
	Type           OutboundType `json:"type"`
	Server         Endpoint     `json:"server"`
	Capabilities   Capabilities `json:"capabilities"`
	Health         Health       `json:"health"`
	Enabled        bool         `json:"enabled"`
	SecretMetadata []string     `json:"secret_metadata,omitempty"`
}

func (outbound Outbound) Validate() error {
	var secretErrors []error
	seen := make(map[string]struct{}, len(outbound.SecretMetadata))
	for _, key := range outbound.SecretMetadata {
		if err := validateConfigName("secret metadata", key); err != nil {
			secretErrors = append(secretErrors, err)
		}
		if _, exists := seen[key]; exists {
			secretErrors = append(secretErrors, fmt.Errorf("duplicate secret metadata %q", key))
		}
		seen[key] = struct{}{}
	}

	var capabilitiesError error
	if !outbound.Capabilities.TCP && !outbound.Capabilities.UDP {
		capabilitiesError = fmt.Errorf("outbound must support TCP, UDP, or both")
	}

	return joinErrors(
		outbound.ID.Validate("outbound id"),
		validateDisplayName("outbound name", outbound.Name),
		outbound.Type.Validate(),
		outbound.Server.Validate(),
		outbound.Health.Validate(),
		capabilitiesError,
		errorsFrom(secretErrors),
	)
}

func errorsFrom(errs []error) error {
	return joinErrors(errs...)
}
