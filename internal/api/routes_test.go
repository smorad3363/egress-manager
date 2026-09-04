package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/routeengine"
	managedSingBox "github.com/egress-manager/egress-manager/internal/singbox"
)

func TestRouteAPIRequiresAuthenticationCSRFAndReviewedHashes(t *testing.T) {
	server, control := newTestAPIServer(t)
	handler := server.Handler()
	store := server.routes.(*database.Store)
	imports, err := managedSingBox.ParseImport("socks5://192.0.2.30:1080#Route")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateOutbound(context.Background(), imports[0].Outbound, imports[0].CredentialDocument, server.now()); err != nil {
		t.Fatal(err)
	}
	route := domain.Route{
		ID: "vpn_clients", Name: "VPN clients", Source: domain.RouteSource{Kind: domain.RouteSourceInterface, Interface: "tun0"},
		OutboundID: imports[0].Outbound.ID, FailurePolicy: domain.FailureBlock, DNSPolicy: domain.DNSFollowOutbound,
		DNSServers: []netip.Addr{netip.MustParseAddr("1.1.1.1")}, IPv4Policy: domain.IPv4FollowOutbound,
		IPv6Policy: domain.IPv6Block, KillSwitch: true, MTU: 1400, TCPMSS: 1360, Enabled: true,
	}
	body, _ := json.Marshal(route)

	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil)
	unauthenticatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticatedResponse.Code)
	}

	cookie, csrf := loginForOutboundTest(t, handler)
	request := func(method, path string, body []byte, includeCSRF bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(string(body)))
		req.AddCookie(cookie)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if includeCSRF {
			req.Header.Set(CSRFHeader, csrf)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := request(http.MethodPost, "/api/v1/routes", body, false); response.Code != http.StatusForbidden {
		t.Fatalf("create without CSRF status = %d", response.Code)
	}
	created := request(http.MethodPost, "/api/v1/routes", body, true)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"id":"vpn_clients"`) {
		t.Fatalf("create status = %d body = %s", created.Code, created.Body.String())
	}
	listed := request(http.MethodGet, "/api/v1/routes?limit=10", nil, false)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"dns_servers":["1.1.1.1"]`) {
		t.Fatalf("list status = %d body = %s", listed.Code, listed.Body.String())
	}

	control.routePlan = routeengine.Review{
		Engine: "coordinated-egress-routing", SingBoxStateHash: "sb-state", RoutingStateHash: "route-state",
		SingBoxCandidateHash: "sb-candidate", RoutingCandidateHash: "route-candidate", NativeCandidateHash: "native-candidate", CombinedCandidateHash: "combined-candidate",
	}
	planned := request(http.MethodPost, "/api/v1/routes/plan", []byte(`{}`), true)
	if planned.Code != http.StatusOK || strings.Contains(planned.Body.String(), "candidate_config") || !strings.Contains(planned.Body.String(), `"combined_candidate_hash":"combined-candidate"`) {
		t.Fatalf("plan status = %d body = %s", planned.Code, planned.Body.String())
	}
	missingHash := request(http.MethodPost, "/api/v1/routes/apply", []byte(`{"expected_combined_candidate_hash":"combined-candidate"}`), true)
	if missingHash.Code != http.StatusBadRequest {
		t.Fatalf("missing hash apply status = %d", missingHash.Code)
	}
	applyBody := []byte(`{"expected_sing_box_state_hash":"sb-state","expected_routing_state_hash":"route-state","expected_sing_box_candidate_hash":"sb-candidate","expected_routing_candidate_hash":"route-candidate","expected_native_candidate_hash":"native-candidate","expected_combined_candidate_hash":"combined-candidate"}`)
	applied := request(http.MethodPost, "/api/v1/routes/apply", applyBody, true)
	if applied.Code != http.StatusOK || !strings.HasPrefix(string(control.lastRouteApply.TransactionID), "route_") || control.lastRouteApply.ExpectedCombinedCandidate != "combined-candidate" {
		t.Fatalf("apply status = %d request = %#v", applied.Code, control.lastRouteApply)
	}
}
