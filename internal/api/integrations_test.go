package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/store"
)

// buildIntegrationServer creates a Server with Adapters wired and a seeded
// owner session (full admin / can_manage_library capabilities).
func buildIntegrationServer(t *testing.T) (*Server, *http.Cookie, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/integrations2.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{AllowedHosts: testAllowedHosts,
		Auth:     authSvc,
		Adapters: st.Q(),
	})
	cookie := &http.Cookie{Name: sessionCookie, Value: tok}
	return srv, cookie, st
}

// ============================================================================
// Tests: GET /api/v1/admin/integrations/lastfm
// ============================================================================

// TestGetLastfmIntegration_DefaultsEmpty checks that GET returns
// {apiKey:"", apiSecretSet:false} when nothing is stored.
func TestGetLastfmIntegration_DefaultsEmpty(t *testing.T) {
	srv, cookie, _ := buildIntegrationServer(t)

	rec := do(t, srv, cookie, http.MethodGet, "/api/v1/admin/integrations/lastfm", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/integrations/lastfm = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		APIKey       string `json:"apiKey"`
		APISecretSet bool   `json:"apiSecretSet"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.APIKey != "" {
		t.Fatalf("apiKey = %q, want empty string", resp.APIKey)
	}
	if resp.APISecretSet {
		t.Fatal("apiSecretSet = true, want false when nothing stored")
	}
}

// TestPutLastfmIntegration_StoresKeyAndSecret verifies that PUT stores the key
// and secret; a subsequent GET shows apiSecretSet:true.
func TestPutLastfmIntegration_StoresKeyAndSecret(t *testing.T) {
	srv, cookie, _ := buildIntegrationServer(t)

	body := `{"apiKey":"my-api-key","apiSecret":"my-api-secret"}`
	rec := do(t, srv, cookie, http.MethodPut, "/api/v1/admin/integrations/lastfm", body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PUT /admin/integrations/lastfm = %d, want 204; body: %s", rec.Code, rec.Body.String())
	}

	// Subsequent GET must show apiKey and apiSecretSet:true
	rec2 := do(t, srv, cookie, http.MethodGet, "/api/v1/admin/integrations/lastfm", "")
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET /admin/integrations/lastfm after PUT = %d, want 200; body: %s", rec2.Code, rec2.Body.String())
	}
	var resp struct {
		APIKey       string `json:"apiKey"`
		APISecretSet bool   `json:"apiSecretSet"`
	}
	if err := json.NewDecoder(rec2.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.APIKey != "my-api-key" {
		t.Fatalf("apiKey = %q, want %q", resp.APIKey, "my-api-key")
	}
	if !resp.APISecretSet {
		t.Fatal("apiSecretSet = false, want true after storing a secret")
	}
}

// TestPutLastfmIntegration_BlankSecretPreservesStored verifies that PUT with
// blank apiSecret does NOT wipe a previously stored secret.
func TestPutLastfmIntegration_BlankSecretPreservesStored(t *testing.T) {
	srv, cookie, _ := buildIntegrationServer(t)

	// First, store a real secret.
	body1 := `{"apiKey":"key1","apiSecret":"real-secret"}`
	rec1 := do(t, srv, cookie, http.MethodPut, "/api/v1/admin/integrations/lastfm", body1)
	if rec1.Code != http.StatusNoContent {
		t.Fatalf("first PUT = %d; body: %s", rec1.Code, rec1.Body.String())
	}

	// Second PUT with blank secret — must NOT wipe the stored secret.
	body2 := `{"apiKey":"key2","apiSecret":""}`
	rec2 := do(t, srv, cookie, http.MethodPut, "/api/v1/admin/integrations/lastfm", body2)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("second PUT = %d; body: %s", rec2.Code, rec2.Body.String())
	}

	// GET must still show apiSecretSet:true (original secret preserved).
	rec3 := do(t, srv, cookie, http.MethodGet, "/api/v1/admin/integrations/lastfm", "")
	var resp struct {
		APIKey       string `json:"apiKey"`
		APISecretSet bool   `json:"apiSecretSet"`
	}
	if err := json.NewDecoder(rec3.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.APIKey != "key2" {
		t.Fatalf("apiKey = %q, want %q", resp.APIKey, "key2")
	}
	if !resp.APISecretSet {
		t.Fatal("apiSecretSet must still be true after PUT with blank secret")
	}
}

// TestPutLastfmIntegration_SentinelSecretPreservesStored verifies that PUT with
// the sentinel value does NOT wipe a previously stored secret.
func TestPutLastfmIntegration_SentinelSecretPreservesStored(t *testing.T) {
	srv, cookie, _ := buildIntegrationServer(t)

	// Store a real secret first.
	body1 := `{"apiKey":"key1","apiSecret":"real-secret"}`
	rec1 := do(t, srv, cookie, http.MethodPut, "/api/v1/admin/integrations/lastfm", body1)
	if rec1.Code != http.StatusNoContent {
		t.Fatalf("first PUT = %d; body: %s", rec1.Code, rec1.Body.String())
	}

	// PUT with the sentinel placeholder — must NOT replace the stored secret.
	sentinelBody := `{"apiKey":"key1","apiSecret":"` + secretSentinel + `"}`
	rec2 := do(t, srv, cookie, http.MethodPut, "/api/v1/admin/integrations/lastfm", sentinelBody)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("sentinel PUT = %d; body: %s", rec2.Code, rec2.Body.String())
	}

	// Secret must still be set.
	rec3 := do(t, srv, cookie, http.MethodGet, "/api/v1/admin/integrations/lastfm", "")
	var resp struct {
		APISecretSet bool `json:"apiSecretSet"`
	}
	if err := json.NewDecoder(rec3.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.APISecretSet {
		t.Fatal("apiSecretSet must still be true after PUT with sentinel secret")
	}
}

// TestGetLastfmIntegration_SecretValueNeverInBody ensures that after storing a
// secret, no GET response body ever contains the actual secret value.
func TestGetLastfmIntegration_SecretValueNeverInBody(t *testing.T) {
	srv, cookie, _ := buildIntegrationServer(t)

	body := `{"apiKey":"some-key","apiSecret":"super-secret-value-12345"}`
	rec := do(t, srv, cookie, http.MethodPut, "/api/v1/admin/integrations/lastfm", body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PUT = %d; body: %s", rec.Code, rec.Body.String())
	}

	rec2 := do(t, srv, cookie, http.MethodGet, "/api/v1/admin/integrations/lastfm", "")
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET = %d; body: %s", rec2.Code, rec2.Body.String())
	}
	respBody := rec2.Body.String()
	if strings.Contains(respBody, "super-secret-value-12345") {
		t.Fatalf("GET response MUST NOT contain the stored secret value; body: %s", respBody)
	}
}
