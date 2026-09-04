// Package interfaceoutbound implements native Linux interface outbound adapters.
package interfaceoutbound

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/egress-manager/egress-manager/internal/domain"
)

const (
	MaximumImportBytes = 128 << 10
	credentialVersion  = 1
)

var linuxInterfaceName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)

type ImportResult struct {
	Outbound           domain.Outbound `json:"outbound"`
	CredentialDocument []byte          `json:"-"`
}

type CredentialDocument struct {
	Version       int                 `json:"version"`
	Kind          domain.OutboundType `json:"kind"`
	InterfaceName string              `json:"interface_name"`
	WireGuard     *WireGuardProfile   `json:"wireguard,omitempty"`
	OpenVPN       *OpenVPNProfile     `json:"openvpn,omitempty"`
}

type WireGuardProfile struct {
	Addresses           []string        `json:"addresses"`
	MTU                 uint16          `json:"mtu,omitempty"`
	PrivateKey          string          `json:"private_key"`
	PeerPublicKey       string          `json:"peer_public_key"`
	PresharedKey        string          `json:"preshared_key,omitempty"`
	AllowedIPs          []string        `json:"allowed_ips"`
	Endpoint            domain.Endpoint `json:"endpoint"`
	PersistentKeepalive uint16          `json:"persistent_keepalive,omitempty"`
}

type OpenVPNProfile struct {
	Protocol string `json:"protocol"`
	Config   string `json:"config"`
}

func ParseImport(input string) (ImportResult, error) {
	if len(input) == 0 || len(input) > MaximumImportBytes {
		return ImportResult{}, fmt.Errorf("interface outbound import must be between 1 and %d bytes", MaximumImportBytes)
	}
	if hasUnsafeControl(input) {
		return ImportResult{}, fmt.Errorf("interface outbound import contains unsupported control characters")
	}
	trimmed := strings.TrimSpace(strings.TrimPrefix(input, "\ufeff"))
	if trimmed == "" {
		return ImportResult{}, fmt.Errorf("interface outbound import is empty")
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "[interface]") {
		return parseWireGuard(trimmed)
	}
	return parseOpenVPN(trimmed)
}

func DecodeCredential(document []byte) (CredentialDocument, error) {
	if len(document) == 0 || len(document) > MaximumImportBytes {
		return CredentialDocument{}, fmt.Errorf("interface outbound credential document has invalid size")
	}
	var decoded CredentialDocument
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return CredentialDocument{}, fmt.Errorf("decode interface outbound credential document")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return CredentialDocument{}, fmt.Errorf("decode interface outbound credential document: trailing data")
	}
	if decoded.Version != credentialVersion || !linuxInterfaceName.MatchString(decoded.InterfaceName) {
		return CredentialDocument{}, fmt.Errorf("unsupported interface outbound credential document")
	}
	switch decoded.Kind {
	case domain.OutboundWireGuard:
		if decoded.WireGuard == nil || decoded.OpenVPN != nil || validateWireGuardProfile(*decoded.WireGuard) != nil {
			return CredentialDocument{}, fmt.Errorf("invalid WireGuard credential document")
		}
	case domain.OutboundOpenVPN:
		if decoded.OpenVPN == nil || decoded.WireGuard != nil || decoded.OpenVPN.Config == "" || len(decoded.OpenVPN.Config) > MaximumImportBytes {
			return CredentialDocument{}, fmt.Errorf("invalid OpenVPN credential document")
		}
	default:
		return CredentialDocument{}, fmt.Errorf("unsupported interface outbound credential kind")
	}
	return decoded, nil
}

func InterfaceName(kind domain.OutboundType, id domain.ID) (string, error) {
	if err := id.Validate("outbound id"); err != nil {
		return "", err
	}
	prefix := ""
	switch kind {
	case domain.OutboundWireGuard:
		prefix = "egmwg"
	case domain.OutboundOpenVPN:
		prefix = "egmov"
	default:
		return "", fmt.Errorf("interface name requires a WireGuard or OpenVPN outbound")
	}
	digest := sha256.Sum256([]byte(kind + "\x00" + domain.OutboundType(id)))
	return prefix + hex.EncodeToString(digest[:5]), nil
}

