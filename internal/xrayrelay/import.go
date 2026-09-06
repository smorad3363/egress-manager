// Package xrayrelay implements the project-owned Xray listener relay adapter.
package xrayrelay

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/egress-manager/egress-manager/internal/domain"
)

const (
	MaximumImportBytes = 128 << 10
	credentialVersion  = 1
	maximumExtraBytes  = 16 << 10
)

var (
	uuidPattern       = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	shortIDPattern    = regexp.MustCompile(`(?i)^[0-9a-f]{0,16}$`)
	publicKeyPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)
	identifierPattern = regexp.MustCompile(`[^a-z0-9]+`)
)

type ImportResult struct {
	Outbound           domain.Outbound `json:"outbound"`
	CredentialDocument []byte          `json:"-"`
}

type CredentialDocument struct {
	Version  int            `json:"version"`
	Outbound map[string]any `json:"outbound"`
}

func IsCompatibleImport(input string) bool {
	input = strings.TrimSpace(input)
	if len(input) < len("vless://") || !strings.EqualFold(input[:len("vless://")], "vless://") {
		return false
	}
	parsed, err := url.Parse(input)
	if err != nil {
		return false
	}
	query := parsed.Query()
	return strings.EqualFold(query.Get("type"), "xhttp") || (strings.EqualFold(query.Get("security"), "reality") && query.Get("pbk") != "")
}

func ParseImport(input string) (ImportResult, error) {
	if len(input) == 0 || len(input) > MaximumImportBytes {
		return ImportResult{}, fmt.Errorf("Xray outbound import must be between 1 and %d bytes", MaximumImportBytes)
	}
	input = strings.TrimSpace(input)
	parsed, err := url.Parse(input)
	if err != nil || !strings.EqualFold(parsed.Scheme, "vless") || parsed.Hostname() == "" {
		return ImportResult{}, fmt.Errorf("Xray relay currently accepts VLESS share links")
	}
	uuid := ""
	if parsed.User != nil {
		uuid = parsed.User.Username()
	}
	if !uuidPattern.MatchString(uuid) {
		return ImportResult{}, fmt.Errorf("VLESS URI requires a canonical UUID")
	}
	portValue, err := strconv.ParseUint(parsed.Port(), 10, 16)
	if err != nil || portValue == 0 {
		return ImportResult{}, fmt.Errorf("VLESS URI requires a valid explicit port")
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	server := domain.Endpoint{Host: host, Port: uint16(portValue)}
	if err := server.Validate(); err != nil {
		return ImportResult{}, fmt.Errorf("VLESS URI endpoint is invalid")
	}
	query := parsed.Query()
	if !strings.EqualFold(query.Get("security"), "reality") {
		return ImportResult{}, fmt.Errorf("Xray relay VLESS import currently requires REALITY security")
	}
	if !strings.EqualFold(query.Get("type"), "xhttp") {
		return ImportResult{}, fmt.Errorf("Xray relay VLESS import currently requires XHTTP transport")
	}
	sni, err := boundedText("sni", query.Get("sni"), 253, true)
	if err != nil || sni == "" || strings.ContainsAny(sni, " /@") {
		return ImportResult{}, fmt.Errorf("REALITY server name is invalid")
	}
	publicKey := query.Get("pbk")
	if !publicKeyPattern.MatchString(publicKey) {
		return ImportResult{}, fmt.Errorf("REALITY public key is invalid")
	}
	shortID := query.Get("sid")
	if !shortIDPattern.MatchString(shortID) || len(shortID)%2 != 0 {
		return ImportResult{}, fmt.Errorf("REALITY short ID is invalid")
	}
	fingerprint := strings.ToLower(strings.TrimSpace(query.Get("fp")))
	if fingerprint == "" {
		fingerprint = "chrome"
	}
	if !validFingerprint(fingerprint) {
		return ImportResult{}, fmt.Errorf("REALITY fingerprint is unsupported")
	}
	path, err := boundedText("path", query.Get("path"), 4096, false)
	if err != nil {
		return ImportResult{}, err
	}
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return ImportResult{}, fmt.Errorf("XHTTP path must start with /")
	}
	mode := strings.ToLower(strings.TrimSpace(query.Get("mode")))
	if mode == "" {
		mode = "auto"
	}
	if mode != "auto" && mode != "packet-up" && mode != "stream-up" {
		return ImportResult{}, fmt.Errorf("XHTTP mode is unsupported")
	}
	xhttp := map[string]any{"path": path, "mode": mode}
	if hostHeader, textErr := boundedText("host", query.Get("host"), 1024, false); textErr != nil {
		return ImportResult{}, textErr
	} else if hostHeader != "" {
		xhttp["host"] = hostHeader
	}
	if extraText := strings.TrimSpace(query.Get("extra")); extraText != "" {
		if len(extraText) > maximumExtraBytes {
			return ImportResult{}, fmt.Errorf("XHTTP extra exceeds %d bytes", maximumExtraBytes)
		}
		var extra any
		decoder := json.NewDecoder(strings.NewReader(extraText))
		decoder.UseNumber()
		if err := decoder.Decode(&extra); err != nil {
			return ImportResult{}, fmt.Errorf("XHTTP extra JSON is invalid")
		}
		var trailing any
		if err := decoder.Decode(&trailing); err == nil {
			return ImportResult{}, fmt.Errorf("XHTTP extra JSON has trailing data")
		}
		if _, ok := extra.(map[string]any); !ok {
			return ImportResult{}, fmt.Errorf("XHTTP extra must be a JSON object")
		}
		xhttp["extra"] = extra
	}
	flow, err := boundedText("flow", query.Get("flow"), 64, false)
	if err != nil {
		return ImportResult{}, err
	}
	user := map[string]any{"id": uuid, "encryption": "none"}
	if flow != "" {
		user["flow"] = flow
	}
	configuration := map[string]any{
		"protocol": "vless",
		"settings": map[string]any{"vnext": []any{map[string]any{
			"address": host,
			"port":    uint16(portValue),
			"users":   []any{user},
		}}},
		"streamSettings": map[string]any{
			"network":  "xhttp",
			"security": "reality",
			"xhttpSettings": xhttp,
			"realitySettings": map[string]any{
				"serverName":  sni,
				"fingerprint": fingerprint,
				"publicKey":   publicKey,
				"shortId":     shortID,
			},
		},
	}
	name := cleanName(parsed.Fragment, "VLESS XHTTP "+host)
	outbound := domain.Outbound{
		ID:           domain.ID(outboundID(name, host, uint16(portValue))),
		Name:         name,
		Adapter:      domain.OutboundAdapter("xray"),
		Type:         domain.OutboundVLESS,
		Server:       server,
		Capabilities: domain.Capabilities{TCP: true, UDP: true},
		Health:       domain.UnknownOutboundHealth(),
		Enabled:      true,
		SecretMetadata: []string{"public_key", "short_id", "uuid"},
	}
	document, err := json.Marshal(CredentialDocument{Version: credentialVersion, Outbound: configuration})
	if err != nil || len(document) > MaximumImportBytes {
		return ImportResult{}, fmt.Errorf("Xray credential document is invalid")
	}
	return ImportResult{Outbound: outbound, CredentialDocument: document}, nil
}

