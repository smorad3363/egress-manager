package interfaceoutbound

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

const (
	ownedStateSchema  = "egress-manager/interface-outbounds/v1"
	MaximumOutbounds  = 128
	maximumStateBytes = 128 << 10
)

var ErrStateChanged = errors.New("owned interface outbound state changed after planning")

type StateEntry struct {
	ID            domain.ID           `json:"id"`
	Kind          domain.OutboundType `json:"kind"`
	InterfaceName string              `json:"interface_name"`
	ConfigHash    string              `json:"config_hash"`
	ServiceName   string              `json:"service_name,omitempty"`
	Addresses     []string            `json:"addresses,omitempty"`
	MTU           uint16              `json:"mtu,omitempty"`
}

type stateDocument struct {
	Schema  string       `json:"schema"`
	Entries []StateEntry `json:"entries"`
}

type State struct {
	Exists  bool         `json:"exists"`
	Hash    string       `json:"hash"`
	Entries []StateEntry `json:"entries"`
	configs map[domain.ID][]byte
}

type Action struct {
	Kind     string `json:"kind"`
	Resource string `json:"resource"`
	Summary  string `json:"summary"`
}

type Review struct {
	Engine           string   `json:"engine"`
	StateHash        string   `json:"state_hash"`
	CandidateHash    string   `json:"candidate_hash"`
	CandidateExists  bool     `json:"candidate_exists"`
	EnabledOutbounds int      `json:"enabled_outbounds"`
	Actions          []Action `json:"actions"`
}

type candidateEntry struct {
	state      StateEntry
	credential CredentialDocument
	config     []byte
}

type ExecutionPlan struct {
	Review    Review
	candidate []byte
	entries   []candidateEntry
}

