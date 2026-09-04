package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	managedInterface "github.com/egress-manager/egress-manager/internal/interfaceoutbound"
)

func main() {
	if len(os.Args) != 3 {
		fail("usage: egress-interface-probe FIXTURE_DIRECTORY OUTPUT_DIRECTORY")
	}
	if err := os.MkdirAll(os.Args[2], 0o700); err != nil {
		fail("create output directory")
	}
	wireGuardInput := read(filepath.Join(os.Args[1], "wireguard.conf"))
	openVPNInput := read(filepath.Join(os.Args[1], "client.ovpn"))
	wireGuard, err := managedInterface.ParseImport(string(wireGuardInput))
	if err != nil {
		fail("parse WireGuard fixture")
	}
	openVPN, err := managedInterface.ParseImport(string(openVPNInput))
	if err != nil {
		fail("parse OpenVPN fixture")
	}
	openVPNCredential, err := managedInterface.DecodeCredential(openVPN.CredentialDocument)
	if err != nil || openVPNCredential.OpenVPN == nil {
		fail("decode normalized OpenVPN credential")
	}
	wireGuardCredential, err := managedInterface.DecodeCredential(wireGuard.CredentialDocument)
	if err != nil || wireGuardCredential.WireGuard == nil {
		fail("decode normalized WireGuard credential")
	}
	public, err := json.Marshal([]any{wireGuard, openVPN})
	if err != nil {
		fail("encode public interface imports")
	}
	for _, secret := range []string{"AQIDBAUG", "TEST_ONLY_PRIVATE_KEY", "TEST_ONLY_PASSWORD", "<key>"} {
		if strings.Contains(string(public), secret) {
			fail("public interface import leaked credential material")
		}
	}
	files := map[string][]byte{
		"wireguard.conf": wireGuardInput,
		"wireguard-id":   []byte(string(wireGuard.Outbound.ID) + "\n"),
		"wireguard-name": []byte(wireGuardCredential.InterfaceName + "\n"),
		"openvpn.ovpn":   []byte(openVPNCredential.OpenVPN.Config),
		"openvpn-id":     []byte(string(openVPN.Outbound.ID) + "\n"),
		"openvpn-name":   []byte(openVPNCredential.InterfaceName + "\n"),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(os.Args[2], name), content, 0o600); err != nil {
			fail("write interface fixture output")
		}
	}
	fmt.Println("PASS: WireGuard and OpenVPN fixtures import deterministically without public secret exposure")
}

func read(path string) []byte {
	content, err := os.ReadFile(path)
	if err != nil {
		fail("read interface fixture")
	}
	return content
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "FAIL:", message)
	os.Exit(1)
}
