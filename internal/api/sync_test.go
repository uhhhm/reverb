package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/auth"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

func newSyncTestServer(t *testing.T) (*Server, *store.Store, string) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/sync_api.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewService(st.Q(), time.Now)
	if err := authSvc.EnsureSeed(context.Background()); err != nil {
		t.Fatal(err)
	}
	serverID, err := syncpkg.EnsureServerDevice(context.Background(), st.Q())
	if err != nil {
		t.Fatal(err)
	}
	pairing := syncpkg.NewPairingService(st.Q())
	syncStore := syncpkg.NewSyncStore(st.Q())
	srv := NewServer(Deps{AllowedHosts: testAllowedHosts,
		Auth:         authSvc,
		Search:       registry.NewRegistry("search"),
		Downloader:   registry.NewRegistry("downloader"),
		Pairing:      pairing,
		SyncStore:    syncStore,
		PairingStore: st.Q(),
		OfflineSet:   st.Q(),
		PairingDB:    st.DB(),
	})
	return srv, st, serverID
}

func redeemDevice(t *testing.T, srv *Server, name string) (deviceID, token string) {
	t.Helper()
	rec := doPostJSON(t, srv, "/api/v1/pairing/code", `{}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code gen for %s = %d", name, rec.Code)
	}
	var cr map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &cr)
	code := cr["code"].(string)
	rec = doPostJSON(t, srv, "/api/v1/pairing/redeem", `{"code":"`+code+`","deviceName":"`+name+`"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem %s = %d: %s", name, rec.Code, rec.Body.String())
	}
	var rr map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &rr)
	return rr["deviceId"], rr["token"]
}

func doSync(t *testing.T, srv *Server, token string, since int64, changes []syncpkg.SyncChange) (int, syncpkg.SyncResponse) {
	t.Helper()
	req := syncpkg.SyncRequest{SinceRevision: since, Changes: changes}
	if req.Changes == nil {
		req.Changes = []syncpkg.SyncChange{}
	}
	body, _ := json.Marshal(req)
	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/sync", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	} else {
		httpReq.AddCookie(&http.Cookie{Name: "reverb_session", Value: "test"})
		// The tokenless server-device fallback is loopback-only.
		httpReq.RemoteAddr = "127.0.0.1:54321"
	}
	srv.Handler().ServeHTTP(rec, httpReq)
	var resp syncpkg.SyncResponse
	if rec.Code == http.StatusOK {
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	}
	return rec.Code, resp
}

