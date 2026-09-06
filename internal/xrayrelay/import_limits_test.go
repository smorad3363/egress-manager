package xrayrelay

import (
	"net/url"
	"strings"
	"testing"
)

func TestParseImportRejectsMissingEndpointRealityAndUnsupportedMode(t *testing.T) {
	validQuery := "security=reality&type=xhttp&sni=edge.example.com&fp=edge&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=0123456789abcdef"
	cases := []string{
		"vless://11111111-2222-4333-8444-555555555555@:2025?" + validQuery,
		"vless://11111111-2222-4333-8444-555555555555@example.test?" + validQuery,
		"vless://11111111-2222-4333-8444-555555555555@example.test:2025?security=reality&type=xhttp&fp=edge&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=0123456789abcdef",
		"vless://11111111-2222-4333-8444-555555555555@example.test:2025?" + validQuery + "&mode=unsupported",
	}
	for index, input := range cases {
		if _, err := ParseImport(input); err == nil {
			t.Fatalf("case %d ParseImport() error = nil", index)
		}
	}
}

func TestParseImportRejectsOversizedDocumentsAndXHTTPExtra(t *testing.T) {
	if _, err := ParseImport(strings.Repeat("x", MaximumImportBytes+1)); err == nil {
		t.Fatal("oversized import was accepted")
	}
	extra := url.QueryEscape(`{"padding":"` + strings.Repeat("a", maximumExtraBytes) + `"}`)
	input := "vless://11111111-2222-4333-8444-555555555555@example.test:2025?security=reality&type=xhttp&sni=edge.example.com&fp=edge&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=0123456789abcdef&extra=" + extra
	if _, err := ParseImport(input); err == nil {
		t.Fatal("oversized XHTTP extra was accepted")
	}
}
