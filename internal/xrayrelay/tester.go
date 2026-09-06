package xrayrelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/system"
)

type ContextDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type Tester struct {
	Runner  system.Runner
	Dialer  ContextDialer
	Timeout time.Duration
	Now     func() time.Time
}

func (tester Tester) Test(ctx context.Context, outbound domain.Outbound, credential []byte) domain.OutboundHealth {
	now := tester.now()
	health := domain.UnknownOutboundHealth()
	health.CheckedAt = &now
	if outbound.Adapter != domain.OutboundAdapterXray || outbound.Type != domain.OutboundVLESS {
		health.Status = domain.HealthUnhealthy
		health.ConfigurationValid = domain.ProbeFailed
		health.Detail = "unsupported Xray outbound adapter or protocol"
		return health
	}
	document, err := DecodeCredential(credential)
	if err != nil {
		health.Status = domain.HealthUnhealthy
		health.ConfigurationValid = domain.ProbeFailed
		health.Detail = "credential document is invalid"
		return health
	}
	endpoint, err := credentialEndpoint(document.Outbound)
	if err != nil || endpoint != outbound.Server {
		health.Status = domain.HealthUnhealthy
		health.ConfigurationValid = domain.ProbeFailed
		health.Detail = "credential metadata does not match outbound"
		return health
	}
	if err := tester.nativeValidate(ctx, document.Outbound); err != nil {
		health.Status = domain.HealthUnhealthy
		health.ConfigurationValid = domain.ProbeFailed
		health.Detail = "Xray rejected the outbound configuration"
		return health
	}
	health.ConfigurationValid = domain.ProbePassed
	started := time.Now()
	dialer := tester.Dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: tester.timeout()}
	}
	dialContext, cancel := context.WithTimeout(ctx, tester.timeout())
	connection, dialErr := dialer.DialContext(dialContext, "tcp", outbound.Server.String())
	cancel()
	if connection != nil {
		_ = connection.Close()
	}
	if dialErr != nil {
		health.Status = domain.HealthUnhealthy
		health.TransportReachable = domain.ProbeFailed
		health.TCP = domain.ProbeFailed
		health.UDP = domain.ProbeUntestable
		health.InternetReachable = domain.ProbeUntestable
		health.Detail = "outbound server transport is unreachable"
		return health
	}
	health.Latency = time.Since(started)
	health.TransportReachable = domain.ProbePassed
	health.TCP = domain.ProbePassed
	health.UDP = domain.ProbeUntestable
	health.InternetReachable = domain.ProbeUntestable
	health.Status = domain.HealthHealthy
	health.Detail = "native configuration and server transport passed; end-to-end internet probe is not run for Xray relay outbounds"
	return health
}

func (tester Tester) nativeValidate(ctx context.Context, outbound map[string]any) error {
	if tester.Runner == nil {
		return fmt.Errorf("Xray tester runner is required")
	}
	copyOutbound := cloneMap(outbound)
	copyOutbound["tag"] = "egm_test_outbound"
	configuration := map[string]any{
		"log":       map[string]any{"loglevel": "none"},
		"inbounds":  []any{},
		"outbounds": []any{copyOutbound},
	}
	candidate, err := json.Marshal(configuration)
	if err != nil || len(candidate) > MaximumConfigurationBytes {
		return fmt.Errorf("encode Xray test candidate")
	}
	directory, err := os.MkdirTemp("", "egress-xray-outbound-test-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "config.json")
	if err := os.WriteFile(path, candidate, 0o600); err != nil {
		return err
	}
	commandContext, cancel := context.WithTimeout(ctx, tester.timeout())
	defer cancel()
	result, runErr := tester.Runner.Run(commandContext, system.Command{Name: "xray", Args: []string{"run", "-test", "-config", path}})
	if runErr != nil || result.ExitCode != 0 {
		return errors.Join(runErr, fmt.Errorf("xray exited with code %d", result.ExitCode))
	}
	return nil
}

func (tester Tester) timeout() time.Duration {
	if tester.Timeout <= 0 || tester.Timeout > 30*time.Second {
		return 5 * time.Second
	}
	return tester.Timeout
}

func (tester Tester) now() time.Time {
	if tester.Now != nil {
		return tester.Now().UTC()
	}
	return time.Now().UTC()
}