func doSyncStatus(t *testing.T, srv *Server, token string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sync/status", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		req.AddCookie(&http.Cookie{Name: "reverb_session", Value: "test"})
		// The tokenless server-device fallback is loopback-only.
		req.RemoteAddr = "127.0.0.1:54321"
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestSyncAPI(t *testing.T) {
	t.Run("valid_token", func(t *testing.T) {
		srv, _, _ := newSyncTestServer(t)
		_, token := redeemDevice(t, srv, "laptop")
		changes := []syncpkg.SyncChange{{EntityType: "track", EntityID: "t1", Field: "title", Value: "hello", UpdatedAt: 1000}}
		code, resp := doSync(t, srv, token, 0, changes)
		if code != http.StatusOK {
			t.Fatalf("sync valid token = %d", code)
		}
		if resp.Accepted != 1 {
			t.Fatalf("accepted %d want 1", resp.Accepted)
		}
		if resp.NewRevision == 0 {
			t.Fatalf("newRev 0")
		}
		if len(resp.Changes) == 0 {
			t.Fatalf("no outbound changes")
		}
		// invalid token should 401
		code, _ = doSync(t, srv, "invalidtokeninvalidtokeninvalidtoken12345", 0, nil)
		if code != http.StatusUnauthorized {
			t.Fatalf("invalid token = %d want 401", code)
		}
	})

	t.Run("local_fallback", func(t *testing.T) {
		srv, st, serverID := newSyncTestServer(t)
		changes := []syncpkg.SyncChange{{EntityType: "track", EntityID: "tLocal", Field: "title", Value: "serverVal", UpdatedAt: 2000}}
		code, resp := doSync(t, srv, "", 0, changes)
		if code != http.StatusOK {
			t.Fatalf("sync local fallback = %d", code)
		}
		if resp.Accepted != 1 {
			t.Fatalf("local accepted %d", resp.Accepted)
		}
		latest, err := syncpkg.NewSyncStore(st.Q()).GetLatestForField(context.Background(), "track", "tLocal", "title")
		_ = serverID
		if err != nil {
			t.Fatalf("GetLatest: %v", err)
		}
		if latest == nil || latest.DeviceID != serverID {
			devs, _ := st.Q().ListDevices(context.Background())
			isServer := false
			for _, d := range devs {
				if d.ID == latest.DeviceID && d.IsServer == 1 {
					isServer = true
				}
			}
			if !isServer {
				t.Fatalf("local fallback should use server device, got %v, devices %v", latest.DeviceID, devs)
			}
		}
	})

	t.Run("lww_merge", func(t *testing.T) {
		srv, st, _ := newSyncTestServer(t)
		_, tokenA := redeemDevice(t, srv, "clientA")
		_, tokenB := redeemDevice(t, srv, "clientB")

		// clientA newer wins
		changesA := []syncpkg.SyncChange{{EntityType: "track", EntityID: "tLWW", Field: "title", Value: "old", UpdatedAt: 1000}}
		code, resp := doSync(t, srv, tokenA, 0, changesA)
		if code != http.StatusOK || resp.Accepted != 1 {
			t.Fatalf("A first sync failed %d %v", code, resp)
		}
		changesB := []syncpkg.SyncChange{{EntityType: "track", EntityID: "tLWW", Field: "title", Value: "new", UpdatedAt: 2000}}
		code, resp = doSync(t, srv, tokenB, 0, changesB)
		if code != http.StatusOK {
			t.Fatalf("B newer sync %d", code)
		}
		if len(resp.Rejected) != 0 {
			t.Fatalf("B newer should not be rejected %v", resp.Rejected)
		}
		// older should lose
		older := []syncpkg.SyncChange{{EntityType: "track", EntityID: "tLWW", Field: "title", Value: "older", UpdatedAt: 500}}
		code, resp = doSync(t, srv, tokenA, 0, older)
		if code != http.StatusOK {
			t.Fatalf("older sync %d", code)
		}
		if len(resp.Rejected) != 1 {
			t.Fatalf("older should be rejected, got %v", resp.Rejected)
		}
		// verify latest is "new"
		ss := syncpkg.NewSyncStore(st.Q())
		latest, _ := ss.GetLatestForField(context.Background(), "track", "tLWW", "title")
		if latest.Value != "new" {
			t.Fatalf("latest %v want new", latest.Value)
		}
		// server tie wins: create server change vs client tie
		// clientA already has older, now server does tie vs clientB's latest (2000)
		// server local fallback with same UpdatedAt 2000 should win due to server tie-break
		// First get device count to know server wins path
		// Use local fallback (server) to send tie
		tieServer := []syncpkg.SyncChange{{EntityType: "track", EntityID: "tTie", Field: "title", Value: "clientVal", UpdatedAt: 3000}}
		// clientB sends clientVal
		code, _ = doSync(t, srv, tokenB, 0, tieServer)
		if code != http.StatusOK {
			t.Fatalf("tie client setup %d", code)
		}
		// server sends same timestamp but serverVal should win
		tieSrvChanges := []syncpkg.SyncChange{{EntityType: "track", EntityID: "tTie", Field: "title", Value: "serverVal", UpdatedAt: 3000}}
		code, resp = doSync(t, srv, "", 0, tieSrvChanges)
		if code != http.StatusOK {
			t.Fatalf("tie server %d", code)
		}
		if len(resp.Rejected) != 0 {
			t.Fatalf("server tie should win, rejected %d", len(resp.Rejected))
		}
		latest, _ = ss.GetLatestForField(context.Background(), "track", "tTie", "title")
		if latest.Value != "serverVal" {
			t.Fatalf("server tie winner %v want serverVal", latest.Value)
		}
		// lex order: two non-server devices same UpdatedAt, smaller lex wins
		// Need to know lex order of device IDs: fetch devices
		devices, _ := st.Q().ListDevices(context.Background())
		var idA, idB string
		for _, d := range devices {
			if d.Name == "clientA" {
				idA = d.ID
			}
			if d.Name == "clientB" {
				idB = d.ID
			}
		}
		// Determine which is smaller lex
		var smallToken, largeToken string
		var smallVal, largeVal string
		if idA < idB {
			smallToken = tokenA
			largeToken = tokenB
			smallVal = "aVal"
			largeVal = "bVal"
		} else {
			smallToken = tokenB
			largeToken = tokenA
			smallVal = "bVal"
			largeVal = "aVal"
		}
		_ = largeToken
		_ = largeVal
		// Now test: after B wrote, A (smaller) should win
		// We already did B then A attempt if A is smaller; but we need to adjust if A is actually larger.
		// Instead create fresh entity tLex2 with large first then small
		// Clean: create tLex2 with large device first, then small
		lexLargeFirst := []syncpkg.SyncChange{{EntityType: "track", EntityID: "tLex2", Field: "title", Value: largeVal, UpdatedAt: 6000}}
		code, _ = doSync(t, srv, largeToken, 0, lexLargeFirst)
		if code != http.StatusOK {
			t.Fatalf("lex large first %d", code)
		}
		lexSmallSecond := []syncpkg.SyncChange{{EntityType: "track", EntityID: "tLex2", Field: "title", Value: smallVal, UpdatedAt: 6000}}
		code, resp = doSync(t, srv, smallToken, 0, lexSmallSecond)
		if code != http.StatusOK {
			t.Fatalf("lex small second %d", code)
		}
		if len(resp.Rejected) != 0 {
			t.Fatalf("lex smaller should win, rejected %d", len(resp.Rejected))
		}
		latest, _ = ss.GetLatestForField(context.Background(), "track", "tLex2", "title")
		if latest.Value != smallVal {
			t.Fatalf("lex winner %v want %s", latest.Value, smallVal)
		}
		// reverse: small first, large second should lose
		lexSmallFirst := []syncpkg.SyncChange{{EntityType: "track", EntityID: "tLex3", Field: "title", Value: smallVal, UpdatedAt: 7000}}
		code, _ = doSync(t, srv, smallToken, 0, lexSmallFirst)
		if code != http.StatusOK {
			t.Fatalf("lex small first %d", code)
		}
		lexLargeSecond := []syncpkg.SyncChange{{EntityType: "track", EntityID: "tLex3", Field: "title", Value: largeVal, UpdatedAt: 7000}}
		code, resp = doSync(t, srv, largeToken, 0, lexLargeSecond)
		if code != http.StatusOK {
			t.Fatalf("lex large second %d", code)
		}
		if len(resp.Rejected) != 1 {
			t.Fatalf("lex larger should lose, rejected %d", len(resp.Rejected))
		}
		latest, _ = ss.GetLatestForField(context.Background(), "track", "tLex3", "title")
		if latest.Value != smallVal {
			t.Fatalf("lex small should remain %v", latest.Value)
		}
		// delete wins even if older: test via API
		delChanges := []syncpkg.SyncChange{{EntityType: "track", EntityID: "tDel", Field: "title", Value: "hello", UpdatedAt: 1000}}
		code, _ = doSync(t, srv, tokenA, 0, delChanges)
		if code != http.StatusOK {
			t.Fatalf("del setup %d", code)
		}
		del := []syncpkg.SyncChange{{EntityType: "track", EntityID: "tDel", Field: "__deleted", Value: nil, UpdatedAt: 900}}
		code, resp = doSync(t, srv, tokenB, 0, del)
		if code != http.StatusOK {
			t.Fatalf("delete older wins %d", code)
		}
		if len(resp.Rejected) != 0 {
			t.Fatalf("delete older should win")
		}
		editAfterDel := []syncpkg.SyncChange{{EntityType: "track", EntityID: "tDel", Field: "title", Value: "resurrect", UpdatedAt: 2000}}
		code, resp = doSync(t, srv, tokenA, 0, editAfterDel)
		if code != http.StatusOK {
			t.Fatalf("edit after delete %d", code)
		}
		if len(resp.Rejected) != 1 {
			t.Fatalf("edit after delete should be rejected")
		}
	})

	t.Run("status", func(t *testing.T) {
		srv, _, _ := newSyncTestServer(t)
		_, token := redeemDevice(t, srv, "statusClient")
		// add a change so revision >0
		changes := []syncpkg.SyncChange{{EntityType: "track", EntityID: "tStatus", Field: "title", Value: "v", UpdatedAt: 1000}}
		_, _ = doSync(t, srv, token, 0, changes)
		// status with token
		rec := doSyncStatus(t, srv, token)
		if rec.Code != http.StatusOK {
			t.Fatalf("status with token = %d: %s", rec.Code, rec.Body.String())
		}
		var stt map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &stt)
		if stt["revision"] == nil {
			t.Fatalf("revision missing %v", stt)
		}
		if stt["deviceCount"] == nil {
			t.Fatalf("deviceCount missing")
		}
		if int(stt["deviceCount"].(float64)) < 2 {
			t.Fatalf("deviceCount %v want >=2", stt["deviceCount"])
		}
		// status with local fallback (no token)
		rec = doSyncStatus(t, srv, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status local = %d: %s", rec.Code, rec.Body.String())
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &stt)
		if stt["revision"] == nil {
			t.Fatalf("local revision missing")
		}
		// invalid token status should 401
		rec = doSyncStatus(t, srv, "invalidtokeninvalidtokeninvalidtoken12345")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status invalid token = %d want 401", rec.Code)
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		st, err := store.Open(t.TempDir() + "/unavail_sync.db")
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		_ = st.Migrate()
		authSvc := auth.NewService(st.Q(), time.Now)
		_ = authSvc.EnsureSeed(context.Background())
		srv := NewServer(Deps{AllowedHosts: testAllowedHosts,
			Auth:       authSvc,
			Search:     registry.NewRegistry("search"),
			Downloader: registry.NewRegistry("downloader"),
		})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", bytes.NewBufferString(`{"sinceRevision":0,"changes":[]}`))
		req.Header.Set("Content-Type", "application/json")
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("sync unavailable = %d want 503", rec.Code)
		}
		rec = doSyncStatus(t, srv, "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status unavailable = %d want 503", rec.Code)
		}
	})

	t.Run("bearer_csrf_exempt", func(t *testing.T) {
		srv, _, _ := newSyncTestServer(t)
		_, token := redeemDevice(t, srv, "csrfClient")
		body, _ := json.Marshal(syncpkg.SyncRequest{SinceRevision: 0, Changes: []syncpkg.SyncChange{}})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Origin", "https://evil.example.com")
		req.Host = "reverb.local"
		srv.Handler().ServeHTTP(rec, req)
		// Bearer should bypass CSRF, so not 403
		if rec.Code == http.StatusForbidden {
			t.Fatalf("bearer CSRF blocked: %s", rec.Body.String())
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("bearer with evil origin = %d want 200, body %s", rec.Code, rec.Body.String())
		}
		// without bearer, cross-origin should be blocked (local cookie case)
		rec2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodPost, "/api/v1/sync", bytes.NewReader(body))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("Origin", "https://evil.example.com")
		req2.Host = "reverb.local"
		srv.Handler().ServeHTTP(rec2, req2)
		if rec2.Code != http.StatusForbidden {
			t.Fatalf("local cross-origin should be blocked, got %d", rec2.Code)
		}
	})
}