func parseWireGuard(input string) (ImportResult, error) {
	section := ""
	peerCount := 0
	interfaceValues := map[string][]string{}
	peerValues := map[string][]string{}
	for _, raw := range strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n") {
		if len(raw) > 8192 {
			return ImportResult{}, fmt.Errorf("WireGuard import contains an oversized line")
		}
		line := strings.TrimSpace(stripINIComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			switch section {
			case "interface":
			case "peer":
				peerCount++
			default:
				return ImportResult{}, fmt.Errorf("WireGuard import contains an unsupported section")
			}
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || section == "" {
			return ImportResult{}, fmt.Errorf("WireGuard import contains an invalid directive")
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if value == "" {
			return ImportResult{}, fmt.Errorf("WireGuard import contains an empty directive")
		}
		values := interfaceValues
		allowed := map[string]bool{"privatekey": true, "address": true, "mtu": true}
		if section == "peer" {
			values = peerValues
			allowed = map[string]bool{"publickey": true, "presharedkey": true, "allowedips": true, "endpoint": true, "persistentkeepalive": true}
		}
		if !allowed[key] {
			return ImportResult{}, fmt.Errorf("WireGuard import contains an unsafe or unsupported directive")
		}
		if key != "address" && key != "allowedips" && len(values[key]) != 0 {
			return ImportResult{}, fmt.Errorf("WireGuard import contains a duplicate directive")
		}
		values[key] = append(values[key], value)
	}
	if peerCount != 1 {
		return ImportResult{}, fmt.Errorf("WireGuard import must contain exactly one peer")
	}
	privateKey, err := oneValue(interfaceValues, "privatekey")
	if err != nil || !validWGKey(privateKey) {
		return ImportResult{}, fmt.Errorf("WireGuard import requires a valid private key")
	}
	publicKey, err := oneValue(peerValues, "publickey")
	if err != nil || !validWGKey(publicKey) {
		return ImportResult{}, fmt.Errorf("WireGuard import requires a valid peer public key")
	}
	preshared := ""
	if values := peerValues["presharedkey"]; len(values) == 1 {
		preshared = values[0]
		if !validWGKey(preshared) {
			return ImportResult{}, fmt.Errorf("WireGuard import contains an invalid preshared key")
		}
	}
	addresses, err := parsePrefixes(interfaceValues["address"], "WireGuard interface address", false)
	if err != nil || len(addresses) == 0 || len(addresses) > 8 {
		return ImportResult{}, fmt.Errorf("WireGuard import requires between 1 and 8 valid interface addresses")
	}
	allowedIPs, err := parsePrefixes(peerValues["allowedips"], "WireGuard allowed IP", true)
	if err != nil || len(allowedIPs) == 0 || len(allowedIPs) > 32 {
		return ImportResult{}, fmt.Errorf("WireGuard import requires between 1 and 32 valid allowed IPs")
	}
	endpointText, err := oneValue(peerValues, "endpoint")
	if err != nil {
		return ImportResult{}, fmt.Errorf("WireGuard import requires one endpoint")
	}
	endpoint, err := parseEndpoint(endpointText)
	if err != nil {
		return ImportResult{}, fmt.Errorf("WireGuard import endpoint is invalid")
	}
	mtu, err := optionalUint16(interfaceValues["mtu"], 576, 9000)
	if err != nil {
		return ImportResult{}, fmt.Errorf("WireGuard import MTU is invalid")
	}
	keepalive, err := optionalUint16(peerValues["persistentkeepalive"], 0, 65535)
	if err != nil {
		return ImportResult{}, fmt.Errorf("WireGuard import persistent keepalive is invalid")
	}
	id := nativeID(domain.OutboundWireGuard, endpoint)
	interfaceName, _ := InterfaceName(domain.OutboundWireGuard, id)
	profile := WireGuardProfile{Addresses: addresses, MTU: mtu, PrivateKey: privateKey, PeerPublicKey: publicKey, PresharedKey: preshared, AllowedIPs: allowedIPs, Endpoint: endpoint, PersistentKeepalive: keepalive}
	outbound := domain.Outbound{ID: id, Name: "WireGuard " + endpoint.Host, Adapter: domain.OutboundAdapterInterface, Type: domain.OutboundWireGuard, Server: endpoint, Capabilities: domain.Capabilities{TCP: true, UDP: true}, Health: domain.UnknownOutboundHealth(), Enabled: true, SecretMetadata: []string{"private_key"}}
	if preshared != "" {
		outbound.SecretMetadata = append(outbound.SecretMetadata, "preshared_key")
	}
	return finalize(outbound, CredentialDocument{Version: credentialVersion, Kind: domain.OutboundWireGuard, InterfaceName: interfaceName, WireGuard: &profile})
}

func parseOpenVPN(input string) (ImportResult, error) {
	lines := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	blocks := map[string]string{}
	directives := map[string]int{}
	preserved := []string{}
	activeBlock := ""
	blockLines := []string{}
	var endpoint domain.Endpoint
	protocol := ""
	for _, raw := range lines {
		if len(raw) > 8192 {
			return ImportResult{}, fmt.Errorf("OpenVPN import contains an oversized line")
		}
		trimmed := strings.TrimSpace(raw)
		if activeBlock != "" {
			if strings.EqualFold(trimmed, "</"+activeBlock+">") {
				content := strings.TrimSpace(strings.Join(blockLines, "\n"))
				if content == "" {
					return ImportResult{}, fmt.Errorf("OpenVPN import contains an empty inline block")
				}
				blocks[activeBlock] = content
				activeBlock = ""
				blockLines = nil
				continue
			}
			if strings.HasPrefix(trimmed, "<") {
				return ImportResult{}, fmt.Errorf("OpenVPN import contains nested or mismatched inline blocks")
			}
			blockLines = append(blockLines, raw)
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "<") && strings.HasSuffix(trimmed, ">") && !strings.HasPrefix(trimmed, "</") {
			name := strings.ToLower(strings.TrimSpace(trimmed[1 : len(trimmed)-1]))
			if !allowedOpenVPNBlock(name) || blocks[name] != "" {
				return ImportResult{}, fmt.Errorf("OpenVPN import contains an unsupported or duplicate inline block")
			}
			activeBlock = name
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "--") {
			return ImportResult{}, fmt.Errorf("OpenVPN import contains an invalid directive")
		}
		name := strings.ToLower(fields[0])
		directives[name]++
		switch name {
		case "client":
			if len(fields) != 1 || directives[name] != 1 {
				return ImportResult{}, fmt.Errorf("OpenVPN import contains an invalid client directive")
			}
		case "dev":
			if len(fields) != 2 || (fields[1] != "tun" && !regexp.MustCompile(`^tun[0-9]+$`).MatchString(fields[1])) || directives[name] != 1 {
				return ImportResult{}, fmt.Errorf("OpenVPN import must use one TUN device")
			}
		case "dev-type":
			if len(fields) != 2 || strings.ToLower(fields[1]) != "tun" || directives[name] != 1 {
				return ImportResult{}, fmt.Errorf("OpenVPN import must use TUN mode")
			}
		case "proto":
			if len(fields) != 2 || directives[name] != 1 {
				return ImportResult{}, fmt.Errorf("OpenVPN import requires one protocol")
			}
			protocol = normalizeOpenVPNProtocol(fields[1])
			if protocol == "" {
				return ImportResult{}, fmt.Errorf("OpenVPN import protocol is unsupported")
			}
		case "remote":
			if len(fields) != 3 || directives[name] != 1 {
				return ImportResult{}, fmt.Errorf("OpenVPN import requires exactly one remote with an explicit port")
			}
			port, parseErr := strconv.ParseUint(fields[2], 10, 16)
			if parseErr != nil || port == 0 {
				return ImportResult{}, fmt.Errorf("OpenVPN import remote is invalid")
			}
			endpoint = domain.Endpoint{Host: normalizeHost(fields[1]), Port: uint16(port)}
		case "ca", "cert", "key", "tls-auth", "tls-crypt", "auth-user-pass":
			if len(fields) > 2 || len(fields) == 2 && fields[1] != "[inline]" {
				return ImportResult{}, fmt.Errorf("OpenVPN import external credential references are unsupported")
			}
		case "nobind", "route-nopull":
			if len(fields) != 1 {
				return ImportResult{}, fmt.Errorf("OpenVPN import contains an invalid lifecycle directive")
			}
		case "auth", "auth-nocache", "cipher", "data-ciphers", "data-ciphers-fallback", "tls-version-min", "tls-version-max", "tls-cipher", "tls-ciphersuites", "remote-cert-tls", "verify-x509-name", "connect-retry", "connect-retry-max", "connect-timeout", "resolv-retry", "persist-key", "persist-tun", "explicit-exit-notify", "keepalive", "ping", "ping-restart", "ping-exit", "reneg-sec", "hand-window", "tran-window", "sndbuf", "rcvbuf", "fast-io", "mssfix", "tun-mtu", "key-direction", "verb", "mute":
			if !safeOpenVPNDirective(name, fields) {
				return ImportResult{}, fmt.Errorf("OpenVPN import contains an invalid safe directive")
			}
			preserved = append(preserved, trimmed)
		default:
			return ImportResult{}, fmt.Errorf("OpenVPN import contains an unsafe or unsupported directive")
		}
	}
	if activeBlock != "" {
		return ImportResult{}, fmt.Errorf("OpenVPN import contains an unterminated inline block")
	}
	if directives["client"] != 1 || directives["dev"] != 1 || directives["proto"] != 1 || directives["remote"] != 1 || endpoint.Host == "" || protocol == "" {
		return ImportResult{}, fmt.Errorf("OpenVPN import requires client, dev tun, proto, and one remote")
	}
	if blocks["ca"] == "" || (blocks["cert"] == "") != (blocks["key"] == "") || blocks["cert"] == "" && blocks["auth-user-pass"] == "" {
		return ImportResult{}, fmt.Errorf("OpenVPN import requires inline CA and inline certificate/key or user credentials")
	}
	if directives["auth-user-pass"] > 0 && blocks["auth-user-pass"] == "" {
		return ImportResult{}, fmt.Errorf("OpenVPN import requires inline user credentials")
	}
	if directives["tls-auth"] > 0 && blocks["tls-auth"] == "" || directives["tls-crypt"] > 0 && blocks["tls-crypt"] == "" {
		return ImportResult{}, fmt.Errorf("OpenVPN import requires inline TLS key material")
	}
	id := nativeID(domain.OutboundOpenVPN, endpoint)
	interfaceName, _ := InterfaceName(domain.OutboundOpenVPN, id)
	normalized := []string{"client", "dev " + interfaceName, "dev-type tun", "nobind", "route-nopull", "proto " + protocol, "remote " + endpoint.Host + " " + strconv.Itoa(int(endpoint.Port))}
	normalized = append(normalized, preserved...)
	blockNames := make([]string, 0, len(blocks))
	for name := range blocks {
		blockNames = append(blockNames, name)
	}
	sort.Strings(blockNames)
	for _, name := range blockNames {
		normalized = append(normalized, "<"+name+">", blocks[name], "</"+name+">")
	}
	config := strings.Join(normalized, "\n") + "\n"
	outbound := domain.Outbound{ID: id, Name: "OpenVPN " + endpoint.Host, Adapter: domain.OutboundAdapterInterface, Type: domain.OutboundOpenVPN, Server: endpoint, Capabilities: domain.Capabilities{TCP: true, UDP: true}, Health: domain.UnknownOutboundHealth(), Enabled: true, SecretMetadata: []string{"profile"}}
	return finalize(outbound, CredentialDocument{Version: credentialVersion, Kind: domain.OutboundOpenVPN, InterfaceName: interfaceName, OpenVPN: &OpenVPNProfile{Protocol: protocol, Config: config}})
}

func finalize(outbound domain.Outbound, credential CredentialDocument) (ImportResult, error) {
	if err := outbound.Validate(); err != nil {
		return ImportResult{}, fmt.Errorf("imported interface outbound metadata is invalid")
	}
	document, err := json.Marshal(credential)
	if err != nil || len(document) > MaximumImportBytes {
		return ImportResult{}, fmt.Errorf("imported interface outbound credential is invalid")
	}
	if _, err := DecodeCredential(document); err != nil {
		return ImportResult{}, err
	}
	return ImportResult{Outbound: outbound, CredentialDocument: document}, nil
}

func nativeID(kind domain.OutboundType, endpoint domain.Endpoint) domain.ID {
	digest := sha256.Sum256([]byte(string(kind) + "\x00" + endpoint.String()))
	return domain.ID(string(kind) + "_" + hex.EncodeToString(digest[:6]))
}

func parseEndpoint(value string) (domain.Endpoint, error) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		return domain.Endpoint{}, err
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return domain.Endpoint{}, fmt.Errorf("invalid port")
	}
	endpoint := domain.Endpoint{Host: normalizeHost(host), Port: uint16(port)}
	probe := domain.Outbound{ID: "endpoint_probe", Name: "Endpoint probe", Adapter: domain.OutboundAdapterInterface, Type: domain.OutboundWireGuard, Server: endpoint, Capabilities: domain.Capabilities{TCP: true}, Health: domain.UnknownOutboundHealth(), SecretMetadata: []string{"private_key"}}
	if err := probe.Validate(); err != nil {
		return domain.Endpoint{}, err
	}
	return endpoint, nil
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if address, err := netip.ParseAddr(host); err == nil {
		return address.String()
	}
	return strings.ToLower(host)
}

