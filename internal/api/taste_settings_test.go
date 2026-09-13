package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/tastesettings"
)

type fakeTasteSettings struct {
	current tastesettings.Settings
	patches []tastesettings.Patch
}

func (f *fakeTasteSettings) Get(context.Context) (tastesettings.Settings, error) {
	return f.current, nil
}

func (f *fakeTasteSettings) Update(_ context.Context, p tastesettings.Patch) (tastesettings.Settings, error) {
	f.patches = append(f.patches, p)
	if p.Adventurousness != nil {
		if *p.Adventurousness < 0 || *p.Adventurousness > 100 {
			return tastesettings.Settings{}, tastesettings.ErrInvalid
		}
		f.current.Adventurousness = *p.Adventurousness
	}
	if p.OnlineRecommendations != nil {
		f.current.OnlineRecommendations = *p.OnlineRecommendations
	}
	return f.current, nil
}

func putSettings(t *testing.T, srv *Server, body string, out any) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/recommendations/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if out != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
	}
	return rec.Code
}

func TestRecommendationSettingsEndpoints(t *testing.T) {
	fake := &fakeTasteSettings{current: tastesettings.Defaults()}
	srv := newTestServer(t)
	srv.deps.TasteSettings = fake

	var got tastesettings.Settings
	if code := getJSON(t, srv, "/recommendations/settings", &got); code != http.StatusOK || got != tastesettings.Defaults() {
		t.Fatalf("GET = %d %+v, want defaults", code, got)
	}
	if code := putSettings(t, srv, `{"onlineRecommendations":false}`, &got); code != http.StatusOK || got.OnlineRecommendations || got.Adventurousness != 50 {
		t.Fatalf("PUT = %d %+v, want online off and adventurousness kept", code, got)
	}
	if p := fake.patches[0]; p.Adventurousness != nil || p.OnlineRecommendations == nil {
		t.Fatalf("patch = %+v, want only the switch", p)
	}
	if code := putSettings(t, srv, `{"adventurousness":101}`, nil); code != http.StatusBadRequest {
		t.Fatalf("out of range: status %d, want 400", code)
	}
	if code := putSettings(t, srv, `not json`, nil); code != http.StatusBadRequest {
		t.Fatalf("bad body: status %d, want 400", code)
	}
}

func TestRecommendationSettingsWithoutModule(t *testing.T) {
	srv := newTestServer(t)
	if code := getJSON(t, srv, "/recommendations/settings", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", code)
	}
}
