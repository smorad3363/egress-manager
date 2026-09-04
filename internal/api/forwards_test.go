package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/nat"
)

func TestPortForwardCRUDPlanApplyAndCSRF(t *testing.T) {
	t.Parallel()

	server, control := newTestAPIServer(t)
	handler := server.Handler()
	cookie, csrf := loginForForwardTest(t, handler)
	forward := domain.PortForward{
		ID: "web_tls", Name: "Web TLS", Protocols: []domain.TransportProtocol{domain.ProtocolTCP},
		ListenAddress: netip.MustParseAddr("203.0.113.10"), ListenPorts: []domain.PortRange{{From: 8443, To: 8443}},
		RemoteAddress: netip.MustParseAddr("10.10.0.5"), RemotePortStart: 443, Enabled: true,
	}

	withoutCSRF := jsonRequest(t, http.MethodPost, "/api/v1/port-forwards", forward)
	withoutCSRF.AddCookie(cookie)
	withoutCSRFResponse := httptest.NewRecorder()
	handler.ServeHTTP(withoutCSRFResponse, withoutCSRF)
	if withoutCSRFResponse.Code != http.StatusForbidden {
		t.Fatalf("create without CSRF status = %d", withoutCSRFResponse.Code)
	}

	create := jsonRequest(t, http.MethodPost, "/api/v1/port-forwards", forward)
	create.AddCookie(cookie)
	create.Header.Set(CSRFHeader, csrf)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v1/port-forwards?limit=10", nil)
	list.AddCookie(cookie)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK || !bytes.Contains(listResponse.Body.Bytes(), []byte(`"id":"web_tls"`)) {
		t.Fatalf("list status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}

	plan := jsonRequest(t, http.MethodPost, "/api/v1/port-forwards/plan", nat.PlanRequest{Family: nat.IPv4, Forwards: []domain.PortForward{forward}})
	plan.AddCookie(cookie)
	plan.Header.Set(CSRFHeader, csrf)
	planResponse := httptest.NewRecorder()
	handler.ServeHTTP(planResponse, plan)
	if planResponse.Code != http.StatusOK || !bytes.Contains(planResponse.Body.Bytes(), []byte(`"owned_table":"ip egm_nat4"`)) {
		t.Fatalf("plan status = %d, body = %s", planResponse.Code, planResponse.Body.String())
	}

	apply := jsonRequest(t, http.MethodPost, "/api/v1/port-forwards/apply", map[string]string{"family": "ipv4"})
	apply.AddCookie(cookie)
	apply.Header.Set(CSRFHeader, csrf)
	applyResponse := httptest.NewRecorder()
	handler.ServeHTTP(applyResponse, apply)
	if applyResponse.Code != http.StatusOK || !bytes.Contains(applyResponse.Body.Bytes(), []byte(`"state":"COMMITTED"`)) {
		t.Fatalf("apply status = %d, body = %s", applyResponse.Code, applyResponse.Body.String())
	}
	if control.calls != 2 {
		t.Fatalf("control calls = %d, want 2", control.calls)
	}

	counters := httptest.NewRequest(http.MethodGet, "/api/v1/port-forwards/counters?family=ipv4", nil)
	counters.AddCookie(cookie)
	countersResponse := httptest.NewRecorder()
	handler.ServeHTTP(countersResponse, counters)
	if countersResponse.Code != http.StatusOK || !bytes.Contains(countersResponse.Body.Bytes(), []byte(`"accepted_packets":5`)) {
		t.Fatalf("counters status = %d, body = %s", countersResponse.Code, countersResponse.Body.String())
	}

	forward.Enabled = false
	update := jsonRequest(t, http.MethodPut, "/api/v1/port-forwards", updateForwardRequest{Forward: forward, ExpectedRevision: 1})
	update.AddCookie(cookie)
	update.Header.Set(CSRFHeader, csrf)
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, update)
	if updateResponse.Code != http.StatusOK || !bytes.Contains(updateResponse.Body.Bytes(), []byte(`"revision":2`)) {
		t.Fatalf("update status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}

	deleteRequest := jsonRequest(t, http.MethodDelete, "/api/v1/port-forwards", deleteForwardRequest{ID: forward.ID, ExpectedRevision: 2})
	deleteRequest.AddCookie(cookie)
	deleteRequest.Header.Set(CSRFHeader, csrf)
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestUnauthenticatedPortForwardMutationNeverReachesControl(t *testing.T) {
	t.Parallel()

	server, control := newTestAPIServer(t)
	request := jsonRequest(t, http.MethodPost, "/api/v1/port-forwards/apply", map[string]string{"family": "ipv4"})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || control.calls != 0 {
		t.Fatalf("status = %d, control calls = %d", response.Code, control.calls)
	}
}

func loginForForwardTest(t *testing.T, handler http.Handler) (*http.Cookie, string) {
	t.Helper()
	login := jsonRequest(t, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "operator", "password": "correct horse battery staple"})
	login.RemoteAddr = "192.0.2.55:54321"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, login)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", response.Code, response.Body.String())
	}
	var body loginResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return response.Result().Cookies()[0], body.CSRFToken
}

func jsonRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	return request
}