func BuildPlan(outbounds []domain.Outbound, credentials map[domain.ID][]byte, state State, interfaces []inventory.Interface) (ExecutionPlan, error) {
	if state.Hash == "" {
		return ExecutionPlan{}, fmt.Errorf("interface outbound state is required")
	}
	if len(outbounds) > MaximumOutbounds {
		return ExecutionPlan{}, fmt.Errorf("interface outbound desired state exceeds %d outbounds", MaximumOutbounds)
	}
	byInterface := make(map[string]inventory.Interface, len(interfaces))
	for _, item := range interfaces {
		if item.Name == "" {
			return ExecutionPlan{}, fmt.Errorf("host inventory contains an unnamed interface")
		}
		if _, exists := byInterface[item.Name]; exists {
			return ExecutionPlan{}, fmt.Errorf("host inventory contains duplicate interface %q", item.Name)
		}
		byInterface[item.Name] = item
	}
	ownedInterfaces := make(map[string]StateEntry, len(state.Entries))
	for _, entry := range state.Entries {
		ownedInterfaces[entry.InterfaceName] = entry
	}
	normalized := append([]domain.Outbound{}, outbounds...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ID < normalized[j].ID })
	seen := map[domain.ID]struct{}{}
	entries := []candidateEntry{}
	for _, outbound := range normalized {
		if err := outbound.Validate(); err != nil {
			return ExecutionPlan{}, fmt.Errorf("validate outbound %q: %w", outbound.ID, err)
		}
		if _, exists := seen[outbound.ID]; exists {
			return ExecutionPlan{}, fmt.Errorf("duplicate outbound %q", outbound.ID)
		}
		seen[outbound.ID] = struct{}{}
		if outbound.Adapter != domain.OutboundAdapterInterface || !outbound.Enabled {
			continue
		}
		documentBytes, exists := credentials[outbound.ID]
		if !exists {
			return ExecutionPlan{}, fmt.Errorf("enabled interface outbound %q has no credential document", outbound.ID)
		}
		credential, err := DecodeCredential(documentBytes)
		if err != nil {
			return ExecutionPlan{}, fmt.Errorf("decode credential for interface outbound %q", outbound.ID)
		}
		expectedInterface, _ := InterfaceName(outbound.Type, outbound.ID)
		if credential.Kind != outbound.Type || credential.InterfaceName != expectedInterface {
			return ExecutionPlan{}, fmt.Errorf("credential public metadata does not match interface outbound %q", outbound.ID)
		}
		config, stateEntry, err := desiredEntry(outbound, credential)
		if err != nil {
			return ExecutionPlan{}, fmt.Errorf("build interface outbound %q: %w", outbound.ID, err)
		}
		if _, exists := byInterface[stateEntry.InterfaceName]; exists {
			owned, ownedExists := ownedInterfaces[stateEntry.InterfaceName]
			if !ownedExists || owned.ID != outbound.ID || owned.Kind != outbound.Type {
				return ExecutionPlan{}, fmt.Errorf("interface outbound %q collides with foreign interface %q", outbound.ID, stateEntry.InterfaceName)
			}
		}
		entries = append(entries, candidateEntry{state: stateEntry, credential: credential, config: config})
	}
	document := stateDocument{Schema: ownedStateSchema, Entries: make([]StateEntry, 0, len(entries))}
	for _, entry := range entries {
		document.Entries = append(document.Entries, entry.state)
	}
	candidate := []byte(nil)
	if len(entries) > 0 {
		encoded, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			return ExecutionPlan{}, fmt.Errorf("encode interface outbound candidate: %w", err)
		}
		candidate = append(encoded, '\n')
	}
	if len(candidate) > maximumStateBytes {
		return ExecutionPlan{}, fmt.Errorf("interface outbound candidate exceeds %d bytes", maximumStateBytes)
	}
	review := Review{Engine: "native-interface-outbounds", StateHash: state.Hash, CandidateHash: hashState(candidate, len(candidate) > 0, entries), CandidateExists: len(candidate) > 0, EnabledOutbounds: len(entries), Actions: []Action{}}
	current := make(map[domain.ID]StateEntry, len(state.Entries))
	for _, entry := range state.Entries {
		current[entry.ID] = entry
	}
	desired := make(map[domain.ID]StateEntry, len(entries))
	for _, entry := range entries {
		desired[entry.state.ID] = entry.state
		kind := "create"
		summary := "Create and verify a project-owned native VPN interface."
		if existing, exists := current[entry.state.ID]; exists {
			kind = "restart"
			summary = "Reconcile and verify the project-owned native VPN interface."
			if equalStateEntry(existing, entry.state) {
				kind = "verify"
				summary = "Verify the unchanged project-owned native VPN interface."
			}
		}
		review.Actions = append(review.Actions, Action{Kind: kind, Resource: entry.state.InterfaceName, Summary: summary})
	}
	for _, entry := range state.Entries {
		if _, exists := desired[entry.ID]; !exists {
			review.Actions = append(review.Actions, Action{Kind: "remove", Resource: entry.InterfaceName, Summary: "Stop and remove a project-owned native VPN interface."})
		}
	}
	return ExecutionPlan{Review: review, candidate: candidate, entries: entries}, nil
}

func InspectState(statePath, runtimeDirectory string) (State, error) {
	content, exists, err := inspectOwnedFile(statePath, maximumStateBytes)
	if err != nil {
		return State{}, err
	}
	if !exists {
		return State{Hash: hashState(nil, false, nil), Entries: []StateEntry{}, configs: map[domain.ID][]byte{}}, nil
	}
	document, err := parseState(content)
	if err != nil {
		return State{}, err
	}
	configs := make(map[domain.ID][]byte, len(document.Entries))
	entries := make([]candidateEntry, 0, len(document.Entries))
	for _, entry := range document.Entries {
		config, configExists, err := inspectOwnedFile(runtimeConfigPath(runtimeDirectory, entry), MaximumImportBytes)
		if err != nil {
			return State{}, err
		}
		if !configExists || hashBytes(config) != entry.ConfigHash {
			return State{}, fmt.Errorf("owned interface outbound runtime configuration changed")
		}
		configs[entry.ID] = config
		entries = append(entries, candidateEntry{state: entry, config: config})
	}
	return State{Exists: true, Hash: hashState(content, true, entries), Entries: append([]StateEntry{}, document.Entries...), configs: configs}, nil
}