func parsePrefixes(values []string, field string, allowUnspecified bool) ([]string, error) {
	result := []string{}
	seen := map[string]struct{}{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
			if err != nil || !prefix.IsValid() || !allowUnspecified && prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() {
				return nil, fmt.Errorf("%s is invalid", field)
			}
			canonical := prefix.String()
			if _, exists := seen[canonical]; !exists {
				seen[canonical] = struct{}{}
				result = append(result, canonical)
			}
		}
	}
	sort.Strings(result)
	return result, nil
}

func oneValue(values map[string][]string, key string) (string, error) {
	if len(values[key]) != 1 {
		return "", fmt.Errorf("one value required")
	}
	return values[key][0], nil
}

func optionalUint16(values []string, minimum, maximum uint64) (uint16, error) {
	if len(values) == 0 {
		return 0, nil
	}
	if len(values) != 1 {
		return 0, fmt.Errorf("one value required")
	}
	value, err := strconv.ParseUint(values[0], 10, 16)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("value is out of range")
	}
	return uint16(value), nil
}

func validWGKey(value string) bool {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	return err == nil && len(decoded) == 32
}

func validateWireGuardProfile(profile WireGuardProfile) error {
	if !validWGKey(profile.PrivateKey) || !validWGKey(profile.PeerPublicKey) {
		return fmt.Errorf("invalid WireGuard keys")
	}
	if profile.PresharedKey != "" && !validWGKey(profile.PresharedKey) {
		return fmt.Errorf("invalid WireGuard preshared key")
	}
	if len(profile.Addresses) == 0 || len(profile.AllowedIPs) == 0 || profile.Endpoint.Host == "" || profile.Endpoint.Port == 0 {
		return fmt.Errorf("incomplete WireGuard profile")
	}
	return nil
}

