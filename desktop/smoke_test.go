package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/uhhhm/reverb/internal/api"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
)

// bootTestApp boots the real desktop composition root against a throwaway XDG
// home, so nothing touches the user's live DB, music dir or config. seedSearch
// controls whether a Deezer search adapter row exists before boot — the
// Everywhere handler 503s early without one, which would skip past the very
// code the SSE assertions cover.
func bootTestApp(t *testing.T, seedSearch bool) *App {
	t.Helper()
	tmp := t.TempDir()

	// Redirect every path the desktop filesystem contract resolves.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	t.Setenv("REVERB_DB", filepath.Join(tmp, "reverb.db"))
	t.Setenv("REVERB_DOWNLOAD_DIR", filepath.Join(tmp, "music"))
	t.Setenv("REVERB_PORT", "")
	if err := os.MkdirAll(filepath.Join(tmp, "music"), 0o755); err != nil {
		t.Fatal(err)
	}

	if seedSearch {
		seedSearchAdapter(t, filepath.Join(tmp, "reverb.db"))
	}

	// nil args: os.Args here belongs to the test binary, and config.Load would
	// reject its flags.
	app, err := boot(nil)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	t.Cleanup(func() { app.OnShutdown(context.Background()) })
	return app
}

// seedSearchAdapter writes an enabled Deezer row into a fresh DB before boot.
func seedSearchAdapter(t *testing.T, dbPath string) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := st.Q().CreateAdapterInstance(context.Background(), db.CreateAdapterInstanceParams{
		ID: uuid.NewString(), Type: "search", Name: "deezer",
		Enabled: 1, Priority: 0, ConfigJson: "{}",
	}); err != nil {
		t.Fatal(err)
	}
}

// nonFlushingWriter is an httptest.ResponseRecorder with Flush hidden, matching
// the Wails webview's ResponseWriter. Embedding the INTERFACE (not the concrete
// recorder) is what drops Flush from the method set.
type nonFlushingWriter struct {
	http.ResponseWriter
	rec *httptest.ResponseRecorder
}

func newNonFlushingWriter() *nonFlushingWriter {
	rec := httptest.NewRecorder()
	return &nonFlushingWriter{ResponseWriter: rec, rec: rec}
}

// TestBootWiresBundledTools covers the dead-code class of bug: the bundled-tool
// resolution existed and was unit-tested, but nothing called it, so the desktop
// app ran with no REVERB_NAVIDROME_BIN and the library never started.
func TestBootWiresBundledTools(t *testing.T) {
	bootTestApp(t, false)

	bin := os.Getenv("REVERB_NAVIDROME_BIN")
	if bin == "" {
		t.Skip("no bundled navidrome present — run desktop/tools/fetch-navidrome.sh")
	}
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("REVERB_NAVIDROME_BIN=%q does not exist: %v", bin, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("REVERB_NAVIDROME_BIN=%q is not executable", bin)
	}
}

// TestBootServesRuntimeConfigWithRealPort covers the second dead-code bug: the
// SPA's __REVERB_PORT__ bypass existed but nothing ever published the value.
func TestBootServesRuntimeConfigWithRealPort(t *testing.T) {
	app := bootTestApp(t, false)
	handler := api.NewServer(app.deps).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runtime-config.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	want := fmt.Sprintf("window.__REVERB_PORT__ = %d;", app.port)
	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("body = %q, want it to contain %q", rec.Body.String(), want)
	}
	if app.port == 0 {
		t.Error("boot did not bind a port")
	}
}

// TestBootStreamsEverywhereToNonFlushingWriter is the regression that broke
// search in the desktop window: the handler demanded an http.Flusher, and the
// Wails webview's writer is not one, so every Everywhere search 500'd there
// while succeeding over the plain HTTP listener.
//
// It asserts the transport only — a 200 event-stream carrying one envelope per
// source — not the search results, so it holds whether or not Deezer is
// reachable. (It does make one outbound call when the network is up.)
func TestBootStreamsEverywhereToNonFlushingWriter(t *testing.T) {
	app := bootTestApp(t, true)
	if app.deps.SearchAggregator == nil {
		t.Fatal("boot did not wire a search aggregator from the seeded deezer row")
	}
	handler := api.NewServer(app.deps).Handler()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/search/everywhere?q=test&type=track", nil).WithContext(ctx)

	w := newNonFlushingWriter()
	if _, isFlusher := http.ResponseWriter(w).(http.Flusher); isFlusher {
		t.Fatal("test writer must NOT implement http.Flusher")
	}
	handler.ServeHTTP(w, req)

	if w.rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.rec.Code, w.rec.Body.String())
	}
	if ct := w.rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
	body := w.rec.Body.String()
	if !strings.Contains(body, "data: ") {
		t.Fatalf("no SSE event was written: %q", body)
	}
	if !strings.Contains(body, `"source":"deezer"`) {
		t.Errorf("expected a deezer envelope, got: %q", body)
	}
}

