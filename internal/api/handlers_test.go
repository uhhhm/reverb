package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMe(t *testing.T) {
	srv := newTestServer(t)
	rr := doGET(t, srv, "/api/v1/me", "")
	if rr.Code != 200 {
		t.Fatalf("GET /me = %d (%s)", rr.Code, rr.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /me body: %v", err)
	}
	if body["isOwner"] != true {
		t.Fatalf("/me isOwner = %v, want true", body["isOwner"])
	}
	if body["id"] == "" || body["username"] == "" {
		t.Fatalf("/me missing identity: %s", rr.Body)
	}
	caps, ok := body["capabilities"].([]any)
	if !ok || len(caps) == 0 {
		t.Fatalf("/me capabilities = %v, want non-empty", body["capabilities"])
	}
}

func TestHealth(t *testing.T) {
	srv := NewServer(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status field = %q, want ok", body["status"])
	}
}