func ParseState(content []byte, exists bool) (State, error) {
	if !exists {
		if len(content) != 0 {
			return State{}, fmt.Errorf("absent interface outbound state has content")
		}
		return State{Hash: hashState(nil, false, nil), Entries: []StateEntry{}, configs: map[domain.ID][]byte{}}, nil
	}
	document, err := parseState(content)
	if err != nil {
		return State{}, err
	}
	return State{Exists: true, Hash: hashState(content, true, nil), Entries: append([]StateEntry{}, document.Entries...), configs: map[domain.ID][]byte{}}, nil
}

func (plan ExecutionPlan) Candidate() []byte { return append([]byte{}, plan.candidate...) }

func desiredEntry(outbound domain.Outbound, credential CredentialDocument) ([]byte, StateEntry, error) {
	entry := StateEntry{ID: outbound.ID, Kind: outbound.Type, InterfaceName: credential.InterfaceName}
	var config []byte
	switch outbound.Type {
	case domain.OutboundWireGuard:
		profile := credential.WireGuard
		if profile == nil || profile.Endpoint != outbound.Server {
			return nil, StateEntry{}, fmt.Errorf("WireGuard credential endpoint does not match public metadata")
		}
		config = renderWireGuard(*profile)
		entry.Addresses = append([]string{}, profile.Addresses...)
		entry.MTU = profile.MTU
	case domain.OutboundOpenVPN:
		profile := credential.OpenVPN
		if profile == nil || !strings.Contains(profile.Config, "remote "+outbound.Server.Host+" "+strconv.Itoa(int(outbound.Server.Port))+"\n") {
			return nil, StateEntry{}, fmt.Errorf("OpenVPN credential endpoint does not match public metadata")
		}
		config = []byte(profile.Config)
		entry.ServiceName = "egress-manager-openvpn@" + string(outbound.ID) + ".service"
	default:
		return nil, StateEntry{}, fmt.Errorf("unsupported native interface outbound type")
	}
	entry.ConfigHash = hashBytes(config)
	return config, entry, nil
}

func renderWireGuard(profile WireGuardProfile) []byte {
	var output strings.Builder
	output.WriteString("[Interface]\nPrivateKey = " + profile.PrivateKey + "\n\n[Peer]\nPublicKey = " + profile.PeerPublicKey + "\n")
	if profile.PresharedKey != "" {
		output.WriteString("PresharedKey = " + profile.PresharedKey + "\n")
	}
	output.WriteString("AllowedIPs = " + strings.Join(profile.AllowedIPs, ", ") + "\n")
	output.WriteString("Endpoint = " + profile.Endpoint.String() + "\n")
	if profile.PersistentKeepalive != 0 {
		output.WriteString("PersistentKeepalive = " + strconv.Itoa(int(profile.PersistentKeepalive)) + "\n")
	}
	return []byte(output.String())
}

