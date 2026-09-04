package singbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/routing"
	"github.com/egress-manager/egress-manager/internal/secrets"
)

const (
	MaximumOutbounds = 128
	maximumCandidate = secrets.MaximumDocumentBytes
	ownedTagPrefix   = "egm_out_"
	ownedRoutePrefix = "egm_route_"
	ownedDNSPrefix   = "egm_dns_"
	directRouteTag   = "egm_direct_fallback"
	ownedSchema      = "https://sing-box.sagernet.org/schema.json#egress-manager-owned"
)

type State struct {
	Exists bool   `json:"exists"`
	Hash   string `json:"hash"`
}

type Action struct {
	Kind     string `json:"kind"`
	Resource string `json:"resource"`
	Summary  string `json:"summary"`
}

type Plan struct {
	Engine           string   `json:"engine"`
	StateHash        string   `json:"state_hash"`
	CandidateHash    string   `json:"candidate_hash"`
	EnabledOutbounds int      `json:"enabled_outbounds"`
	RoutedRoutes     int      `json:"routed_routes,omitempty"`
	Actions          []Action `json:"actions"`
}

type ExecutionPlan struct {
	Plan      Plan
	candidate []byte
}

func (plan ExecutionPlan) Candidate() []byte {
	return append([]byte{}, plan.candidate...)
}

type generatedConfiguration struct {
	Schema    string           `json:"$schema"`
	Log       generatedLog     `json:"log"`
	Inbounds  []map[string]any `json:"inbounds,omitempty"`
	Endpoints []map[string]any `json:"endpoints,omitempty"`
	Outbounds []map[string]any `json:"outbounds"`
	DNS       map[string]any   `json:"dns,omitempty"`
	Route     map[string]any   `json:"route,omitempty"`
}

type generatedLog struct {
	Disabled bool `json:"disabled"`
}

func BuildPlan(outbounds []domain.Outbound, credentials map[domain.ID][]byte, state State) (ExecutionPlan, error) {
	if state.Hash == "" {
		return ExecutionPlan{}, fmt.Errorf("sing-box state is required")
	}
	if len(outbounds) > MaximumOutbounds {
		return ExecutionPlan{}, fmt.Errorf("sing-box desired state exceeds %d outbounds", MaximumOutbounds)
	}
	normalized := append([]domain.Outbound{}, outbounds...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ID < normalized[j].ID })
	seen := make(map[domain.ID]struct{}, len(normalized))
	configuration := generatedConfiguration{
		Schema:    ownedSchema,
		Log:       generatedLog{Disabled: true},
		Endpoints: []map[string]any{},
		Outbounds: []map[string]any{},
	}
	public := Plan{Engine: "sing-box", StateHash: state.Hash, Actions: []Action{}}
	if state.Exists {
		public.Actions = append(public.Actions, Action{Kind: "replace", Resource: "owned sing-box configuration", Summary: "Atomically replace the project-owned sing-box configuration."})
	} else {
		public.Actions = append(public.Actions, Action{Kind: "create", Resource: "owned sing-box configuration", Summary: "Create the project-owned sing-box configuration."})
	}
	for _, outbound := range normalized {
		if err := outbound.Validate(); err != nil {
			return ExecutionPlan{}, fmt.Errorf("validate outbound %q: %w", outbound.ID, err)
		}
		if outbound.Adapter != domain.OutboundAdapterSingBox {
			return ExecutionPlan{}, fmt.Errorf("outbound %q does not use the sing-box adapter", outbound.ID)
		}
		if _, exists := seen[outbound.ID]; exists {
			return ExecutionPlan{}, fmt.Errorf("duplicate outbound %q", outbound.ID)
		}
		seen[outbound.ID] = struct{}{}
		if !outbound.Enabled {
			continue
		}
		documentBytes, exists := credentials[outbound.ID]
		if !exists {
			return ExecutionPlan{}, fmt.Errorf("enabled outbound %q has no credential document", outbound.ID)
		}
		document, err := DecodeCredential(documentBytes)
		if err != nil {
			return ExecutionPlan{}, fmt.Errorf("decode credential for outbound %q", outbound.ID)
		}
		kind, host, port, err := publicFields(document.Outbound)
		if err != nil || kind != outbound.Type || host != outbound.Server.Host || port != outbound.Server.Port {
			return ExecutionPlan{}, fmt.Errorf("credential public metadata does not match outbound %q", outbound.ID)
		}
		metadata := collectSecretMetadata(document.Outbound)
		expectedMetadata := append([]string{}, outbound.SecretMetadata...)
		sort.Strings(expectedMetadata)
		if !slices.Equal(metadata, expectedMetadata) {
			return ExecutionPlan{}, fmt.Errorf("credential secret metadata does not match outbound %q", outbound.ID)
		}
		document.Outbound["tag"] = ownedTagPrefix + string(outbound.ID)
		if outbound.Type == domain.OutboundWireGuard {
			endpoint, err := wireGuardEndpoint(document.Outbound)
			if err != nil {
				return ExecutionPlan{}, fmt.Errorf("convert WireGuard endpoint %q", outbound.ID)
			}
			configuration.Endpoints = append(configuration.Endpoints, endpoint)
		} else {
			configuration.Outbounds = append(configuration.Outbounds, document.Outbound)
		}
		public.EnabledOutbounds++
		public.Actions = append(public.Actions, Action{Kind: "outbound", Resource: string(outbound.ID), Summary: "Configure an enabled sing-box outbound."})
	}
	candidate, err := json.MarshalIndent(configuration, "", "  ")
	if err != nil {
		return ExecutionPlan{}, fmt.Errorf("encode sing-box candidate: %w", err)
	}
	candidate = append(candidate, '\n')
	if len(candidate) > maximumCandidate {
		return ExecutionPlan{}, fmt.Errorf("sing-box candidate exceeds %d bytes", maximumCandidate)
	}
	public.CandidateHash = hashBytes(candidate)
	return ExecutionPlan{Plan: public, candidate: candidate}, nil
}

