package interfaceoutbound

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func TestWireGuardImportIsTypedDeterministicAndSecretSafe(t *testing.T) {
	t.Parallel()
	input := readFixture(t, "wireguard.conf")
	first, err := ParseImport(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseImport(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Outbound, second.Outbound) || first.Outbound.Adapter != domain.OutboundAdapterInterface || first.Outbound.Type != domain.OutboundWireGuard {
		t.Fatalf("unexpected metadata: %#v", first.Outbound)
	}
	credential, err := DecodeCredential(first.CredentialDocument)
	if err != nil {
		t.Fatal(err)
	}
	if credential.WireGuard == nil || credential.InterfaceName == "" || len(credential.InterfaceName) > 15 || credential.WireGuard.Endpoint.Host != "vpn.example.com" {
		t.Fatalf("unexpected credential shape: %#v", credential)
	}
	public, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "AQIDBAUG") || strings.Contains(string(public), "QUJDREVG") {
		t.Fatalf("public result leaked WireGuard key material: %s", public)
	}
}

func TestOpenVPNImportNormalizesOwnedLifecycleWithoutLeakingProfile(t *testing.T) {
	t.Parallel()
	result, err := ParseImport(readFixture(t, "client.ovpn"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outbound.Adapter != domain.OutboundAdapterInterface || result.Outbound.Type != domain.OutboundOpenVPN || result.Outbound.Server.Port != 1194 {
		t.Fatalf("unexpected metadata: %#v", result.Outbound)
	}
	credential, err := DecodeCredential(result.CredentialDocument)
	if err != nil {
		t.Fatal(err)
	}
	if credential.OpenVPN == nil || !strings.Contains(credential.OpenVPN.Config, "route-nopull\n") || !strings.Contains(credential.OpenVPN.Config, "dev "+credential.InterfaceName+"\n") {
		t.Fatalf("normalized profile lacks owned route/device controls")
	}
	public, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "TEST_ONLY_PASSWORD") || strings.Contains(string(public), "TEST_ONLY_PRIVATE_KEY") {
		t.Fatalf("public result leaked OpenVPN credentials: %s", public)
	}
}

func TestImportRejectsUnsafeProfilesWithoutEchoingInput(t *testing.T) {
	t.Parallel()
	privateKey := "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="
	publicKey := "ISIjJCUmJygpKissLS4vMDEyMzQ1Njc4OTo7PD0+P0A="
	secret := "TEST_ONLY_SHOULD_NOT_BE_ECHOED"
	invalid := []string{
		"[Interface]\nPrivateKey = " + privateKey + "\nAddress=10.0.0.2/32\nPostUp = " + secret + "\n[Peer]\nPublicKey=" + publicKey + "\nAllowedIPs=0.0.0.0/0\nEndpoint=vpn.example.com:51820",
		"[Interface]\nPrivateKey = " + privateKey + "\nAddress=10.0.0.2/32\nSaveConfig=true\n[Peer]\nPublicKey=" + publicKey + "\nAllowedIPs=0.0.0.0/0\nEndpoint=vpn.example.com:51820",
		"[Interface]\nPrivateKey = " + privateKey + "\nAddress=10.0.0.2/32\n[Peer]\nPublicKey=" + publicKey + "\nAllowedIPs=0.0.0.0/0\nEndpoint=vpn.example.com:51820\n[Peer]\nPublicKey=" + publicKey,
		"client\ndev tap\nproto udp\nremote vpn.example.com 1194\nup " + secret + "\n<ca>\nTEST\n</ca>\n<auth-user-pass>\nu\np\n</auth-user-pass>",
		"client\ndev tun\nproto udp\nremote vpn.example.com 1194\nplugin " + secret + "\n<ca>\nTEST\n</ca>\n<auth-user-pass>\nu\np\n</auth-user-pass>",
		"client\ndev tun\nproto udp\nremote vpn.example.com 1194\nca /tmp/" + secret + "\nauth-user-pass /tmp/password",
		"client\ndev tun\nproto udp\nremote vpn.example.com 1194\nroute 0.0.0.0 0.0.0.0\n<ca>\nTEST\n</ca>\n<auth-user-pass>\nu\np\n</auth-user-pass>",
		strings.Repeat("x", MaximumImportBytes+1),
	}
	for index, input := range invalid {
		if _, err := ParseImport(input); err == nil {
			t.Fatalf("unsafe input %d accepted", index)
		} else if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked input content: %v", err)
		}
	}
}

func TestInterfaceNameIsStableBoundedAndKindSpecific(t *testing.T) {
	t.Parallel()
	wireGuard, err := InterfaceName(domain.OutboundWireGuard, "vpn_primary")
	if err != nil {
		t.Fatal(err)
	}
	openVPN, err := InterfaceName(domain.OutboundOpenVPN, "vpn_primary")
	if err != nil {
		t.Fatal(err)
	}
	if wireGuard == openVPN || len(wireGuard) > 15 || len(openVPN) > 15 {
		t.Fatalf("unsafe names: %q %q", wireGuard, openVPN)
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
