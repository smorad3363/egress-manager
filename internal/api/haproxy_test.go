package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
	managedHAProxy "github.com/egress-manager/egress-manager/internal/haproxy"
)

func TestHAProxyCRUDPlanApplyStatsAndCSRF(t *testing.T) {
	t.Parallel()
	server, control := newTestAPIServer(t)
	handler := server.Handler()
	cookie, csrf := loginForForwardTest(t, handler)
	backend := domain.HAProxyBackend{ID: "api_primary", Name: "API Primary", Server: domain.Endpoint{Host: "10.20.0.10", Port: 8080}, Weight: 100, HealthCheck: true, Health: domain.Health{Status: domain.HealthUnknown}, Enabled: true}
	frontend := domain.HAProxyFrontend{ID: "public_api", Name: "Public API", Bind: netip.MustParseAddr("192.0.2.10"), Port: 443, BackendIDs: []domain.ID{backend.ID}, Algorithm: domain.BalanceRoundRobin, Enabled: true}

	withoutCSRF := jsonRequest(t, http.MethodPost, "/api/v1/haproxy/backends", backend)
	withoutCSRF.AddCookie(cookie)
	withoutCSRFResponse := httptest.NewRecorder()
	handler.ServeHTTP(withoutCSRFResponse, withoutCSRF)
	if withoutCSRFResponse.Code != http.StatusForbidden {
		t.Fatalf("create without CSRF status = %d", withoutCSRFResponse.Code)
	}

	createBackend := jsonRequest(t, http.MethodPost, "/api/v1/haproxy/backends", backend)
	createBackend.AddCookie(cookie)
	createBackend.Header.Set(CSRFHeader, csrf)
	createBackendResponse := httptest.NewRecorder()
	handler.ServeHTTP(createBackendResponse, createBackend)
	if createBackendResponse.Code != http.StatusCreated {
		t.Fatalf("backend create = %d %s", createBackendResponse.Code, createBackendResponse.Body.String())
	}
	createFrontend := jsonRequest(t, http.MethodPost, "/api/v1/haproxy/frontends", frontend)
	createFrontend.AddCookie(cookie)
	createFrontend.Header.Set(CSRFHeader, csrf)
	createFrontendResponse := httptest.NewRecorder()
	handler.ServeHTTP(createFrontendResponse, createFrontend)
	if createFrontendResponse.Code != http.StatusCreated {
		t.Fatalf("frontend create = %d %s", createFrontendResponse.Code, createFrontendResponse.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v1/haproxy/frontends?limit=10", nil)
	list.AddCookie(cookie)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK || !bytes.Contains(listResponse.Body.Bytes(), []byte(`"id":"public_api"`)) {
		t.Fatalf("frontend list = %d %s", listResponse.Code, listResponse.Body.String())
	}

	plan := jsonRequest(t, http.MethodPost, "/api/v1/haproxy/plan", managedHAProxy.PlanRequest{Frontends: []domain.HAProxyFrontend{frontend}, Backends: []domain.HAProxyBackend{backend}})
	plan.AddCookie(cookie)
	plan.Header.Set(CSRFHeader, csrf)
	planResponse := httptest.NewRecorder()
	handler.ServeHTTP(planResponse, plan)
	if planResponse.Code != http.StatusOK || !bytes.Contains(planResponse.Body.Bytes(), []byte(`"engine":"haproxy"`)) {
		t.Fatalf("plan = %d %s", planResponse.Code, planResponse.Body.String())
	}

	apply := jsonRequest(t, http.MethodPost, "/api/v1/haproxy/apply", struct{}{})
	apply.AddCookie(cookie)
	apply.Header.Set(CSRFHeader, csrf)
	applyResponse := httptest.NewRecorder()
	handler.ServeHTTP(applyResponse, apply)
	if applyResponse.Code != http.StatusOK || !bytes.Contains(applyResponse.Body.Bytes(), []byte(`"state":"COMMITTED"`)) {
		t.Fatalf("apply = %d %s", applyResponse.Code, applyResponse.Body.String())
	}

	stats := httptest.NewRequest(http.MethodGet, "/api/v1/haproxy/stats", nil)
	stats.AddCookie(cookie)
	statsResponse := httptest.NewRecorder()
	handler.ServeHTTP(statsResponse, stats)
	if statsResponse.Code != http.StatusOK || !bytes.Contains(statsResponse.Body.Bytes(), []byte(`"pid":42`)) {
		t.Fatalf("stats = %d %s", statsResponse.Code, statsResponse.Body.String())
	}
	if control.calls != 3 {
		t.Fatalf("control calls = %d, want 3", control.calls)
	}

	deleteBackend := jsonRequest(t, http.MethodDelete, "/api/v1/haproxy/backends", deleteForwardRequest{ID: backend.ID, ExpectedRevision: 1})
	deleteBackend.AddCookie(cookie)
	deleteBackend.Header.Set(CSRFHeader, csrf)
	deleteBackendResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteBackendResponse, deleteBackend)
	if deleteBackendResponse.Code != http.StatusConflict {
		t.Fatalf("referenced backend delete = %d %s", deleteBackendResponse.Code, deleteBackendResponse.Body.String())
	}
	deleteFrontend := jsonRequest(t, http.MethodDelete, "/api/v1/haproxy/frontends", deleteForwardRequest{ID: frontend.ID, ExpectedRevision: 1})
	deleteFrontend.AddCookie(cookie)
	deleteFrontend.Header.Set(CSRFHeader, csrf)
	deleteFrontendResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteFrontendResponse, deleteFrontend)
	if deleteFrontendResponse.Code != http.StatusNoContent {
		t.Fatalf("frontend delete = %d", deleteFrontendResponse.Code)
	}
}

func TestUnauthenticatedHAProxyApplyNeverReachesControl(t *testing.T) {
	t.Parallel()
	server, control := newTestAPIServer(t)
	request := jsonRequest(t, http.MethodPost, "/api/v1/haproxy/apply", struct{}{})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || control.calls != 0 {
		t.Fatalf("status = %d, calls = %d", response.Code, control.calls)
	}
}
