package ipc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/logging"
)

func testAuthenticator(t *testing.T) *Authenticator {
	t.Helper()
	key := sha256.Sum256([]byte("test-only-ipc-key"))
	authenticator, err := NewAuthenticator(key)
	if err != nil {
		t.Fatal(err)
	}
	return authenticator
}

type codedBusyError struct{}

func (codedBusyError) Error() string        { return "internal detail" }
func (codedBusyError) IPCErrorCode() string { return "busy" }

type codedBypassError struct{}

func (codedBypassError) Error() string        { return "internal bypass detail" }
func (codedBypassError) IPCErrorCode() string { return "bypass_active" }

func TestRequestAuthenticationRejectsTamperAndStaleTimestamp(t *testing.T) {
	t.Parallel()

	authenticator := testAuthenticator(t)
	now := time.Now().UTC().Truncate(time.Second)
	request := Request{
		Version:     ProtocolVersion,
		OperationID: "0123456789abcdef0123456789abcdef",
		Timestamp:   now.Unix(),
		Nonce:       base64.RawURLEncoding.EncodeToString(make([]byte, nonceBytes)),
		Operation:   OperationHealth,
		Payload:     json.RawMessage(`{}`),
	}
	if err := authenticator.SignRequest(&request); err != nil {
		t.Fatal(err)
	}
	if err := authenticator.VerifyRequest(request, now); err != nil {
		t.Fatal(err)
	}

	tampered := request
	tampered.Payload = json.RawMessage(`{"root":true}`)
	if err := authenticator.VerifyRequest(tampered, now); err == nil {
		t.Fatal("VerifyRequest() accepted a modified payload")
	}
	if err := authenticator.VerifyRequest(request, now.Add(maximumClockSkew+time.Second)); err == nil {
		t.Fatal("VerifyRequest() accepted a stale timestamp")
	}
}

func TestAuthenticatedUnixRoundTripAndReplayRejection(t *testing.T) {
	if testing.Short() {
		t.Skip("Unix socket integration test")
	}

	authenticator := testAuthenticator(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, err := NewServer(authenticator, logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Handle(OperationHealth, func(ctx context.Context, payload json.RawMessage) (any, error) {
		return map[string]string{"status": "ok", "operation_id": logging.OperationID(ctx)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := server.Handle(OperationRecoveryRun, func(context.Context, json.RawMessage) (any, error) {
		return nil, codedBusyError{}
	}); err != nil {
		t.Fatal(err)
	}
	if err := server.Handle(OperationRoutesApply, func(context.Context, json.RawMessage) (any, error) {
		return nil, codedBypassError{}
	}); err != nil {
		t.Fatal(err)
	}
	var mutationCalled atomic.Bool
	if err := server.Handle(OperationNATApply, func(context.Context, json.RawMessage) (any, error) {
		mutationCalled.Store(true)
		return map[string]string{"state": "COMMITTED"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(t.TempDir(), "egressd.sock")
	listener, err := ListenUnix(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("socket permissions = %o, want 660", info.Mode().Perm())
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverResult := make(chan error, 1)
	go func() { serverResult <- server.Serve(ctx, listener) }()
	unsignedMutation := Request{
		Version: ProtocolVersion, OperationID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Timestamp: time.Now().UTC().Unix(),
		Nonce: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x7}, nonceBytes)), Operation: OperationNATApply, Payload: json.RawMessage(`{}`),
	}
	unsignedResponse := rawCall(t, socketPath, unsignedMutation)
	if unsignedResponse.Success || unsignedResponse.ErrorCode != "unauthorized" || mutationCalled.Load() {
		t.Fatalf("unsigned mutation response = %#v; called = %v", unsignedResponse, mutationCalled.Load())
	}

	randomness := make([]byte, 128)
	for index := range randomness {
		randomness[index] = byte(index)
	}
	client := Client{
		SocketPath:    socketPath,
		Authenticator: authenticator,
		Timeout:       2 * time.Second,
		Random:        bytes.NewReader(randomness),
	}
	callContext := logging.WithOperationID(context.Background(), "fedcba9876543210fedcba9876543210")
	var response map[string]string
	if err := client.Call(callContext, OperationHealth, map[string]any{}, &response); err != nil {
		t.Fatal(err)
	}
	if response["status"] != "ok" || response["operation_id"] != "fedcba9876543210fedcba9876543210" {
		t.Fatalf("response = %#v", response)
	}
	if err := client.Call(context.Background(), OperationRecoveryRun, struct{}{}, nil); !IsRemoteError(err, "busy") {
		t.Fatalf("coded busy error = %v", err)
	}
	if err := client.Call(context.Background(), OperationRoutesApply, struct{}{}, nil); !IsRemoteError(err, "bypass_active") {
		t.Fatalf("coded bypass error = %v", err)
	}

	request := Request{
		Version:     ProtocolVersion,
		OperationID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Timestamp:   time.Now().UTC().Unix(),
		Nonce:       base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x9}, nonceBytes)),
		Operation:   OperationHealth,
		Payload:     json.RawMessage(`{}`),
	}
	if err := authenticator.SignRequest(&request); err != nil {
		t.Fatal(err)
	}
	first := rawCall(t, socketPath, request)
	if !first.Success {
		t.Fatalf("first raw call failed: %#v", first)
	}
	second := rawCall(t, socketPath, request)
	if second.Success || second.ErrorCode != "replay" {
		t.Fatalf("replayed call response = %#v", second)
	}

	cancel()
	select {
	case err := <-serverResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("IPC server did not stop")
	}
}

func TestListenUnixRefusesRegularFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "egressd.sock")
	if err := os.WriteFile(path, []byte("owned by user"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenUnix(path); err == nil {
		t.Fatal("ListenUnix() replaced a non-socket path")
	}
}

func rawCall(t *testing.T, socketPath string, request Request) Response {
	t.Helper()
	connection, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}
