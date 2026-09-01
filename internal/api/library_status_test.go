package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
)

func TestLibraryStatus_ReportsSupervisorState(t *testing.T) {
	srv, cookie := libTestServer(t, &fakeLibrary{})
	// Rebuild with LibraryStatus closure, reusing auth from libTestServer.
	srv2 := NewServer(Deps{AllowedHosts: testAllowedHosts,
		Auth:          srv.deps.Auth,
		Library:       &fakeLibrary{},
		Search:        srv.deps.Search,
		Downloader:    srv.deps.Downloader,
		LibraryStatus: func() (string, string) { return "built-in", "starting" },
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/library/status", nil)
	req.AddCookie(cookie)
	srv2.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var dto struct{ Mode, State string }
	_ = json.Unmarshal(rec.Body.Bytes(), &dto)
	if dto.Mode != "built-in" || dto.State != "starting" {
		t.Fatalf("got %+v, want built-in/starting", dto)
	}
}

func TestLibraryStatus_FallbackLibraryPresent(t *testing.T) {
	srv, cookie := libTestServer(t, &fakeLibrary{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/library/status", nil)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var dto struct{ Mode, State string }
	_ = json.Unmarshal(rec.Body.Bytes(), &dto)
	if dto.Mode != "external" || dto.State != "ready" {
		t.Fatalf("got %+v, want external/ready", dto)
	}
}

func TestLibraryStatus_FallbackNoLibrary(t *testing.T) {
	// Build a server with no library configured (Library: nil).
	st, err := store.Open(t.TempDir() + "/ls.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{AllowedHosts: testAllowedHosts,
		Auth:       authSvc,
		Library:    nil,
		Search:     registry.NewRegistry("search"),
		Downloader: registry.NewRegistry("downloader"),
	})
	cookie := &http.Cookie{Name: sessionCookie, Value: tok}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/library/status", nil)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var dto struct{ Mode, State string }
	_ = json.Unmarshal(rec.Body.Bytes(), &dto)
	if dto.Mode != "external" || dto.State != "unconfigured" {
		t.Fatalf("got %+v, want external/unconfigured", dto)
	}
}
