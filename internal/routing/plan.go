// Package routing plans project-owned interface and subnet egress routing.
package routing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

const (
	MaximumRoutes         = 128
	maximumCandidateBytes = 128 << 10
	ownedSchema           = "egress-manager/routing/v1"
)

var linuxInterfacePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)

type Settings struct {
	TableBase              uint32
	RulePriorityBase       uint32
	ProtectedLocalPrefixes []netip.Prefix
	InterfaceOutbounds     map[domain.ID]string
}

type ResolvedEndpoints map[domain.ID][]netip.Addr

type State struct {
	Exists bool          `json:"exists"`
	Hash   string        `json:"hash"`
	Routes []RouteIntent `json:"-"`
}

type Action struct {
	Kind     string `json:"kind"`
	Resource string `json:"resource"`
	Summary  string `json:"summary"`
}

type Plan struct {
	Engine        string   `json:"engine"`
	StateHash     string   `json:"state_hash"`
	CandidateHash string   `json:"candidate_hash"`
	EnabledRoutes int      `json:"enabled_routes"`
	Actions       []Action `json:"actions"`
}

type ExecutionPlan struct {
	Plan      Plan
	candidate []byte
}

func (plan ExecutionPlan) Candidate() []byte { return append([]byte{}, plan.candidate...) }

type Candidate struct {
	Schema string        `json:"schema"`
	Routes []RouteIntent `json:"routes"`
}

type RouteIntent struct {
	ID                 domain.ID              `json:"id"`
	Source             domain.RouteSource     `json:"source"`
	IngressInterface   string                 `json:"ingress_interface"`
	TunnelInterface    string                 `json:"tunnel_interface"`
	RoutingTable       uint32                 `json:"routing_table"`
	RulePriority       uint32                 `json:"rule_priority"`
	OutboundID         domain.ID              `json:"outbound_id"`
	OutboundAdapter    domain.OutboundAdapter `json:"outbound_adapter"`
	FallbackOutboundID domain.ID              `json:"fallback_outbound_id,omitempty"`
	SelectedOutboundID domain.ID              `json:"selected_outbound_id,omitempty"`
	UseDirect          bool                   `json:"use_direct,omitempty"`
	FailurePolicy      domain.FailurePolicy   `json:"failure_policy"`
	DNSPolicy          domain.DNSPolicy       `json:"dns_policy"`
	DNSServers         []netip.Addr           `json:"dns_servers,omitempty"`
	IPv4Policy         domain.IPv4Policy      `json:"ipv4_policy"`
	IPv6Policy         domain.IPv6Policy      `json:"ipv6_policy"`
	KillSwitch         bool                   `json:"kill_switch"`
	MTU                uint16                 `json:"mtu,omitempty"`
	TCPMSS             uint16                 `json:"tcp_mss,omitempty"`
	BypassAddresses    []netip.Addr           `json:"bypass_addresses"`
	TunnelAddresses    []netip.Prefix         `json:"tunnel_addresses,omitempty"`
}

