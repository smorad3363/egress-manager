package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/secrets"
)

func openTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "egress-manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return database
}

func TestMigrationsAreIdempotent(t *testing.T) {
	t.Parallel()

	database := openTestDatabase(t)
	if err := Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(version) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 7 {
		t.Fatalf("migration count = %d, want 7", count)
	}
}

func TestXrayBindingRepositoryUsesRevisionsKeysetsAndEnabledInboundUniqueness(t *testing.T) {
	t.Parallel()
	database := openTestDatabase(t)
	store := NewStore(database)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	binding := domain.XrayBinding{ID: "native_route", InboundTag: "vless-in", OutboundTag: "proxy-de", Enabled: true}
	created, err := store.CreateXrayBinding(ctx, binding, now)
	if err != nil || created.Revision != 1 {
		t.Fatalf("created binding = %#v, error = %v", created, err)
	}
	duplicate := domain.XrayBinding{ID: "duplicate", InboundTag: binding.InboundTag, OutboundTag: "direct", Enabled: true}
	if _, err := store.CreateXrayBinding(ctx, duplicate, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate enabled inbound error = %v", err)
	}
	binding.OutboundTag = "direct"
	updated, err := store.UpdateXrayBinding(ctx, binding, 1, now.Add(time.Second))
	if err != nil || updated.Revision != 2 || updated.Binding.OutboundTag != "direct" {
		t.Fatalf("updated binding = %#v, error = %v", updated, err)
	}
	if _, err := store.UpdateXrayBinding(ctx, binding, 1, now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	items, err := store.ListXrayBindings(ctx, "", 10)
	if err != nil || len(items) != 1 || items[0].Binding.ID != binding.ID {
		t.Fatalf("binding list = %#v, error = %v", items, err)
	}
	after, err := store.ListXrayBindings(ctx, binding.ID, 10)
	if err != nil || len(after) != 0 {
		t.Fatalf("binding keyset page = %#v, error = %v", after, err)
	}
	var selectID, order, from int
	var detail string
	if err := database.QueryRow(`EXPLAIN QUERY PLAN SELECT id FROM xray_bindings WHERE outbound_tag = ? AND enabled = 1`, "direct").Scan(&selectID, &order, &from, &detail); err != nil || !strings.Contains(detail, "idx_xray_bindings_outbound") {
		t.Fatalf("query plan = %q, error = %v", detail, err)
	}
	if err := store.DeleteXrayBinding(ctx, binding.ID, 2); err != nil {
		t.Fatal(err)
	}
}

func TestRouteRepositoryProtectsOutboundsAndUsesKeysetRevisions(t *testing.T) {
	t.Parallel()
	database := openTestDatabase(t)
	protector, err := secrets.NewProtector([32]byte{5, 4, 3, 2}, strings.NewReader(strings.Repeat("r", 128)))
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewProtectedStore(database, protector)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	newOutbound := func(id domain.ID, name string) domain.Outbound {
		return domain.Outbound{ID: id, Name: name, Adapter: domain.OutboundAdapterSingBox, Type: domain.OutboundSOCKS5, Server: domain.Endpoint{Host: "192.0.2.40", Port: 1080}, Capabilities: domain.Capabilities{TCP: true, UDP: true}, Health: domain.UnknownOutboundHealth(), Enabled: true}
	}
	primary := newOutbound("route_primary", "Route primary")
	fallback := newOutbound("route_fallback", "Route fallback")
	for _, outbound := range []domain.Outbound{primary, fallback} {
		if _, err := store.CreateOutbound(ctx, outbound, []byte(`{"version":1}`), now); err != nil {
			t.Fatal(err)
		}
	}
	route := domain.Route{
		ID: "vpn_clients", Name: "VPN clients", Source: domain.RouteSource{Kind: domain.RouteSourceSubnet, Subnet: netip.MustParsePrefix("10.8.0.0/24")},
		OutboundID: primary.ID, FailurePolicy: domain.FailureBlock, DNSPolicy: domain.DNSFollowOutbound,
		DNSServers: []netip.Addr{netip.MustParseAddr("1.1.1.1")},
		IPv4Policy: domain.IPv4FollowOutbound, IPv6Policy: domain.IPv6Block, KillSwitch: true, MTU: 1400, TCPMSS: 1360, Enabled: true,
	}
	created, err := store.CreateRoute(ctx, route, now)
	if err != nil || created.Revision != 1 {
		t.Fatalf("created route = %#v, error = %v", created, err)
	}
	for query, index := range map[string]string{
		`EXPLAIN QUERY PLAN SELECT id FROM egress_routes WHERE outbound_id = ?`:          "idx_egress_routes_outbound",
		`EXPLAIN QUERY PLAN SELECT id FROM egress_routes WHERE fallback_outbound_id = ?`: "idx_egress_routes_fallback_outbound",
	} {
		var selectID, order, from int
		var detail string
		if err := database.QueryRow(query, string(primary.ID)).Scan(&selectID, &order, &from, &detail); err != nil || !strings.Contains(detail, index) {
			t.Fatalf("query plan = %q, error = %v, want index %s", detail, err, index)
		}
	}
	duplicate := route
	duplicate.ID = "vpn_clients_duplicate"
	duplicate.Name = "VPN clients duplicate"
	if _, err := store.CreateRoute(ctx, duplicate, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate enabled selector error = %v", err)
	}
	if err := store.DeleteOutbound(ctx, primary.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete referenced primary error = %v", err)
	}
	route.FailurePolicy = domain.FailureFailover
	route.FallbackOutboundID = fallback.ID
	updated, err := store.UpdateRoute(ctx, route, 1, now.Add(time.Second))
	if err != nil || updated.Revision != 2 || updated.Route.FallbackOutboundID != fallback.ID {
		t.Fatalf("updated route = %#v, error = %v", updated, err)
	}
	if err := store.DeleteOutbound(ctx, fallback.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete referenced fallback error = %v", err)
	}
	items, err := store.ListRoutes(ctx, "", 10)
	if err != nil || len(items) != 1 || items[0].Route.ID != route.ID {
		t.Fatalf("route list = %#v, error = %v", items, err)
	}
	after, err := store.ListRoutes(ctx, route.ID, 10)
	if err != nil || len(after) != 0 {
		t.Fatalf("route keyset page = %#v, error = %v", after, err)
	}
	if _, err := store.UpdateRoute(ctx, route, 1, now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale route update error = %v", err)
	}
	if err := store.DeleteRoute(ctx, route.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteOutbound(ctx, primary.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteOutbound(ctx, fallback.ID, 1); err != nil {
		t.Fatal(err)
	}
}

func TestOutboundRepositoryEncryptsCredentialsAndUsesOptimisticRevision(t *testing.T) {
	t.Parallel()
	database := openTestDatabase(t)
	protector, err := secrets.NewProtector([32]byte{9, 8, 7, 6}, strings.NewReader(strings.Repeat("n", 128)))
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewProtectedStore(database, protector)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	outbound := domain.Outbound{
		ID: "vless_primary", Name: "VLESS Primary", Adapter: domain.OutboundAdapterSingBox, Type: domain.OutboundVLESS,
		Server: domain.Endpoint{Host: "edge.example.com", Port: 443}, Capabilities: domain.Capabilities{TCP: true, UDP: true},
		Health: domain.UnknownOutboundHealth(), Enabled: true, SecretMetadata: []string{"uuid"},
	}
	document := []byte(`{"uuid":"TEST_ONLY_NOT_A_SECRET"}`)
	created, err := store.CreateOutbound(ctx, outbound, document, now)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 {
		t.Fatalf("created revision = %d", created.Revision)
	}
	var definition string
	var ciphertext []byte
	if err := database.QueryRow(`SELECT o.definition, c.ciphertext FROM outbounds o JOIN outbound_credentials c ON c.outbound_id = o.id WHERE o.id = ?`, string(outbound.ID)).Scan(&definition, &ciphertext); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(definition, "TEST_ONLY_NOT_A_SECRET") || strings.Contains(string(ciphertext), "TEST_ONLY_NOT_A_SECRET") {
		t.Fatal("stored outbound leaked credential plaintext")
	}
	opened, err := store.OutboundCredential(ctx, outbound.ID)
	if err != nil || string(opened) != string(document) {
		t.Fatalf("opened credential = %q, error = %v", opened, err)
	}
	outbound.Enabled = false
	updated, err := store.UpdateOutbound(ctx, outbound, 1, nil, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.Outbound.Enabled {
		t.Fatalf("updated outbound = %#v", updated)
	}
	changedEndpoint := outbound
	changedEndpoint.Server.Host = "other.example.com"
	if _, err := store.UpdateOutbound(ctx, changedEndpoint, 2, nil, now.Add(2*time.Second)); err == nil || !strings.Contains(err.Error(), "without replacement credential") {
		t.Fatalf("credential-shape update error = %v", err)
	}
	if _, err := store.UpdateOutbound(ctx, outbound, 1, nil, now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	clone := outbound
	clone.ID = "vless_clone"
	clone.Name = "VLESS Clone"
	cloned, err := store.CloneOutbound(ctx, outbound.ID, 2, clone, now.Add(3*time.Second))
	if err != nil || cloned.Revision != 1 {
		t.Fatalf("cloned outbound = %#v, error = %v", cloned, err)
	}
	clonedDocument, err := store.OutboundCredential(ctx, clone.ID)
	if err != nil || string(clonedDocument) != string(document) {
		t.Fatalf("cloned credential = %q, error = %v", clonedDocument, err)
	}
	if err := store.DeleteOutbound(ctx, clone.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteOutbound(ctx, outbound.ID, 2); err != nil {
		t.Fatal(err)
	}
	var credentials int
	if err := database.QueryRow(`SELECT COUNT(*) FROM outbound_credentials`).Scan(&credentials); err != nil || credentials != 0 {
		t.Fatalf("credential rows = %d, error = %v", credentials, err)
	}
	batchOutbound := outbound
	batchOutbound.ID = "batch_atomic"
	batchOutbound.Name = "Batch atomic"
	if _, err := store.CreateOutbounds(ctx, []NewOutbound{{Outbound: batchOutbound, CredentialDocument: document}, {Outbound: batchOutbound, CredentialDocument: document}}, now.Add(4*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate batch error = %v", err)
	}
	var batchRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM outbounds WHERE id = ?`, string(batchOutbound.ID)).Scan(&batchRows); err != nil || batchRows != 0 {
		t.Fatalf("rolled-back batch rows = %d, error = %v", batchRows, err)
	}
}

func TestHAProxyRepositoriesEnforceReferencesAndOptimisticRevision(t *testing.T) {
	t.Parallel()
	store := NewStore(openTestDatabase(t))
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	primary := domain.HAProxyBackend{ID: "api_primary", Name: "API Primary", Server: domain.Endpoint{Host: "10.20.0.10", Port: 8080}, Weight: 100, HealthCheck: true, Health: domain.Health{Status: domain.HealthUnknown}, Enabled: true}
	backup := domain.HAProxyBackend{ID: "api_backup", Name: "API Backup", Server: domain.Endpoint{Host: "10.20.0.11", Port: 8080}, Weight: 50, Backup: true, HealthCheck: true, Health: domain.Health{Status: domain.HealthUnknown}, Enabled: true}
	if _, err := store.CreateHAProxyBackend(ctx, primary, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateHAProxyBackend(ctx, backup, now); err != nil {
		t.Fatal(err)
	}
	frontend := domain.HAProxyFrontend{ID: "public_api", Name: "Public API", Bind: netip.MustParseAddr("192.0.2.10"), Port: 443, BackendIDs: []domain.ID{primary.ID, backup.ID}, Algorithm: domain.BalanceLeastConn, Enabled: true}
	created, err := store.CreateHAProxyFrontend(ctx, frontend, now)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 {
		t.Fatalf("created revision = %d", created.Revision)
	}
	if err := store.DeleteHAProxyBackend(ctx, primary.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete referenced backend error = %v", err)
	}
	frontend.Algorithm = domain.BalanceRoundRobin
	updated, err := store.UpdateHAProxyFrontend(ctx, frontend, 1, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.Frontend.Algorithm != domain.BalanceRoundRobin {
		t.Fatalf("updated frontend = %#v", updated)
	}
	if _, err := store.UpdateHAProxyFrontend(ctx, frontend, 1, now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	backends, err := store.ListHAProxyBackends(ctx, "api_backup", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(backends) != 1 || backends[0].Backend.ID != primary.ID {
		t.Fatalf("backend keyset page = %#v", backends)
	}
	if err := store.DeleteHAProxyFrontend(ctx, frontend.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteHAProxyBackend(ctx, primary.ID, 1); err != nil {
		t.Fatal(err)
	}
	missing := frontend
	missing.ID = "missing_ref"
	missing.Name = "Missing reference"
	missing.BackendIDs = []domain.ID{"does_not_exist"}
	if _, err := store.CreateHAProxyFrontend(ctx, missing, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("missing backend reference error = %v", err)
	}
}

func TestStoreAdminSessionAndThrottleQueries(t *testing.T) {
	t.Parallel()

	database := openTestDatabase(t)
	store := NewStore(database)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	admin := Admin{
		ID:           "admin-primary",
		Username:     "operator",
		PasswordHash: "$argon2id$fixture",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := store.CreateAdmin(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAdmin(ctx, admin); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate CreateAdmin() error = %v, want ErrConflict", err)
	}
	loaded, err := store.AdminByUsername(ctx, "OPERATOR")
	if err != nil {
		t.Fatal(err)
	}
	if loaded != admin {
		t.Fatalf("AdminByUsername() = %#v, want %#v", loaded, admin)
	}

	tokenHash := sha256.Sum256([]byte("token"))
	csrfHash := sha256.Sum256([]byte("csrf"))
	session := Session{
		TokenHash:  tokenHash,
		AdminID:    admin.ID,
		CSRFHash:   csrfHash,
		CreatedAt:  now,
		ExpiresAt:  now.Add(time.Hour),
		LastSeenAt: now,
	}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	active, err := store.ActiveSession(ctx, tokenHash, now)
	if err != nil {
		t.Fatal(err)
	}
	if active.AdminID != admin.ID || active.TokenHash != tokenHash || active.CSRFHash != csrfHash {
		t.Fatalf("ActiveSession() = %#v", active)
	}
	if err := store.RevokeSession(ctx, tokenHash, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActiveSession(ctx, tokenHash, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked ActiveSession() error = %v, want ErrNotFound", err)
	}

	bucket := sha256.Sum256([]byte("operator|192.0.2.10"))
	for index := 0; index < 3; index++ {
		if err := store.RecordLoginAttempt(ctx, bucket, false, now.Add(time.Duration(index)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	count, err := store.CountRecentFailedAttempts(ctx, bucket, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("CountRecentFailedAttempts() = %d, want 3", count)
	}
}

func TestSettingsUpsert(t *testing.T) {
	t.Parallel()

	store := NewStore(openTestDatabase(t))
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.PutSetting(ctx, "panel_port", "43127", now); err != nil {
		t.Fatal(err)
	}
	if err := store.PutSetting(ctx, "panel_port", "43128", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	value, err := store.Setting(ctx, "panel_port")
	if err != nil {
		t.Fatal(err)
	}
	if value != "43128" {
		t.Fatalf("Setting() = %q, want 43128", value)
	}
}

func TestPortForwardRepositoryUsesOptimisticRevisionAndKeysetPagination(t *testing.T) {
	t.Parallel()

	store := NewStore(openTestDatabase(t))
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	forward := domain.PortForward{
		ID: "forward_a", Name: "Forward A", Protocols: []domain.TransportProtocol{domain.ProtocolTCP},
		ListenAddress: netip.MustParseAddr("203.0.113.10"), ListenPorts: []domain.PortRange{{From: 8443, To: 8443}},
		RemoteAddress: netip.MustParseAddr("10.10.0.5"), RemotePortStart: 443, Enabled: false,
	}
	created, err := store.CreatePortForward(ctx, forward, now)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 {
		t.Fatalf("created revision = %d", created.Revision)
	}
	forward.Enabled = true
	updated, err := store.UpdatePortForward(ctx, forward, 1, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || !updated.Forward.Enabled {
		t.Fatalf("updated = %#v", updated)
	}
	if _, err := store.UpdatePortForward(ctx, forward, 1, now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v, want ErrConflict", err)
	}
	second := forward
	second.ID = "forward_b"
	second.Name = "Forward B"
	if _, err := store.CreatePortForward(ctx, second, now); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListPortForwards(ctx, "forward_a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Forward.ID != "forward_b" {
		t.Fatalf("keyset page = %#v", page)
	}
	if err := store.DeletePortForward(ctx, forward.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete error = %v, want ErrConflict", err)
	}
	if err := store.DeletePortForward(ctx, forward.ID, 2); err != nil {
		t.Fatal(err)
	}
}

func TestThrottleQueryUsesCompositeIndex(t *testing.T) {
	t.Parallel()

	database := openTestDatabase(t)
	var detail string
	row := database.QueryRow(`
        EXPLAIN QUERY PLAN
        SELECT COUNT(id)
        FROM login_attempts
        WHERE bucket_hash = ? AND succeeded = 0 AND attempted_at >= ?
    `, make([]byte, 32), time.Now().Add(-time.Hour).Unix())
	var identifier, parent, notUsed int
	if err := row.Scan(&identifier, &parent, &notUsed, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "idx_login_attempts_bucket_result_time") {
		t.Fatalf("query plan does not use throttle index: %s", detail)
	}
}

func TestOutboundFilteredKeysetQueryUsesCompositeIndex(t *testing.T) {
	t.Parallel()
	database := openTestDatabase(t)
	var detail string
	row := database.QueryRow(`
        EXPLAIN QUERY PLAN
        SELECT id
        FROM outbounds
        WHERE enabled = ? AND adapter = ? AND id > ?
        ORDER BY id
        LIMIT ?
    `, true, "sing-box", "", 100)
	var identifier, parent, notUsed int
	if err := row.Scan(&identifier, &parent, &notUsed, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "idx_outbounds_enabled_adapter_id") {
		t.Fatalf("query plan does not use outbound composite index: %s", detail)
	}
}

func TestRetentionQueryUsesTimeIndex(t *testing.T) {
	t.Parallel()

	database := openTestDatabase(t)
	var detail string
	row := database.QueryRow(`
        EXPLAIN QUERY PLAN
        SELECT id
        FROM login_attempts
        WHERE attempted_at < ?
        ORDER BY attempted_at
        LIMIT ?
    `, time.Now().Add(-24*time.Hour).Unix(), 1_000)
	var identifier, parent, notUsed int
	if err := row.Scan(&identifier, &parent, &notUsed, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "idx_login_attempts_retention") {
		t.Fatalf("query plan does not use retention index: %s", detail)
	}
}

func TestOperationJournalTransitionsAndUnfinishedIndex(t *testing.T) {
	t.Parallel()

	database := openTestDatabase(t)
	store := NewStore(database)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	operation := domain.Transaction{
		ID: "nat_001", Operation: "nat_apply", State: domain.TransactionPrepared,
		RequestedChange: `{}`, PreviousSnapshot: `absent`, CandidateConfig: `table ip egm_nat4 {}`,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionOperation(ctx, operation.ID, domain.TransactionPrepared, domain.TransactionValidated, now.Add(time.Second), ""); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionOperation(ctx, operation.ID, domain.TransactionPrepared, domain.TransactionFailed, now.Add(2*time.Second), "stale"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale transition error = %v, want ErrConflict", err)
	}
	loaded, err := store.Operation(ctx, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != domain.TransactionValidated || loaded.PreviousSnapshot != "absent" {
		t.Fatalf("operation = %#v", loaded)
	}
	unfinished, err := store.UnfinishedOperations(ctx, 10)
	if err != nil || len(unfinished) != 1 {
		t.Fatalf("unfinished = %#v, error = %v", unfinished, err)
	}

	var detail string
	row := database.QueryRow(`
        EXPLAIN QUERY PLAN
        SELECT id
        FROM operation_journal
        WHERE state NOT IN ('COMMITTED', 'ROLLED_BACK', 'FAILED')
        ORDER BY updated_at
        LIMIT ?
    `, 100)
	var identifier, parent, notUsed int
	if err := row.Scan(&identifier, &parent, &notUsed, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "idx_operation_journal_unfinished") {
		t.Fatalf("query plan does not use unfinished-operation index: %s", detail)
	}

	row = database.QueryRow(`
        EXPLAIN QUERY PLAN
        SELECT id
        FROM operation_journal
        WHERE state IN ('COMMITTED', 'ROLLED_BACK', 'FAILED') AND updated_at < ?
        ORDER BY updated_at
        LIMIT ?
    `, now.Add(time.Hour).Unix(), 100)
	if err := row.Scan(&identifier, &parent, &notUsed, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "idx_operation_journal_finished_retention") {
		t.Fatalf("query plan does not use finished-operation retention index: %s", detail)
	}
}
