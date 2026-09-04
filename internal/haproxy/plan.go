// Package haproxy plans and operates the project-owned HAProxy instance.
package haproxy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

const (
	MaximumFrontends = 128
	MaximumBackends  = 256
	maximumCandidate = 128 << 10
)

type Settings struct {
	RuntimeSocket  string
	PIDFile        string
	MaxConnections uint32
	ConnectTimeout time.Duration
	ClientTimeout  time.Duration
	ServerTimeout  time.Duration
	ProtectedPorts []uint16
}

type State struct {
	Exists   bool                `json:"exists"`
	Hash     string              `json:"hash"`
	Bindings []inventory.Binding `json:"bindings"`
}

type Action struct {
	Kind     string `json:"kind"`
	Resource string `json:"resource"`
	Summary  string `json:"summary"`
}

type Plan struct {
	Engine           string   `json:"engine"`
	StateHash        string   `json:"state_hash"`
	EnabledFrontends int      `json:"enabled_frontends"`
	EnabledBackends  int      `json:"enabled_backends"`
	Actions          []Action `json:"actions"`
	Candidate        string   `json:"candidate"`
}

func ParseState(content []byte, exists bool) (State, error) {
	if !exists {
		if len(content) != 0 {
			return State{}, fmt.Errorf("absent HAProxy state contains data")
		}
		return State{Hash: hashState(nil, false), Bindings: []inventory.Binding{}}, nil
	}
	if len(content) == 0 || len(content) > maximumCandidate {
		return State{}, fmt.Errorf("owned HAProxy configuration is empty or oversized")
	}
	bindings := make([]inventory.Binding, 0)
	inOwnedFrontend := false
	for _, rawLine := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "frontend" {
			inOwnedFrontend = strings.HasPrefix(fields[1], "egm_fe_")
			continue
		}
		if len(fields) >= 2 && (fields[0] == "backend" || fields[0] == "global" || fields[0] == "defaults") {
			inOwnedFrontend = false
		}
		if inOwnedFrontend && len(fields) == 2 && fields[0] == "bind" {
			host, portText, err := net.SplitHostPort(fields[1])
			if err != nil {
				return State{}, fmt.Errorf("parse owned HAProxy bind: %w", err)
			}
			address, err := netip.ParseAddr(host)
			port, portErr := strconv.ParseUint(portText, 10, 16)
			if err != nil || portErr != nil || port == 0 {
				return State{}, fmt.Errorf("parse owned HAProxy bind")
			}
			bindings = append(bindings, inventory.Binding{Protocol: "tcp", Address: address.String(), Port: uint16(port)})
			if len(bindings) > MaximumFrontends {
				return State{}, fmt.Errorf("owned HAProxy configuration has too many frontends")
			}
		}
	}
	return State{Exists: true, Hash: hashState(content, true), Bindings: bindings}, nil
}

