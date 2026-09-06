package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPanelHandlerServesAssetsAndDelegatesAPI(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(`<html><div id="root">panel</div></html>`), 0o644); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(root, "assets", "app.js"), []byte(`console.log("panel")`), 0o644); err != nil { t.Fatal(err) }
	apiHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.Header().Set("Content-Type", "application/json"); _, _ = io.WriteString(writer, `{"status":"ok"}`) })
	panel, err := panelHandler(apiHandler, root)
	if err != nil { t.Fatalf("panelHandler() error = %v", err) }
	for _, tc := range []struct{ path string; wantStatus int; wantContent string }{{"/api/v1/health", 200, `"status":"ok"`}, {"/assets/app.js", 200, `console.log`}, {"/routes", 200, `id="root"`}} {
		recorder := httptest.NewRecorder(); panel.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if recorder.Code != tc.wantStatus { t.Fatalf("GET %s status = %d, want %d", tc.path, recorder.Code, tc.wantStatus) }
		if !strings.Contains(recorder.Body.String(), tc.wantContent) { t.Fatalf("GET %s body = %q, want content %q", tc.path, recorder.Body.String(), tc.wantContent) }
	}
}

func TestPanelHandlerRejectsStaticMutation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(`<html></html>`), 0o644); err != nil { t.Fatal(err) }
	panel, err := panelHandler(http.NotFoundHandler(), root); if err != nil { t.Fatal(err) }
	recorder := httptest.NewRecorder(); panel.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routes", nil))
	if recorder.Code != http.StatusMethodNotAllowed { t.Fatalf("POST static route status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed) }
}
