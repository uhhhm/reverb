package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uhhhm/reverb/internal/crop"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
)

func cropServer(t *testing.T) (*Server, *http.Cookie) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/crop.db")
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
		Search:     registry.NewRegistry("search"),
		Downloader: registry.NewRegistry("downloader"),
		Crop:       crop.New(st.Q()),
	})
	return srv, &http.Cookie{Name: sessionCookie, Value: tok}
}

func doCrop(t *testing.T, srv *Server, cookie *http.Cookie, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/api/v1/library/track/t1/crop", bytes.NewBufferString(body))
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// Cropping is non-destructive, so the whole round trip — crop, uncrop, crop
// again — has to work without anything having been lost in between.
func TestCropRoundTripIsReversible(t *testing.T) {
	srv, cookie := cropServer(t)

	if rec := doCrop(t, srv, cookie, http.MethodPut, `{"startMs":5000,"endMs":120000}`); rec.Code != http.StatusOK {
		t.Fatalf("put status = %d: %s", rec.Code, rec.Body.String())
	}
	var got crop.Points
	rec := doCrop(t, srv, cookie, http.MethodGet, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.StartMs != 5000 || got.EndMs != 120000 {
		t.Fatalf("crop = %+v", got)
	}

	if rec := doCrop(t, srv, cookie, http.MethodDelete, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rec.Code)
	}
	rec = doCrop(t, srv, cookie, http.MethodGet, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.StartMs != 0 || got.EndMs != 0 {
		t.Fatalf("after uncrop = %+v, want the whole file", got)
	}

	// Re-cropping after an uncrop must work — nothing was destroyed.
	if rec := doCrop(t, srv, cookie, http.MethodPut, `{"startMs":1000,"endMs":2000}`); rec.Code != http.StatusOK {
		t.Fatalf("re-crop status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = doCrop(t, srv, cookie, http.MethodGet, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.StartMs != 1000 || got.EndMs != 2000 {
		t.Fatalf("re-crop = %+v", got)
	}
}

// An end at or before the start would leave nothing to play.
func TestCropRejectsInvertedBoundaries(t *testing.T) {
	srv, cookie := cropServer(t)
	if rec := doCrop(t, srv, cookie, http.MethodPut, `{"startMs":5000,"endMs":5000}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if rec := doCrop(t, srv, cookie, http.MethodPut, `{"startMs":5000,"endMs":1000}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// An open-ended crop (trim only the intro) is the common case.
func TestCropAllowsOpenEnd(t *testing.T) {
	srv, cookie := cropServer(t)
	if rec := doCrop(t, srv, cookie, http.MethodPut, `{"startMs":8000}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got crop.Points
	rec := doCrop(t, srv, cookie, http.MethodGet, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.StartMs != 8000 || got.EndMs != 0 {
		t.Fatalf("crop = %+v, want an open end", got)
	}
}

// Cropping to the full length is not a crop; storing it would leave a row that
// says nothing.
func TestCropOfWholeTrackClearsTheRow(t *testing.T) {
	srv, cookie := cropServer(t)
	if rec := doCrop(t, srv, cookie, http.MethodPut, `{"startMs":3000,"endMs":9000}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec := doCrop(t, srv, cookie, http.MethodPut, `{"startMs":0,"endMs":0}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got crop.Points
	rec := doCrop(t, srv, cookie, http.MethodGet, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != (crop.Points{}) {
		t.Fatalf("crop = %+v, want cleared", got)
	}
}
