package inventory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egress-manager/egress-manager/internal/system"
)

type FileReader interface {
	ReadFile(string) ([]byte, error)
}

type OSFiles struct{}

func (OSFiles) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

type Collector struct {
	Runner  system.Runner
	Files   FileReader
	Timeout time.Duration
	Now     func() time.Time
}

type probe struct {
	name    string
	command system.Command
}

var capabilityProbes = []probe{
	{name: "nftables", command: system.Command{Name: "nft", Args: []string{"--version"}}},
	{name: "iptables", command: system.Command{Name: "iptables", Args: []string{"--version"}}},
	{name: "HAProxy", command: system.Command{Name: "haproxy", Args: []string{"-vv"}}},
	{name: "sing-box", command: system.Command{Name: "sing-box", Args: []string{"version"}}},
	{name: "Xray", command: system.Command{Name: "xray", Args: []string{"version"}}},
	{name: "OpenVPN", command: system.Command{Name: "openvpn", Args: []string{"--version"}}},
	{name: "WireGuard", command: system.Command{Name: "wg", Args: []string{"--version"}}},
}

func (collector Collector) Collect(ctx context.Context) (Inventory, error) {
	if collector.Runner == nil || collector.Files == nil {
		return Inventory{}, fmt.Errorf("inventory runner and file reader are required")
	}
	timeout := collector.Timeout
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 2 * time.Second
	}
	now := collector.Now
	if now == nil {
		now = time.Now
	}
	result := Inventory{
		GeneratedAt:  now().UTC(),
		Interfaces:   []Interface{},
		Routes:       []Route{},
		PolicyRules:  []PolicyRule{},
		Listeners:    []Listener{},
		DNS:          DNSState{Source: "/etc/resolv.conf", Servers: []string{}, SearchDomains: []string{}},
		Capabilities: []Capability{},
		Warnings:     []string{},
	}

	if output, err := collector.run(ctx, timeout, system.Command{Name: "ip", Args: []string{"-j", "address", "show"}}); err != nil {
		result.Warnings = append(result.Warnings, "interface inventory unavailable")
	} else if parsed, err := parseInterfaces(output); err != nil {
		result.Warnings = append(result.Warnings, "interface inventory malformed")
	} else {
		result.Interfaces = parsed
	}
	if output, err := collector.run(ctx, timeout, system.Command{Name: "ip", Args: []string{"-j", "route", "show", "table", "all"}}); err != nil {
		result.Warnings = append(result.Warnings, "route inventory unavailable")
	} else if parsed, err := parseRoutes(output); err != nil {
		result.Warnings = append(result.Warnings, "route inventory malformed")
	} else {
		result.Routes = parsed
	}
	if output, err := collector.run(ctx, timeout, system.Command{Name: "ip", Args: []string{"-j", "rule", "show"}}); err != nil {
		result.Warnings = append(result.Warnings, "policy rule inventory unavailable")
	} else if parsed, err := parsePolicyRules(output); err != nil {
		result.Warnings = append(result.Warnings, "policy rule inventory malformed")
	} else {
		result.PolicyRules = parsed
	}
	if output, err := collector.run(ctx, timeout, system.Command{Name: "ip", Args: []string{"-j", "-6", "route", "show", "table", "all"}}); err != nil {
		result.Warnings = append(result.Warnings, "IPv6 route inventory unavailable")
	} else if parsed, err := parseRoutes(output); err != nil || len(result.Routes)+len(parsed) > maximumRoutes {
		result.Warnings = append(result.Warnings, "IPv6 route inventory malformed or oversized")
	} else {
		result.Routes = append(result.Routes, parsed...)
	}
	if output, err := collector.run(ctx, timeout, system.Command{Name: "ip", Args: []string{"-j", "-6", "rule", "show"}}); err != nil {
		result.Warnings = append(result.Warnings, "IPv6 policy rule inventory unavailable")
	} else if parsed, err := parsePolicyRules(output); err != nil || len(result.PolicyRules)+len(parsed) > maximumPolicyRules {
		result.Warnings = append(result.Warnings, "IPv6 policy rule inventory malformed or oversized")
	} else {
		result.PolicyRules = append(result.PolicyRules, parsed...)
	}
	if output, err := collector.run(ctx, timeout, system.Command{Name: "ss", Args: []string{"-H", "-lntu", "-p"}}); err != nil {
		result.Warnings = append(result.Warnings, "listener inventory unavailable")
	} else if parsed, err := parseListeners(output); err != nil {
		result.Warnings = append(result.Warnings, "listener inventory malformed")
	} else {
		result.Listeners = parsed
	}

	if resolvConf, err := collector.Files.ReadFile("/etc/resolv.conf"); err != nil {
		result.Warnings = append(result.Warnings, "DNS inventory unavailable")
	} else {
		result.DNS = parseDNS(resolvConf)
	}

	processOutput, processErr := collector.run(ctx, timeout, system.Command{Name: "ps", Args: []string{"-eo", "comm=,args="}})
	result.Capabilities = collector.collectCapabilities(ctx, timeout, processOutput, processErr == nil)
	if len(result.Capabilities) > maximumCapabilities {
		return Inventory{}, fmt.Errorf("capability inventory exceeds %d records", maximumCapabilities)
	}
	sort.Strings(result.Warnings)
	return result, nil
}

