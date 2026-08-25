package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postWithOrigin issues a POST carrying an explicit Origin header and Host.
func postWithOrigin(t *testing.T, srv *Server, path, origin, host, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if host != "" {
		req.Host = host
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestCSRFBlocksCrossOrigin(t *testing.T) {
	srv := newTestServer(t)
	rr := postWithOrigin(t, srv, "/api/v1/downloads/pause", "http://evil.example", "reverb.local", "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST = %d, want 403", rr.Code)
	}
}

func TestCSRFAllowsSameOrigin(t *testing.T) {
	srv := newTestServer(t)
	// Same host in Origin and Host → passes the guard; the handler then runs
	// (with no download manager wired it errors), so anything but 403 means the
	// request was not blocked at the guard.
	rr := postWithOrigin(t, srv, "/api/v1/downloads/pause", "http://reverb.local", "reverb.local", "")
	if rr.Code == http.StatusForbidden {
		t.Fatalf("same-origin POST was blocked (%d); should reach the handler", rr.Code)
	}
}

func TestCSRFAllowsMissingOrigin(t *testing.T) {
	srv := newTestServer(t)
	// No Origin/Referer (curl / native client) → not a CSRF vector → allowed through.
	rr := postWithOrigin(t, srv, "/api/v1/downloads/pause", "", "reverb.local", "")
	if rr.Code == http.StatusForbidden {
		t.Fatalf("origin-less POST was blocked (%d); should reach the handler", rr.Code)
	}
}

func TestCSRFDoesNotBlockGET(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	req.Header.Set("Origin", "http://evil.example")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cross-origin GET = %d, want 200 (reads are exempt)", rec.Code)
	}
}

func TestSecurityHeadersPresent(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	srv.Handler().ServeHTTP(rec, req)
	h := rec.Header()
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := h.Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
	if h.Get("Content-Security-Policy") == "" {
		t.Error("missing Content-Security-Policy header")
	}
}
