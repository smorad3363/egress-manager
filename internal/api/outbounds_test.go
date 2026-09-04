package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	managedSingBox "github.com/egress-manager/egress-manager/internal/singbox"
)

const outboundTestSecret = "outbound-api-password"

func TestOutboundAPIRequiresAuthenticationCSRFAndNeverReturnsSecrets(t *testing.T) {
	server, control := newTestAPIServer(t)
	handler := server.Handler()
	store := server.outbounds.(*database.Store)
	parsed, err := managedSingBox.ParseImport("socks5://operator:" + outboundTestSecret + "@192.0.2.30:1080#Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.CreateOutbound(context.Background(), parsed[0].Outbound, parsed[0].CredentialDocument, server.now())
	if err != nil {
		t.Fatal(err)
	}

	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/v1/outbounds", nil)
	unauthenticatedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticatedRecorder, unauthenticated)
	if unauthenticatedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticatedRecorder.Code)
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

	listed := request(http.MethodGet, "/api/v1/outbounds?limit=10", "", false)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), outboundTestSecret) || strings.Contains(listed.Body.String(), "operator") {
		t.Fatalf("unsafe list response: status = %d body = %s", listed.Code, listed.Body.String())
	}
	missingCSRF := request(http.MethodPut, "/api/v1/outbounds", `{}`, false)
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("update without CSRF status = %d", missingCSRF.Code)
	}

	updatedOutbound := stored.Outbound
	updatedOutbound.Enabled = false
	updateBody, _ := json.Marshal(map[string]any{"outbound": updatedOutbound, "expected_revision": stored.Revision})
	updated := request(http.MethodPut, "/api/v1/outbounds", string(updateBody), true)
	if updated.Code != http.StatusOK || strings.Contains(updated.Body.String(), outboundTestSecret) {
		t.Fatalf("update status = %d body = %s", updated.Code, updated.Body.String())
	}

	cloneBody, _ := json.Marshal(map[string]any{"source_id": stored.Outbound.ID, "expected_revision": 2, "id": "amsterdam_copy", "name": "Amsterdam copy"})
	clone := request(http.MethodPost, "/api/v1/outbounds/clone", string(cloneBody), true)
	if clone.Code != http.StatusCreated || strings.Contains(clone.Body.String(), outboundTestSecret) {
		t.Fatalf("clone status = %d body = %s", clone.Code, clone.Body.String())
	}

	control.singBoxImport = managedSingBox.ImportResponse{Outbounds: []domain.Outbound{parsed[0].Outbound}}
	importBody := `{"input":"socks5://operator:` + outboundTestSecret + `@192.0.2.31:1080#Imported"}`
	imported := request(http.MethodPost, "/api/v1/outbounds/import", importBody, true)
	if imported.Code != http.StatusCreated || strings.Contains(imported.Body.String(), outboundTestSecret) || strings.Contains(imported.Body.String(), "operator") {
		t.Fatalf("unsafe import response: status = %d body = %s", imported.Code, imported.Body.String())
	}
	if !strings.Contains(control.lastImport.Input, outboundTestSecret) {
		t.Fatal("import input did not reach authenticated control service")
	}

	control.singBoxTest = managedSingBox.TestResponse{Results: []managedSingBox.TestResult{{Outbound: parsed[0].Outbound, Health: domain.UnknownOutboundHealth()}}}
	tested := request(http.MethodPost, "/api/v1/outbounds/test", importBody, true)
	if tested.Code != http.StatusOK || strings.Contains(tested.Body.String(), outboundTestSecret) || strings.Contains(tested.Body.String(), "operator") {
		t.Fatalf("unsafe test response: status = %d body = %s", tested.Code, tested.Body.String())
	}

	control.singBoxPlan = managedSingBox.Plan{Engine: "sing-box", StateHash: "state", CandidateHash: "candidate", EnabledOutbounds: 1, Actions: []managedSingBox.Action{{Kind: "outbound", Resource: string(stored.Outbound.ID), Summary: "Configure an enabled sing-box outbound."}}}
	planned := request(http.MethodPost, "/api/v1/outbounds/plan", `{}`, true)
	if planned.Code != http.StatusOK || strings.Contains(planned.Body.String(), outboundTestSecret) || strings.Contains(planned.Body.String(), "candidate_config") {
		t.Fatalf("unsafe plan response: status = %d body = %s", planned.Code, planned.Body.String())
	}
	applied := request(http.MethodPost, "/api/v1/outbounds/apply", `{"expected_state_hash":"state","expected_candidate_hash":"candidate"}`, true)
	if applied.Code != http.StatusOK || !strings.HasPrefix(string(control.lastSingBoxApply.TransactionID), "singbox_") || control.lastSingBoxApply.ExpectedStateHash != "state" || control.lastSingBoxApply.ExpectedCandidateHash != "candidate" {
		t.Fatalf("apply status = %d transaction = %q", applied.Code, control.lastSingBoxApply.TransactionID)
	}
}

func TestOutboundAPIRejectsUnknownFieldsAndStaleRevision(t *testing.T) {
	server, _ := newTestAPIServer(t)
	handler := server.Handler()
	cookie, csrf := loginForOutboundTest(t, handler)
	request := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.AddCookie(cookie)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(CSRFHeader, csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := request("/api/v1/outbounds/import", `{"input":"socks5://192.0.2.10:1080","secret":true}`); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown import field status = %d", response.Code)
	}
	if response := request("/api/v1/outbounds/test", `{"id":"missing","expected_revision":0,"input":"socks5://192.0.2.10:1080"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous test status = %d", response.Code)
	}
}

func loginForOutboundTest(t *testing.T, handler http.Handler) (*http.Cookie, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"operator","password":"correct horse battery staple"}`))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "192.0.2.80:54000"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d body = %s", response.Code, response.Body.String())
	}
	var body loginResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return response.Result().Cookies()[0], body.CSRFToken
}
