package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeUpdater records what the handlers ask of it.
type fakeUpdater struct {
	status     map[string]any
	checks     chan struct{}
	installed  int
	dismissed  int
	installErr error
}

func newFakeUpdater() *fakeUpdater {
	return &fakeUpdater{
		status: map[string]any{"currentVersion": "v1.0.0", "staged": "v2.0.0"},
		checks: make(chan struct{}, 4),
	}
}

func (f *fakeUpdater) Status() any { return f.status }
func (f *fakeUpdater) Check(ctx context.Context) {
	f.checks <- struct{}{}
}
func (f *fakeUpdater) Install() error { f.installed++; return f.installErr }
func (f *fakeUpdater) Dismiss()       { f.dismissed++ }

func serverWithUpdater(t *testing.T, u UpdateService) *Server {
	t.Helper()
	srv := newTestServer(t)
	srv.deps.Update = u
	return srv
}

func doUpdateReq(t *testing.T, srv *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(""))
	req.Host = "example.com"
	if method != http.MethodGet {
		req.Header.Set("Origin", "http://example.com")
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestUpdateStatusReportsServiceState(t *testing.T) {
	f := newFakeUpdater()
	rr := doUpdateReq(t, serverWithUpdater(t, f), http.MethodGet, "/api/v1/update")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /update = %d, want 200", rr.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["staged"] != "v2.0.0" {
		t.Fatalf("staged = %v, want v2.0.0", got["staged"])
	}
}

// A build with no updater (the server/Docker binary) must say so rather than
// pretending it can update itself.
func TestUpdateEndpointsUnavailableWithoutUpdater(t *testing.T) {
	srv := newTestServer(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/update"},
		{http.MethodPost, "/api/v1/update/check"},
		{http.MethodPost, "/api/v1/update/install"},
		{http.MethodPost, "/api/v1/update/dismiss"},
	} {
		rr := doUpdateReq(t, srv, tc.method, tc.path)
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s = %d, want 503", tc.method, tc.path, rr.Code)
		}
	}
}

// The check runs detached from the request: the response must not wait for a
// download that can take minutes.
func TestUpdateCheckReturnsBeforeTheCheckFinishes(t *testing.T) {
	f := newFakeUpdater()
	rr := doUpdateReq(t, serverWithUpdater(t, f), http.MethodPost, "/api/v1/update/check")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST /update/check = %d, want 202", rr.Code)
	}
	select {
	case <-f.checks:
	case <-time.After(2 * time.Second):
		t.Fatal("check was never started")
	}
}

func TestUpdateInstall(t *testing.T) {
	f := newFakeUpdater()
	srv := serverWithUpdater(t, f)
	if rr := doUpdateReq(t, srv, http.MethodPost, "/api/v1/update/install"); rr.Code != http.StatusOK {
		t.Fatalf("POST /update/install = %d, want 200", rr.Code)
	}
	if f.installed != 1 {
		t.Fatalf("Install called %d times, want 1", f.installed)
	}

	f.installErr = errors.New("no update is ready to install")
	rr := doUpdateReq(t, srv, http.MethodPost, "/api/v1/update/install")
	if rr.Code != http.StatusConflict {
		t.Fatalf("failed install = %d, want 409", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "no update is ready") {
		t.Fatalf("install failure did not explain itself: %s", rr.Body.String())
	}
}

func TestUpdateDismiss(t *testing.T) {
	f := newFakeUpdater()
	if rr := doUpdateReq(t, serverWithUpdater(t, f), http.MethodPost, "/api/v1/update/dismiss"); rr.Code != http.StatusOK {
		t.Fatalf("POST /update/dismiss = %d, want 200", rr.Code)
	}
	if f.dismissed != 1 {
		t.Fatalf("Dismiss called %d times, want 1", f.dismissed)
	}
}
