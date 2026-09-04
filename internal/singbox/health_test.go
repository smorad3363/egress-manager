package singbox

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
)

type validatorFunc func(context.Context, []byte) error

func (function validatorFunc) Validate(ctx context.Context, candidate []byte) error {
	return function(ctx, candidate)
}

type dialerFunc func(context.Context, string, string) (net.Conn, error)

func (function dialerFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return function(ctx, network, address)
}

type probeFunc func(context.Context, domain.Outbound, ExecutionPlan) (ConnectivityResult, error)

func (function probeFunc) Probe(ctx context.Context, outbound domain.Outbound, plan ExecutionPlan) (ConnectivityResult, error) {
	return function(ctx, outbound, plan)
}

func healthFixture(t *testing.T) (domain.Outbound, []byte) {
	t.Helper()
	imports, err := ParseImport(readFixture(t, "trojan.uri"))
	if err != nil {
		t.Fatal(err)
	}
	return imports[0].Outbound, imports[0].CredentialDocument
}

func TestHealthDistinguishesConfigurationTransportAndInternet(t *testing.T) {
	t.Parallel()
	outbound, credential := healthFixture(t)
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	configurationFailure := Tester{
		Validator: validatorFunc(func(context.Context, []byte) error { return errors.New("invalid") }),
		Dialer:    dialerFunc(func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("must not dial") }),
		Now:       func() time.Time { return now },
	}.Test(context.Background(), outbound, credential)
	if configurationFailure.ConfigurationValid != domain.ProbeFailed || configurationFailure.Detail != "configuration_invalid" {
		t.Fatalf("configuration failure = %#v", configurationFailure)
	}

	transportFailure := Tester{
		Validator: validatorFunc(func(context.Context, []byte) error { return nil }),
		Dialer:    dialerFunc(func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("unreachable") }),
		Now:       func() time.Time { return now },
	}.Test(context.Background(), outbound, credential)
	if transportFailure.ConfigurationValid != domain.ProbePassed || transportFailure.TransportReachable != domain.ProbeFailed || transportFailure.InternetReachable != domain.ProbeUnknown {
		t.Fatalf("transport failure = %#v", transportFailure)
	}

	left, right := net.Pipe()
	defer right.Close()
	internetFailure := Tester{
		Validator: validatorFunc(func(context.Context, []byte) error { return nil }),
		Dialer:    dialerFunc(func(context.Context, string, string) (net.Conn, error) { return left, nil }),
		Probe: probeFunc(func(context.Context, domain.Outbound, ExecutionPlan) (ConnectivityResult, error) {
			return ConnectivityResult{}, errors.New("no internet")
		}),
		Now: func() time.Time { return now },
	}.Test(context.Background(), outbound, credential)
	if internetFailure.TransportReachable != domain.ProbePassed || internetFailure.InternetReachable != domain.ProbeFailed || internetFailure.Detail != "internet_unreachable" {
		t.Fatalf("internet failure = %#v", internetFailure)
	}
}

func TestHealthReturnsExternalIPTCPUDPAndLatency(t *testing.T) {
	t.Parallel()
	outbound, credential := healthFixture(t)
	left, right := net.Pipe()
	defer right.Close()
	tester := Tester{
		Validator: validatorFunc(func(context.Context, []byte) error { return nil }),
		Dialer:    dialerFunc(func(context.Context, string, string) (net.Conn, error) { return left, nil }),
		Probe: probeFunc(func(context.Context, domain.Outbound, ExecutionPlan) (ConnectivityResult, error) {
			return ConnectivityResult{ExternalIP: "203.0.113.20", TCP: domain.ProbePassed, UDP: domain.ProbeUntestable, Latency: 25 * time.Millisecond}, nil
		}),
	}
	health := tester.Test(context.Background(), outbound, credential)
	if health.Status != domain.HealthHealthy || health.ExternalIP != "203.0.113.20" || health.TCP != domain.ProbePassed || health.UDP != domain.ProbeUntestable || health.Latency != 25*time.Millisecond {
		t.Fatalf("health = %#v", health)
	}
}
