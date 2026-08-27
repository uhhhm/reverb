package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
)

// fakeLibrary implements library.LibraryAdapter (+ browse interfaces) for tests.
type fakeLibrary struct {
	lastRange string

	// playlist-mutation recorders
	createdName string
}

func (fakeLibrary) Type() string                             { return "library" }
func (fakeLibrary) Name() string                             { return "fake" }
func (fakeLibrary) ConfigSchema() registry.ConfigSchema      { return registry.ConfigSchema{} }
func (fakeLibrary) Init(cfg map[string]any) error            { return nil }
func (fakeLibrary) TestConnection(ctx context.Context) error { return nil }
func (fakeLibrary) Search(ctx context.Context, q string, types []core.EntityType) (core.SearchResults, error) {
	return core.SearchResults{Tracks: []core.Track{{ID: "t1", Title: "Song " + q}}}, nil
}
func (fakeLibrary) GetArtist(ctx context.Context, id string) (core.Artist, error) {
	return core.Artist{ID: id, Name: "Artist"}, nil
}
func (fakeLibrary) GetAlbum(ctx context.Context, id string) (core.Album, error) {
	return core.Album{ID: id, Name: "Album"}, nil
}
func (fakeLibrary) GetPlaylists(ctx context.Context) ([]core.Playlist, error) {
	return []core.Playlist{{ID: "p1", Name: "Mix"}}, nil
}
func (fakeLibrary) GetPlaylist(ctx context.Context, id string) (core.Playlist, error) {
	return core.Playlist{ID: id, Name: "Mix", Tracks: []core.Track{{ID: "t1", Title: "Song"}}}, nil
}
func (f *fakeLibrary) CreatePlaylist(ctx context.Context, name string) (core.Playlist, error) {
	f.createdName = name
	return core.Playlist{ID: "p-new", Name: name}, nil
}
func (f *fakeLibrary) AddTracksToPlaylist(ctx context.Context, playlistID string, trackIDs []string) error {
	return nil
}
func (f *fakeLibrary) Stream(ctx context.Context, trackID string, opts core.StreamOpts, rangeHeader string) (core.StreamHandle, error) {
	f.lastRange = rangeHeader
	status := http.StatusOK
	cr := ""
	if rangeHeader != "" {
		status = http.StatusPartialContent
		cr = "bytes 0-3/100"
	}
	return core.StreamHandle{
		Body:          io.NopCloser(strings.NewReader("abcd")),
		ContentType:   "audio/mpeg",
		ContentLength: 4,
		AcceptRanges:  "bytes",
		ContentRange:  cr,
		StatusCode:    status,
	}, nil
}
func (fakeLibrary) CoverArt(ctx context.Context, id string, size int) (core.CoverArt, error) {
	return core.CoverArt{Body: io.NopCloser(strings.NewReader("img")), ContentType: "image/jpeg"}, nil
}
func (fakeLibrary) StartScan(ctx context.Context) error { return nil }
func (fakeLibrary) ScanStatus(ctx context.Context) (core.ScanStatus, error) {
	return core.ScanStatus{}, nil
}
func (fakeLibrary) GetArtistsBrowse(ctx context.Context) ([]core.Artist, error) {
	return []core.Artist{{ID: "ar1", Name: "Artist"}}, nil
}
func (fakeLibrary) GetAlbumsBrowse(ctx context.Context, listType string, size int) ([]core.Album, error) {
	return []core.Album{{ID: "al1", Name: "Album"}}, nil
}
func (fakeLibrary) GetSongsBrowse(ctx context.Context, size, offset int) ([]core.Track, error) {
	return []core.Track{
		{ID: "t1", Title: "Song One", Artist: "Artist One"},
		{ID: "t2", Title: "Song Two", Artist: "Artist Two"},
	}, nil
}

func libTestServer(t *testing.T, lib *fakeLibrary) (*Server, *http.Cookie) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/api.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{
		Auth:       authSvc,
		Library:    lib,
		Search:     registry.NewRegistry("search"),
		Downloader: registry.NewRegistry("downloader"),
	})
	return srv, &http.Cookie{Name: sessionCookie, Value: tok}
}