func BuildPlan(settings Settings, frontends []domain.HAProxyFrontend, backends []domain.HAProxyBackend, listeners []inventory.Listener, state State) (Plan, error) {
	if err := settings.validate(); err != nil {
		return Plan{}, err
	}
	if state.Hash == "" || state.Bindings == nil {
		return Plan{}, fmt.Errorf("HAProxy state is required")
	}
	if len(frontends) > MaximumFrontends || len(backends) > MaximumBackends {
		return Plan{}, fmt.Errorf("HAProxy desired state exceeds frontend or backend limits")
	}
	backendByID := make(map[domain.ID]domain.HAProxyBackend, len(backends))
	normalizedBackends := append([]domain.HAProxyBackend{}, backends...)
	for _, backend := range normalizedBackends {
		if err := backend.Validate(); err != nil {
			return Plan{}, fmt.Errorf("validate HAProxy backend %q: %w", backend.ID, err)
		}
		if _, exists := backendByID[backend.ID]; exists {
			return Plan{}, fmt.Errorf("duplicate HAProxy backend %q", backend.ID)
		}
		backendByID[backend.ID] = backend
	}
	sort.Slice(normalizedBackends, func(i, j int) bool { return normalizedBackends[i].ID < normalizedBackends[j].ID })
	normalizedFrontends := append([]domain.HAProxyFrontend{}, frontends...)
	seenFrontends := map[domain.ID]struct{}{}
	desiredListeners := make([]inventory.Listener, 0, len(frontends))
	protected := map[uint16]struct{}{}
	for _, port := range settings.ProtectedPorts {
		protected[port] = struct{}{}
	}
	for _, frontend := range normalizedFrontends {
		if err := frontend.Validate(); err != nil {
			return Plan{}, fmt.Errorf("validate HAProxy frontend %q: %w", frontend.ID, err)
		}
		if _, exists := seenFrontends[frontend.ID]; exists {
			return Plan{}, fmt.Errorf("duplicate HAProxy frontend %q", frontend.ID)
		}
		seenFrontends[frontend.ID] = struct{}{}
		enabledBackends := 0
		for _, backendID := range frontend.BackendIDs {
			backend, exists := backendByID[backendID]
			if !exists {
				return Plan{}, fmt.Errorf("HAProxy frontend %q references missing backend %q", frontend.ID, backendID)
			}
			if backend.Enabled {
				enabledBackends++
			}
		}
		if frontend.Enabled && enabledBackends == 0 {
			return Plan{}, fmt.Errorf("HAProxy frontend %q has no enabled backend", frontend.ID)
		}
		if !frontend.Enabled {
			continue
		}
		if _, exists := protected[frontend.Port]; exists {
			return Plan{}, fmt.Errorf("HAProxy frontend %q captures protected port %d", frontend.ID, frontend.Port)
		}
		binding := inventory.Binding{Protocol: "tcp", Address: frontend.Bind.String(), Port: frontend.Port}
		if conflicts, err := inventory.DetectConflicts([]inventory.Binding{binding}, desiredListeners); err != nil {
			return Plan{}, err
		} else if len(conflicts) > 0 {
			return Plan{}, fmt.Errorf("HAProxy frontends overlap on %s:%d", frontend.Bind, frontend.Port)
		}
		desiredListeners = append(desiredListeners, inventory.Listener{Protocol: "tcp", Address: frontend.Bind.String(), Port: frontend.Port})
		filtered := filterOwnedListeners(listeners, state.Bindings)
		if conflicts, err := inventory.DetectConflicts([]inventory.Binding{binding}, filtered); err != nil {
			return Plan{}, err
		} else if len(conflicts) > 0 {
			return Plan{}, fmt.Errorf("HAProxy frontend %q conflicts with listener on %s:%d", frontend.ID, conflicts[0].Existing.Address, frontend.Port)
		}
	}
	sort.Slice(normalizedFrontends, func(i, j int) bool { return normalizedFrontends[i].ID < normalizedFrontends[j].ID })
	plan := Plan{Engine: "haproxy", StateHash: state.Hash, Actions: []Action{}}
	if state.Exists {
		plan.Actions = append(plan.Actions, Action{Kind: "replace", Resource: "owned HAProxy configuration", Summary: "Atomically replace the project-owned HAProxy configuration."})
	} else {
		plan.Actions = append(plan.Actions, Action{Kind: "create", Resource: "owned HAProxy configuration", Summary: "Create the project-owned HAProxy configuration."})
	}
	for _, backend := range normalizedBackends {
		if backend.Enabled {
			plan.EnabledBackends++
		}
	}
	for _, frontend := range normalizedFrontends {
		if frontend.Enabled {
			plan.EnabledFrontends++
			plan.Actions = append(plan.Actions, Action{Kind: "frontend", Resource: string(frontend.ID), Summary: "Configure a TCP frontend and backend pool."})
		}
	}
	plan.Candidate = render(settings, normalizedFrontends, backendByID)
	if len(plan.Candidate) > maximumCandidate {
		return Plan{}, fmt.Errorf("HAProxy candidate exceeds %d bytes", maximumCandidate)
	}
	return plan, nil
}

