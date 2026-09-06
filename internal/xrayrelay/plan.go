package xrayrelay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strings"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

const (
	MaximumRelays             = 128
	MaximumConfigurationBytes = 256 << 10
	inboundTagPrefix          = "egm_relay_in_"
	outboundTagPrefix         = "egm_out_"
	blockTag                  = "egm_block"
)

type State struct {
	Exists    bool            `json:"exists"`
	Hash      string          `json:"hash"`
	Listeners []OwnedListener `json:"listeners,omitempty"`
}

type OwnedListener struct {
	Address  string `json:"address"`
	Port     uint16 `json:"port"`
	Protocol string `json:"protocol"`
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
	CandidateExists  bool     `json:"candidate_exists"`
	EnabledRelays    int      `json:"enabled_relays"`
	EnabledOutbounds int      `json:"enabled_outbounds"`
	Actions          []Action `json:"actions"`
}

type ExecutionPlan struct {
	Review    Plan
	candidate []byte
}

func (plan ExecutionPlan) Candidate() []byte { return append([]byte{}, plan.candidate...) }

type Settings struct {
	ProtectedPorts []uint16
}

type generatedConfig struct {
	Log       map[string]any   `json:"log,omitempty"`
	Inbounds  []map[string]any `json:"inbounds"`
	Outbounds []map[string]any `json:"outbounds"`
	Routing   map[string]any   `json:"routing"`
}

func InspectState(path string) (State, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{Hash: hashState(nil, false)}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read Xray relay configuration: %w", err)
	}
	state, err := ParseState(content, true)
	if err != nil {
		return State{}, err
	}
	return state, nil
}

