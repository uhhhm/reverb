package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/auth"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
)

// testDirty is a minimal ConfigDirty for tests.
type testDirty struct{ b atomic.Bool }

func (d *testDirty) Set()        { d.b.Store(true) }
func (d *testDirty) Dirty() bool { return d.b.Load() }

// fakeAdapter is a controllable registry.Plugin with one Secret field.
type fakeAdapter struct {
	typ     string
	name    string
	testErr error
}

func (a *fakeAdapter) Type() string { return a.typ }
func (a *fakeAdapter) Name() string { return a.name }
func (a *fakeAdapter) ConfigSchema() registry.ConfigSchema {
	return registry.ConfigSchema{Fields: []registry.ConfigField{
		{Key: "url", Label: "URL", Type: "string", Required: true},
		{Key: "token", Label: "Token", Type: "string", Required: true, Secret: true},
	}}
}
func (a *fakeAdapter) Init(map[string]any) error            { return nil }
func (a *fakeAdapter) TestConnection(context.Context) error { return a.testErr }

type adapterServerOpts struct {
	dirty   ConfigDirty
	testErr error // controls the fake search adapter's TestConnection
}

// adapterTestServer builds a Server with a temp store, an authed session, and a
// search registry containing a controllable fake adapter named "fake".
func adapterTestServer(t *testing.T, opts adapterServerOpts) (*Server, *http.Cookie) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/adapters.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc, tok := seededAuthToken(t, st)

	searchReg := registry.NewRegistry("search")
	searchReg.Register("fake", func() registry.Plugin {
		return &fakeAdapter{typ: "search", name: "fake", testErr: opts.testErr}
	})

	srv := NewServer(Deps{
		Auth:        authSvc,
		Adapters:    st.Q(),
		Search:      searchReg,
		Downloader:  registry.NewRegistry("downloader"),
		Lib:         registry.NewRegistry("library"),
		ConfigDirty: opts.dirty,
	})
	return srv, &http.Cookie{Name: sessionCookie, Value: tok}
}

// seededAuthToken seeds the household owner and returns the auth service plus a
// (now meaningless) session token. The browser UI is implicitly the owner, so
// the token value is ignored by requireAuth; kept for test-compat with doGET/do.
func seededAuthToken(t *testing.T, st *store.Store) (*auth.Service, string) {
	t.Helper()
	authSvc := auth.NewService(st.Q(), time.Now)
	ctx := context.Background()
	if err := authSvc.EnsureSeed(ctx); err != nil {
		t.Fatal(err)
	}
	return authSvc, ""
}

// newTestServer builds a minimal Server backed by a fresh migrated+seeded store.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/api.db")
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
	return NewServer(Deps{
		Auth:       authSvc,
		Search:     registry.NewRegistry("search"),
		Downloader: registry.NewRegistry("downloader"),
	})
}

// doGET issues a GET with the given session token (empty token → no auth cookie).
func doGET(t *testing.T, srv *Server, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// doPOST issues a POST with an optional session token string (empty = no cookie).
func doPOST(t *testing.T, srv *Server, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// doPATCH issues a PATCH with an optional session token and a JSON body.
func doPATCH(t *testing.T, srv *Server, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, path, bytes.NewBufferString(body))
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// doDELETE issues a DELETE with an optional session token.
func doDELETE(t *testing.T, srv *Server, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// do fires an authenticated HTTP request against the server and returns the recorder.
func do(t *testing.T, srv *Server, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	var rdr *bytes.Buffer
	if body != "" {
		rdr = bytes.NewBufferString(body)
	} else {
		rdr = bytes.NewBufferString("")
	}
	req := httptest.NewRequest(method, path, rdr)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}
