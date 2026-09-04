package app

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/config"
	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/ipc"
)

func TestDeriveAuthenticationKeyIsDomainSeparated(t *testing.T) {
	t.Parallel()

	var key [32]byte
	key[0] = 1
	derived, err := deriveAuthenticationKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if derived == key || derived == [32]byte{} {
		t.Fatal("derived authentication key was not domain separated")
	}
}

func TestDaemonAndWebLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket integration runs on Linux")
	}

	directory := t.TempDir()
	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(portListener.Addr().(*net.TCPAddr).Port)
	if err := portListener.Close(); err != nil {
		t.Fatal(err)
	}
	configuration := config.Default(directory)
	configuration.ListenPort = port
	configPath := filepath.Join(directory, "config.json")
	if err := config.Save(configPath, configuration); err != nil {
		t.Fatal(err)
	}
	encodedKey, err := config.GenerateSharedKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(directory, "ipc.key")
	if err := os.WriteFile(keyPath, []byte(encodedKey+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	daemonContext, cancelDaemon := context.WithCancel(context.Background())
	daemonResult := make(chan error, 1)
	go func() {
		daemonResult <- RunDaemon(daemonContext, DaemonOptions{ConfigPath: configPath, KeyPath: keyPath, Logger: logger})
	}()
	waitFor(t, 3*time.Second, func() bool {
		info, statErr := os.Stat(configuration.ControlSocketPath)
		return statErr == nil && info.Mode()&os.ModeSocket != 0
	})
	sharedKey, err := config.LoadSharedKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	ipcAuthenticator, err := ipc.NewAuthenticator(sharedKey)
	if err != nil {
		t.Fatal(err)
	}
	malformedClient := ipc.Client{SocketPath: configuration.ControlSocketPath, Authenticator: ipcAuthenticator}
	if err := malformedClient.Call(context.Background(), ipc.OperationHealth, map[string]bool{"mutate": true}, nil); !ipc.IsRemoteError(err, "operation_failed") {
		t.Fatalf("malformed typed payload error = %v", err)
	}
	var hostInventory inventory.Inventory
	if err := malformedClient.Call(context.Background(), ipc.OperationInventory, struct{}{}, &hostInventory); err != nil {
		t.Fatalf("typed inventory call failed: %v", err)
	}
	if len(hostInventory.Capabilities) == 0 {
		t.Fatal("typed inventory response omitted capability states")
	}

	if err := ProvisionAdmin(context.Background(), configPath, keyPath, "operator", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	webContext, cancelWeb := context.WithCancel(context.Background())
	webResult := make(chan error, 1)
	go func() {
		webResult <- RunWeb(webContext, WebOptions{ConfigPath: configPath, KeyPath: keyPath, Logger: logger})
	}()

	var response *http.Response
	waitFor(t, 5*time.Second, func() bool {
		request, requestErr := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(int(port))+"/api/v1/health", nil)
		if requestErr != nil {
			return false
		}
		client := &http.Client{Timeout: time.Second}
		response, requestErr = client.Do(request)
		return requestErr == nil
	})
	if response == nil {
		t.Fatal("web server did not return a response")
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"ok"`) {
		t.Fatalf("health response = %d %s", response.StatusCode, body)
	}

	cancelWeb()
	if err := <-webResult; err != nil {
		t.Fatal(err)
	}
	cancelDaemon()
	if err := <-daemonResult; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(configuration.ControlSocketPath); !os.IsNotExist(err) {
		t.Fatalf("owned socket remained after shutdown: %v", err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