func (intent RouteIntent) Validate() error {
	if err := intent.ID.Validate("owned route id"); err != nil || !linuxInterfacePattern.MatchString(intent.TunnelInterface) || !linuxInterfacePattern.MatchString(intent.IngressInterface) || intent.RoutingTable == 0 || intent.RulePriority == 0 {
		return fmt.Errorf("owned routing state contains an invalid route identity or resource")
	}
	if err := intent.OutboundAdapter.Validate(); err != nil {
		return fmt.Errorf("owned route uses an invalid outbound adapter")
	}
	if intent.OutboundAdapter != domain.OutboundAdapterSingBox && intent.OutboundAdapter != domain.OutboundAdapterInterface {
		return fmt.Errorf("owned route uses an unsupported outbound adapter")
	}
	if intent.OutboundAdapter == domain.OutboundAdapterSingBox && intent.TunnelInterface != tunnelName(intent.ID) {
		return fmt.Errorf("owned sing-box route uses an invalid tunnel interface")
	}
	if intent.OutboundAdapter == domain.OutboundAdapterInterface && !strings.HasPrefix(intent.TunnelInterface, "egmwg") && !strings.HasPrefix(intent.TunnelInterface, "egmov") {
		return fmt.Errorf("owned native route uses an invalid tunnel interface")
	}
	if intent.UseDirect {
		if intent.FailurePolicy != domain.FailureDirect || intent.SelectedOutboundID != "" {
			return fmt.Errorf("owned route contains an invalid direct selection")
		}
	} else {
		if err := intent.SelectedOutboundID.Validate("selected outbound id"); err != nil {
			return err
		}
		if intent.SelectedOutboundID != intent.OutboundID && (intent.FailurePolicy != domain.FailureFailover || intent.SelectedOutboundID != intent.FallbackOutboundID) {
			return fmt.Errorf("owned route selected an undeclared outbound")
		}
	}
	route := domain.Route{
		ID: intent.ID, Name: string(intent.ID), Source: intent.Source, OutboundID: intent.OutboundID,
		FallbackOutboundID: intent.FallbackOutboundID, FailurePolicy: intent.FailurePolicy, DNSPolicy: intent.DNSPolicy,
		DNSServers: intent.DNSServers, IPv4Policy: intent.IPv4Policy, IPv6Policy: intent.IPv6Policy,
		KillSwitch: intent.KillSwitch, MTU: intent.MTU, TCPMSS: intent.TCPMSS, Enabled: true,
	}
	if err := route.Validate(); err != nil {
		return fmt.Errorf("owned route policy is invalid: %w", err)
	}
	if intent.Source.Kind == domain.RouteSourceInterface && intent.Source.Interface != intent.IngressInterface {
		return fmt.Errorf("owned route ingress does not match its interface source")
	}
	if intent.OutboundAdapter == domain.OutboundAdapterSingBox {
		if len(intent.TunnelAddresses) != 2 || !validTunnelAddress(intent.TunnelAddresses[0], true) || !validTunnelAddress(intent.TunnelAddresses[1], false) {
			return fmt.Errorf("owned route contains invalid tunnel addresses")
		}
	} else if len(intent.TunnelAddresses) != 0 {
		return fmt.Errorf("owned native route contains synthetic tunnel addresses")
	}
	if len(intent.BypassAddresses) < 1 || len(intent.BypassAddresses) > 32 {
		return fmt.Errorf("owned route contains an invalid outbound bypass set")
	}
	seen := make(map[netip.Addr]struct{}, len(intent.BypassAddresses))
	for _, address := range intent.BypassAddresses {
		if !address.IsValid() || address != address.Unmap() || address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() {
			return fmt.Errorf("owned route contains an unsafe outbound bypass address")
		}
		if _, exists := seen[address]; exists {
			return fmt.Errorf("owned route contains duplicate outbound bypass addresses")
		}
		seen[address] = struct{}{}
	}
	return nil
}

func validTunnelAddress(prefix netip.Prefix, ipv4 bool) bool {
	if !prefix.IsValid() || prefix.Addr().Is4() != ipv4 || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() {
		return false
	}
	if ipv4 {
		return prefix.Bits() == 30 && netip.MustParsePrefix("198.18.0.0/15").Contains(prefix.Addr())
	}
	return prefix.Bits() == 126 && netip.MustParsePrefix("fd45:474d::/32").Contains(prefix.Addr())
}

