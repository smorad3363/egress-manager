package xrayrelay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
)

const syntheticVLESSURI = "vless://11111111-2222-4333-8444-555555555555@example.test:2025?security=reality&type=xhttp&path=%2Frelay&mode=auto&extra=%7B%22scMaxEachPostBytes%22%3A1000000%2C%22xPaddingBytes%22%3A%22100-1000%22%7D&sni=edge.example.com&fp=edge&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=0123456789abcdef#Synthetic%20relay"

func TestParseImportRealityXHTTP(t *testing.T) {
	if !IsCompatibleImport(syntheticVLESSURI) {
		t.Fatal("expected compatible Xray import")
	}
	result, err := ParseImport(syntheticVLESSURI)
	if err != nil {
		t.Fatalf("ParseImport() error = %v", err)
	}
	if result.Outbound.Adapter != domain.OutboundAdapterXray || result.Outbound.Type != domain.OutboundVLESS {
		t.Fatalf("unexpected outbound adapter/type: %#v", result.Outbound)
	}
	if result.Outbound.Server.Host != "example.test" || result.Outbound.Server.Port != 2025 {
		t.Fatalf("unexpected endpoint: %#v", result.Outbound.Server)
	}
	if result.Outbound.Name != "Synthetic relay" {
		t.Fatalf("unexpected name %q", result.Outbound.Name)
	}
	credential, err := DecodeCredential(result.CredentialDocument)
	if err != nil {
		t.Fatalf("DecodeCredential() error = %v", err)
	}
	stream := credential.Outbound["streamSettings"].(map[string]any)
	if stream["network"] != "xhttp" || stream["security"] != "reality" {
		t.Fatalf("unexpected stream settings: %#v", stream)
	}
	reality := stream["realitySettings"].(map[string]any)
	if reality["serverName"] != "edge.example.com" || reality["fingerprint"] != "edge" {
		t.Fatalf("unexpected reality settings: %#v", reality)
	}
	xhttp := stream["xhttpSettings"].(map[string]any)
	if xhttp["path"] != "/relay" || xhttp["mode"] != "auto" {
		t.Fatalf("unexpected xhttp settings: %#v", xhttp)
	}
	public, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"11111111-2222-4333-8444-555555555555", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "0123456789abcdef"} {
		if strings.Contains(string(public), secret) {
			t.Fatalf("public import result leaked secret %q", secret)
		}
	}
}

func TestParseImportRejectsMalformedRealityXHTTP(t *testing.T) {
	tests := []string{
		"vless://not-a-uuid@example.test:2025?security=reality&type=xhttp&sni=edge.example.com&fp=edge&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=0123456789abcdef",
		"vless://11111111-2222-4333-8444-555555555555@example.test:2025?security=tls&type=xhttp&sni=edge.example.com&fp=edge&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=0123456789abcdef",
		"vless://11111111-2222-4333-8444-555555555555@example.test:2025?security=reality&type=ws&sni=edge.example.com&fp=edge&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=0123456789abcdef",
		"vless://11111111-2222-4333-8444-555555555555@example.test:2025?security=reality&type=xhttp&sni=edge.example.com&fp=unknown&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=0123456789abcdef",
		"vless://11111111-2222-4333-8444-555555555555@example.test:2025?security=reality&type=xhttp&sni=edge.example.com&fp=edge&pbk=short&sid=0123456789abcdef",
		"vless://11111111-2222-4333-8444-555555555555@example.test:2025?security=reality&type=xhttp&sni=edge.example.com&fp=edge&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=xyz",
		"vless://11111111-2222-4333-8444-555555555555@example.test:2025?security=reality&type=xhttp&path=relative&sni=edge.example.com&fp=edge&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=0123456789abcdef",
		"vless://11111111-2222-4333-8444-555555555555@example.test:2025?security=reality&type=xhttp&extra=%5B1%2C2%5D&sni=edge.example.com&fp=edge&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=0123456789abcdef",
	}
	for index, input := range tests {
		if _, err := ParseImport(input); err == nil {
			t.Fatalf("case %d ParseImport() error = nil", index)
		}
	}
}
