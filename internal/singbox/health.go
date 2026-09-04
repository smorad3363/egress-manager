package singbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/system"
)

type CandidateValidator interface {
	Validate(context.Context, []byte) error
}

type TransportDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type ConnectivityProbe interface {
	Probe(context.Context, domain.Outbound, ExecutionPlan) (ConnectivityResult, error)
}

type ConnectivityResult struct {
	ExternalIP string
	TCP        domain.ProbeStatus
	UDP        domain.ProbeStatus
	Latency    time.Duration
}

type Tester struct {
	Validator CandidateValidator
	Dialer    TransportDialer
	Probe     ConnectivityProbe
	Timeout   time.Duration
	Now       func() time.Time
}

func (tester Tester) Test(ctx context.Context, outbound domain.Outbound, credential []byte) domain.OutboundHealth {
	health := domain.UnknownOutboundHealth()
	checkedAt := tester.now()
	health.CheckedAt = &checkedAt
	if !outbound.Enabled {
		outbound.Enabled = true
	}
	state, _ := ParseState(nil, false)
	plan, err := BuildPlan([]domain.Outbound{outbound}, map[domain.ID][]byte{outbound.ID: credential}, state)
	if err != nil || tester.Validator == nil || tester.Dialer == nil {
		health.Status = domain.HealthUnhealthy
		health.ConfigurationValid = domain.ProbeFailed
		health.Detail = "configuration_invalid"
		return health
	}
	testContext, cancel := context.WithTimeout(ctx, tester.timeout())
	defer cancel()
	if err := tester.Validator.Validate(testContext, plan.Candidate()); err != nil {
		health.Status = domain.HealthUnhealthy
		health.ConfigurationValid = domain.ProbeFailed
		health.Detail = "configuration_invalid"
		return health
	}
	health.ConfigurationValid = domain.ProbePassed
	transportStart := time.Now()
	connection, err := tester.Dialer.DialContext(testContext, "tcp", outbound.Server.String())
	if err != nil {
		health.Status = domain.HealthUnhealthy
		health.TransportReachable = domain.ProbeFailed
		health.InternetReachable = domain.ProbeUnknown
		health.TCP = domain.ProbeFailed
		health.UDP = capabilityProbeStatus(outbound.Capabilities.UDP)
		health.Detail = "transport_unreachable"
		return health
	}
	_ = connection.Close()
	health.TransportReachable = domain.ProbePassed
	health.Latency = time.Since(transportStart)
	if tester.Probe == nil {
		health.Status = domain.HealthDegraded
		health.InternetReachable = domain.ProbeUntestable
		health.TCP = domain.ProbeUntestable
		health.UDP = capabilityProbeStatus(outbound.Capabilities.UDP)
		health.Detail = "internet_probe_unavailable"
		return health
	}
	result, err := tester.Probe.Probe(testContext, outbound, plan)
	if err != nil {
		health.Status = domain.HealthUnhealthy
		health.InternetReachable = domain.ProbeFailed
		health.TCP = domain.ProbeFailed
		health.UDP = capabilityProbeStatus(outbound.Capabilities.UDP)
		health.Detail = "internet_unreachable"
		return health
	}
	if result.ExternalIP != "" {
		address, parseErr := netip.ParseAddr(result.ExternalIP)
		if parseErr != nil || address.String() != result.ExternalIP {
			health.Status = domain.HealthDegraded
			health.InternetReachable = domain.ProbeFailed
			health.TCP = domain.ProbeFailed
			health.UDP = capabilityProbeStatus(outbound.Capabilities.UDP)
			health.Detail = "external_ip_invalid"
			return health
		}
	}
	health.Status = domain.HealthHealthy
	health.InternetReachable = domain.ProbePassed
	health.ExternalIP = result.ExternalIP
	health.TCP = result.TCP
	health.UDP = result.UDP
	if result.Latency > 0 {
		health.Latency = result.Latency
	}
	if health.TCP != domain.ProbePassed {
		health.Status = domain.HealthDegraded
	}
	if !outbound.Capabilities.UDP {
		health.UDP = domain.ProbeUnsupported
	} else if health.UDP == "" || health.UDP == domain.ProbeUnknown {
		health.UDP = domain.ProbeUntestable
	}
	if err := health.Validate(); err != nil {
		return domain.OutboundHealth{Status: domain.HealthUnhealthy, CheckedAt: &checkedAt, ConfigurationValid: domain.ProbePassed, TransportReachable: domain.ProbePassed, InternetReachable: domain.ProbeFailed, TCP: domain.ProbeFailed, UDP: capabilityProbeStatus(outbound.Capabilities.UDP), Detail: "probe_result_invalid"}
	}
	return health
}

type NativeValidator struct {
	Runner        system.Runner
	TempDirectory string
	Timeout       time.Duration
}

func (validator NativeValidator) Validate(ctx context.Context, candidate []byte) error {
	if validator.Runner == nil || len(candidate) == 0 || len(candidate) > maximumCandidate {
		return fmt.Errorf("sing-box native validator input is invalid")
	}
	directory := validator.TempDirectory
	if directory == "" {
		directory = os.TempDir()
	}
	if !filepath.IsAbs(directory) {
		return fmt.Errorf("sing-box validation directory must be absolute")
	}
	file, err := os.CreateTemp(directory, ".egress-sing-box-check-*.json")
	if err != nil {
		return fmt.Errorf("create sing-box validation file: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(candidate); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	timeout := validator.Timeout
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 5 * time.Second
	}
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, runErr := validator.Runner.Run(commandContext, system.Command{Name: "sing-box", Args: []string{"check", "-c", path}})
	if runErr != nil || result.ExitCode != 0 {
		return errors.Join(runErr, fmt.Errorf("sing-box validation failed"))
	}
	return nil
}

func capabilityProbeStatus(supported bool) domain.ProbeStatus {
	if supported {
		return domain.ProbeUntestable
	}
	return domain.ProbeUnsupported
}

func (tester Tester) now() time.Time {
	if tester.Now != nil {
		return tester.Now().UTC()
	}
	return time.Now().UTC()
}

func (tester Tester) timeout() time.Duration {
	if tester.Timeout <= 0 || tester.Timeout > time.Minute {
		return 15 * time.Second
	}
	return tester.Timeout
}