// BuildRoutedPlan composes project-owned TUN inbounds and explicit route policy
// with the same authenticated outbound desired state used by BuildPlan.
func BuildRoutedPlan(outbounds []domain.Outbound, credentials map[domain.ID][]byte, intents []routing.RouteIntent, state State) (ExecutionPlan, error) {
	base, err := BuildPlan(outbounds, credentials, state)
	if err != nil {
		return ExecutionPlan{}, err
	}
	if len(intents) > routing.MaximumRoutes {
		return ExecutionPlan{}, fmt.Errorf("sing-box routed state exceeds %d routes", routing.MaximumRoutes)
	}
	var configuration generatedConfiguration
	if err := json.Unmarshal(base.candidate, &configuration); err != nil {
		return ExecutionPlan{}, fmt.Errorf("decode generated sing-box candidate: %w", err)
	}
	available := make(map[string]struct{}, len(configuration.Outbounds)+len(configuration.Endpoints))
	for _, item := range append(append([]map[string]any{}, configuration.Outbounds...), configuration.Endpoints...) {
		if tag, ok := item["tag"].(string); ok {
			available[tag] = struct{}{}
		}
	}
	normalized := append([]routing.RouteIntent{}, intents...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ID < normalized[j].ID })
	seenRoutes := make(map[domain.ID]struct{}, len(normalized))
	rules := []map[string]any{}
	dnsServers := []map[string]any{}
	dnsRules := []map[string]any{}
	directRequired := false
	for _, intent := range normalized {
		if err := intent.Validate(); err != nil {
			return ExecutionPlan{}, fmt.Errorf("validate routed intent %q: %w", intent.ID, err)
		}
		if _, exists := seenRoutes[intent.ID]; exists {
			return ExecutionPlan{}, fmt.Errorf("duplicate routed intent %q", intent.ID)
		}
		seenRoutes[intent.ID] = struct{}{}
		target := ownedTagPrefix + string(intent.SelectedOutboundID)
		if intent.UseDirect {
			target = directRouteTag
			directRequired = true
		} else if _, exists := available[target]; !exists {
			return ExecutionPlan{}, fmt.Errorf("routed intent %q selected an unavailable outbound", intent.ID)
		}
		inboundTag := ownedRoutePrefix + string(intent.ID)
		addresses := make([]string, len(intent.TunnelAddresses))
		for index, address := range intent.TunnelAddresses {
			addresses[index] = address.String()
		}
		mtu := intent.MTU
		if mtu == 0 {
			mtu = 1500
		}
		configuration.Inbounds = append(configuration.Inbounds, map[string]any{
			"type": "tun", "tag": inboundTag, "interface_name": intent.TunnelInterface,
			"address": addresses, "mtu": mtu, "auto_route": false, "stack": "system",
		})
		switch intent.DNSPolicy {
		case domain.DNSFollowOutbound:
			for index, address := range intent.DNSServers {
				serverTag := fmt.Sprintf("%s%s_%d", ownedDNSPrefix, intent.ID, index)
				dnsServers = append(dnsServers, map[string]any{
					"type": "udp", "tag": serverTag, "server": address.String(), "server_port": 53, "detour": target,
				})
				if index == 0 {
					dnsRules = append(dnsRules, map[string]any{"inbound": []string{inboundTag}, "action": "route", "server": serverTag})
				}
			}
			rules = append(rules, map[string]any{"inbound": []string{inboundTag}, "port": []uint16{53}, "action": "hijack-dns"})
		case domain.DNSBlock:
			rules = append(rules, map[string]any{"inbound": []string{inboundTag}, "port": []uint16{53, 853}, "action": "reject", "method": "drop"})
		case domain.DNSSystem:
			directRequired = true
			rules = append(rules, map[string]any{"inbound": []string{inboundTag}, "port": []uint16{53, 853}, "action": "route", "outbound": directRouteTag})
		}
		rules = append(rules,
			familyRule(inboundTag, 4, intent.IPv4Policy == domain.IPv4Block, intent.IPv4Policy == domain.IPv4Direct, target),
			familyRule(inboundTag, 6, intent.IPv6Policy == domain.IPv6Block, intent.IPv6Policy == domain.IPv6Direct, target),
		)
		if intent.IPv4Policy == domain.IPv4Direct || intent.IPv6Policy == domain.IPv6Direct {
			directRequired = true
		}
		base.Plan.RoutedRoutes++
		base.Plan.Actions = append(base.Plan.Actions, Action{Kind: "route", Resource: string(intent.ID), Summary: "Configure a dedicated TUN with explicit DNS and IP-family policy."})
	}
	if directRequired {
		configuration.Outbounds = append(configuration.Outbounds, map[string]any{"type": "direct", "tag": directRouteTag})
	}
	if len(dnsServers) != 0 {
		configuration.DNS = map[string]any{"servers": dnsServers, "rules": dnsRules}
	}
	if len(rules) != 0 {
		configuration.Route = map[string]any{"auto_detect_interface": true, "rules": rules}
	}
	candidate, err := json.MarshalIndent(configuration, "", "  ")
	if err != nil {
		return ExecutionPlan{}, fmt.Errorf("encode routed sing-box candidate: %w", err)
	}
	candidate = append(candidate, '\n')
	if len(candidate) > maximumCandidate {
		return ExecutionPlan{}, fmt.Errorf("sing-box candidate exceeds %d bytes", maximumCandidate)
	}
	base.candidate = candidate
	base.Plan.CandidateHash = hashBytes(candidate)
	return base, nil
}