// helpers for sync tests that need db import to avoid unused
var _ db.Device

// TestSyncRemoteFallbackRejected covers the tokenless server-device fallback
// being loopback-only. requireAuth injects the local user into every request,
// so before this gate any host on the network could author sync changes as the
// server device with no pairing token at all.
func TestSyncRemoteFallbackRejected(t *testing.T) {
	srv, st, serverID := newSyncTestServer(t)
	changes := []syncpkg.SyncChange{{EntityType: "track", EntityID: "tRemote", Field: "title", Value: "pwned", UpdatedAt: 2000}}
	body, _ := json.Marshal(syncpkg.SyncRequest{SinceRevision: 0, Changes: changes})

	for _, remote := range []string{"192.168.1.50:44444", "10.0.0.7:1234", "[2001:db8::1]:443"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "reverb_session", Value: "test"})
		req.RemoteAddr = remote
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("tokenless sync from %s = %d, want 401", remote, rec.Code)
		}
	}

	// A forged X-Forwarded-For must not buy loopback treatment.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	req.AddCookie(&http.Cookie{Name: "reverb_session", Value: "test"})
	req.RemoteAddr = "192.168.1.50:44444"
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("spoofed X-Forwarded-For = %d, want 401", rec.Code)
	}

	latest, err := syncpkg.NewSyncStore(st.Q()).GetLatestForField(context.Background(), "track", "tRemote", "title")
	if err != nil {
		t.Fatalf("GetLatest: %v", err)
	}
	if latest != nil {
		t.Fatalf("remote caller wrote a change as %v (server device %v)", latest.DeviceID, serverID)
	}

	// Loopback still works, so the built-in UI is unaffected.
	if code, _ := doSync(t, srv, "", 0, changes); code != http.StatusOK {
		t.Fatalf("loopback fallback = %d, want 200", code)
	}
}