func BuildPlan(settings Settings, routes []domain.Route, outbounds []domain.Outbound, host inventory.Inventory, resolved ResolvedEndpoints, state State) (ExecutionPlan, error) {
	if err := settings.validate(); err != nil {
		return ExecutionPlan{}, err
	}
	if state.Hash == "" {
		return ExecutionPlan{}, fmt.Errorf("routing state is required")
	}
	if len(routes) > MaximumRoutes {
		return ExecutionPlan{}, fmt.Errorf("routing desired state exceeds %d routes", MaximumRoutes)
	}
	interfaces, err := indexInterfaces(host.Interfaces)
	if err != nil {
		return ExecutionPlan{}, err
	}
	outboundByID, err := indexOutbounds(outbounds)
	if err != nil {
		return ExecutionPlan{}, err
	}
	usedTables := existingNumericTables(host.Routes)
	usedPriorities := existingPriorities(host.PolicyRules)
	usedPrefixes := existingPrefixes(host)
	releaseOwnedResources(state.Routes, host, usedTables, usedPriorities, &usedPrefixes)
	normalized := append([]domain.Route{}, routes...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ID < normalized[j].ID })
	seenIDs := map[domain.ID]struct{}{}
	selectors := make([]routeSelector, 0, len(normalized))
	candidate := Candidate{Schema: ownedSchema, Routes: []RouteIntent{}}
	public := Plan{Engine: "policy-routing", StateHash: state.Hash, Actions: []Action{}}
	operation := "create"
	summary := "Create only project-owned TUN, policy-routing, DNS, and kill-switch state."
	if state.Exists {
		operation = "replace"
		summary = "Replace only project-owned TUN, policy-routing, DNS, and kill-switch state."
	}
	public.Actions = append(public.Actions, Action{Kind: operation, Resource: "owned egress routing state", Summary: summary})
	for _, route := range normalized {
		if err := route.Validate(); err != nil {
			return ExecutionPlan{}, fmt.Errorf("validate route %q: %w", route.ID, err)
		}
		if _, exists := seenIDs[route.ID]; exists {
			return ExecutionPlan{}, fmt.Errorf("duplicate route %q", route.ID)
		}
		seenIDs[route.ID] = struct{}{}
		if !route.Enabled {
			continue
		}
		if route.Source.Kind != domain.RouteSourceInterface && route.Source.Kind != domain.RouteSourceSubnet {
			return ExecutionPlan{}, fmt.Errorf("route %q source kind is not supported by interface/subnet routing", route.ID)
		}
		ingress, sourcePrefix, err := resolveSource(route, interfaces, settings.ProtectedLocalPrefixes)
		if err != nil {
			return ExecutionPlan{}, err
		}
		selector := routeSelector{id: route.ID, ingress: ingress, prefix: sourcePrefix}
		for _, existing := range selectors {
			if selectorsOverlap(existing, selector) {
				return ExecutionPlan{}, fmt.Errorf("route %q overlaps enabled route %q on ingress %q", route.ID, existing.id, ingress)
			}
		}
		selectors = append(selectors, selector)
		primary, exists := outboundByID[route.OutboundID]
		if !exists || !primary.Enabled {
			return ExecutionPlan{}, fmt.Errorf("route %q references a missing or disabled primary outbound", route.ID)
		}
		if primary.Adapter == domain.OutboundAdapterInterface && route.FailurePolicy == domain.FailureDirect {
			return ExecutionPlan{}, fmt.Errorf("route %q native interface outbound does not support direct failure policy", route.ID)
		}
		if primary.Adapter != domain.OutboundAdapterSingBox && primary.Adapter != domain.OutboundAdapterInterface {
			return ExecutionPlan{}, fmt.Errorf("route %q outbound adapter does not support routed traffic", route.ID)
		}
		bypass, err := endpointAddresses(primary, resolved)
		if err != nil {
			return ExecutionPlan{}, fmt.Errorf("route %q primary outbound: %w", route.ID, err)
		}
		if route.FailurePolicy == domain.FailureFailover {
			fallback, exists := outboundByID[route.FallbackOutboundID]
			if !exists || !fallback.Enabled || fallback.Adapter != primary.Adapter {
				return ExecutionPlan{}, fmt.Errorf("route %q references a missing, disabled, or incompatible fallback outbound", route.ID)
			}
			fallbackAddresses, endpointErr := endpointAddresses(fallback, resolved)
			if endpointErr != nil {
				return ExecutionPlan{}, fmt.Errorf("route %q fallback outbound: %w", route.ID, endpointErr)
			}
			bypass = append(bypass, fallbackAddresses...)
		}
		bypass = normalizeAddresses(bypass)
		table, priority, slot, err := allocatePolicySlot(route.ID, settings, usedTables, usedPriorities, usedPrefixes)
		if err != nil {
			return ExecutionPlan{}, err
		}
		selected := primary.ID
		useDirect := false
		if primary.Health.Status == domain.HealthUnhealthy {
			switch route.FailurePolicy {
			case domain.FailureFailover:
				fallback := outboundByID[route.FallbackOutboundID]
				if fallback.Health.Status != domain.HealthUnhealthy && fallback.Health.Status != domain.HealthDisabled {
					selected = fallback.ID
				}
			case domain.FailureDirect:
				selected = ""
				useDirect = true
			}
		}
		tunnelInterface := tunnelName(route.ID)
		tunnelAddresses := slotTunnelAddresses(slot)
		if primary.Adapter == domain.OutboundAdapterInterface {
			selectedInterface, exists := settings.InterfaceOutbounds[selected]
			if !exists || !linuxInterfacePattern.MatchString(selectedInterface) {
				return ExecutionPlan{}, fmt.Errorf("route %q selected native outbound has no owned interface", route.ID)
			}
			link, exists := interfaces[selectedInterface]
			if !exists || strings.EqualFold(link.State, "down") {
				return ExecutionPlan{}, fmt.Errorf("route %q selected native outbound interface is unavailable", route.ID)
			}
			tunnelInterface = selectedInterface
			tunnelAddresses = nil
		}
		intent := RouteIntent{
			ID: route.ID, Source: route.Source, IngressInterface: ingress, TunnelInterface: tunnelInterface, RoutingTable: table, RulePriority: priority,
			OutboundID: route.OutboundID, OutboundAdapter: primary.Adapter, FallbackOutboundID: route.FallbackOutboundID, SelectedOutboundID: selected, UseDirect: useDirect,
			FailurePolicy: route.FailurePolicy, DNSPolicy: route.DNSPolicy, DNSServers: append([]netip.Addr{}, route.DNSServers...), IPv4Policy: route.IPv4Policy, IPv6Policy: route.IPv6Policy,
			KillSwitch: route.KillSwitch, MTU: route.MTU, TCPMSS: route.TCPMSS, BypassAddresses: bypass, TunnelAddresses: tunnelAddresses,
		}
		candidate.Routes = append(candidate.Routes, intent)
		public.EnabledRoutes++
		tunnelSummary := "Create a dedicated project-owned sing-box TUN path."
		if primary.Adapter == domain.OutboundAdapterInterface {
			tunnelSummary = "Use the verified project-owned native VPN interface."
		}
		public.Actions = append(public.Actions,
			Action{Kind: "tun", Resource: intent.TunnelInterface, Summary: tunnelSummary},
			Action{Kind: "policy", Resource: string(route.ID), Summary: "Select traffic, bypass outbound endpoints, and enforce explicit IP and DNS policies."},
		)
		if route.KillSwitch {
			public.Actions = append(public.Actions, Action{Kind: "kill_switch", Resource: string(route.ID), Summary: "Block unintended fallback while the selected egress is unavailable."})
		}
	}
	encoded, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return ExecutionPlan{}, fmt.Errorf("encode routing candidate: %w", err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maximumCandidateBytes {
		return ExecutionPlan{}, fmt.Errorf("routing candidate exceeds %d bytes", maximumCandidateBytes)
	}
	public.CandidateHash = hash(encoded)
	return ExecutionPlan{Plan: public, candidate: encoded}, nil
}