func parseState(content []byte) (stateDocument, error) {
	if len(content) == 0 || len(content) > maximumStateBytes {
		return stateDocument{}, fmt.Errorf("interface outbound state has invalid size")
	}
	var document stateDocument
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return stateDocument{}, fmt.Errorf("decode interface outbound state: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return stateDocument{}, fmt.Errorf("decode interface outbound state: trailing data")
	}
	if document.Schema != ownedStateSchema || len(document.Entries) == 0 || len(document.Entries) > MaximumOutbounds {
		return stateDocument{}, fmt.Errorf("interface outbound state is not project-owned")
	}
	seenIDs := map[domain.ID]struct{}{}
	seenInterfaces := map[string]struct{}{}
	for index, entry := range document.Entries {
		if err := validateStateEntry(entry); err != nil {
			return stateDocument{}, err
		}
		if index > 0 && document.Entries[index-1].ID >= entry.ID {
			return stateDocument{}, fmt.Errorf("interface outbound state entries are not uniquely sorted")
		}
		if _, exists := seenIDs[entry.ID]; exists {
			return stateDocument{}, fmt.Errorf("interface outbound state contains duplicate IDs")
		}
		if _, exists := seenInterfaces[entry.InterfaceName]; exists {
			return stateDocument{}, fmt.Errorf("interface outbound state contains duplicate interfaces")
		}
		seenIDs[entry.ID] = struct{}{}
		seenInterfaces[entry.InterfaceName] = struct{}{}
	}
	return document, nil
}

func validateStateEntry(entry StateEntry) error {
	if err := entry.ID.Validate("interface outbound state ID"); err != nil {
		return err
	}
	expected, err := InterfaceName(entry.Kind, entry.ID)
	if err != nil || entry.InterfaceName != expected || len(entry.ConfigHash) != sha256.Size*2 {
		return fmt.Errorf("interface outbound state entry is invalid")
	}
	if _, err := hex.DecodeString(entry.ConfigHash); err != nil {
		return fmt.Errorf("interface outbound state config hash is invalid")
	}
	switch entry.Kind {
	case domain.OutboundWireGuard:
		if entry.ServiceName != "" || len(entry.Addresses) == 0 {
			return fmt.Errorf("WireGuard state entry is invalid")
		}
		if _, err := parsePrefixes(entry.Addresses, "WireGuard state address", false); err != nil {
			return err
		}
	case domain.OutboundOpenVPN:
		if entry.ServiceName != "egress-manager-openvpn@"+string(entry.ID)+".service" || len(entry.Addresses) != 0 || entry.MTU != 0 {
			return fmt.Errorf("OpenVPN state entry is invalid")
		}
	default:
		return fmt.Errorf("interface outbound state kind is unsupported")
	}
	return nil
}

func inspectOwnedFile(path string, maximum int) ([]byte, bool, error) {
	if !filepath.IsAbs(path) {
		return nil, false, fmt.Errorf("owned interface outbound path must be absolute")
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect owned interface outbound file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > int64(maximum) {
		return nil, false, fmt.Errorf("owned interface outbound file is unsafe")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("read owned interface outbound file: %w", err)
	}
	return content, true, nil
}

func runtimeConfigPath(directory string, entry StateEntry) string {
	extension := ".wg.conf"
	if entry.Kind == domain.OutboundOpenVPN {
		extension = ".ovpn"
	}
	return filepath.Join(directory, string(entry.ID)+extension)
}

func hashState(content []byte, exists bool, entries []candidateEntry) string {
	digest := sha256.New()
	if exists {
		_, _ = digest.Write([]byte{1})
	} else {
		_, _ = digest.Write([]byte{0})
	}
	_, _ = digest.Write([]byte(strconv.Itoa(len(content)) + "\x00"))
	_, _ = digest.Write(content)
	for _, entry := range entries {
		_, _ = digest.Write([]byte(string(entry.state.ID) + "\x00" + strconv.Itoa(len(entry.config)) + "\x00"))
		_, _ = digest.Write(entry.config)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func hashBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func equalStateEntry(left, right StateEntry) bool {
	if left.ID != right.ID || left.Kind != right.Kind || left.InterfaceName != right.InterfaceName || left.ConfigHash != right.ConfigHash || left.ServiceName != right.ServiceName || left.MTU != right.MTU || len(left.Addresses) != len(right.Addresses) {
		return false
	}
	for index := range left.Addresses {
		if left.Addresses[index] != right.Addresses[index] {
			return false
		}
	}
	return true
}
