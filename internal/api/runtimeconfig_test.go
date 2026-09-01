package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRuntimeConfigPublishesPortWhenSet(t *testing.T) {
	srv := NewServer(Deps{AllowedHosts: testAllowedHosts, Desktop: true, LocalAPIPort: 41234})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runtime-config.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("content-type = %q", ct)
	}
	if got := rec.Body.String(); !strings.Contains(got, "window.__REVERB_PORT__ = 41234;") {
		t.Errorf("body = %q", got)
	}
}

func TestRuntimeConfigIsInertWhenPortUnset(t *testing.T) {
	// Same-origin deployments (Docker, dev) must not get a port injected — the
	// SPA derives its URLs from location there.
	srv := NewServer(Deps{AllowedHosts: testAllowedHosts})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runtime-config.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "__REVERB_PORT__") {
		t.Errorf("must not define __REVERB_PORT__, got %q", rec.Body.String())
	}
}

func TestRuntimeConfigNeedsNoSession(t *testing.T) {
	// It must load before a session exists, so it cannot sit behind auth.
	srv := NewServer(Deps{AllowedHosts: testAllowedHosts, Desktop: true, LocalAPIPort: 9999})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runtime-config.js", nil))
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("runtime-config.js must not require authentication")
	}
}