func ParseState(content []byte, exists bool) (State, error) {
	state := State{Exists: exists, Hash: hashState(content, exists)}
	if !exists {
		if len(content) != 0 {
			return State{}, fmt.Errorf("absent Xray relay state contains data")
		}
		return state, nil
	}
	if len(content) == 0 || len(content) > MaximumConfigurationBytes {
		return State{}, fmt.Errorf("Xray relay configuration has invalid size")
	}
	var root struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Listen   string `json:"listen"`
			Port     uint16 `json:"port"`
			Protocol string `json:"protocol"`
			Settings struct {
				Network string `json:"network"`
			} `json:"settings"`
		} `json:"inbounds"`
		Outbounds []struct {
			Tag string `json:"tag"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(content, &root); err != nil {
		return State{}, fmt.Errorf("decode Xray relay configuration")
	}
	for _, inbound := range root.Inbounds {
		if !strings.HasPrefix(inbound.Tag, inboundTagPrefix) || inbound.Protocol != "dokodemo-door" || inbound.Port == 0 {
			return State{}, fmt.Errorf("Xray relay configuration contains a non-owned inbound")
		}
		address, err := netip.ParseAddr(inbound.Listen)
		if err != nil || address.String() != inbound.Listen {
			return State{}, fmt.Errorf("Xray relay configuration contains an invalid listener")
		}
		for _, protocol := range splitNetwork(inbound.Settings.Network) {
			state.Listeners = append(state.Listeners, OwnedListener{Address: inbound.Listen, Port: inbound.Port, Protocol: protocol})
		}
	}
	for _, outbound := range root.Outbounds {
		if outbound.Tag != blockTag && !strings.HasPrefix(outbound.Tag, outboundTagPrefix) {
			return State{}, fmt.Errorf("Xray relay configuration contains a non-owned outbound")
		}
	}
	return state, nil
}

func BuildPlan(settings Settings, relays []domain.Relay, outbounds []domain.Outbound, credentials map[domain.ID][]byte, listeners []inventory.Listener, state State) (ExecutionPlan, error) {
	if state.Hash == "" {
		return ExecutionPlan{}, fmt.Errorf("Xray relay state is required")
	}
	if len(relays) > MaximumRelays {
		return ExecutionPlan{}, fmt.Errorf("relay desired state exceeds %d entries", MaximumRelays)
	}
	protected := make(map[uint16]struct{}, len(settings.ProtectedPorts))
	for _, port := range settings.ProtectedPorts {
		if port != 0 {
			protected[port] = struct{}{}
		}
	}
	outboundByID := make(map[domain.ID]domain.Outbound, len(outbounds))
	for _, outbound := range outbounds {
		if err := outbound.Validate(); err != nil {
			return ExecutionPlan{}, fmt.Errorf("validate outbound %q: %w", outbound.ID, err)
		}
		outboundByID[outbound.ID] = outbound
	}
	ownedListeners := make(map[string]struct{}, len(state.Listeners))
	for _, listener := range state.Listeners {
		ownedListeners[listenerKey(listener.Protocol, listener.Address, listener.Port)] = struct{}{}
	}

	normalized := append([]domain.Relay{}, relays...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ID < normalized[j].ID })
	seenIDs := make(map[domain.ID]struct{}, len(normalized))
	active := make([]domain.Relay, 0, len(normalized))
	for _, relay := range normalized {
		if err := relay.Validate(); err != nil {
			return ExecutionPlan{}, fmt.Errorf("validate relay %q: %w", relay.ID, err)
		}
		if _, exists := seenIDs[relay.ID]; exists {
			return ExecutionPlan{}, fmt.Errorf("duplicate relay %q", relay.ID)
		}
		seenIDs[relay.ID] = struct{}{}
		if !relay.Enabled {
			continue
		}
		if _, blocked := protected[relay.ListenPort]; blocked {
			return ExecutionPlan{}, fmt.Errorf("relay %q uses a protected management port", relay.ID)
		}
		selected, exists := outboundByID[relay.OutboundID]
		if !exists || !selected.Enabled {
			return ExecutionPlan{}, fmt.Errorf("relay %q selected a missing or disabled outbound", relay.ID)
		}
		if selected.Adapter != domain.OutboundAdapterXray {
			return ExecutionPlan{}, fmt.Errorf("relay %q requires an Xray-adapter outbound", relay.ID)
		}
		if relay.Network.RequiresTCP() && !selected.Capabilities.TCP || relay.Network.RequiresUDP() && !selected.Capabilities.UDP {
			return ExecutionPlan{}, fmt.Errorf("relay %q selected an outbound without required network capability", relay.ID)
		}
		active = append(active, relay)
	}
	for index, relay := range active {
		for otherIndex := 0; otherIndex < index; otherIndex++ {
			if relayListenersOverlap(relay, active[otherIndex]) {
				return ExecutionPlan{}, fmt.Errorf("relay %q overlaps listener of relay %q", relay.ID, active[otherIndex].ID)
			}
		}
		for _, listener := range listeners {
			protocol := strings.ToLower(listener.Protocol)
			if protocol != "tcp" && protocol != "udp" || listener.Port != relay.ListenPort || !networkHas(relay.Network, protocol) || !addressesOverlap(relay.ListenAddress, listener.Address) {
				continue
			}
			if _, owned := ownedListeners[listenerKey(protocol, relay.ListenAddress, relay.ListenPort)]; owned {
				continue
			}
			if _, owned := ownedListeners[listenerKey(protocol, listener.Address, listener.Port)]; owned {
				continue
			}
			return ExecutionPlan{}, fmt.Errorf("relay %q conflicts with an existing %s listener on port %d", relay.ID, protocol, relay.ListenPort)
		}
	}

	review := Plan{Engine: "xray-relay", StateHash: state.Hash, Actions: []Action{}}
	if len(active) == 0 {
		review.CandidateExists = false
		review.CandidateHash = hashState(nil, false)
		if state.Exists {
			review.Actions = append(review.Actions, Action{Kind: "remove", Resource: "owned Xray relay configuration", Summary: "Stop the project-owned relay runtime and remove its configuration."})
		}
		return ExecutionPlan{Review: review}, nil
	}
	configuration := generatedConfig{
		Log:       map[string]any{"loglevel": "warning"},
		Inbounds:  []map[string]any{},
		Outbounds: []map[string]any{},
		Routing:   map[string]any{"domainStrategy": "AsIs", "rules": []any{}},
	}
	rules := []any{}
	usedOutbounds := make(map[domain.ID]struct{})
	for _, relay := range active {
		inboundTag := inboundTagPrefix + string(relay.ID)
		configuration.Inbounds = append(configuration.Inbounds, map[string]any{
			"tag":      inboundTag,
			"listen":   relay.ListenAddress,
			"port":     relay.ListenPort,
			"protocol": "dokodemo-door",
			"settings": map[string]any{"address": relay.Destination.Host, "port": relay.Destination.Port, "network": string(relay.Network)},
		})
		target := outboundTagPrefix + string(relay.OutboundID)
		rule := map[string]any{"type": "field", "inboundTag": []string{inboundTag}, "outboundTag": target}
		if len(relay.SourceCIDRs) > 0 {
			sources := make([]string, len(relay.SourceCIDRs))
			for index, prefix := range relay.SourceCIDRs {
				sources[index] = prefix.String()
			}
			rule["source"] = sources
			rules = append(rules, rule, map[string]any{"type": "field", "inboundTag": []string{inboundTag}, "outboundTag": blockTag})
		} else {
			rules = append(rules, rule)
		}
		usedOutbounds[relay.OutboundID] = struct{}{}
		review.EnabledRelays++
		review.Actions = append(review.Actions, Action{Kind: "relay", Resource: string(relay.ID), Summary: fmt.Sprintf("Listen on %s:%d and relay through the selected outbound to %s.", relay.ListenAddress, relay.ListenPort, relay.Destination.String())})
	}
	ids := make([]string, 0, len(usedOutbounds))
	for id := range usedOutbounds {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, rawID := range ids {
		id := domain.ID(rawID)
		document, exists := credentials[id]
		if !exists {
			return ExecutionPlan{}, fmt.Errorf("enabled relay outbound %q has no credential document", id)
		}
		credential, err := DecodeCredential(document)
		if err != nil {
			return ExecutionPlan{}, fmt.Errorf("decode Xray credential for outbound %q: %w", id, err)
		}
		selected := outboundByID[id]
		endpoint, err := credentialEndpoint(credential.Outbound)
		if err != nil || endpoint != selected.Server {
			return ExecutionPlan{}, fmt.Errorf("Xray credential public metadata does not match outbound %q", id)
		}
		copyDocument := cloneMap(credential.Outbound)
		copyDocument["tag"] = outboundTagPrefix + rawID
		configuration.Outbounds = append(configuration.Outbounds, copyDocument)
		review.EnabledOutbounds++
		review.Actions = append(review.Actions, Action{Kind: "outbound", Resource: rawID, Summary: "Configure the referenced project-owned Xray outbound."})
	}
	configuration.Outbounds = append(configuration.Outbounds, map[string]any{"protocol": "blackhole", "tag": blockTag})
	configuration.Routing["rules"] = rules
	candidate, err := json.MarshalIndent(configuration, "", "  ")
	if err != nil {
		return ExecutionPlan{}, fmt.Errorf("encode Xray relay candidate: %w", err)
	}
	candidate = append(candidate, '\n')
	if len(candidate) > MaximumConfigurationBytes {
		return ExecutionPlan{}, fmt.Errorf("Xray relay candidate exceeds %d bytes", MaximumConfigurationBytes)
	}
	review.CandidateExists = true
	review.CandidateHash = hashState(candidate, true)
	if state.Exists {
		review.Actions = append([]Action{{Kind: "replace", Resource: "owned Xray relay configuration", Summary: "Atomically replace the project-owned Xray relay configuration."}}, review.Actions...)
	} else {
		review.Actions = append([]Action{{Kind: "create", Resource: "owned Xray relay configuration", Summary: "Create the project-owned Xray relay configuration."}}, review.Actions...)
	}
	return ExecutionPlan{Review: review, candidate: candidate}, nil
}

func credentialEndpoint(outbound map[string]any) (domain.Endpoint, error) {
	settings, ok := outbound["settings"].(map[string]any)
	if !ok {
		return domain.Endpoint{}, fmt.Errorf("Xray credential settings are invalid")
	}
	vnext, ok := settings["vnext"].([]any)
	if !ok || len(vnext) != 1 {
		return domain.Endpoint{}, fmt.Errorf("Xray VLESS credential endpoint is invalid")
	}
	server, ok := vnext[0].(map[string]any)
	if !ok {
		return domain.Endpoint{}, fmt.Errorf("Xray VLESS credential endpoint is invalid")
	}
	host, ok := server["address"].(string)
	if !ok {
		return domain.Endpoint{}, fmt.Errorf("Xray VLESS credential host is invalid")
	}
	var port uint16
	switch value := server["port"].(type) {
	case float64:
		if value > 0 && value <= 65535 && value == float64(uint16(value)) {
			port = uint16(value)
		}
	case json.Number:
		parsed, err := value.Int64()
		if err == nil && parsed > 0 && parsed <= 65535 {
			port = uint16(parsed)
		}
	case uint16:
		port = value
	}
	endpoint := domain.Endpoint{Host: host, Port: port}
	if err := endpoint.Validate(); err != nil {
		return domain.Endpoint{}, err
	}
	return endpoint, nil
}

func relayListenersOverlap(left, right domain.Relay) bool {
	return left.ListenPort == right.ListenPort && addressesOverlap(left.ListenAddress, right.ListenAddress) && networksOverlap(left.Network, right.Network)
}

func addressesOverlap(left, right string) bool {
	leftAddr, leftErr := netip.ParseAddr(strings.Trim(left, "[]"))
	rightAddr, rightErr := netip.ParseAddr(strings.Trim(right, "[]"))
	if leftErr != nil || rightErr != nil {
		return left == right
	}
	if leftAddr.BitLen() != rightAddr.BitLen() {
		return false
	}
	return leftAddr == rightAddr || leftAddr.IsUnspecified() || rightAddr.IsUnspecified()
}

func networksOverlap(left, right domain.RelayNetwork) bool {
	return left.RequiresTCP() && right.RequiresTCP() || left.RequiresUDP() && right.RequiresUDP()
}

func networkHas(network domain.RelayNetwork, protocol string) bool {
	return protocol == "tcp" && network.RequiresTCP() || protocol == "udp" && network.RequiresUDP()
}

func listenerKey(protocol, address string, port uint16) string {
	return strings.ToLower(protocol) + "|" + address + "|" + fmt.Sprint(port)
}

func splitNetwork(network string) []string {
	result := []string{}
	for _, item := range strings.Split(network, ",") {
		item = strings.TrimSpace(strings.ToLower(item))
		if item == "tcp" || item == "udp" {
			result = append(result, item)
		}
	}
	return result
}

func cloneMap(source map[string]any) map[string]any {
	encoded, _ := json.Marshal(source)
	var cloned map[string]any
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func hashState(content []byte, exists bool) string {
	marker := byte(0)
	if exists {
		marker = 1
	}
	digest := sha256.Sum256(append([]byte{marker}, content...))
	return hex.EncodeToString(digest[:])
}
