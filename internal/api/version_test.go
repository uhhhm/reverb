package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVersionEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		deps     Deps
		want     string
		wantRepo string
	}{
		{name: "explicit version", deps: Deps{Version: "1.2.3"}, want: "1.2.3"},
		{name: "empty defaults to dev", deps: Deps{}, want: "dev"},
		{name: "reports update repo", deps: Deps{Version: "1.2.3", UpdateRepo: "me/fork"}, want: "1.2.3", wantRepo: "me/fork"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer(tt.deps)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("content-type = %q, want application/json", ct)
			}
			var body struct {
				Version    string `json:"version"`
				UpdateRepo string `json:"updateRepo"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Version != tt.want {
				t.Fatalf("version = %q, want %q", body.Version, tt.want)
			}
			if body.UpdateRepo != tt.wantRepo {
				t.Fatalf("updateRepo = %q, want %q", body.UpdateRepo, tt.wantRepo)
			}
		})
	}
}
