// Package singbox implements the sing-box outbound adapter.
package singbox

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
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/egress-manager/egress-manager/internal/domain"
)

const (
	MaximumImportBytes   = 128 << 10
	MaximumImportedItems = 64
	credentialVersion    = 1
)

var identifierCharacter = regexp.MustCompile(`[^a-z0-9]+`)

type ImportResult struct {
	Outbound           domain.Outbound `json:"outbound"`
	CredentialDocument []byte          `json:"-"`
}

type CredentialDocument struct {
	Version  int            `json:"version"`
	Outbound map[string]any `json:"outbound"`
}

func ParseImport(input string) ([]ImportResult, error) {
	if len(input) == 0 || len(input) > MaximumImportBytes {
		return nil, fmt.Errorf("outbound import must be between 1 and %d bytes", MaximumImportBytes)
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, fmt.Errorf("outbound import is empty")
	}
	if strings.HasPrefix(input, "{") || strings.HasPrefix(input, "[") {
		return parseJSONImport([]byte(input))
	}
	result, err := parseURI(input)
	if err != nil {
		return nil, err
	}
	return []ImportResult{result}, nil
}

func DecodeCredential(document []byte) (CredentialDocument, error) {
	if len(document) == 0 || len(document) > MaximumImportBytes {
		return CredentialDocument{}, fmt.Errorf("sing-box credential document has invalid size")
	}
	var decoded CredentialDocument
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return CredentialDocument{}, fmt.Errorf("decode sing-box credential document")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return CredentialDocument{}, fmt.Errorf("decode sing-box credential document: trailing data")
	}
	if decoded.Version != credentialVersion || len(decoded.Outbound) == 0 {
		return CredentialDocument{}, fmt.Errorf("unsupported sing-box credential document")
	}
	if _, _, _, err := publicFields(decoded.Outbound); err != nil {
		return CredentialDocument{}, err
	}
	return decoded, nil
}

func parseURI(input string) (ImportResult, error) {
	if strings.HasPrefix(strings.ToLower(input), "vmess://") {
		return parseVMessURI(input)
	}
	parsed, err := url.Parse(input)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ImportResult{}, fmt.Errorf("outbound URI is invalid")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "hy2" {
		scheme = "hysteria2"
	}
	if scheme == "wg" {
		scheme = "wireguard"
	}
	if scheme == "ss" {
		return parseShadowsocksURI(parsed)
	}
	kind := domain.OutboundType(scheme)
	if err := kind.Validate(); err != nil {
		return ImportResult{}, fmt.Errorf("outbound URI protocol is unsupported")
	}
	host, port, err := uriEndpoint(parsed)
	if err != nil {
		return ImportResult{}, err
	}
	query := parsed.Query()
	configuration := map[string]any{"type": singBoxType(kind), "server": host, "server_port": port}
	name := cleanName(parsed.Fragment, strings.ToUpper(string(kind))+" "+host)
	configuration["tag"] = outboundID(kind, name, host, port)
	username := ""
	password := ""
	if parsed.User != nil {
		username = parsed.User.Username()
		password, _ = parsed.User.Password()
	}
	switch kind {
	case domain.OutboundVLESS:
		if username == "" {
			return ImportResult{}, fmt.Errorf("VLESS URI requires a UUID")
		}
		configuration["uuid"] = username
		copyQuery(configuration, query, "flow", "packet_encoding")
		applyTLSAndTransport(configuration, query)
	case domain.OutboundTrojan:
		if username == "" {
			return ImportResult{}, fmt.Errorf("Trojan URI requires a password")
		}
		configuration["password"] = username
		applyTLSAndTransport(configuration, query)
	case domain.OutboundHysteria2:
		secret := username
		if password != "" {
			secret = password
		}
		if secret == "" {
			return ImportResult{}, fmt.Errorf("Hysteria2 URI requires a password")
		}
		configuration["password"] = secret
		copyQuery(configuration, query, "up_mbps", "down_mbps", "obfs", "obfs_password")
		applyTLSAndTransport(configuration, query)
	case domain.OutboundTUIC:
		if username == "" || password == "" {
			return ImportResult{}, fmt.Errorf("TUIC URI requires UUID and password")
		}
		configuration["uuid"] = username
		configuration["password"] = password
		copyQuery(configuration, query, "congestion_control", "udp_relay_mode", "zero_rtt_handshake", "heartbeat")
		applyTLSAndTransport(configuration, query)
	case domain.OutboundSOCKS5:
		configuration["version"] = "5"
		if username != "" {
			configuration["username"] = username
		}
		if password != "" {
			configuration["password"] = password
		}
	case domain.OutboundWireGuard:
		if username == "" {
			return ImportResult{}, fmt.Errorf("WireGuard URI requires a private key")
		}
		configuration["private_key"] = username
		copyQuery(configuration, query, "peer_public_key", "pre_shared_key", "local_address", "mtu", "reserved")
	default:
		return ImportResult{}, fmt.Errorf("outbound URI protocol is unsupported")
	}
	return finalizeImport(kind, name, host, port, configuration)
}

