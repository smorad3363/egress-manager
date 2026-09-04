package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func validOutbound() Outbound {
	return Outbound{
		ID:   "vless-de",
		Name: "VLESS Germany",
		Type: OutboundVLESS,
		Server: Endpoint{
			Host: "edge.example.com",
			Port: 443,
		},
		Capabilities: Capabilities{TCP: true, UDP: true},
		Health:       Health{Status: HealthUnknown},
		Enabled:      true,
		SecretMetadata: []string{
			"uuid",
			"reality_public_key",
		},
	}
}

func TestOutboundValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Outbound)
	}{
		{name: "valid", mutate: func(*Outbound) {}},
		{name: "invalid endpoint", mutate: func(outbound *Outbound) { outbound.Server.Host = "https://bad host" }},
		{name: "unknown protocol", mutate: func(outbound *Outbound) { outbound.Type = "unknown" }},
		{name: "no capability", mutate: func(outbound *Outbound) { outbound.Capabilities = Capabilities{} }},
		{name: "duplicate secret key", mutate: func(outbound *Outbound) { outbound.SecretMetadata = []string{"uuid", "uuid"} }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			outbound := validOutbound()
			test.mutate(&outbound)
			err := outbound.Validate()
			if test.name == "valid" && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if test.name != "valid" && err == nil {
				t.Fatal("Validate() expected an error")
			}
		})
	}
}

func TestOutboundJSONContainsMetadataButNoSecretValues(t *testing.T) {
	t.Parallel()

	outbound := validOutbound()
	encoded, err := json.Marshal(outbound)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"uuid"`) {
		t.Fatalf("JSON does not contain secret metadata: %s", text)
	}
	if strings.Contains(text, "private_key_value") {
		t.Fatalf("JSON leaked a secret value: %s", text)
	}
}