func stripINIComment(line string) string {
	if index := strings.IndexAny(line, "#;"); index >= 0 {
		return line[:index]
	}
	return line
}

func allowedOpenVPNBlock(name string) bool {
	switch name {
	case "ca", "cert", "key", "tls-auth", "tls-crypt", "auth-user-pass":
		return true
	default:
		return false
	}
}

func normalizeOpenVPNProtocol(value string) string {
	switch strings.ToLower(value) {
	case "udp", "udp4", "udp6", "tcp-client", "tcp4-client", "tcp6-client":
		return strings.ToLower(value)
	case "tcp":
		return "tcp-client"
	default:
		return ""
	}
}

func safeOpenVPNDirective(name string, fields []string) bool {
	if len(fields) == 0 || len(fields) > 8 {
		return false
	}
	for _, field := range fields[1:] {
		if len(field) > 512 || strings.ContainsAny(field, "\r\n\x00") {
			return false
		}
	}
	if name == "verb" {
		if len(fields) != 2 {
			return false
		}
		value, err := strconv.Atoi(fields[1])
		return err == nil && value >= 0 && value <= 4
	}
	if name == "key-direction" {
		return len(fields) == 2 && (fields[1] == "0" || fields[1] == "1")
	}
	return true
}

func hasUnsafeControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t' {
			return true
		}
	}
	return false
}
