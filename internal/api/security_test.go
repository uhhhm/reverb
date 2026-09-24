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
	rr := postWithOrigin(t, srv, "/api/v1/downloads/pause", "http://evil.example", "example.com", "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST = %d, want 403", rr.Code)
	}
}

func TestCSRFAllowsSameOrigin(t *testing.T) {
	srv := newTestServer(t)
	// Same host in Origin and Host → passes the guard; the handler then runs
	// (with no download manager wired it errors), so anything but 403 means the
	// request was not blocked at the guard.
	rr := postWithOrigin(t, srv, "/api/v1/downloads/pause", "http://example.com", "example.com", "")
	if rr.Code == http.StatusForbidden {
		t.Fatalf("same-origin POST was blocked (%d); should reach the handler", rr.Code)
	}
}

func TestCSRFAllowsMissingOrigin(t *testing.T) {
	srv := newTestServer(t)
	// No Origin/Referer (curl / native client) → not a CSRF vector → allowed through.
	rr := postWithOrigin(t, srv, "/api/v1/downloads/pause", "", "example.com", "")
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

// The phone profile serves the owner API on a loopback port other apps on the
// phone can reach, so it takes a per-launch secret. Every path answers the
// same way without it, so a probe learns nothing about which routes exist.
func TestLocalSecretGuardsEveryPath(t *testing.T) {
	srv := NewServer(Deps{LocalSecret: "launch-secret"})
	get := func(path, secret string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "127.0.0.1:4533"
		if secret != "" {
			req.Header.Set(LocalSecretHeader, secret)
		}
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	for _, path := range []string{"/api/v1/version", "/api/v1/pairing/devices", "/api/v1/no-such-route", "/", "/index.html"} {
		for _, secret := range []string{"", "launch-secre", "launch-secret!", "wrong-secret!"} {
			if got := get(path, secret); got != http.StatusUnauthorized {
				t.Errorf("GET %s with %q = %d, want 401", path, secret, got)
			}
		}
	}
	if got := get("/api/v1/version", "launch-secret"); got != http.StatusOK {
		t.Fatalf("with the secret = %d, want 200", got)
	}
}

// Desktop and server builds take no secret: the SPA calls the API as it is.
func TestNoLocalSecretLeavesTheAPIAsItWas(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /version without a secret = %d, want 200", rec.Code)
	}
}