func (collector Collector) run(ctx context.Context, timeout time.Duration, command system.Command) ([]byte, error) {
	probeContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := collector.Runner.Run(probeContext, command)
	if err != nil || result.ExitCode != 0 {
		return nil, errors.Join(err, fmt.Errorf("probe exited with code %d", result.ExitCode))
	}
	return result.Stdout, nil
}

func (collector Collector) collectCapabilities(ctx context.Context, timeout time.Duration, processOutput []byte, processAvailable bool) []Capability {
	capabilities := make([]Capability, len(capabilityProbes))
	var waitGroup sync.WaitGroup
	for index, item := range capabilityProbes {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			output, err := collector.run(ctx, timeout, item.command)
			capability := Capability{Name: item.name, Available: err == nil}
			if err == nil {
				capability.Version = firstLine(output)
			}
			if item.name == "iptables" {
				lower := strings.ToLower(capability.Version)
				switch {
				case strings.Contains(lower, "nf_tables"):
					capability.Backend = "nf_tables"
				case strings.Contains(lower, "legacy"):
					capability.Backend = "legacy"
				}
			}
			capabilities[index] = capability
		}()
	}
	waitGroup.Wait()

	processes := ""
	if processAvailable {
		processes = strings.ToLower(string(processOutput))
	}
	processIndicators := map[string][]string{
		"HAProxy":   {"haproxy"},
		"sing-box":  {"sing-box"},
		"Xray":      {"xray"},
		"OpenVPN":   {"openvpn"},
		"WireGuard": {"wireguard", "wg-quick"},
		"Marzban":   {"marzban"},
		"3x-ui":     {"3x-ui", "x-ui"},
	}
	byName := make(map[string]int, len(capabilities))
	for index := range capabilities {
		byName[capabilities[index].Name] = index
	}
	for name, indicators := range processIndicators {
		running := false
		for _, indicator := range indicators {
			if containsProcessIndicator(processes, indicator) {
				running = true
				break
			}
		}
		if index, exists := byName[name]; exists {
			capabilities[index].Running = running
			capabilities[index].Available = capabilities[index].Available || running
		} else {
			capabilities = append(capabilities, Capability{Name: name, Available: running, Running: running})
		}
	}
	sort.Slice(capabilities, func(i, j int) bool {
		return strings.ToLower(capabilities[i].Name) < strings.ToLower(capabilities[j].Name)
	})
	return capabilities
}

func firstLine(output []byte) string {
	line := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
	if len(line) > 160 {
		line = line[:160]
	}
	return line
}

func containsProcessIndicator(processes, indicator string) bool {
	for _, line := range strings.Split(processes, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		for _, field := range fields {
			normalized := strings.ReplaceAll(field, "\\", "/")
			for _, component := range strings.Split(normalized, "/") {
				if component == indicator || strings.HasPrefix(component, indicator+"-") {
					return true
				}
			}
		}
	}
	return false
}
