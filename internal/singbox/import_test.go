package singbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func TestURIImportFixtures(t *testing.T) {
	t.Parallel()
	fixtures := []struct {
		name string
		kind domain.OutboundType
	}{
		{"vless.uri", domain.OutboundVLESS},
		{"trojan.uri", domain.OutboundTrojan},
		{"shadowsocks.uri", domain.OutboundShadowsocks},
		{"vmess.uri", domain.OutboundVMess},
		{"hysteria2.uri", domain.OutboundHysteria2},
		{"tuic.uri", domain.OutboundTUIC},
		{"socks5.uri", domain.OutboundSOCKS5},
		{"wireguard.uri", domain.OutboundWireGuard},
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			input := readFixture(t, fixture.name)
			results, err := ParseImport(input)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].Outbound.Type != fixture.kind || results[0].Outbound.Adapter != domain.OutboundAdapterSingBox {
				t.Fatalf("result = %#v", results)
			}
			if err := results[0].Outbound.Validate(); err != nil {
				t.Fatal(err)
			}
			credential, err := DecodeCredential(results[0].CredentialDocument)
			if err != nil || credential.Outbound["type"] != singBoxType(fixture.kind) {
				t.Fatalf("credential = %#v, error = %v", credential, err)
			}
			publicJSON, err := json.Marshal(results[0])
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(publicJSON), "TEST_ONLY") || strings.Contains(string(publicJSON), "00000000-0000-4000") {
				t.Fatalf("public import result leaked credential: %s", publicJSON)
			}
		})
	}
}

func TestSingBoxJSONSocksTypeMapsToGenericSOCKS5(t *testing.T) {
	t.Parallel()
	input := `{"type":"socks","tag":"SOCKS","server":"socks.example.com","server_port":1080}`
	results, err := ParseImport(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Outbound.Type != domain.OutboundSOCKS5 {
		t.Fatalf("results = %#v", results)
	}
}

func TestJSONImportFixture(t *testing.T) {
	t.Parallel()
	results, err := ParseImport(readFixture(t, "sing-box.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Outbound.Type != domain.OutboundVLESS || results[1].Outbound.Type != domain.OutboundSOCKS5 {
		t.Fatalf("results = %#v", results)
	}
	if strings.Join(results[0].Outbound.SecretMetadata, ",") != "uuid" {
		t.Fatalf("VLESS metadata = %#v", results[0].Outbound.SecretMetadata)
	}
	if strings.Join(results[1].Outbound.SecretMetadata, ",") != "password,username" {
		t.Fatalf("SOCKS metadata = %#v", results[1].Outbound.SecretMetadata)
	}
}

func TestImportRejectsMalformedAndOversizedInputsWithoutEcho(t *testing.T) {
	t.Parallel()
	secret := "TEST_ONLY_SHOULD_NOT_BE_ECHOED"
	invalid := []string{
		"trojan://" + secret + "@bad host:443",
		"vmess://" + secret,
		`{"type":"vless","server":"edge.example.com","server_port":443,"uuid":"` + secret + `"} trailing`,
		strings.Repeat("x", MaximumImportBytes+1),
	}
	for _, input := range invalid {
		_, err := ParseImport(input)
		if err == nil {
			t.Fatalf("invalid import accepted")
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked input credential: %v", err)
		}
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}
