package domain

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
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
	OutboundOpenVPN     OutboundType = "openvpn"
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
	OutboundOpenVPN,
}

type OutboundAdapter string

const (
	OutboundAdapterSingBox   OutboundAdapter = "sing-box"
	OutboundAdapterInterface OutboundAdapter = "interface"
)

func (adapter OutboundAdapter) Validate() error {
	if adapter != OutboundAdapterSingBox && adapter != OutboundAdapterInterface {
		return fmt.Errorf("unsupported outbound adapter %q", adapter)
	}
	return nil
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

type ProbeStatus string

const (
	ProbeUnknown     ProbeStatus = "unknown"
	ProbePassed      ProbeStatus = "passed"
	ProbeFailed      ProbeStatus = "failed"
	ProbeUnsupported ProbeStatus = "unsupported"
	ProbeUntestable  ProbeStatus = "untestable"
)

func (status ProbeStatus) Validate() error {
	switch status {
	case ProbeUnknown, ProbePassed, ProbeFailed, ProbeUnsupported, ProbeUntestable:
		return nil
	default:
		return fmt.Errorf("unsupported probe status %q", status)
	}
}

type OutboundHealth struct {
	Status             HealthStatus  `json:"status"`
	CheckedAt          *time.Time    `json:"checked_at,omitempty"`
	ConfigurationValid ProbeStatus   `json:"configuration_valid"`
	TransportReachable ProbeStatus   `json:"transport_reachable"`
	InternetReachable  ProbeStatus   `json:"internet_reachable"`
	ExternalIP         string        `json:"external_ip,omitempty"`
	TCP                ProbeStatus   `json:"tcp"`
	UDP                ProbeStatus   `json:"udp"`
	Latency            time.Duration `json:"latency,omitempty"`
	Detail             string        `json:"detail,omitempty"`
}

func UnknownOutboundHealth() OutboundHealth {
	return OutboundHealth{
		Status:             HealthUnknown,
		ConfigurationValid: ProbeUnknown,
		TransportReachable: ProbeUnknown,
		InternetReachable:  ProbeUnknown,
		TCP:                ProbeUnknown,
		UDP:                ProbeUnknown,
	}
}

func (health OutboundHealth) Validate() error {
	var externalIPError error
	if health.ExternalIP != "" {
		if address, err := netip.ParseAddr(health.ExternalIP); err != nil || address.String() != health.ExternalIP {
			externalIPError = fmt.Errorf("external IP must be a canonical IP address")
		}
	}
	if health.Latency < 0 || health.Latency > 24*time.Hour {
		return fmt.Errorf("outbound health latency must be between zero and 24h")
	}
	if len(health.Detail) > 256 {
		return fmt.Errorf("outbound health detail must not exceed 256 bytes")
	}
	return joinErrors(
		health.Status.Validate(),
		health.ConfigurationValid.Validate(),
		health.TransportReachable.Validate(),
		health.InternetReachable.Validate(),
		health.TCP.Validate(),
		health.UDP.Validate(),
		externalIPError,
	)
}

type Outbound struct {
	ID             ID              `json:"id"`
	Name           string          `json:"name"`
	Adapter        OutboundAdapter `json:"adapter"`
	Type           OutboundType    `json:"type"`
	Server         Endpoint        `json:"server"`
	Capabilities   Capabilities    `json:"capabilities"`
	Health         OutboundHealth  `json:"health"`
	Enabled        bool            `json:"enabled"`
	SecretMetadata []string        `json:"secret_metadata,omitempty"`
}

func (outbound Outbound) Validate() error {
	var secretErrors []error
	if len(outbound.SecretMetadata) > 32 {
		secretErrors = append(secretErrors, fmt.Errorf("outbound must not contain more than 32 secret metadata entries"))
	}
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
	for _, required := range requiredOutboundSecrets(outbound.Type) {
		if _, exists := seen[required]; !exists {
			secretErrors = append(secretErrors, fmt.Errorf("%s outbound requires %s secret metadata", outbound.Type, required))
		}
	}

	var capabilitiesError error
	if !outbound.Capabilities.TCP && !outbound.Capabilities.UDP {
		capabilitiesError = fmt.Errorf("outbound must support TCP, UDP, or both")
	}
	var adapterTypeError error
	switch outbound.Adapter {
	case OutboundAdapterSingBox:
		if outbound.Type == OutboundOpenVPN {
			adapterTypeError = fmt.Errorf("openvpn outbound requires the interface adapter")
		}
	case OutboundAdapterInterface:
		if outbound.Type != OutboundWireGuard && outbound.Type != OutboundOpenVPN {
			adapterTypeError = fmt.Errorf("interface adapter supports only wireguard and openvpn outbounds")
		}
	}

	return joinErrors(
		outbound.ID.Validate("outbound id"),
		validateDisplayName("outbound name", outbound.Name),
		outbound.Adapter.Validate(),
		outbound.Type.Validate(),
		outbound.Server.Validate(),
		outbound.Health.Validate(),
		capabilitiesError,
		adapterTypeError,
		errorsFrom(secretErrors),
	)
}

func requiredOutboundSecrets(kind OutboundType) []string {
	switch kind {
	case OutboundVLESS, OutboundVMess:
		return []string{"uuid"}
	case OutboundTrojan, OutboundShadowsocks, OutboundHysteria2:
		return []string{"password"}
	case OutboundTUIC:
		return []string{"uuid", "password"}
	case OutboundWireGuard:
		return []string{"private_key"}
	case OutboundOpenVPN:
		return []string{"profile"}
	default:
		return nil
	}
}

func errorsFrom(errs []error) error {
	return joinErrors(errs...)
}