func parseShadowsocksURI(parsed *url.URL) (ImportResult, error) {
	host, port, err := uriEndpoint(parsed)
	if err != nil {
		return ImportResult{}, err
	}
	if parsed.User == nil {
		return ImportResult{}, fmt.Errorf("Shadowsocks URI requires method and password")
	}
	method := parsed.User.Username()
	password, hasPassword := parsed.User.Password()
	if !hasPassword {
		decoded, decodeErr := decodeBase64(method)
		if decodeErr != nil {
			return ImportResult{}, fmt.Errorf("Shadowsocks URI credentials are invalid")
		}
		method, password, hasPassword = strings.Cut(string(decoded), ":")
	}
	if method == "" || !hasPassword || password == "" {
		return ImportResult{}, fmt.Errorf("Shadowsocks URI requires method and password")
	}
	name := cleanName(parsed.Fragment, "Shadowsocks "+host)
	configuration := map[string]any{
		"type": "shadowsocks", "tag": outboundID(domain.OutboundShadowsocks, name, host, port),
		"server": host, "server_port": port, "method": method, "password": password,
	}
	return finalizeImport(domain.OutboundShadowsocks, name, host, port, configuration)
}

func parseVMessURI(input string) (ImportResult, error) {
	encoded := strings.TrimSpace(input[len("vmess://"):])
	decoded, err := decodeBase64(encoded)
	if err != nil || len(decoded) == 0 || len(decoded) > MaximumImportBytes {
		return ImportResult{}, fmt.Errorf("VMess URI payload is invalid")
	}
	var source struct {
		Name       string `json:"ps"`
		Host       string `json:"add"`
		Port       string `json:"port"`
		UUID       string `json:"id"`
		AlterID    string `json:"aid"`
		Security   string `json:"scy"`
		Network    string `json:"net"`
		Path       string `json:"path"`
		HostHeader string `json:"host"`
		TLS        string `json:"tls"`
		SNI        string `json:"sni"`
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	if err := decoder.Decode(&source); err != nil {
		return ImportResult{}, fmt.Errorf("VMess URI payload is invalid")
	}
	portValue, err := strconv.ParseUint(source.Port, 10, 16)
	if err != nil || portValue == 0 || source.UUID == "" {
		return ImportResult{}, fmt.Errorf("VMess URI endpoint or UUID is invalid")
	}
	host, err := normalizeHost(source.Host)
	if err != nil {
		return ImportResult{}, fmt.Errorf("VMess URI endpoint is invalid")
	}
	port := uint16(portValue)
	name := cleanName(source.Name, "VMess "+host)
	configuration := map[string]any{
		"type": "vmess", "tag": outboundID(domain.OutboundVMess, name, host, port),
		"server": host, "server_port": port, "uuid": source.UUID,
	}
	if source.Security != "" {
		configuration["security"] = source.Security
	}
	if source.AlterID != "" {
		if value, parseErr := strconv.ParseUint(source.AlterID, 10, 32); parseErr == nil {
			configuration["alter_id"] = value
		}
	}
	if source.TLS != "" && source.TLS != "none" {
		tls := map[string]any{"enabled": true}
		if source.SNI != "" {
			tls["server_name"] = source.SNI
		}
		configuration["tls"] = tls
	}
	if source.Network != "" && source.Network != "tcp" {
		transport := map[string]any{"type": source.Network}
		if source.Path != "" {
			transport["path"] = source.Path
		}
		if source.HostHeader != "" {
			transport["headers"] = map[string]any{"Host": source.HostHeader}
		}
		configuration["transport"] = transport
	}
	return finalizeImport(domain.OutboundVMess, name, host, port, configuration)
}

func parseJSONImport(input []byte) ([]ImportResult, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("sing-box JSON import is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("sing-box JSON import has trailing data")
	}
	var candidates []any
	switch value := root.(type) {
	case map[string]any:
		if raw, exists := value["outbounds"]; exists {
			list, ok := raw.([]any)
			if !ok {
				return nil, fmt.Errorf("sing-box outbounds must be an array")
			}
			candidates = list
		} else {
			candidates = []any{value}
		}
	case []any:
		candidates = value
	default:
		return nil, fmt.Errorf("sing-box JSON import must contain an outbound object or array")
	}
	if len(candidates) == 0 || len(candidates) > MaximumImportedItems {
		return nil, fmt.Errorf("sing-box JSON import must contain between 1 and %d outbounds", MaximumImportedItems)
	}
	results := make([]ImportResult, 0, len(candidates))
	for _, raw := range candidates {
		configuration, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("sing-box outbound entry must be an object")
		}
		kind, host, port, err := publicFields(configuration)
		if err != nil {
			return nil, err
		}
		name, _ := configuration["tag"].(string)
		name = cleanName(name, strings.ToUpper(string(kind))+" "+host)
		result, err := finalizeImport(kind, name, host, port, configuration)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func finalizeImport(kind domain.OutboundType, name, host string, port uint16, configuration map[string]any) (ImportResult, error) {
	if err := requiredCredentialFields(kind, configuration); err != nil {
		return ImportResult{}, err
	}
	metadata := collectSecretMetadata(configuration)
	outbound := domain.Outbound{
		ID: domain.ID(outboundID(kind, name, host, port)), Name: name, Adapter: domain.OutboundAdapterSingBox, Type: kind,
		Server: domain.Endpoint{Host: host, Port: port}, Capabilities: domain.Capabilities{TCP: true, UDP: true},
		Health: domain.UnknownOutboundHealth(), Enabled: true, SecretMetadata: metadata,
	}
	if err := outbound.Validate(); err != nil {
		return ImportResult{}, fmt.Errorf("imported outbound metadata is invalid")
	}
	document, err := json.Marshal(CredentialDocument{Version: credentialVersion, Outbound: configuration})
	if err != nil || len(document) > MaximumImportBytes {
		return ImportResult{}, fmt.Errorf("imported outbound credential is invalid")
	}
	return ImportResult{Outbound: outbound, CredentialDocument: document}, nil
}

func publicFields(configuration map[string]any) (domain.OutboundType, string, uint16, error) {
	typeText, ok := configuration["type"].(string)
	if !ok {
		return "", "", 0, fmt.Errorf("sing-box outbound type is required")
	}
	kind, ok := domainType(strings.ToLower(typeText))
	if !ok {
		return "", "", 0, fmt.Errorf("sing-box outbound type is unsupported")
	}
	if err := kind.Validate(); err != nil {
		return "", "", 0, fmt.Errorf("sing-box outbound type is unsupported")
	}
	hostText, ok := configuration["server"].(string)
	if !ok {
		return "", "", 0, fmt.Errorf("sing-box outbound server is required")
	}
	host, err := normalizeHost(hostText)
	if err != nil {
		return "", "", 0, fmt.Errorf("sing-box outbound server is invalid")
	}
	port, err := jsonPort(configuration["server_port"])
	if err != nil {
		return "", "", 0, err
	}
	configuration["type"] = singBoxType(kind)
	configuration["server"] = host
	configuration["server_port"] = port
	return kind, host, port, nil
}

func requiredCredentialFields(kind domain.OutboundType, configuration map[string]any) error {
	required := map[domain.OutboundType][]string{
		domain.OutboundVLESS: {"uuid"}, domain.OutboundTrojan: {"password"}, domain.OutboundShadowsocks: {"method", "password"},
		domain.OutboundVMess: {"uuid"}, domain.OutboundHysteria2: {"password"}, domain.OutboundTUIC: {"uuid", "password"},
		domain.OutboundWireGuard: {"private_key"},
	}
	for _, key := range required[kind] {
		value, exists := configuration[key]
		if !exists || strings.TrimSpace(fmt.Sprint(value)) == "" {
			return fmt.Errorf("sing-box %s outbound requires %s", kind, key)
		}
	}
	return nil
}

func collectSecretMetadata(configuration map[string]any) []string {
	secretKeys := map[string]struct{}{
		"uuid": {}, "password": {}, "username": {}, "private_key": {}, "pre_shared_key": {}, "obfs_password": {}, "token": {},
	}
	seen := map[string]struct{}{}
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				if _, secret := secretKeys[strings.ToLower(key)]; secret && strings.TrimSpace(fmt.Sprint(child)) != "" {
					seen[strings.ToLower(key)] = struct{}{}
				}
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(configuration)
	metadata := make([]string, 0, len(seen))
	for key := range seen {
		metadata = append(metadata, key)
	}
	sort.Strings(metadata)
	return metadata
}

func uriEndpoint(parsed *url.URL) (string, uint16, error) {
	host, err := normalizeHost(parsed.Hostname())
	if err != nil {
		return "", 0, fmt.Errorf("outbound URI server is invalid")
	}
	portValue, err := strconv.ParseUint(parsed.Port(), 10, 16)
	if err != nil || portValue == 0 {
		return "", 0, fmt.Errorf("outbound URI requires a valid explicit port")
	}
	return host, uint16(portValue), nil
}

func normalizeHost(host string) (string, error) {
	host = strings.TrimSpace(host)
	if address, err := netip.ParseAddr(host); err == nil {
		return address.String(), nil
	}
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "\r\n\t /@") {
		return "", fmt.Errorf("invalid host")
	}
	return strings.ToLower(host), nil
}

func jsonPort(value any) (uint16, error) {
	var text string
	switch typed := value.(type) {
	case uint16:
		if typed == 0 {
			return 0, fmt.Errorf("sing-box outbound server_port is invalid")
		}
		return typed, nil
	case int:
		text = strconv.Itoa(typed)
	case json.Number:
		text = typed.String()
	case float64:
		text = strconv.FormatFloat(typed, 'f', -1, 64)
	case string:
		text = typed
	default:
		return 0, fmt.Errorf("sing-box outbound server_port is required")
	}
	port, err := strconv.ParseUint(text, 10, 16)
	if err != nil || port == 0 {
		return 0, fmt.Errorf("sing-box outbound server_port is invalid")
	}
	return uint16(port), nil
}

func applyTLSAndTransport(configuration map[string]any, query url.Values) {
	security := strings.ToLower(query.Get("security"))
	if security == "tls" || security == "reality" || query.Get("tls") == "1" {
		tls := map[string]any{"enabled": true}
		if serverName := boundedValue(query.Get("sni")); serverName != "" {
			tls["server_name"] = serverName
		}
		if query.Get("allowInsecure") == "1" || strings.EqualFold(query.Get("insecure"), "true") {
			tls["insecure"] = true
		}
		configuration["tls"] = tls
	}
	transportType := boundedValue(query.Get("type"))
	if transportType != "" && transportType != "tcp" {
		transport := map[string]any{"type": transportType}
		if path := boundedValue(query.Get("path")); path != "" {
			transport["path"] = path
		}
		if host := boundedValue(query.Get("host")); host != "" {
			transport["headers"] = map[string]any{"Host": host}
		}
		if service := boundedValue(query.Get("serviceName")); service != "" {
			transport["service_name"] = service
		}
		configuration["transport"] = transport
	}
}

func copyQuery(configuration map[string]any, query url.Values, keys ...string) {
	for _, key := range keys {
		if value := boundedValue(query.Get(key)); value != "" {
			configuration[key] = value
		}
	}
}

func boundedValue(value string) string {
	if len(value) > 4096 {
		return ""
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return ""
		}
	}
	return value
}

func cleanName(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	runes := []rune(value)
	if len(runes) > 96 {
		value = string(runes[:96])
	}
	return value
}

func outboundID(kind domain.OutboundType, name, host string, port uint16) string {
	base := identifierCharacter.ReplaceAllString(strings.ToLower(name), "_")
	base = strings.Trim(base, "_")
	if base == "" {
		base = string(kind)
	}
	if len(base) > 47 {
		base = strings.TrimRight(base[:47], "_")
	}
	digest := sha256.Sum256([]byte(string(kind) + "\x00" + name + "\x00" + net.JoinHostPort(host, strconv.Itoa(int(port)))))
	return base + "_" + hex.EncodeToString(digest[:6])
}

func decodeBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	for _, encoding := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("invalid base64")
}

func domainType(adapterType string) (domain.OutboundType, bool) {
	if adapterType == "socks" || adapterType == "socks5" {
		return domain.OutboundSOCKS5, true
	}
	kind := domain.OutboundType(adapterType)
	return kind, kind.Validate() == nil
}

func singBoxType(kind domain.OutboundType) string {
	if kind == domain.OutboundSOCKS5 {
		return "socks"
	}
	return string(kind)
}
