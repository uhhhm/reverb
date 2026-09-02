package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postWithHost issues a same-origin POST for a given Host, so the request
// passes csrfGuard and only hostGuard can reject it. That is exactly the shape
// of a DNS-rebinding request: the attacker controls both headers and makes
// them agree.
func postWithHost(t *testing.T, srv *Server, host string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/downloads/pause", strings.NewReader(""))
	req.Host = host
	req.Header.Set("Origin", "http://"+host)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestHostGuardBlocksRebindingHost(t *testing.T) {
	srv := newTestServer(t)
	rr := postWithHost(t, srv, "evil.example")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("POST with Host=evil.example = %d, want 403: csrfGuard alone cannot "+
			"catch rebinding because Origin and Host agree", rr.Code)
	}
}

func TestHostGuardAllowsLoopback(t *testing.T) {
	srv := newTestServer(t)
	for _, host := range []string{
		"127.0.0.1:8090",
		"127.0.0.1",
		"localhost:8090",
		"localhost",
		"[::1]:8090",
		"127.2.3.4:8090",
	} {
		if rr := postWithHost(t, srv, host); rr.Code == http.StatusForbidden {
			t.Errorf("Host=%q was blocked; loopback is the normal case", host)
		}
	}
}

func TestHostGuardAllowsConfiguredHost(t *testing.T) {
	srv := newTestServer(t)
	srv.deps.AllowedHosts = []string{"music.example.com"}
	if rr := postWithHost(t, srv, "music.example.com"); rr.Code == http.StatusForbidden {
		t.Fatal("configured host was blocked; a reverse proxy must be able to forward its own Host")
	}
	// The port a proxy forwards is not knowable in advance, so a configured
	// bare host matches regardless of port.
	if rr := postWithHost(t, srv, "music.example.com:8443"); rr.Code == http.StatusForbidden {
		t.Fatal("configured host with a port was blocked")
	}
	if rr := postWithHost(t, srv, "other.example.com"); rr.Code != http.StatusForbidden {
		t.Fatal("an unconfigured host was allowed")
	}
}

// Reads are guarded too. A rebound page is same-origin as far as the browser is
// concerned, so it reads every response it gets back -- the library, play
// history, pairing devices, adapter URLs, the Last.fm key.
func TestHostGuardBlocksGET(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/api/v1/version", "/api/v1/library/albums", "/api/v1/pairing/devices"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "evil.example"
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %s with foreign Host = %d, want 403", path, rec.Code)
		}
	}
}

func TestHostGuardAllowsLoopbackGET(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	req.Host = "127.0.0.1:8090"
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET from loopback = %d, want 200", rec.Code)
	}
}

// An unvalidated Bearer header used to be a way past both guards on every route
// under /api/v1/sync/, including /sync/trigger, which checked no token at all.
func TestHostGuardBearerExemptionIsNarrow(t *testing.T) {
	srv := newTestServer(t)
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodPost, "/api/v1/sync/trigger"},
		{http.MethodGet, "/api/v1/sync/status"},
		{http.MethodPost, "/api/v1/sync"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
		req.Host = "evil.example"
		req.Header.Set("Origin", "http://evil.example")
		req.Header.Set("Authorization", "Bearer not-a-real-token")
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with an unvalidated Bearer token = %d, want 403", tc.method, tc.path, rec.Code)
		}
	}
}

func TestHostGuardSkippedInDev(t *testing.T) {
	srv := newTestServer(t)
	srv.deps.Dev = true
	if rr := postWithHost(t, srv, "evil.example"); rr.Code == http.StatusForbidden {
		t.Fatal("hostGuard ran in dev mode, where the Vite proxy uses arbitrary hosts")
	}
}

// The adapter test endpoint executes the binary named by binary_path, so it is
// the concrete payload rebinding was worth reaching. It must be behind the guard.
func TestHostGuardCoversAdapterTest(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/adapters/test",
		strings.NewReader(`{"name":"spotdl","config":{"output_dir":"/tmp","binary_path":"/bin/sh"}}`))
	req.Host = "evil.example"
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("adapter test with foreign Host = %d, want 403", rec.Code)
	}
}

// The WebSocket upgrade is a GET, so hostGuard's method check does not cover
// it; handleWS has to make the same check itself.
func TestHostGuardBlocksWebSocketFromForeignHost(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	req.Host = "evil.example"
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("WS upgrade from a rebound host = %d, want 403", rec.Code)
	}
}

func TestHostGuardRejectsEmptyHost(t *testing.T) {
	srv := newTestServer(t)
	if srv.hostAllowed("") {
		t.Fatal("an absent Host was treated as allowed; a deny-list check must fail closed")
	}
}

// The packaged desktop app serves the SPA through the Wails asset server, whose
// requests carry Host "wails" (or "wails.localhost" on Windows). Without an
// allowance for it every mutation from the window is rejected and the app is
// read-only unless run with --dev.
func TestHostGuardAllowsDesktopWindow(t *testing.T) {
	srv := newTestServer(t)
	srv.deps.Desktop = true
	for _, host := range []string{"wails", "wails.localhost"} {
		if rr := postWithHost(t, srv, host); rr.Code == http.StatusForbidden {
			t.Errorf("Host=%q was blocked in the desktop build: %s", host, rr.Body.String())
		}
	}
}

// Only the desktop build serves that origin. In server mode "wails.localhost"
// is a name a browser can resolve to loopback, so it stays untrusted.
func TestHostGuardBlocksWailsHostInServerMode(t *testing.T) {
	srv := newTestServer(t)
	for _, host := range []string{"wails", "wails.localhost"} {
		if rr := postWithHost(t, srv, host); rr.Code != http.StatusForbidden {
			t.Errorf("Host=%q was allowed outside the desktop build (%d)", host, rr.Code)
		}
	}
}
