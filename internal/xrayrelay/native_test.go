package xrayrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/system"
)

const nativeRelayUUID = "11111111-2222-4333-8444-555555555555"

func TestNativeGeneratedRealityXHTTPCandidateAgainstPinnedXray(t *testing.T) {
	binary := os.Getenv("XRAY_NATIVE_BINARY")
	if binary == "" {
		t.Skip("XRAY_NATIVE_BINARY is not set")
	}
	parsed, err := ParseImport(syntheticVLESSURI)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := ParseState(nil, false)
	relay := domain.Relay{
		ID: "native_reality", Name: "Native Reality XHTTP", ListenAddress: "127.0.0.1", ListenPort: 46111, Network: domain.RelayTCP,
		Destination: domain.Endpoint{Host: "127.0.0.1", Port: 46112}, OutboundID: parsed.Outbound.ID, Enabled: true,
	}
	plan, err := BuildPlan(Settings{ProtectedPorts: []uint16{22, 47604}}, []domain.Relay{relay}, []domain.Outbound{parsed.Outbound}, map[domain.ID][]byte{parsed.Outbound.ID: parsed.CredentialDocument}, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "xray-relay-reality-xhttp.json")
	if err := os.WriteFile(path, plan.Candidate(), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (system.ExecRunner{}).Run(context.Background(), system.Command{Name: binary, Args: []string{"run", "-test", "-config", path}})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("native Xray candidate validation failed: exit=%d err=%v stderr=%s stdout=%s", result.ExitCode, err, result.Stderr, result.Stdout)
	}
}

func TestNativeTCPListenerRelaysThroughSelectedXrayOutboundAndFailsClosed(t *testing.T) {
	binary := os.Getenv("XRAY_NATIVE_BINARY")
	if binary == "" {
		t.Skip("XRAY_NATIVE_BINARY is not set")
	}

	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	backendPort := uint16(backend.Addr().(*net.TCPAddr).Port)
	var backendAccepts atomic.Int64
	backendDone := make(chan struct{})
	go func() {
		defer close(backendDone)
		for {
			connection, acceptErr := backend.Accept()
			if acceptErr != nil {
				return
			}
			backendAccepts.Add(1)
			go func() {
				defer connection.Close()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()

	serverPort := freeTCPPort(t)
	relayPort := freeTCPPort(t)
	for relayPort == serverPort || relayPort == int(backendPort) {
		relayPort = freeTCPPort(t)
	}

	serverConfig := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{
			"listen": "127.0.0.1", "port": serverPort, "protocol": "vless",
			"settings": map[string]any{"clients": []any{map[string]any{"id": nativeRelayUUID}}, "decryption": "none"},
		}},
		"outbounds": []any{map[string]any{"protocol": "freedom", "tag": "direct"}},
	}
	serverPath := writeJSON(t, "xray-server.json", serverConfig)
	server := startNativeXray(t, binary, serverPath)
	waitTCP(t, fmt.Sprintf("127.0.0.1:%d", serverPort), 5*time.Second)

	outbound := domain.Outbound{
		ID: "native_vless", Name: "Native VLESS", Adapter: domain.OutboundAdapterXray, Type: domain.OutboundVLESS,
		Server: domain.Endpoint{Host: "127.0.0.1", Port: uint16(serverPort)},
		Capabilities: domain.Capabilities{TCP: true}, Health: domain.UnknownOutboundHealth(), Enabled: true, SecretMetadata: []string{"uuid"},
	}
	credential, err := json.Marshal(CredentialDocument{Version: credentialVersion, Outbound: map[string]any{
		"protocol": "vless",
		"settings": map[string]any{"vnext": []any{map[string]any{
			"address": "127.0.0.1", "port": uint16(serverPort),
			"users": []any{map[string]any{"id": nativeRelayUUID, "encryption": "none"}},
		}}},
		"streamSettings": map[string]any{"network": "tcp"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	state, _ := ParseState(nil, false)
	relay := domain.Relay{
		ID: "native_tcp", Name: "Native TCP relay", ListenAddress: "127.0.0.1", ListenPort: uint16(relayPort), Network: domain.RelayTCP,
		Destination: domain.Endpoint{Host: "127.0.0.1", Port: backendPort}, OutboundID: outbound.ID, Enabled: true,
	}
	plan, err := BuildPlan(Settings{ProtectedPorts: []uint16{22, 47604}}, []domain.Relay{relay}, []domain.Outbound{outbound}, map[domain.ID][]byte{outbound.ID: credential}, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	relayPath := filepath.Join(t.TempDir(), "xray-relay.json")
	if err := os.WriteFile(relayPath, plan.Candidate(), 0o600); err != nil {
		t.Fatal(err)
	}
	relayProcess := startNativeXray(t, binary, relayPath)
	waitTCP(t, fmt.Sprintf("127.0.0.1:%d", relayPort), 5*time.Second)

	payload := []byte("egress-manager-native-relay\n")
	connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", relayPort), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := connection.Write(payload); err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	received := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, received); err != nil {
		_ = connection.Close()
		t.Fatalf("read relayed payload: %v", err)
	}
	_ = connection.Close()
	if !bytes.Equal(received, payload) {
		t.Fatalf("relayed payload = %q, want %q", received, payload)
	}

	server.stop(t)
	acceptsBeforeFailure := backendAccepts.Load()
	failedConnection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", relayPort), 2*time.Second)
	if err == nil {
		_ = failedConnection.SetDeadline(time.Now().Add(1200 * time.Millisecond))
		_, _ = failedConnection.Write([]byte("must-not-go-direct"))
		buffer := make([]byte, 1)
		_, readErr := failedConnection.Read(buffer)
		_ = failedConnection.Close()
		if readErr == nil {
			t.Fatal("relay returned data after selected outbound was stopped")
		}
	}
	time.Sleep(250 * time.Millisecond)
	if backendAccepts.Load() != acceptsBeforeFailure {
		t.Fatalf("backend received a direct fallback connection after outbound failure: before=%d after=%d", acceptsBeforeFailure, backendAccepts.Load())
	}

	relayProcess.stop(t)
	_ = backend.Close()
	<-backendDone
}

type nativeXrayProcess struct {
	command *exec.Cmd
	cancel  context.CancelFunc
	stderr  *bytes.Buffer
	waited  bool
}

func startNativeXray(t *testing.T, binary, configPath string) *nativeXrayProcess {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, binary, "run", "-config", configPath)
	stderr := &bytes.Buffer{}
	command.Stdout = stderr
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	process := &nativeXrayProcess{command: command, cancel: cancel, stderr: stderr}
	t.Cleanup(func() {
		if !process.waited {
			process.stop(t)
		}
	})
	return process
}

func (process *nativeXrayProcess) stop(t *testing.T) {
	t.Helper()
	if process.waited {
		return
	}
	process.cancel()
	done := make(chan error, 1)
	go func() { done <- process.command.Wait() }()
	select {
	case <-done:
		process.waited = true
	case <-time.After(3 * time.Second):
		_ = process.command.Process.Kill()
		<-done
		process.waited = true
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func waitTCP(t *testing.T, address string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("listener %s did not become ready", address)
}

func writeJSON(t *testing.T, name string, value any) string {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
