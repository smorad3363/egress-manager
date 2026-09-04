package config

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSaveAndLoad(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "egress.json")
	configuration := Default(directory)
	configuration.ListenPort = 43127
	if err := Save(path, configuration); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, configuration) {
		t.Fatalf("Load() = %#v, want %#v", loaded, configuration)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("configuration permissions = %o, want no group/other access", info.Mode().Perm())
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "egress.json")
	data := []byte(`{"listen_address":"127.0.0.1","listen_port":40000,"data_directory":"/var/lib/egress-manager","database_path":"/var/lib/egress-manager/app.db","control_socket_path":"/run/egress-manager/egressd.sock","session_cookie_name":"egress_session","typo":true}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted unknown configuration field")
	}
}

func TestLoadDefaultsRoutingStatePathForExistingConfigurations(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "egress.json")
	configuration := Default(directory)
	configuration.ListenPort = 43127
	configuration.RoutingStatePath = ""
	configuration.InterfaceStatePath = ""
	configuration.InterfaceRuntimeDirectory = ""
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RoutingStatePath != filepath.Join(directory, "routing.json") {
		t.Fatalf("routing state path = %q", loaded.RoutingStatePath)
	}
	if loaded.InterfaceStatePath != "/run/egress-manager/interface-outbounds/state.json" || loaded.InterfaceRuntimeDirectory != "/run/egress-manager/interface-outbounds" {
		t.Fatalf("interface runtime defaults = %q %q", loaded.InterfaceStatePath, loaded.InterfaceRuntimeDirectory)
	}
}

func TestPortSelectorExcludesProtectedAndBusyPorts(t *testing.T) {
	t.Parallel()

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	busyPort := uint16(busy.Addr().(*net.TCPAddr).Port)
	if busyPort == 65535 {
		t.Skip("ephemeral port left no adjacent candidate")
	}

	selector := PortSelector{
		Random: bytes.NewReader(make([]byte, portRandomAttempts*4)),
		Listen: net.Listen,
		Min:    busyPort,
		Max:    busyPort + 1,
	}
	excluded := ProtectedInstallerPorts(busyPort)
	selected, err := selector.Select(netip.MustParseAddr("127.0.0.1"), excluded)
	if err != nil {
		t.Fatal(err)
	}
	if selected != busyPort+1 {
		t.Fatalf("Select() = %d, want %d", selected, busyPort+1)
	}
}

func TestNonLoopbackConfigurationRequiresTLS(t *testing.T) {
	t.Parallel()

	configuration := Default(t.TempDir())
	configuration.ListenAddress = "0.0.0.0"
	configuration.ListenPort = 443
	if err := configuration.Validate(); err == nil {
		t.Fatal("Validate() accepted a non-loopback HTTP listener")
	}
	configuration.TLSCertificatePath = filepath.Join(configuration.DataDirectory, "tls.crt")
	configuration.TLSPrivateKeyPath = filepath.Join(configuration.DataDirectory, "tls.key")
	if err := configuration.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestGenerateAndLoadSharedKey(t *testing.T) {
	t.Parallel()

	encoded, err := GenerateSharedKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ipc.key")
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	key, err := LoadSharedKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if key == [32]byte{} {
		t.Fatal("LoadSharedKey() returned a zero key")
	}
}