func familyRule(inboundTag string, version int, block, direct bool, target string) map[string]any {
	rule := map[string]any{"inbound": []string{inboundTag}, "ip_version": version}
	if block {
		rule["action"] = "reject"
		rule["method"] = "drop"
		return rule
	}
	if direct {
		target = directRouteTag
	}
	rule["action"] = "route"
	rule["outbound"] = target
	return rule
}

func ParseState(content []byte, exists bool) (State, error) {
	if !exists {
		if len(content) != 0 {
			return State{}, fmt.Errorf("absent sing-box state contains data")
		}
		return State{Hash: hashState(nil, false)}, nil
	}
	if len(content) == 0 || len(content) > maximumCandidate {
		return State{}, fmt.Errorf("owned sing-box configuration is empty or oversized")
	}
	var configuration generatedConfiguration
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configuration); err != nil {
		return State{}, fmt.Errorf("owned sing-box configuration is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return State{}, fmt.Errorf("owned sing-box configuration has trailing data")
	}
	if configuration.Schema != ownedSchema || !configuration.Log.Disabled || len(configuration.Outbounds)+len(configuration.Endpoints) > MaximumOutbounds+1 || len(configuration.Inbounds) > routing.MaximumRoutes {
		return State{}, fmt.Errorf("sing-box configuration is not project-owned")
	}
	seenTags := map[string]struct{}{}
	ownedItems := append(append([]map[string]any{}, configuration.Outbounds...), configuration.Endpoints...)
	for _, outbound := range ownedItems {
		tag, ok := outbound["tag"].(string)
		if !ok || (!strings.HasPrefix(tag, ownedTagPrefix) && tag != directRouteTag) {
			return State{}, fmt.Errorf("sing-box configuration contains a foreign outbound")
		}
		if tag == directRouteTag {
			if kind, _ := outbound["type"].(string); kind != "direct" {
				return State{}, fmt.Errorf("sing-box configuration contains an invalid owned direct outbound")
			}
		} else {
			id := domain.ID(strings.TrimPrefix(tag, ownedTagPrefix))
			if err := id.Validate("owned outbound id"); err != nil {
				return State{}, fmt.Errorf("sing-box configuration contains an invalid owned outbound")
			}
		}
		if _, exists := seenTags[tag]; exists {
			return State{}, fmt.Errorf("sing-box configuration contains duplicate tags")
		}
		seenTags[tag] = struct{}{}
	}
	for _, inbound := range configuration.Inbounds {
		tag, ok := inbound["tag"].(string)
		kind, _ := inbound["type"].(string)
		if !ok || kind != "tun" || !strings.HasPrefix(tag, ownedRoutePrefix) {
			return State{}, fmt.Errorf("sing-box configuration contains a foreign inbound")
		}
		id := domain.ID(strings.TrimPrefix(tag, ownedRoutePrefix))
		if err := id.Validate("owned route id"); err != nil {
			return State{}, fmt.Errorf("sing-box configuration contains an invalid owned route inbound")
		}
		if _, exists := seenTags[tag]; exists {
			return State{}, fmt.Errorf("sing-box configuration contains duplicate tags")
		}
		seenTags[tag] = struct{}{}
	}
	return State{Exists: true, Hash: hashState(content, true)}, nil
}

