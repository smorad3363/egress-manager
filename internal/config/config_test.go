package config

import (
	"bytes"
	"net"
	"net/netip"
	"os"
	"path/filepath"
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
	if loaded != configuration {
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