// TestSyncDeviceIDSpoofRejected covers the HTTP path enforcing the same
// authorship gate as the P2P path: a paired device may not author changes
// attributed to another device without a valid signature from that device.
func TestSyncDeviceIDSpoofRejected(t *testing.T) {
	srv, st, serverID := newSyncTestServer(t)
	_, attackerToken := redeemDevice(t, srv, "attacker")
	victimID, _ := redeemDevice(t, srv, "victim")

	for _, spoofed := range []string{victimID, serverID} {
		changes := []syncpkg.SyncChange{{
			DeviceID:   spoofed,
			EntityType: "playlist",
			EntityID:   "pl-" + spoofed,
			Field:      "name",
			Value:      "pwned",
			UpdatedAt:  9999999999999,
		}}
		code, resp := doSync(t, srv, attackerToken, 0, changes)
		if code != http.StatusOK {
			t.Fatalf("spoof as %s = %d, want 200 with rejection", spoofed, code)
		}
		if resp.Accepted != 0 || len(resp.Rejected) != 1 {
			t.Fatalf("spoof as %s: accepted=%d rejected=%d, want 0 and 1", spoofed, resp.Accepted, len(resp.Rejected))
		}
		latest, err := syncpkg.NewSyncStore(st.Q()).GetLatestForField(context.Background(), "playlist", "pl-"+spoofed, "name")
		if err != nil {
			t.Fatalf("GetLatest: %v", err)
		}
		if latest != nil {
			t.Fatalf("forged change stored as %v", latest.DeviceID)
		}
	}

	// The attacker can still author under its own identity.
	own := []syncpkg.SyncChange{{EntityType: "playlist", EntityID: "pl-own", Field: "name", Value: "mine", UpdatedAt: 1000}}
	code, resp := doSync(t, srv, attackerToken, 0, own)
	if code != http.StatusOK || resp.Accepted != 1 {
		t.Fatalf("own-device change = %d accepted=%d, want 200 and 1", code, resp.Accepted)
	}
}

