package interfaceoutbound

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/system"
)

type Tester struct {
	Runner           system.Runner
	RuntimeDirectory string
	Timeout          time.Duration
	Now              func() time.Time
}

func (tester Tester) Test(ctx context.Context, outbound domain.Outbound, credentialBytes []byte, state State) domain.OutboundHealth {
	health := domain.UnknownOutboundHealth()
	checkedAt := tester.now()
	health.CheckedAt = &checkedAt
	health.InternetReachable = domain.ProbeUntestable
	health.TCP = capabilityStatus(outbound.Capabilities.TCP)
	health.UDP = capabilityStatus(outbound.Capabilities.UDP)
	if tester.Runner == nil || outbound.Adapter != domain.OutboundAdapterInterface {
		return failedHealth(health, "configuration_invalid")
	}
	credential, err := DecodeCredential(credentialBytes)
	if err != nil || credential.Kind != outbound.Type {
		return failedHealth(health, "configuration_invalid")
	}
	configuration, desired, err := desiredEntry(outbound, credential)
	if err != nil {
		return failedHealth(health, "configuration_invalid")
	}
	executor := Executor{Runner: tester.Runner, RuntimeDirectory: tester.RuntimeDirectory, Timeout: tester.Timeout}
	if err := executor.validateNative(ctx, []candidateEntry{{state: desired, credential: credential, config: configuration}}); err != nil {
		return failedHealth(health, "configuration_invalid")
	}
	health.ConfigurationValid = domain.ProbePassed
	var current *StateEntry
	for index := range state.Entries {
		if state.Entries[index].ID == outbound.ID {
			current = &state.Entries[index]
			break
		}
	}
	if current == nil || !equalStateEntry(*current, desired) {
		health.Status = domain.HealthDegraded
		health.Detail = "lifecycle_not_applied"
		return health
	}
	runtimeConfiguration, exists := state.configs[outbound.ID]
	if !exists || hashBytes(runtimeConfiguration) != current.ConfigHash || executor.verifyEntry(ctx, runtimeConfigSnapshot{Entry: *current, Content: runtimeConfiguration}) != nil {
		health.Status = domain.HealthUnhealthy
		health.TransportReachable = domain.ProbeFailed
		health.Detail = "lifecycle_unavailable"
		return health
	}
	if outbound.Type == domain.OutboundWireGuard {
		output, err := executor.output(ctx, system.Command{Name: "wg", Args: []string{"show", current.InterfaceName, "latest-handshakes"}})
		if err != nil {
			health.Status = domain.HealthUnhealthy
			health.TransportReachable = domain.ProbeFailed
			health.Detail = "lifecycle_unavailable"
			return health
		}
		if hasHandshake(output) {
			health.Status = domain.HealthHealthy
			health.TransportReachable = domain.ProbePassed
			health.Detail = "wireguard_handshake_observed"
			return health
		}
		health.Status = domain.HealthDegraded
		health.TransportReachable = domain.ProbeUntestable
		health.Detail = "wireguard_handshake_not_observed"
		return health
	}
	health.Status = domain.HealthDegraded
	health.TransportReachable = domain.ProbeUntestable
	health.Detail = "openvpn_service_active"
	return health
}

func failedHealth(health domain.OutboundHealth, detail string) domain.OutboundHealth {
	health.Status = domain.HealthUnhealthy
	health.ConfigurationValid = domain.ProbeFailed
	health.Detail = detail
	return health
}

func capabilityStatus(supported bool) domain.ProbeStatus {
	if supported {
		return domain.ProbeUntestable
	}
	return domain.ProbeUnsupported
}

func hasHandshake(output []byte) bool {
	if len(output) == 0 || len(output) > 4096 {
		return false
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return false
	}
	start := 0
	step := 1
	if len(fields)%2 == 0 {
		start = 1
		step = 2
	}
	for index := start; index < len(fields); index += step {
		value, err := strconv.ParseInt(fields[index], 10, 64)
		if err != nil {
			return false
		}
		if value > 0 {
			return true
		}
	}
	return false
}

func (tester Tester) now() time.Time {
	if tester.Now != nil {
		return tester.Now().UTC()
	}
	return time.Now().UTC()
}
