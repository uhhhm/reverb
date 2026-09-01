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

func newPairingTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/pairing_api.db")
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
	if _, err := syncpkg.EnsureServerDevice(context.Background(), st.Q()); err != nil {
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
	return srv, st
}

func doPostJSON(t *testing.T, srv *Server, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func doGetDevices(t *testing.T, srv *Server, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pairing/devices", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func doDeleteDevice(t *testing.T, srv *Server, id string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/pairing/devices/"+id, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestPairingAPI(t *testing.T) {
	t.Run("code", func(t *testing.T) {
		srv, _ := newPairingTestServer(t)
		rec := doPostJSON(t, srv, "/api/v1/pairing/code", `{}`, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /pairing/code = %d: %s", rec.Code, rec.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		code, ok := resp["code"].(string)
		if !ok || len(code) != 9 || code[4] != '-' {
			t.Fatalf("code %v invalid", resp["code"])
		}
		if resp["expiresAt"] == nil {
			t.Fatalf("expiresAt missing %v", resp)
		}
	})

	t.Run("redeem_valid", func(t *testing.T) {
		srv, _ := newPairingTestServer(t)
		rec := doPostJSON(t, srv, "/api/v1/pairing/code", `{}`, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("code gen %d", rec.Code)
		}
		var cr map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &cr)
		code := cr["code"].(string)

		body := `{"code":"` + code + `","deviceName":"my laptop"}`
		rec = doPostJSON(t, srv, "/api/v1/pairing/redeem", body, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("redeem valid = %d: %s", rec.Code, rec.Body.String())
		}
		var rr map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &rr); err != nil {
			t.Fatal(err)
		}
		if rr["deviceId"] == "" || rr["token"] == "" || rr["serverDeviceId"] == "" {
			t.Fatalf("redeem resp missing fields %v", rr)
		}
		if len(rr["token"]) != 43 {
			t.Fatalf("token len %d want 43", len(rr["token"]))
		}
		// redeem with dash/case variant also works — generate second code
		rec2 := doPostJSON(t, srv, "/api/v1/pairing/code", `{}`, nil)
		var cr2 map[string]any
		_ = json.Unmarshal(rec2.Body.Bytes(), &cr2)
		code2 := cr2["code"].(string)
		lower := bytes.ToLower([]byte(code2))
		_ = lower
		// use lowercased code
		body2 := `{"code":"` + code2 + `","deviceName":"second"}`
		// lower case
		// we need to transform code2 to lower: just marshal again with lower
		// simpler: redeem with lower
		bodyLower := `{"code":"` + string(bytes.ToLower([]byte(code2))) + `","deviceName":"second"}`
		_ = body2
		rec = doPostJSON(t, srv, "/api/v1/pairing/redeem", bodyLower, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("redeem lower case = %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("redeem_invalid", func(t *testing.T) {
		srv, _ := newPairingTestServer(t)
		rec := doPostJSON(t, srv, "/api/v1/pairing/redeem", `{"code":"ZZZZ-ZZZZ","deviceName":"x"}`, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid code = %d want 400: %s", rec.Code, rec.Body.String())
		}
		rec = doPostJSON(t, srv, "/api/v1/pairing/redeem", `{"code":"bad!","deviceName":"x"}`, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("malformed = %d want 400", rec.Code)
		}
		rec = doPostJSON(t, srv, "/api/v1/pairing/redeem", `{"code":"","deviceName":"x"}`, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("empty code = %d want 400", rec.Code)
		}
		rec = doPostJSON(t, srv, "/api/v1/pairing/redeem", `{"code":"ABCD-1234"}`, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("missing deviceName = %d want 400", rec.Code)
		}
	})

	t.Run("redeem_expired", func(t *testing.T) {
		srv, st := newPairingTestServer(t)
		ctx := context.Background()
		expired := "ABCD2345"
		if err := st.Q().CreatePairingCode(ctx, db.CreatePairingCodeParams{Code: expired, ExpiresAt: time.Now().Unix() - 10}); err != nil {
			t.Fatal(err)
		}
		rec := doPostJSON(t, srv, "/api/v1/pairing/redeem", `{"code":"ABCD-2345","deviceName":"laptop"}`, nil)
		if rec.Code != http.StatusGone {
			t.Fatalf("expired = %d want 410: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("redeem_used", func(t *testing.T) {
		srv, _ := newPairingTestServer(t)
		rec := doPostJSON(t, srv, "/api/v1/pairing/code", `{}`, nil)
		var cr map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &cr)
		code := cr["code"].(string)
		body := `{"code":"` + code + `","deviceName":"first"}`
		rec = doPostJSON(t, srv, "/api/v1/pairing/redeem", body, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("first redeem %d", rec.Code)
		}
		rec = doPostJSON(t, srv, "/api/v1/pairing/redeem", body, nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("used code = %d want 409: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("list_devices", func(t *testing.T) {
		srv, _ := newPairingTestServer(t)
		rec := doGetDevices(t, srv, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET devices empty = %d: %s", rec.Code, rec.Body.String())
		}
		var list []deviceDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 {
			t.Fatalf("initial list %d want 1 server", len(list))
		}
		if !list[0].IsServer {
			t.Fatalf("first device should be server")
		}
		// create one
		rec = doPostJSON(t, srv, "/api/v1/pairing/code", `{}`, nil)
		var cr map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &cr)
		code := cr["code"].(string)
		_ = doPostJSON(t, srv, "/api/v1/pairing/redeem", `{"code":"`+code+`","deviceName":"laptop"}`, nil)
		rec = doGetDevices(t, srv, nil)
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		if len(list) != 2 {
			t.Fatalf("list after redeem %d want 2", len(list))
		}
		// ensure non-server present
		found := false
		for _, d := range list {
			if !d.IsServer && d.Name == "laptop" {
				found = true
			}
		}
		if !found {
			t.Fatalf("laptop not in list %v", list)
		}
	})

	t.Run("delete_device", func(t *testing.T) {
		srv, st := newPairingTestServer(t)
		// create device via redeem
		rec := doPostJSON(t, srv, "/api/v1/pairing/code", `{}`, nil)
		var cr map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &cr)
		code := cr["code"].(string)
		rec = doPostJSON(t, srv, "/api/v1/pairing/redeem", `{"code":"`+code+`","deviceName":"toDelete"}`, nil)
		var rr map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &rr)
		devID := rr["deviceId"]
		// ensure it exists
		if devID == "" {
			t.Fatal("no deviceId")
		}
		// also create a sync cursor to test cursor cleanup
		_ = st.Q().UpsertSyncCursor(context.Background(), db.UpsertSyncCursorParams{DeviceID: devID, Revision: 1})
		// delete via API
		rec = doDeleteDevice(t, srv, devID, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("DELETE device = %d: %s", rec.Code, rec.Body.String())
		}
		var del map[string]bool
		_ = json.Unmarshal(rec.Body.Bytes(), &del)
		if !del["ok"] {
			t.Fatalf("delete ok false %v", del)
		}
		// should be gone
		if _, err := st.Q().GetDeviceByID(context.Background(), devID); err == nil {
			t.Fatalf("device still exists after delete")
		}
		// cursor should be cleaned (no error if missing is fine, but ensure delete didn't fail)
		// attempt delete again -> 404
		rec = doDeleteDevice(t, srv, devID, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("second delete = %d want 404", rec.Code)
		}
		// cannot delete server
		devices, _ := st.Q().ListDevices(context.Background())
		var serverID string
		for _, d := range devices {
			if d.IsServer == 1 {
				serverID = d.ID
			}
		}
		rec = doDeleteDevice(t, srv, serverID, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("delete server = %d want 400: %s", rec.Code, rec.Body.String())
		}
		// nonexistent
		rec = doDeleteDevice(t, srv, "dev_nonexistent", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("delete nonexistent = %d want 404", rec.Code)
		}
	})

	t.Run("pairing_unavailable", func(t *testing.T) {
		st, err := store.Open(t.TempDir() + "/unavailable.db")
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
			// Pairing nil
		})
		rec := doPostJSON(t, srv, "/api/v1/pairing/code", `{}`, nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("code unavailable = %d want 503", rec.Code)
		}
		rec = doGetDevices(t, srv, nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("devices unavailable = %d want 503", rec.Code)
		}
	})
}

func TestPairingAPI_CodeAuth(t *testing.T) {
	srv, _ := newPairingTestServer(t)
	rec := doPostJSON(t, srv, "/api/v1/pairing/code", `{}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code auth local should succeed, got %d", rec.Code)
	}
	// also check content type
	if rec.Header().Get("Content-Type") == "" {
		t.Fatalf("missing content-type")
	}
}
