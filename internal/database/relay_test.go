package database

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func TestRelayRepositoryCRUDRevisionAndOutboundProtection(t *testing.T) {
	database := openTestDatabase(t)
	store := NewStore(database)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	outbound := domain.Outbound{
		ID: "xray_out", Name: "Xray out", Adapter: domain.OutboundAdapterXray, Type: domain.OutboundVLESS,
		Server: domain.Endpoint{Host: "example.test", Port: 443}, Capabilities: domain.Capabilities{TCP: true, UDP: true},
		Health: domain.UnknownOutboundHealth(), Enabled: true, SecretMetadata: []string{"uuid"},
	}
	if _, err := store.CreateOutbound(ctx, outbound, []byte(`{"version":1,"outbound":{"protocol":"vless"}}`), now); err != nil {
		t.Fatal(err)
	}
	relay := domain.Relay{
		ID: "ssh_gateway", Name: "SSH gateway", ListenAddress: "0.0.0.0", ListenPort: 6111, Network: domain.RelayTCP,
		Destination: domain.Endpoint{Host: "91.107.220.12", Port: 6111}, OutboundID: outbound.ID,
		SourceCIDRs: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, Enabled: true,
	}
	created, err := store.CreateRelay(ctx, relay, now)
	if err != nil || created.Revision != 1 {
		t.Fatalf("CreateRelay() = %#v, %v", created, err)
	}
	loaded, err := store.Relay(ctx, relay.ID)
	if err != nil || loaded.Relay.Destination != relay.Destination || len(loaded.Relay.SourceCIDRs) != 1 {
		t.Fatalf("Relay() = %#v, %v", loaded, err)
	}
	relay.Name = "SSH gateway updated"
	updated, err := store.UpdateRelay(ctx, relay, 1, now.Add(time.Second))
	if err != nil || updated.Revision != 2 || updated.Relay.Name != relay.Name {
		t.Fatalf("UpdateRelay() = %#v, %v", updated, err)
	}
	if _, err := store.UpdateRelay(ctx, relay, 1, now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale UpdateRelay() error = %v", err)
	}
	if err := store.DeleteOutbound(ctx, outbound.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("referenced outbound delete error = %v", err)
	}
	if err := store.DeleteRelay(ctx, relay.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteOutbound(ctx, outbound.ID, 1); err != nil {
		t.Fatalf("delete unreferenced outbound: %v", err)
	}
}
