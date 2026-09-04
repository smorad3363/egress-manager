package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managedXray "github.com/egress-manager/egress-manager/internal/xray"
)

func TestXrayAPIRequiresAuthenticationCSRFAndExactReviewedHashes(t *testing.T) {
	server, control := newTestAPIServer(t)
	handler := server.Handler()
	control.xrayReport = managedXray.Report{Installations: []managedXray.Installation{{
		Kind: managedXray.Marzban, ConfigPath: "/var/lib/marzban/xray_config.json", ConfigHash: "config-hash",
		Ownership: managedXray.ForeignOwnership, MutationStrategy: managedXray.ReadOnly, Limitations: []string{"Panel-managed configuration is read-only."},
	}}, Warnings: []string{}}
	control.xrayPlan = managedXray.FragmentReview{
		Engine: "xray", ServiceName: "xray.service", ManagedPath: "/etc/xray/zzzz-egress-manager-routing.json",
		ForeignStateHash: "foreign-hash", FragmentStateHash: "fragment-hash", CandidateHash: "candidate-hash", CandidateExists: true,
	}
	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/v1/xray/discovery", nil)
	unauthenticatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticatedResponse.Code)
	}
	cookie, csrf := loginForOutboundTest(t, handler)
	request := func(method, path, body string, includeCSRF bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(cookie)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if includeCSRF {
			req.Header.Set(CSRFHeader, csrf)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	discovery := request(http.MethodGet, "/api/v1/xray/discovery", "", false)
	if discovery.Code != http.StatusOK || !strings.Contains(discovery.Body.String(), `"mutation_strategy":"read_only"`) || strings.Contains(discovery.Body.String(), "candidate_config") {
		t.Fatalf("discovery status = %d body = %s", discovery.Code, discovery.Body.String())
	}
	binding := `{"id":"native_route","inbound_tag":"VLESS inbound:443","outbound_tag":"proxy/de","enabled":true}`
	if response := request(http.MethodPost, "/api/v1/xray/bindings", binding, false); response.Code != http.StatusForbidden {
		t.Fatalf("create without CSRF status = %d", response.Code)
	}
	created := request(http.MethodPost, "/api/v1/xray/bindings", binding, true)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"id":"native_route"`) {
		t.Fatalf("create status = %d body = %s", created.Code, created.Body.String())
	}
	listed := request(http.MethodGet, "/api/v1/xray/bindings?limit=10", "", false)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"outbound_tag":"proxy/de"`) {
		t.Fatalf("list status = %d body = %s", listed.Code, listed.Body.String())
	}
	planned := request(http.MethodPost, "/api/v1/xray/plan", `{}`, true)
	if planned.Code != http.StatusOK || !strings.Contains(planned.Body.String(), `"candidate_hash":"candidate-hash"`) || strings.Contains(planned.Body.String(), "candidate_config") {
		t.Fatalf("plan status = %d body = %s", planned.Code, planned.Body.String())
	}
	missing := request(http.MethodPost, "/api/v1/xray/apply", `{"expected_candidate_hash":"candidate-hash"}`, true)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing hash status = %d", missing.Code)
	}
	applied := request(http.MethodPost, "/api/v1/xray/apply", `{"expected_foreign_state_hash":"foreign-hash","expected_fragment_state_hash":"fragment-hash","expected_candidate_hash":"candidate-hash"}`, true)
	if applied.Code != http.StatusOK || !strings.HasPrefix(string(control.lastXrayApply.TransactionID), "xray_") || control.lastXrayApply.ExpectedCandidateHash != "candidate-hash" {
		t.Fatalf("apply status = %d request = %#v", applied.Code, control.lastXrayApply)
	}
}
