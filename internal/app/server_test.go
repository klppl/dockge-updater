package app

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	webui "dockge-updater/web"
)

func TestServerServesDashboardAndSettingsAPI(t *testing.T) {
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager, err := NewManager(root, NewDocker(&fakeRunner{}), NewStore(filepath.Join(root, "data", "state.json")), "test", logger)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(manager, webui.Files, logger)

	dashboard := httptest.NewRecorder()
	handler.ServeHTTP(dashboard, httptest.NewRequest(http.MethodGet, "/", nil))
	if dashboard.Code != http.StatusOK || !strings.Contains(dashboard.Body.String(), "Dockge Updater") {
		t.Fatalf("dashboard response: status=%d body=%q", dashboard.Code, dashboard.Body.String())
	}
	if policy := dashboard.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "default-src 'self'") {
		t.Fatalf("expected content security policy, got %q", policy)
	}

	payload := []byte(`{"checkTime":"05:30","autoUpdatePolicy":"weekly","updateTime":"02:15","updateWeekday":"Saturday"}`)
	settings := httptest.NewRecorder()
	handler.ServeHTTP(settings, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(payload)))
	if settings.Code != http.StatusOK {
		t.Fatalf("settings response: status=%d body=%q", settings.Code, settings.Body.String())
	}
	if got := manager.Settings(); got.CheckTime != "05:30" || got.UpdateWeekday != "Saturday" {
		t.Fatalf("settings were not applied: %#v", got)
	}

	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"version":"test"`) {
		t.Fatalf("status response: status=%d body=%q", status.Code, status.Body.String())
	}

	for _, asset := range []string{"/tokens.css", "/app.css", "/app.js"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, asset, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected asset %s to return 200 OK, got %d", asset, rec.Code)
		}
	}
}