func ParseState(content []byte, exists bool) (State, error) {
	if !exists {
		if len(content) != 0 {
			return State{}, fmt.Errorf("absent routing state contains data")
		}
		return State{Hash: stateHash(nil, false)}, nil
	}
	if len(content) == 0 || len(content) > maximumCandidateBytes {
		return State{}, fmt.Errorf("owned routing state is empty or oversized")
	}
	var candidate Candidate
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&candidate); err != nil {
		return State{}, fmt.Errorf("owned routing state is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return State{}, fmt.Errorf("owned routing state has trailing data")
	}
	if candidate.Schema != ownedSchema || len(candidate.Routes) > MaximumRoutes {
		return State{}, fmt.Errorf("routing state is not project-owned")
	}
	seen := map[domain.ID]struct{}{}
	for _, intent := range candidate.Routes {
		if err := intent.Validate(); err != nil {
			return State{}, fmt.Errorf("owned routing state contains an invalid route: %w", err)
		}
		if _, exists := seen[intent.ID]; exists {
			return State{}, fmt.Errorf("owned routing state contains duplicate routes")
		}
		seen[intent.ID] = struct{}{}
	}
	return State{Exists: true, Hash: stateHash(content, true), Routes: append([]RouteIntent{}, candidate.Routes...)}, nil
}

