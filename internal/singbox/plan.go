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
	"github.com/egress-manager/egress-manager/internal/secrets"
)

const (
	MaximumOutbounds = 128
	maximumCandidate = secrets.MaximumDocumentBytes
	ownedTagPrefix   = "egm_out_"
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
	Endpoints []map[string]any `json:"endpoints,omitempty"`
	Outbounds []map[string]any `json:"outbounds"`
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
	if configuration.Schema != ownedSchema || !configuration.Log.Disabled || len(configuration.Outbounds)+len(configuration.Endpoints) > MaximumOutbounds {
		return State{}, fmt.Errorf("sing-box configuration is not project-owned")
	}
	seenTags := map[string]struct{}{}
	ownedItems := append(append([]map[string]any{}, configuration.Outbounds...), configuration.Endpoints...)
	for _, outbound := range ownedItems {
		tag, ok := outbound["tag"].(string)
		if !ok || !strings.HasPrefix(tag, ownedTagPrefix) {
			return State{}, fmt.Errorf("sing-box configuration contains a foreign outbound")
		}
		id := domain.ID(strings.TrimPrefix(tag, ownedTagPrefix))
		if err := id.Validate("owned outbound id"); err != nil {
			return State{}, fmt.Errorf("sing-box configuration contains an invalid owned outbound")
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
