package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/uhhhm/reverb/internal/notinterested"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
)

func notInterestedServer(t *testing.T) (*Server, *store.Store, *http.Cookie) {
	t.Helper()
	srv, st, cookie := trackSyncServer(t)
	srv.deps.NotInterested = notinterested.New(st.Q(), nil)
	srv.deps.Catalog = st.Q()
	return srv, st, cookie
}

func postMark(t *testing.T, srv *Server, cookie *http.Cookie, body string) notinterested.Mark {
	t.Helper()
	rec := do(t, srv, cookie, http.MethodPost, "/api/v1/not-interested", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark: status %d: %s", rec.Code, rec.Body.String())
	}
	var m notinterested.Mark
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func listMarks(t *testing.T, srv *Server) []notinterested.Mark {
	t.Helper()
	var body struct {
		Marks []notinterested.Mark `json:"marks"`
	}
	if code := getJSON(t, srv, "/not-interested", &body); code != http.StatusOK {
		t.Fatalf("list: status %d", code)
	}
	return body.Marks
}

func TestMarkSearchTrackNotInterested(t *testing.T) {
	srv, _, cookie := notInterestedServer(t)
	m := postMark(t, srv, cookie, `{"kind":"track","source":"deezer","externalId":"1","title":"D.A.N.C.E.","artist":"Justice"}`)
	if m.Key != notinterested.TrackMark("deezer", "1", "", "").Key {
		t.Fatalf("key %q is not the source-track key", m.Key)
	}
	if marks := listMarks(t, srv); len(marks) != 1 || marks[0].Title != "D.A.N.C.E." {
		t.Fatalf("marks = %+v", marks)
	}
}

// A library track is identified by its catalog entity's original metadata, so
// the mark matches the one another device makes for the same recording — even
// when this device shows the track under a rename.
func TestMarkLibraryTrackUsesCatalogIdentity(t *testing.T) {
	srv, st, cookie := notInterestedServer(t)
	ctx := context.Background()
	if err := st.Q().InsertCatalogEntity(ctx, db.InsertCatalogEntityParams{
		ID: "cat_1", Kind: "track", Title: "One More Time", Artist: "Daft Punk", Album: "Discovery", DurationMs: 320000,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Q().UpsertBackendBinding(ctx, db.UpsertBackendBindingParams{CatalogID: "cat_1", LibraryIdentity: "lib", BackendID: "backend_1"}); err != nil {
		t.Fatal(err)
	}

	m := postMark(t, srv, cookie, `{"kind":"track","source":"library","trackId":"backend_1","title":"My Rename","artist":"Someone Else"}`)
	want := notinterested.LibraryTrackMark("One More Time", "Daft Punk", "Discovery", 320000)
	if m.Key != want.Key || m.Title != "One More Time" {
		t.Fatalf("mark = %+v, want key %s from the catalog identity", m, want.Key)
	}
}

func TestMarkArtistNotInterested(t *testing.T) {
	srv, _, cookie := notInterestedServer(t)
	m := postMark(t, srv, cookie, `{"kind":"artist","source":"deezer","id":"27","name":"Daft Punk"}`)
	if m.Key != notinterested.ArtistMark("Daft Punk").Key || m.Kind != notinterested.KindArtist {
		t.Fatalf("mark = %+v", m)
	}
}

func TestUndoNotInterested(t *testing.T) {
	srv, _, cookie := notInterestedServer(t)
	m := postMark(t, srv, cookie, `{"kind":"artist","source":"deezer","id":"27","name":"Daft Punk"}`)
	if rec := do(t, srv, cookie, http.MethodDelete, "/api/v1/not-interested", `{"key":"`+m.Key+`"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("undo: status %d: %s", rec.Code, rec.Body.String())
	}
	if marks := listMarks(t, srv); len(marks) != 0 {
		t.Fatalf("marks after undo = %+v", marks)
	}
}

func TestMarkNotInterestedRejectsIncompleteRequests(t *testing.T) {
	srv, _, cookie := notInterestedServer(t)
	for _, body := range []string{
		`{"kind":"track","source":"deezer"}`,
		`{"kind":"track","source":"library"}`,
		`{"kind":"artist","source":"deezer","id":"27"}`,
		`{"kind":"album","source":"deezer","id":"1"}`,
		`not json`,
	} {
		if rec := do(t, srv, cookie, http.MethodPost, "/api/v1/not-interested", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", body, rec.Code)
		}
	}
	if rec := do(t, srv, cookie, http.MethodDelete, "/api/v1/not-interested", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("undo without key: status %d, want 400", rec.Code)
	}
}

func TestNotInterestedUnavailableWithoutService(t *testing.T) {
	srv, _, cookie := trackSyncServer(t)
	if rec := do(t, srv, cookie, http.MethodGet, "/api/v1/not-interested", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
}