func wireGuardEndpoint(legacy map[string]any) (map[string]any, error) {
	host, _ := legacy["server"].(string)
	port, err := jsonPort(legacy["server_port"])
	if err != nil {
		return nil, err
	}
	privateKey, _ := legacy["private_key"].(string)
	publicKey, _ := legacy["peer_public_key"].(string)
	localAddress, _ := legacy["local_address"].(string)
	if privateKey == "" || publicKey == "" || localAddress == "" {
		return nil, fmt.Errorf("WireGuard private key, peer public key, and local address are required")
	}
	addresses := strings.Split(localAddress, ",")
	for index := range addresses {
		addresses[index] = strings.TrimSpace(addresses[index])
		if prefix, parseErr := netip.ParsePrefix(addresses[index]); parseErr != nil || prefix.String() != addresses[index] {
			return nil, fmt.Errorf("WireGuard local address is invalid")
		}
	}
	peer := map[string]any{"address": host, "port": port, "public_key": publicKey, "allowed_ips": []string{"0.0.0.0/0", "::/0"}}
	if value, exists := legacy["pre_shared_key"]; exists {
		peer["pre_shared_key"] = value
	}
	if value, exists := legacy["reserved"]; exists {
		peer["reserved"] = value
	}
	endpoint := map[string]any{"type": "wireguard", "tag": legacy["tag"], "address": addresses, "private_key": privateKey, "peers": []any{peer}}
	if mtuText, ok := legacy["mtu"].(string); ok && mtuText != "" {
		mtu, parseErr := strconv.ParseUint(mtuText, 10, 16)
		if parseErr != nil || mtu < 576 {
			return nil, fmt.Errorf("WireGuard MTU is invalid")
		}
		endpoint["mtu"] = mtu
	}
	return endpoint, nil
}

func InspectState(path string) (State, error) {
	content, exists, err := inspectOwnedConfiguration(path)
	if err != nil {
		return State{}, err
	}
	return ParseState(content, exists)
}

func inspectOwnedConfiguration(path string) ([]byte, bool, error) {
	if !filepath.IsAbs(path) {
		return nil, false, fmt.Errorf("sing-box configuration path must be absolute")
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect owned sing-box configuration: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > maximumCandidate {
		return nil, false, fmt.Errorf("owned sing-box configuration is not a bounded regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("read owned sing-box configuration: %w", err)
	}
	if _, err := ParseState(content, true); err != nil {
		return nil, false, err
	}
	return content, true, nil
}

func hashState(content []byte, exists bool) string {
	prefix := []byte("absent\x00")
	if exists {
		prefix = []byte("present\x00")
	}
	return hashBytes(append(prefix, content...))
}

func hashBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