func DecodeCredential(document []byte) (CredentialDocument, error) {
	if len(document) == 0 || len(document) > MaximumImportBytes {
		return CredentialDocument{}, fmt.Errorf("Xray credential document has invalid size")
	}
	var decoded CredentialDocument
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return CredentialDocument{}, fmt.Errorf("decode Xray credential document")
	}
	if decoded.Version != credentialVersion || len(decoded.Outbound) == 0 {
		return CredentialDocument{}, fmt.Errorf("unsupported Xray credential document")
	}
	protocol, _ := decoded.Outbound["protocol"].(string)
	if protocol != "vless" {
		return CredentialDocument{}, fmt.Errorf("unsupported Xray credential protocol")
	}
	return decoded, nil
}

func validFingerprint(value string) bool {
	switch value {
	case "chrome", "firefox", "safari", "ios", "android", "edge", "360", "qq", "random", "randomized":
		return true
	default:
		return false
	}
}

func boundedText(name, value string, maximum int, lower bool) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > maximum {
		return "", fmt.Errorf("%s is too long", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", fmt.Errorf("%s contains a control character", name)
		}
	}
	if lower {
		value = strings.ToLower(value)
	}
	return value, nil
}

func cleanName(value, fallback string) string {
	value, _ = url.QueryUnescape(value)
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

func outboundID(name, host string, port uint16) string {
	base := identifierPattern.ReplaceAllString(strings.ToLower(name), "_")
	base = strings.Trim(base, "_")
	if base == "" {
		base = "xray_vless"
	}
	if len(base) > 47 {
		base = strings.TrimRight(base[:47], "_")
	}
	digest := sha256.Sum256([]byte(name + "\x00" + host + "\x00" + strconv.Itoa(int(port))))
	return base + "_" + hex.EncodeToString(digest[:6])
}