func (settings Settings) validate() error {
	var errs []error
	if !strings.HasPrefix(settings.RuntimeSocket, "/") || strings.ContainsAny(settings.RuntimeSocket, "\n\r\t ") {
		errs = append(errs, fmt.Errorf("HAProxy runtime socket path must be absolute and contain no whitespace"))
	}
	if !strings.HasPrefix(settings.PIDFile, "/") || strings.ContainsAny(settings.PIDFile, "\n\r\t ") {
		errs = append(errs, fmt.Errorf("HAProxy PID file path must be absolute and contain no whitespace"))
	}
	if settings.MaxConnections < 1 || settings.MaxConnections > 1_000_000 {
		errs = append(errs, fmt.Errorf("HAProxy max connections must be between 1 and 1000000"))
	}
	for _, timeout := range []time.Duration{settings.ConnectTimeout, settings.ClientTimeout, settings.ServerTimeout} {
		if timeout < time.Millisecond || timeout > 24*time.Hour {
			errs = append(errs, fmt.Errorf("HAProxy timeouts must be between 1ms and 24h"))
		}
	}
	seen := map[uint16]struct{}{}
	for _, port := range settings.ProtectedPorts {
		if port == 0 {
			errs = append(errs, fmt.Errorf("HAProxy protected ports must be nonzero"))
		}
		if _, exists := seen[port]; exists {
			errs = append(errs, fmt.Errorf("duplicate HAProxy protected port %d", port))
		}
		seen[port] = struct{}{}
	}
	return errors.Join(errs...)
}

func render(settings Settings, frontends []domain.HAProxyFrontend, backends map[domain.ID]domain.HAProxyBackend) string {
	var output strings.Builder
	output.WriteString("# Egress Manager owned HAProxy configuration\n")
	output.WriteString("global\n  master-worker\n  maxconn " + strconv.FormatUint(uint64(settings.MaxConnections), 10) + "\n  stats socket " + settings.RuntimeSocket + " mode 660 level operator\n\n")
	output.WriteString("defaults\n  mode tcp\n  timeout connect " + duration(settings.ConnectTimeout) + "\n  timeout client " + duration(settings.ClientTimeout) + "\n  timeout server " + duration(settings.ServerTimeout) + "\n\n")
	for _, frontend := range frontends {
		if !frontend.Enabled {
			continue
		}
		output.WriteString("frontend egm_fe_" + string(frontend.ID) + "\n")
		output.WriteString("  bind " + net.JoinHostPort(frontend.Bind.String(), strconv.Itoa(int(frontend.Port))) + "\n")
		output.WriteString("  default_backend egm_pool_" + string(frontend.ID) + "\n\n")
		output.WriteString("backend egm_pool_" + string(frontend.ID) + "\n")
		output.WriteString("  balance " + string(frontend.Algorithm) + "\n")
		for _, backendID := range frontend.BackendIDs {
			backend := backends[backendID]
			output.WriteString("  server egm_srv_" + string(backend.ID) + " " + backend.Server.String() + " weight " + strconv.Itoa(int(backend.Weight)))
			if backend.HealthCheck {
				output.WriteString(" check")
			}
			if backend.Backup {
				output.WriteString(" backup")
			}
			if !backend.Enabled {
				output.WriteString(" disabled")
			}
			output.WriteByte('\n')
		}
		output.WriteByte('\n')
	}
	output.WriteString("# pidfile is supplied by the service command: " + settings.PIDFile + "\n")
	return output.String()
}

func filterOwnedListeners(listeners []inventory.Listener, bindings []inventory.Binding) []inventory.Listener {
	filtered := make([]inventory.Listener, 0, len(listeners))
	for _, listener := range listeners {
		owned := false
		if strings.EqualFold(listener.Process, "haproxy") {
			for _, binding := range bindings {
				bindingAddress, bindingErr := netip.ParseAddr(binding.Address)
				listenerAddress, listenerErr := netip.ParseAddr(listener.Address)
				if bindingErr == nil && listenerErr == nil && bindingAddress.Unmap() == listenerAddress.Unmap() && binding.Port == listener.Port && strings.EqualFold(binding.Protocol, listener.Protocol) {
					owned = true
					break
				}
			}
		}
		if !owned {
			filtered = append(filtered, listener)
		}
	}
	return filtered
}

func duration(value time.Duration) string {
	if value%time.Second == 0 {
		return strconv.FormatInt(int64(value/time.Second), 10) + "s"
	}
	return strconv.FormatInt(value.Milliseconds(), 10) + "ms"
}

func hashState(content []byte, exists bool) string {
	hash := sha256.New()
	if exists {
		_, _ = hash.Write([]byte("present\x00"))
	} else {
		_, _ = hash.Write([]byte("absent\x00"))
	}
	_, _ = hash.Write(content)
	return hex.EncodeToString(hash.Sum(nil))
}
