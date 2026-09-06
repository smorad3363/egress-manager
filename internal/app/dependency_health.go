package app

import (
	"context"
	"fmt"
	"time"

	"github.com/egress-manager/egress-manager/internal/config"
	"github.com/egress-manager/egress-manager/internal/domain"
	managedHAProxy "github.com/egress-manager/egress-manager/internal/haproxy"
	managedInterface "github.com/egress-manager/egress-manager/internal/interfaceoutbound"
	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/reliability"
	"github.com/egress-manager/egress-manager/internal/routeengine"
	"github.com/egress-manager/egress-manager/internal/routing"
	managedSingBox "github.com/egress-manager/egress-manager/internal/singbox"
	"github.com/egress-manager/egress-manager/internal/system"
	managedXray "github.com/egress-manager/egress-manager/internal/xray"
)

type dependencyMonitorOptions struct {
	configuration     config.Config
	runner            system.Runner
	collector         inventory.Collector
	haproxyRuntime    managedHAProxy.Runtime
	interfaceExecutor managedInterface.Executor
	routeExecutor     routeengine.Executor
	loadInterfacePlan func(context.Context) (managedInterface.ExecutionPlan, error)
	discoverXray      func(context.Context) (managedXray.Report, error)
}

func newDependencyMonitor(options dependencyMonitorOptions) (*reliability.Monitor, error) {
	if options.runner == nil || options.haproxyRuntime == nil || options.loadInterfacePlan == nil || options.discoverXray == nil {
		return nil, fmt.Errorf("dependency monitor sources are required")
	}
	monitor := &reliability.Monitor{Timeout: 5 * time.Second, Interval: 30 * time.Second}
	monitor.Probes = []reliability.DependencyProbe{
		{Name: "system_tools", Check: func(ctx context.Context) (reliability.DependencyObservation, error) {
			ipAvailable := healthCommand(ctx, options.runner, system.Command{Name: "ip", Args: []string{"-Version"}})
			nftAvailable := healthCommand(ctx, options.runner, system.Command{Name: "nft", Args: []string{"--version"}})
			iptablesAvailable := healthCommand(ctx, options.runner, system.Command{Name: "iptables", Args: []string{"--version"}})
			observation := dependencyObservation(domain.HealthHealthy, "available")
			observation.ExecutableAvailable = domain.ProbePassed
			if !ipAvailable || !nftAvailable && !iptablesAvailable {
				observation.Status = domain.HealthUnhealthy
				observation.ExecutableAvailable = domain.ProbeFailed
				observation.Detail = "required_tools_unavailable"
			}
			return observation, nil
		}},
		{Name: "haproxy", Check: func(ctx context.Context) (reliability.DependencyObservation, error) {
			state, err := managedHAProxy.InspectState(options.configuration.HAProxyConfigPath)
			if err != nil {
				return unhealthyDependency("configuration_invalid", domain.ProbeFailed), nil
			}
			if !state.Exists {
				return disabledDependency("not_configured"), nil
			}
			observation := dependencyObservation(domain.HealthHealthy, "healthy")
			if !healthCommand(ctx, options.runner, system.Command{Name: "haproxy", Args: []string{"-vv"}}) {
				observation.Status = domain.HealthUnhealthy
				observation.ExecutableAvailable = domain.ProbeFailed
				observation.Detail = "required_tool_unavailable"
				return observation, nil
			}
			observation.ExecutableAvailable = domain.ProbePassed
			if !healthCommand(ctx, options.runner, system.Command{Name: "haproxy", Args: []string{"-c", "-f", options.configuration.HAProxyConfigPath}}) {
				observation.Status = domain.HealthUnhealthy
				observation.ConfigurationValid = domain.ProbeFailed
				observation.Detail = "configuration_invalid"
				return observation, nil
			}
			observation.ConfigurationValid = domain.ProbePassed
			runtime, err := options.haproxyRuntime.Snapshot(ctx)
			if err != nil || runtime.Info.PID < 1 {
				observation.Status = domain.HealthUnhealthy
				observation.ProcessRunning = domain.ProbeFailed
				observation.TransportReachable = domain.ProbeFailed
				observation.Detail = "runtime_unavailable"
				return observation, nil
			}
			observation.ProcessRunning = domain.ProbePassed
			observation.TransportReachable = domain.ProbePassed
			return observation, nil
		}},
		{Name: "singbox", Check: func(ctx context.Context) (reliability.DependencyObservation, error) {
			state, err := managedSingBox.InspectState(options.configuration.SingBoxConfigPath)
			if err != nil {
				return unhealthyDependency("configuration_invalid", domain.ProbeFailed), nil
			}
			if !state.Exists {
				return disabledDependency("not_configured"), nil
			}
			observation := dependencyObservation(domain.HealthHealthy, "healthy")
			if !healthCommand(ctx, options.runner, system.Command{Name: "sing-box", Args: []string{"version"}}) {
				observation.Status = domain.HealthUnhealthy
				observation.ExecutableAvailable = domain.ProbeFailed
				observation.Detail = "required_tool_unavailable"
				return observation, nil
			}
			observation.ExecutableAvailable = domain.ProbePassed
			if !healthCommand(ctx, options.runner, system.Command{Name: "sing-box", Args: []string{"check", "-c", options.configuration.SingBoxConfigPath}}) {
				observation.Status = domain.HealthUnhealthy
				observation.ConfigurationValid = domain.ProbeFailed
				observation.Detail = "configuration_invalid"
				return observation, nil
			}
			observation.ConfigurationValid = domain.ProbePassed
			if !healthCommand(ctx, options.runner, system.Command{Name: "systemctl", Args: []string{"is-active", "--quiet", "egress-manager-sing-box.service"}}) {
				observation.Status = domain.HealthUnhealthy
				observation.ProcessRunning = domain.ProbeFailed
				observation.Detail = "process_inactive"
				return observation, nil
			}
			observation.ProcessRunning = domain.ProbePassed
			return observation, nil
		}},
		{Name: "native_interfaces", Check: func(ctx context.Context) (reliability.DependencyObservation, error) {
			state, err := managedInterface.InspectState(options.configuration.InterfaceStatePath, options.configuration.InterfaceRuntimeDirectory)
			if err != nil {
				return unhealthyDependency("configuration_invalid", domain.ProbeFailed), nil
			}
			if len(state.Entries) == 0 {
				return disabledDependency("not_configured"), nil
			}
			observation := dependencyObservation(domain.HealthHealthy, "healthy")
			requiredTools := make(map[string]system.Command, 2)
			for _, entry := range state.Entries {
				command := system.Command{Name: "wg", Args: []string{"--version"}}
				if entry.Kind == domain.OutboundOpenVPN {
					command = system.Command{Name: "openvpn", Args: []string{"--version"}}
				}
				requiredTools[command.Name] = command
			}
			for _, command := range requiredTools {
				if !healthCommand(ctx, options.runner, command) {
					return unhealthyDependency("required_tool_unavailable", domain.ProbeFailed), nil
				}
			}
			observation.ExecutableAvailable = domain.ProbePassed
			plan, err := options.loadInterfacePlan(ctx)
			if err != nil {
				return unhealthyDependency("configuration_invalid", domain.ProbeFailed), nil
			}
			observation.ConfigurationValid = domain.ProbePassed
			if err := options.interfaceExecutor.VerifyRuntime(ctx, plan); err != nil {
				observation.Status = domain.HealthUnhealthy
				observation.ProcessRunning = domain.ProbeFailed
				observation.TransportReachable = domain.ProbeFailed
				observation.Detail = "runtime_unavailable"
				return observation, nil
			}
			observation.ProcessRunning = domain.ProbePassed
			observation.TransportReachable = domain.ProbeUntestable
			for _, entry := range state.Entries {
				if entry.Kind != domain.OutboundWireGuard {
					continue
				}
				result, ok := healthCommandResult(ctx, options.runner, system.Command{Name: "wg", Args: []string{"show", entry.InterfaceName, "latest-handshakes"}})
				if !ok {
					observation.Status = domain.HealthUnhealthy
					observation.TransportReachable = domain.ProbeFailed
					observation.Detail = "transport_probe_failed"
					return observation, nil
				}
				if managedInterface.HasHandshakeEvidence(result.Stdout) {
					observation.TransportReachable = domain.ProbePassed
				}
			}
			return observation, nil
		}},
		{Name: "routing", Check: func(ctx context.Context) (reliability.DependencyObservation, error) {
			bypass, err := routeengine.InspectBypassState(options.configuration.BypassStatePath)
			if err != nil {
				return unhealthyDependency("bypass_state_invalid", domain.ProbeFailed), nil
			}
			if bypass.Active {
				return disabledDependency("bypass_active"), nil
			}
			state, err := routing.InspectState(options.configuration.RoutingStatePath)
			if err != nil {
				return unhealthyDependency("configuration_invalid", domain.ProbeFailed), nil
			}
			if !state.Exists || len(state.Routes) == 0 {
				return disabledDependency("not_configured"), nil
			}
			host, err := options.collector.Collect(ctx)
			if err != nil {
				return unhealthyDependency("inventory_unavailable", domain.ProbeUnknown), nil
			}
			plan, exists, err := options.routeExecutor.BuildAppliedPlan(ctx, host, options.configuration.ProtectedManagementCIDRs)
			if err != nil || !exists {
				return unhealthyDependency("configuration_invalid", domain.ProbeFailed), nil
			}
			observation := dependencyObservation(domain.HealthHealthy, "healthy")
			observation.ExecutableAvailable = domain.ProbePassed
			observation.ConfigurationValid = domain.ProbePassed
			if err := options.routeExecutor.VerifyRuntime(ctx, plan); err != nil {
				observation.Status = domain.HealthUnhealthy
				observation.ProcessRunning = domain.ProbeFailed
				observation.TransportReachable = domain.ProbeFailed
				observation.Detail = "runtime_unavailable"
				return observation, nil
			}
			observation.ProcessRunning = domain.ProbePassed
			observation.TransportReachable = domain.ProbePassed
			return observation, nil
		}},
		{Name: "xray", Check: func(ctx context.Context) (reliability.DependencyObservation, error) {
			report, err := options.discoverXray(ctx)
			if err != nil {
				return unhealthyDependency("discovery_failed", domain.ProbeUnknown), nil
			}
			managed := make([]managedXray.Installation, 0, 1)
			for _, installation := range report.Installations {
				if installation.MutationStrategy == managedXray.ManagedFragment {
					managed = append(managed, installation)
				}
			}
			if len(managed) == 0 {
				return disabledDependency("not_managed"), nil
			}
			if len(managed) != 1 {
				return unhealthyDependency("ambiguous_installation", domain.ProbeFailed), nil
			}
			installation := managed[0]
			observation := dependencyObservation(domain.HealthHealthy, "healthy")
			if installation.XrayExecutable == "" {
				observation.Status = domain.HealthUnhealthy
				observation.ExecutableAvailable = domain.ProbeFailed
				observation.Detail = "required_tool_unavailable"
				return observation, nil
			}
			observation.ExecutableAvailable = domain.ProbePassed
			if err := (managedXray.FragmentExecutor{Runner: options.runner, Installation: installation}).ValidateRuntimeConfiguration(ctx); err != nil {
				return unhealthyDependency("configuration_invalid", domain.ProbeFailed), nil
			}
			observation.ConfigurationValid = domain.ProbePassed
			if !installation.ServiceActive {
				observation.Status = domain.HealthUnhealthy
				observation.ProcessRunning = domain.ProbeFailed
				observation.Detail = "process_inactive"
				return observation, nil
			}
			observation.ProcessRunning = domain.ProbePassed
			return observation, nil
		}},
	}
	if err := monitor.Validate(); err != nil {
		return nil, err
	}
	return monitor, nil
}

func dependencyObservation(status domain.HealthStatus, detail string) reliability.DependencyObservation {
	observation := reliability.UnknownDependencyObservation()
	observation.Status = status
	observation.Detail = detail
	observation.InternetReachable = domain.ProbeUntestable
	return observation
}

func disabledDependency(detail string) reliability.DependencyObservation {
	return dependencyObservation(domain.HealthDisabled, detail)
}

func unhealthyDependency(detail string, configuration domain.ProbeStatus) reliability.DependencyObservation {
	observation := dependencyObservation(domain.HealthUnhealthy, detail)
	observation.ConfigurationValid = configuration
	return observation
}

func healthCommand(ctx context.Context, runner system.Runner, command system.Command) bool {
	_, ok := healthCommandResult(ctx, runner, command)
	return ok
}

func healthCommandResult(ctx context.Context, runner system.Runner, command system.Command) (system.Result, bool) {
	result, err := runner.Run(ctx, command)
	return result, err == nil && result.ExitCode == 0
}