// /sync/trigger is only meaningful once the libp2p host (and with it the
// syncer) exists; without one it must report unavailable rather than 404/500.
func TestSyncTriggerUnavailableWithoutSyncer(t *testing.T) {
	srv, _, _ := newSyncTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync/trigger", nil)
	req.AddCookie(&http.Cookie{Name: "reverb_session", Value: "test"})
	req.RemoteAddr = "127.0.0.1:54321"
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("sync trigger without syncer = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
}

// The desktop window reaches the API in-process through the Wails asset server,
// which fabricates a TEST-NET RemoteAddr. Treating that as remote made every
// unauthenticated sync call from the packaged app 401, including the status the
// UI polls.
func TestSyncStatusFromDesktopWindow(t *testing.T) {
	srv, _, _ := newSyncTestServer(t)
	srv.deps.Desktop = true

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sync/status", nil)
	req.Host = "wails"
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync status from the desktop window = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// The same shape outside the desktop build is a genuinely non-local caller.
// The Host is loopback here so the request reaches authenticateSync at all --
// what is under test is that the transport address, not the Host, decides.
func TestSyncStatusRejectsNonLoopbackInServerMode(t *testing.T) {
	srv, _, _ := newSyncTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sync/status", nil)
	req.Host = "127.0.0.1:8090"
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("sync status from a remote peer = %d, want 401", rec.Code)
	}
}
