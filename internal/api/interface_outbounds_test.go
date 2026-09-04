package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
	managedInterface "github.com/egress-manager/egress-manager/internal/interfaceoutbound"
)

func TestInterfaceOutboundAPIRequiresAuthenticationCSRFAndExactHashes(t *testing.T) {
	server, control := newTestAPIServer(t)
	handler := server.Handler()
	control.interfaceImport = managedInterface.ImportResponse{Outbound: domain.Outbound{
		ID: "wireguard_test", Name: "WireGuard test", Adapter: domain.OutboundAdapterInterface, Type: domain.OutboundWireGuard,
		Server: domain.Endpoint{Host: "vpn.example.com", Port: 51820}, Capabilities: domain.Capabilities{TCP: true, UDP: true}, Health: domain.UnknownOutboundHealth(), Enabled: true, SecretMetadata: []string{"private_key"},
	}}
	control.interfacePlan = managedInterface.Review{Engine: "native-interface-outbounds", StateHash: "state-hash", CandidateHash: "candidate-hash", CandidateExists: true, EnabledOutbounds: 1, Actions: []managedInterface.Action{{Kind: "create", Resource: "egmwg0123456789", Summary: "Create owned interface."}}}
	control.interfaceTest = managedInterface.TestResponse{Outbound: control.interfaceImport.Outbound, Health: domain.OutboundHealth{Status: domain.HealthDegraded, ConfigurationValid: domain.ProbePassed, TransportReachable: domain.ProbeUntestable, InternetReachable: domain.ProbeUntestable, TCP: domain.ProbeUntestable, UDP: domain.ProbeUntestable, Detail: "lifecycle_not_applied"}}
	unauthenticated := httptest.NewRequest(http.MethodPost, "/api/v1/interface-outbounds/plan", strings.NewReader(`{}`))
	unauthenticated.Header.Set("Content-Type", "application/json")
	unauthenticatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticatedResponse.Code)
	}
	cookie, csrf := loginForOutboundTest(t, handler)
	request := func(path, body string, includeCSRF bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		if includeCSRF {
			req.Header.Set(CSRFHeader, csrf)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := request("/api/v1/interface-outbounds/import", `{"input":"TEST_ONLY_PRIVATE_PROFILE"}`, false); response.Code != http.StatusForbidden {
		t.Fatalf("import without CSRF status = %d", response.Code)
	}
	imported := request("/api/v1/interface-outbounds/import", `{"input":"TEST_ONLY_PRIVATE_PROFILE"}`, true)
	if imported.Code != http.StatusCreated || strings.Contains(imported.Body.String(), "TEST_ONLY_PRIVATE_PROFILE") || control.lastInterfaceImport.Input != "TEST_ONLY_PRIVATE_PROFILE" {
		t.Fatalf("import status = %d body = %s request = %#v", imported.Code, imported.Body.String(), control.lastInterfaceImport)
	}
	tested := request("/api/v1/interface-outbounds/test", `{"id":"wireguard_test","expected_revision":1}`, true)
	if tested.Code != http.StatusOK || !strings.Contains(tested.Body.String(), `"detail":"lifecycle_not_applied"`) || control.lastInterfaceTest.ID != "wireguard_test" {
		t.Fatalf("test status = %d body = %s request = %#v", tested.Code, tested.Body.String(), control.lastInterfaceTest)
	}
	planned := request("/api/v1/interface-outbounds/plan", `{}`, true)
	if planned.Code != http.StatusOK || !strings.Contains(planned.Body.String(), `"candidate_hash":"candidate-hash"`) || strings.Contains(planned.Body.String(), "candidate_config") {
		t.Fatalf("plan status = %d body = %s", planned.Code, planned.Body.String())
	}
	missing := request("/api/v1/interface-outbounds/apply", `{"expected_candidate_hash":"candidate-hash"}`, true)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing hash status = %d", missing.Code)
	}
	applied := request("/api/v1/interface-outbounds/apply", `{"expected_state_hash":"state-hash","expected_candidate_hash":"candidate-hash"}`, true)
	if applied.Code != http.StatusOK || !strings.HasPrefix(string(control.lastInterfaceApply.TransactionID), "interface_") || control.lastInterfaceApply.ExpectedStateHash != "state-hash" || control.lastInterfaceApply.ExpectedCandidateHash != "candidate-hash" {
		t.Fatalf("apply status = %d request = %#v", applied.Code, control.lastInterfaceApply)
	}
}
