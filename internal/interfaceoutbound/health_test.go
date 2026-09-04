package interfaceoutbound

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/system"
)

type healthRunner struct {
	t         *testing.T
	handshake string
	failCheck bool
}

func (runner *healthRunner) Run(_ context.Context, command system.Command) (system.Result, error) {
	runner.t.Helper()
	joined := command.Name + " " + strings.Join(command.Args, " ")
	switch {
	case strings.HasPrefix(joined, "wg-quick strip "):
		if runner.failCheck {
			return system.Result{ExitCode: 1}, nil
		}
		return system.Result{ExitCode: 0}, nil
	case strings.HasPrefix(joined, "ip -json -details link show dev egmwg"):
		name := command.Args[len(command.Args)-1]
		return system.Result{Stdout: []byte(`[{"ifname":"` + name + `","linkinfo":{"info_kind":"wireguard"}}]`), ExitCode: 0}, nil
	case strings.HasPrefix(joined, "wg show egmwg") && strings.HasSuffix(joined, " latest-handshakes"):
		return system.Result{Stdout: []byte(runner.handshake + "\n"), ExitCode: 0}, nil
	default:
		runner.t.Fatalf("unexpected health command: %s", joined)
		return system.Result{ExitCode: -1}, nil
	}
}

func TestHealthRequiresValidationLifecycleAndHandshakeEvidence(t *testing.T) {
	t.Parallel()
	imported, err := ParseImport(readFixture(t, "wireguard.conf"))
	if err != nil {
		t.Fatal(err)
	}
	credential, err := DecodeCredential(imported.CredentialDocument)
	if err != nil {
		t.Fatal(err)
	}
	configuration, entry, err := desiredEntry(imported.Outbound, credential)
	if err != nil {
		t.Fatal(err)
	}
	runtimeDirectory := t.TempDir()
	if err := os.Chmod(runtimeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDirectory, string(entry.ID)+".wg.conf"), configuration, 0o600); err != nil {
		t.Fatal(err)
	}
	state := State{Exists: true, Hash: strings.Repeat("a", 64), Entries: []StateEntry{entry}, configs: map[domain.ID][]byte{entry.ID: configuration}}
	now := time.Date(2026, 9, 4, 20, 0, 0, 0, time.UTC)
	tester := Tester{Runner: &healthRunner{t: t, handshake: "TEST_ONLY_PUBLIC_KEY 1800000000"}, RuntimeDirectory: runtimeDirectory, Now: func() time.Time { return now }}
	health := tester.Test(context.Background(), imported.Outbound, imported.CredentialDocument, state)
	if health.Status != domain.HealthHealthy || health.ConfigurationValid != domain.ProbePassed || health.TransportReachable != domain.ProbePassed || health.InternetReachable != domain.ProbeUntestable || health.Detail != "wireguard_handshake_observed" {
		t.Fatalf("health = %#v", health)
	}

	tester.Runner = &healthRunner{t: t, handshake: "TEST_ONLY_PUBLIC_KEY 0"}
	health = tester.Test(context.Background(), imported.Outbound, imported.CredentialDocument, state)
	if health.Status != domain.HealthDegraded || health.TransportReachable != domain.ProbeUntestable || health.Detail != "wireguard_handshake_not_observed" {
		t.Fatalf("no-handshake health = %#v", health)
	}

	tester.Runner = &healthRunner{t: t, failCheck: true}
	health = tester.Test(context.Background(), imported.Outbound, imported.CredentialDocument, state)
	if health.Status != domain.HealthUnhealthy || health.ConfigurationValid != domain.ProbeFailed || health.Detail != "configuration_invalid" {
		t.Fatalf("invalid health = %#v", health)
	}
}