// TestBootAcceptsWebSocketFromWailsOrigin covers the desktop realtime path
// end-to-end: the window dials the 127.0.0.1 listener directly (the AssetServer
// cannot carry an upgrade), which makes the handshake cross-origin.
func TestBootAcceptsWebSocketFromWailsOrigin(t *testing.T) {
	app := bootTestApp(t, false)

	// Serve the real listener boot bound, exactly as the window would reach it.
	go func() { _ = app.srv.Serve(app.ln) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/api/v1/ws", app.port)
	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {"wails://wails"}},
	})
	if err != nil {
		t.Fatalf("websocket dial from the wails window origin: %v", err)
	}
	c.Close(websocket.StatusNormalClosure, "")
}

// TestBootOffersYtdlpDownloader checks the new adapter reached the registry the
// desktop composition root actually builds, not just cmd/reverb's.
func TestBootOffersYtdlpDownloader(t *testing.T) {
	app := bootTestApp(t, false)
	handler := api.NewServer(app.deps).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/adapters/available", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ytdlp") {
		t.Errorf("ytdlp missing from available adapters: %s", rec.Body.String())
	}
}

// TestBootStartsBundledNavidrome is the full end-to-end check: boot the real
// composition root, start the supervisor it built, and wait for the bundled
// Navidrome to actually answer HTTP.
//
// It runs on a free port (REVERB_NAVIDROME_PORT) so it never collides with an
// already-running Reverb, and skips when the binary has not been fetched.
func TestBootStartsBundledNavidrome(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a real navidrome process")
	}
	port := freePort(t)
	t.Setenv("REVERB_NAVIDROME_PORT", strconv.Itoa(port))

	app := bootTestApp(t, false)
	if os.Getenv("REVERB_NAVIDROME_BIN") == "" {
		t.Skip("no bundled navidrome present — run desktop/tools/fetch-navidrome.sh")
	}
	if app.runtime.Bundle.Supervisor == nil {
		t.Fatal("boot did not build a navidrome supervisor")
	}
	app.StartServices()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/ping")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				// It answers. The supervisor must also converge on ready, so a
				// healthy child that Reverb believes is down still fails — but
				// give its probe (every 500ms) time to observe what we just saw.
				for time.Now().Before(deadline) {
					if app.runtime.Bundle.Supervisor.Ready() {
						return
					}
					time.Sleep(250 * time.Millisecond)
				}
				t.Fatalf("navidrome answers on %s but the supervisor never left health %q",
					base, app.runtime.Bundle.Supervisor.Health())
			}
			lastErr = fmt.Errorf("ping status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("bundled navidrome never answered on %s: %v", base, lastErr)
}

// freePort reserves an ephemeral port and releases it, so the child can bind it.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestBootAcceptsMutationFromWailsAssetServer covers the desktop REST path
// end-to-end. The window's fetch calls are relative, so they go through the
// Wails AssetServer, which builds them from the wails:// URL: Host is "wails"
// and the peer address is a fabricated TEST-NET one. Without an allowance for
// that shape, every mutation from the packaged app is rejected and the app is
// read-only outside --dev.
func TestBootAcceptsMutationFromWailsAssetServer(t *testing.T) {
	app := bootTestApp(t, false)
	handler := api.NewServer(app.deps).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/downloads/pause", nil)
	req.Host = "wails"
	req.Header.Set("Origin", "wails://wails")
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Fatalf("mutation from the desktop window was rejected: %s", rec.Body.String())
	}
}

// The sync status the UI polls arrives in the same shape, and authenticates on
// the request being local.
func TestBootAuthenticatesSyncFromWailsAssetServer(t *testing.T) {
	app := bootTestApp(t, false)
	handler := api.NewServer(app.deps).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sync/status", nil)
	req.Host = "wails"
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("sync status from the desktop window = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}