func doAuthed(t *testing.T, srv *Server, method, target string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, nil)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestLibrarySearchHandler(t *testing.T) {
	srv, cookie := libTestServer(t, &fakeLibrary{})
	rec := doAuthed(t, srv, http.MethodGet, "/api/v1/library/search?q=hello", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var res core.SearchResults
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Tracks) != 1 || res.Tracks[0].Title != "Song hello" {
		t.Fatalf("results: %+v", res)
	}
}

func TestLibraryArtistAlbumPlaylistsHandlers(t *testing.T) {
	srv, cookie := libTestServer(t, &fakeLibrary{})
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/api/v1/library/artist/ar1", "Artist"},
		{"/api/v1/library/album/al1", "Album"},
		{"/api/v1/library/artists", "Artist"},
		{"/api/v1/library/albums?type=newest", "Album"},
		{"/api/v1/library/songs", "Song One"},
	} {
		rec := doAuthed(t, srv, http.MethodGet, tc.path, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d: %s", tc.path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("%s body missing %q: %s", tc.path, tc.want, rec.Body.String())
		}
	}
}

func doAuthedBody(t *testing.T, srv *Server, method, target, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestCreatePlaylistHandler(t *testing.T) {
	// handleCreatePlaylist now calls svc.CreateManaged — wire a fakeSync that
	// returns a SyncedPlaylistDetail.
	createDet := core.SyncedPlaylistDetail{
		SyncedPlaylist: core.SyncedPlaylist{
			ID:     "managed-1",
			Name:   "Road Trip",
			Source: "local",
			Mode:   "once",
		},
	}
	svc := &fakeSync{createDet: createDet}
	srv, cookie := syncTestServer(t, svc)
	rec := doAuthedBody(t, srv, http.MethodPost, "/api/v1/playlists", `{"name":"Road Trip"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var det core.SyncedPlaylistDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &det); err != nil {
		t.Fatal(err)
	}
	if det.Name != "Road Trip" || det.ID == "" {
		t.Fatalf("detail: %+v", det)
	}
	if svc.lastCreateName != "Road Trip" {
		t.Fatalf("CreateManaged not called with name: %q", svc.lastCreateName)
	}
}

func TestCreatePlaylistHandlerRejectsEmptyName(t *testing.T) {
	// Sync service must be non-nil to avoid 503; empty name rejected at handler level.
	svc := &fakeSync{}
	srv, cookie := syncTestServer(t, svc)
	for _, body := range []string{`{"name":""}`, `{"name":"   "}`, `{}`} {
		rec := doAuthedBody(t, srv, http.MethodPost, "/api/v1/playlists", body, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestCreatePlaylistHandlerNoSyncService(t *testing.T) {
	// When no sync service is configured (nil), handler returns 503.
	st, err := store.Open(t.TempDir() + "/noop.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{
		Auth:       authSvc,
		Search:     registry.NewRegistry("search"),
		Downloader: registry.NewRegistry("downloader"),
		// Sync intentionally not set
	})
	cookie := &http.Cookie{Name: sessionCookie, Value: tok}
	rec := doAuthedBody(t, srv, http.MethodPost, "/api/v1/playlists", `{"name":"Test"}`, cookie)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestPlaylistMutationsReturn503WhenNoLibrary(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/np.db")
	t.Cleanup(func() { st.Close() })
	_ = st.Migrate()
	authSvc, tok := seededAuthToken(t, st)
	// No sync service — POST /playlists returns 503 (sync unavailable).
	srv := NewServer(Deps{Auth: authSvc, Library: nil,
		Search: registry.NewRegistry("search"), Downloader: registry.NewRegistry("downloader")})
	cookie := &http.Cookie{Name: sessionCookie, Value: tok}

	rec := doAuthedBody(t, srv, http.MethodPost, "/api/v1/playlists", `{"name":"x"}`, cookie)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("create status = %d, want 503", rec.Code)
	}
}

func TestLibraryNilAdapterReturns503(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/n.db")
	t.Cleanup(func() { st.Close() })
	_ = st.Migrate()
	authSvc, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{Auth: authSvc, Library: nil,
		Search: registry.NewRegistry("search"), Downloader: registry.NewRegistry("downloader")})
	rec := doAuthed(t, srv, http.MethodGet, "/api/v1/library/search?q=x", &http.Cookie{Name: sessionCookie, Value: tok})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestImportSyncedPlaylistRouteResponds(t *testing.T) {
	// POST /api/v1/playlists/import-synced: 200 with sync service, 503 without.
	t.Run("with sync service returns 200", func(t *testing.T) {
		svc := &fakeSync{detail: core.SyncedPlaylistDetail{
			SyncedPlaylist: core.SyncedPlaylist{ID: "imp-1", Name: "Imported"},
		}}
		srv, cookie := syncTestServer(t, svc)
		body := `{"url":"https://open.spotify.com/playlist/XYZ","downloadMissing":false}`
		rec := doAuthedBody(t, srv, http.MethodPost, "/api/v1/playlists/import-synced", body, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("without sync service returns 503", func(t *testing.T) {
		srv, cookie := syncTestServer(t, nil)
		body := `{"url":"https://open.spotify.com/playlist/XYZ"}`
		rec := doAuthedBody(t, srv, http.MethodPost, "/api/v1/playlists/import-synced", body, cookie)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
		}
	})
}

// libTestServerWithOverrides is libTestServer plus a live override service, so
// rename round-trips can be asserted end to end.
func libTestServerWithOverrides(t *testing.T, lib *fakeLibrary) (*Server, *http.Cookie) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/api.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{
		Auth:       authSvc,
		Library:    lib,
		Search:     registry.NewRegistry("search"),
		Downloader: registry.NewRegistry("downloader"),
		Overrides:  override.New(st.Q()),
	})
	return srv, &http.Cookie{Name: sessionCookie, Value: tok}
}

func TestLibrarySongsHandler(t *testing.T) {
	srv, cookie := libTestServer(t, &fakeLibrary{})
	rec := doAuthed(t, srv, http.MethodGet, "/api/v1/library/songs", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var songs []core.Track
	if err := json.Unmarshal(rec.Body.Bytes(), &songs); err != nil {
		t.Fatal(err)
	}
	if len(songs) != 2 || songs[0].Title != "Song One" {
		t.Fatalf("songs: %+v", songs)
	}
}

func TestRenameTrackAppliesToSongList(t *testing.T) {
	srv, cookie := libTestServerWithOverrides(t, &fakeLibrary{})

	rec := doAuthedBody(t, srv, http.MethodPut, "/api/v1/library/track/t1/name",
		`{"title":"Better Title","artist":"Better Artist"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename status = %d: %s", rec.Code, rec.Body.String())
	}

	rec = doAuthed(t, srv, http.MethodGet, "/api/v1/library/songs", cookie)
	var songs []core.Track
	if err := json.Unmarshal(rec.Body.Bytes(), &songs); err != nil {
		t.Fatal(err)
	}
	if songs[0].Title != "Better Title" || songs[0].Artist != "Better Artist" {
		t.Fatalf("override not applied: %+v", songs[0])
	}
	// Untouched tracks keep the library's own names.
	if songs[1].Title != "Song Two" {
		t.Fatalf("unrelated track changed: %+v", songs[1])
	}
}

func TestRenameTrackBlankClearsOverride(t *testing.T) {
	srv, cookie := libTestServerWithOverrides(t, &fakeLibrary{})

	doAuthedBody(t, srv, http.MethodPut, "/api/v1/library/track/t1/name", `{"title":"Renamed","artist":""}`, cookie)
	rec := doAuthedBody(t, srv, http.MethodPut, "/api/v1/library/track/t1/name", `{"title":"","artist":""}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d: %s", rec.Code, rec.Body.String())
	}

	rec = doAuthed(t, srv, http.MethodGet, "/api/v1/library/songs", cookie)
	var songs []core.Track
	if err := json.Unmarshal(rec.Body.Bytes(), &songs); err != nil {
		t.Fatal(err)
	}
	if songs[0].Title != "Song One" {
		t.Fatalf("override not cleared: %+v", songs[0])
	}
}

func TestRenameTrackUnavailableWithoutOverrides(t *testing.T) {
	srv, cookie := libTestServer(t, &fakeLibrary{})
	rec := doAuthedBody(t, srv, http.MethodPut, "/api/v1/library/track/t1/name", `{"title":"x"}`, cookie)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
}