func InspectState(path string) (State, error) {
	if !filepath.IsAbs(path) {
		return State{}, fmt.Errorf("routing state path must be absolute")
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return ParseState(nil, false)
	}
	if err != nil {
		return State{}, fmt.Errorf("inspect routing state: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > maximumCandidateBytes {
		return State{}, fmt.Errorf("owned routing state is not a bounded regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return State{}, fmt.Errorf("read routing state: %w", err)
	}
	return ParseState(content, true)
}

type routeSelector struct {
	id      domain.ID
	ingress string
	prefix  netip.Prefix
}

func (settings Settings) validate() error {
	if settings.TableBase < 1000 || settings.TableBase > 65000-MaximumRoutes || settings.RulePriorityBase < 1000 || settings.RulePriorityBase > 65000-MaximumRoutes {
		return fmt.Errorf("routing table and rule priority bases must leave %d bounded slots", MaximumRoutes)
	}
	var errs []error
	for _, prefix := range settings.ProtectedLocalPrefixes {
		if !prefix.IsValid() || prefix != prefix.Masked() {
			errs = append(errs, fmt.Errorf("protected local prefixes must be canonical"))
		}
	}
	for id, name := range settings.InterfaceOutbounds {
		if err := id.Validate("interface outbound ID"); err != nil || !linuxInterfacePattern.MatchString(name) || !strings.HasPrefix(name, "egmwg") && !strings.HasPrefix(name, "egmov") {
			errs = append(errs, fmt.Errorf("interface outbound mapping is invalid"))
		}
	}
	return errors.Join(errs...)
}

func indexInterfaces(items []inventory.Interface) (map[string]inventory.Interface, error) {
	result := make(map[string]inventory.Interface, len(items))
	for _, item := range items {
		if item.Name == "" {
			return nil, fmt.Errorf("host inventory contains an unnamed interface")
		}
		if _, exists := result[item.Name]; exists {
			return nil, fmt.Errorf("host inventory contains duplicate interface %q", item.Name)
		}
		result[item.Name] = item
	}
	return result, nil
}

func indexOutbounds(items []domain.Outbound) (map[domain.ID]domain.Outbound, error) {
	result := make(map[domain.ID]domain.Outbound, len(items))
	for _, item := range items {
		if err := item.Validate(); err != nil {
			return nil, fmt.Errorf("validate outbound %q: %w", item.ID, err)
		}
		if _, exists := result[item.ID]; exists {
			return nil, fmt.Errorf("duplicate outbound %q", item.ID)
		}
		result[item.ID] = item
	}
	return result, nil
}

func resolveSource(route domain.Route, interfaces map[string]inventory.Interface, protected []netip.Prefix) (string, netip.Prefix, error) {
	if route.Source.Kind == domain.RouteSourceInterface {
		item, exists := interfaces[route.Source.Interface]
		if !exists || item.State != "up" || item.Name == "lo" {
			return "", netip.Prefix{}, fmt.Errorf("route %q source interface is missing, down, or loopback", route.ID)
		}
		for _, address := range interfacePrefixes(item) {
			if overlapsAny(address, protected) {
				return "", netip.Prefix{}, fmt.Errorf("route %q source interface carries a protected management prefix", route.ID)
			}
		}
		return item.Name, netip.Prefix{}, nil
	}
	prefix := route.Source.Subnet
	if prefix.Addr().IsLoopback() || prefix.Addr().IsMulticast() || prefix.Addr().IsUnspecified() || overlapsAny(prefix, protected) {
		return "", netip.Prefix{}, fmt.Errorf("route %q source subnet is unsafe or overlaps protected management space", route.ID)
	}
	matches := []string{}
	for name, item := range interfaces {
		if item.State != "up" || name == "lo" {
			continue
		}
		for _, address := range interfacePrefixes(item) {
			if prefixesOverlap(address, prefix) {
				matches = append(matches, name)
				break
			}
		}
	}
	sort.Strings(matches)
	if len(matches) != 1 {
		return "", netip.Prefix{}, fmt.Errorf("route %q source subnet must map to exactly one active ingress interface", route.ID)
	}
	return matches[0], prefix, nil
}

func interfacePrefixes(item inventory.Interface) []netip.Prefix {
	result := []netip.Prefix{}
	for _, address := range item.Addresses {
		if prefix, err := netip.ParsePrefix(address.CIDR); err == nil {
			result = append(result, prefix.Masked())
		}
	}
	return result
}

func selectorsOverlap(left, right routeSelector) bool {
	if left.ingress != right.ingress {
		return false
	}
	if !left.prefix.IsValid() || !right.prefix.IsValid() {
		return true
	}
	return prefixesOverlap(left.prefix, right.prefix)
}

func prefixesOverlap(left, right netip.Prefix) bool {
	return left.IsValid() && right.IsValid() && left.Addr().BitLen() == right.Addr().BitLen() && (left.Contains(right.Addr()) || right.Contains(left.Addr()))
}

func overlapsAny(candidate netip.Prefix, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefixesOverlap(candidate, prefix) {
			return true
		}
	}
	return false
}

func endpointAddresses(outbound domain.Outbound, resolved ResolvedEndpoints) ([]netip.Addr, error) {
	addresses := []netip.Addr{}
	if address, err := netip.ParseAddr(outbound.Server.Host); err == nil {
		addresses = append(addresses, address.Unmap())
	} else {
		addresses = append(addresses, resolved[outbound.ID]...)
	}
	if len(addresses) == 0 || len(addresses) > 16 {
		return nil, fmt.Errorf("outbound endpoint hostname must resolve to between 1 and 16 addresses")
	}
	for index, address := range addresses {
		addresses[index] = address.Unmap()
		if !addresses[index].IsValid() || addresses[index].IsUnspecified() || addresses[index].IsLoopback() || addresses[index].IsMulticast() {
			return nil, fmt.Errorf("outbound endpoint resolved to an unsafe address")
		}
	}
	return normalizeAddresses(addresses), nil
}

func normalizeAddresses(input []netip.Addr) []netip.Addr {
	seen := map[netip.Addr]struct{}{}
	result := make([]netip.Addr, 0, len(input))
	for _, address := range input {
		address = address.Unmap()
		if _, exists := seen[address]; !exists {
			seen[address] = struct{}{}
			result = append(result, address)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Less(result[j]) })
	return result
}

func existingNumericTables(routes []inventory.Route) map[uint32]struct{} {
	result := map[uint32]struct{}{}
	for _, route := range routes {
		if value, err := strconv.ParseUint(route.Table, 10, 32); err == nil && value > 0 {
			result[uint32(value)] = struct{}{}
		}
	}
	return result
}

func existingPriorities(rules []inventory.PolicyRule) map[uint32]struct{} {
	result := map[uint32]struct{}{}
	for _, rule := range rules {
		if rule.Priority > 0 {
			result[uint32(rule.Priority)] = struct{}{}
		}
	}
	return result
}

func releaseOwnedResources(previous []RouteIntent, host inventory.Inventory, tables, priorities map[uint32]struct{}, prefixes *[]netip.Prefix) {
	ownedInterfaces := make(map[string]struct{}, len(previous))
	for _, intent := range previous {
		table := strconv.FormatUint(uint64(intent.RoutingTable), 10)
		foreignTable := false
		for _, route := range host.Routes {
			if route.Table == table && route.Protocol != OwnedRouteProtocol {
				foreignTable = true
				break
			}
		}
		if !foreignTable {
			delete(tables, intent.RoutingTable)
		}
		foreignPriority := false
		for _, rule := range host.PolicyRules {
			if rule.Priority == int(intent.RulePriority) && rule.Protocol != OwnedRouteProtocol {
				foreignPriority = true
				break
			}
		}
		if !foreignPriority {
			delete(priorities, intent.RulePriority)
		}
		ownedInterfaces[intent.TunnelInterface] = struct{}{}
	}
	filtered := (*prefixes)[:0]
	for _, prefix := range *prefixes {
		owned := false
		for _, item := range host.Interfaces {
			if _, exists := ownedInterfaces[item.Name]; !exists {
				continue
			}
			for _, address := range interfacePrefixes(item) {
				if address == prefix {
					owned = true
					break
				}
			}
		}
		if !owned {
			filtered = append(filtered, prefix)
		}
	}
	*prefixes = filtered
}

func existingPrefixes(host inventory.Inventory) []netip.Prefix {
	result := []netip.Prefix{}
	for _, item := range host.Interfaces {
		result = append(result, interfacePrefixes(item)...)
	}
	for _, route := range host.Routes {
		if prefix, err := netip.ParsePrefix(route.Destination); err == nil {
			result = append(result, prefix.Masked())
		}
	}
	return result
}

func allocatePolicySlot(id domain.ID, settings Settings, usedTables, usedPriorities map[uint32]struct{}, usedPrefixes []netip.Prefix) (uint32, uint32, uint32, error) {
	digest := sha256.Sum256([]byte(id))
	start := uint32(digest[0])<<8 | uint32(digest[1])
	for offset := uint32(0); offset < MaximumRoutes; offset++ {
		slot := (start + offset) % MaximumRoutes
		table := settings.TableBase + slot
		priority := settings.RulePriorityBase + slot
		if _, exists := usedTables[table]; exists {
			continue
		}
		if _, exists := usedPriorities[priority]; exists {
			continue
		}
		addresses := slotTunnelAddresses(slot)
		if overlapsAny(addresses[0], usedPrefixes) || overlapsAny(addresses[1], usedPrefixes) {
			continue
		}
		usedTables[table] = struct{}{}
		usedPriorities[priority] = struct{}{}
		return table, priority, slot, nil
	}
	return 0, 0, 0, fmt.Errorf("no free project-owned routing policy slot remains")
}

func slotTunnelAddresses(slot uint32) []netip.Prefix {
	ipv4 := netip.AddrFrom4([4]byte{198, 18, byte(slot / 64), byte(slot%64*4 + 1)})
	ipv6 := netip.AddrFrom16([16]byte{0xfd, 0x45, 0x47, 0x4d, byte(slot >> 8), byte(slot), 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
	return []netip.Prefix{netip.PrefixFrom(ipv4, 30), netip.PrefixFrom(ipv6, 126)}
}

func tunnelName(id domain.ID) string {
	digest := sha256.Sum256([]byte(id))
	return "egm" + hex.EncodeToString(digest[:4])
}

func stateHash(content []byte, exists bool) string {
	prefix := []byte("absent\x00")
	if exists {
		prefix = []byte("present\x00")
	}
	return hash(append(prefix, content...))
}

func hash(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
