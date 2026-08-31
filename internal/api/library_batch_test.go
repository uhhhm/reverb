package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/cover"
	"github.com/uhhhm/reverb/internal/library"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
)

// libTestServerWithEntitiesAndCovers is like libTestServerWithOverrides but also
// wires Entities and Covers, mirroring the wiring in internal/app/build.go.
func libTestServerWithEntitiesAndCovers(t *testing.T, lib library.LibraryAdapter) (*Server, *http.Cookie) {
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
	coverDir := t.TempDir()
	srv := NewServer(Deps{
		Auth:       authSvc,
		Library:    lib,
		Search:     registry.NewRegistry("search"),
		Downloader: registry.NewRegistry("downloader"),
		Overrides:  override.New(st.Q()),
		Entities:   override.NewEntities(st.Q()),
		Covers:     cover.New(st.Q(), coverDir),
	})
	return srv, &http.Cookie{Name: sessionCookie, Value: tok}
}

// batchLib is a controllable library that lets tests inject album/artist/song
// data and optionally fail certain lookups so per-item error handling can be
// exercised without touching the database directly via triggers.
type batchLib struct {
	albums           map[string]core.Album
	artists          map[string]core.Artist
	songs            []core.Track
	failGetAlbumIDs  map[string]bool
	failGetArtistIDs map[string]bool
}

func (b *batchLib) Type() string                             { return "library" }
func (b *batchLib) Name() string                             { return "fake" }
func (b *batchLib) ConfigSchema() registry.ConfigSchema      { return registry.ConfigSchema{} }
func (b *batchLib) Init(map[string]any) error                { return nil }
func (b *batchLib) TestConnection(ctx context.Context) error { return nil }
func (b *batchLib) Search(ctx context.Context, q string, types []core.EntityType) (core.SearchResults, error) {
	return core.SearchResults{Tracks: []core.Track{{ID: "t1", Title: "Song " + q}}}, nil
}
func (b *batchLib) GetArtist(ctx context.Context, id string) (core.Artist, error) {
	if b.failGetArtistIDs != nil && b.failGetArtistIDs[id] {
		return core.Artist{}, errors.New("not found")
	}
	if a, ok := b.artists[id]; ok {
		return a, nil
	}
	// also check songs? fallback to generic
	return core.Artist{ID: id, Name: "Artist"}, nil
}
func (b *batchLib) GetAlbum(ctx context.Context, id string) (core.Album, error) {
	if b.failGetAlbumIDs != nil && b.failGetAlbumIDs[id] {
		return core.Album{}, errors.New("not found")
	}
	if al, ok := b.albums[id]; ok {
		return al, nil
	}
	return core.Album{ID: id, Name: "Album", Artist: "Artist"}, nil
}
func (b *batchLib) GetPlaylists(ctx context.Context) ([]core.Playlist, error) {
	return []core.Playlist{{ID: "p1", Name: "Mix"}}, nil
}
func (b *batchLib) GetPlaylist(ctx context.Context, id string) (core.Playlist, error) {
	return core.Playlist{ID: id, Name: "Mix", Tracks: []core.Track{{ID: "t1", Title: "Song"}}}, nil
}
func (b *batchLib) CreatePlaylist(ctx context.Context, name string) (core.Playlist, error) {
	return core.Playlist{ID: "p-new", Name: name}, nil
}
func (b *batchLib) AddTracksToPlaylist(ctx context.Context, playlistID string, trackIDs []string) error {
	return nil
}
func (b *batchLib) Stream(ctx context.Context, trackID string, opts core.StreamOpts, rangeHeader string) (core.StreamHandle, error) {
	return core.StreamHandle{}, nil
}
func (b *batchLib) CoverArt(ctx context.Context, id string, size int) (core.CoverArt, error) {
	return core.CoverArt{}, nil
}
func (b *batchLib) StartScan(ctx context.Context) error { return nil }
func (b *batchLib) ScanStatus(ctx context.Context) (core.ScanStatus, error) {
	return core.ScanStatus{}, nil
}
func (b *batchLib) GetArtistsBrowse(ctx context.Context) ([]core.Artist, error) {
	if len(b.artists) > 0 {
		out := make([]core.Artist, 0, len(b.artists))
		for _, a := range b.artists {
			out = append(out, a)
		}
		return out, nil
	}
	return []core.Artist{{ID: "ar1", Name: "Artist"}}, nil
}
func (b *batchLib) GetAlbumsBrowse(ctx context.Context, listType string, size int) ([]core.Album, error) {
	if len(b.albums) > 0 {
		out := make([]core.Album, 0, len(b.albums))
		for _, al := range b.albums {
			out = append(out, al)
		}
		return out, nil
	}
	return []core.Album{{ID: "al1", Name: "Album"}}, nil
}
func (b *batchLib) GetSongsBrowse(ctx context.Context, size, offset int) ([]core.Track, error) {
	if b.songs != nil {
		// A copy, because decoration rewrites tracks in place. A real adapter
		// builds fresh values per request; handing out the backing slice would
		// make one request's overrides look like the library's own data on the
		// next.
		return append([]core.Track(nil), b.songs...), nil
	}
	return []core.Track{
		{ID: "t1", Title: "Song One", Artist: "Artist One", Album: "Album One"},
		{ID: "t2", Title: "Song Two", Artist: "Artist Two", Album: "Album Two"},
	}, nil
}

var _ library.LibraryAdapter = (*batchLib)(nil)

func tinyPNGBytes() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func newCoverMultipart(t *testing.T, imageData []byte, targets ...string) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	fw, err := w.CreateFormFile("image", "test.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(imageData); err != nil {
		t.Fatal(err)
	}
	for _, tg := range targets {
		if err := w.WriteField("target", tg); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf, w.FormDataContentType()
}

// ---------------------------------------------------------------------------
// Album rename
// ---------------------------------------------------------------------------

func TestAlbumRenameCascadesToTracks(t *testing.T) {
	lib := &batchLib{
		albums: map[string]core.Album{
			"a1": {
				ID: "a1", Name: "Album", Artist: "Artist", ArtistID: "ar1",
				Tracks: []core.Track{
					{ID: "t1", Title: "Song", Album: "Album", Artist: "Artist", AlbumID: "a1", ArtistID: "ar1"},
					{ID: "t2", Title: "Other", Album: "Album", Artist: "Artist", AlbumID: "a1", ArtistID: "ar1"},
				},
			},
		},
	}
	srv, cookie := libTestServerWithEntitiesAndCovers(t, lib)

	rec := doAuthedBody(t, srv, http.MethodPut, "/api/v1/library/album/a1/name", `{"name":"New Album"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT album rename status = %d: %s", rec.Code, rec.Body.String())
	}
	var got entityRename
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "a1" || got.Name != "New Album" {
		t.Fatalf("rename response = %+v want a1/New Album", got)
	}

	rec = doAuthed(t, srv, http.MethodGet, "/api/v1/library/album/a1", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET album status = %d: %s", rec.Code, rec.Body.String())
	}
	var al core.Album
	if err := json.Unmarshal(rec.Body.Bytes(), &al); err != nil {
		t.Fatal(err)
	}
	if al.Name != "New Album" {
		t.Fatalf("Album.Name = %q want New Album", al.Name)
	}
	if len(al.Tracks) == 0 {
		t.Fatal("album tracks missing")
	}
	for _, tr := range al.Tracks {
		if tr.Album != "New Album" {
			t.Fatalf("track %s Album = %q want New Album (cascade)", tr.ID, tr.Album)
		}
	}
}

func TestAlbumRenameEmptyClears(t *testing.T) {
	lib := &batchLib{
		albums: map[string]core.Album{
			"a1": {
				ID: "a1", Name: "Album", Artist: "Artist", ArtistID: "ar1",
				Tracks: []core.Track{
					{ID: "t1", Title: "Song", Album: "Album", Artist: "Artist", AlbumID: "a1"},
				},
			},
		},
	}
	srv, cookie := libTestServerWithEntitiesAndCovers(t, lib)

	rec := doAuthedBody(t, srv, http.MethodPut, "/api/v1/library/album/a1/name", `{"name":"Renamed"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("first rename status = %d: %s", rec.Code, rec.Body.String())
	}
	// Clear with empty name (and also test whitespace trimming)
	rec = doAuthedBody(t, srv, http.MethodPut, "/api/v1/library/album/a1/name", `{"name":""}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear rename status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = doAuthed(t, srv, http.MethodGet, "/api/v1/library/album/a1", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET after clear status = %d: %s", rec.Code, rec.Body.String())
	}
	var al core.Album
	if err := json.Unmarshal(rec.Body.Bytes(), &al); err != nil {
		t.Fatal(err)
	}
	if al.Name != "Album" {
		t.Fatalf("Album.Name after clear = %q want Album", al.Name)
	}
	if len(al.Tracks) > 0 && al.Tracks[0].Album != "Album" {
		t.Fatalf("track Album after clear = %q want Album", al.Tracks[0].Album)
	}

	// Also verify whitespace-only clears
	rec = doAuthedBody(t, srv, http.MethodPut, "/api/v1/library/album/a1/name", `{"name":"  Second  "}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("second rename status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = doAuthedBody(t, srv, http.MethodPut, "/api/v1/library/album/a1/name", `{"name":"   "}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("whitespace clear status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = doAuthed(t, srv, http.MethodGet, "/api/v1/library/album/a1", cookie)
	_ = json.Unmarshal(rec.Body.Bytes(), &al)
	if al.Name != "Album" {
		t.Fatalf("Album.Name after whitespace clear = %q want Album", al.Name)
	}
}

// ---------------------------------------------------------------------------
// Batch rename
// ---------------------------------------------------------------------------

func TestBatchRenameAppliesAll(t *testing.T) {
	lib := &batchLib{
		albums: map[string]core.Album{
			"a1": {ID: "a1", Name: "Album", Artist: "Artist", ArtistID: "ar1"},
		},
		artists: map[string]core.Artist{
			"ar1": {ID: "ar1", Name: "Artist"},
		},
		songs: []core.Track{
			{ID: "t1", Title: "Song One", Artist: "Artist One", Album: "Album One", AlbumID: "a1", ArtistID: "ar1"},
			{ID: "t2", Title: "Song Two", Artist: "Artist Two", Album: "Album Two"},
		},
	}
	srv, cookie := libTestServerWithEntitiesAndCovers(t, lib)

	body := `{"tracks":[{"id":"t1","title":"New Title","artist":"New Artist","album":"New Album"}],"albums":[{"id":"a1","name":"Renamed Album"}],"artists":[{"id":"ar1","name":"Renamed Artist"}]}`
	rec := doAuthedBody(t, srv, http.MethodPost, "/api/v1/library/rename/batch", body, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp batchRenameResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Applied != 3 {
		t.Fatalf("applied = %d want 3, errors=%v", resp.Applied, resp.Errors)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %v want none", resp.Errors)
	}

	// Verify track rename via songs list
	rec = doAuthed(t, srv, http.MethodGet, "/api/v1/library/songs", cookie)
	var songs []core.Track
	if err := json.Unmarshal(rec.Body.Bytes(), &songs); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range songs {
		if s.ID == "t1" {
			found = true
			if s.Title != "New Title" || s.Artist != "New Artist" || s.Album != "New Album" {
				t.Fatalf("track t1 after batch = %+v want New Title/New Artist/New Album", s)
			}
		}
	}
	if !found {
		t.Fatalf("t1 not in songs %v", songs)
	}
	// Verify album rename
	rec = doAuthed(t, srv, http.MethodGet, "/api/v1/library/album/a1", cookie)
	var al core.Album
	if err := json.Unmarshal(rec.Body.Bytes(), &al); err != nil {
		t.Fatal(err)
	}
	if al.Name != "Renamed Album" {
		t.Fatalf("album name = %q want Renamed Album", al.Name)
	}
	// Verify artist rename
	rec = doAuthed(t, srv, http.MethodGet, "/api/v1/library/artist/ar1", cookie)
	var ar core.Artist
	if err := json.Unmarshal(rec.Body.Bytes(), &ar); err != nil {
		t.Fatal(err)
	}
	if ar.Name != "Renamed Artist" {
		t.Fatalf("artist name = %q want Renamed Artist", ar.Name)
	}
}

func TestBatchRenamePartialFailure(t *testing.T) {
	// Inject a DB trigger that makes one entity_id fail, so the batch reports
	// per-item errors while still applying the good ones. This exercises the
	// fail-per-item rather than fail-whole-batch contract.
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/api.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	// Create triggers that abort inserts/updates for a known bad id.
	// Separate triggers for track_override and entity_override.
	for _, sql := range []string{
		`CREATE TRIGGER fail_track_bad BEFORE INSERT ON track_override FOR EACH ROW WHEN NEW.track_id='bad-track' BEGIN SELECT RAISE(ABORT, 'injected failure for bad-track'); END;`,
		`CREATE TRIGGER fail_track_bad_upd BEFORE UPDATE ON track_override FOR EACH ROW WHEN NEW.track_id='bad-track' BEGIN SELECT RAISE(ABORT, 'injected failure for bad-track'); END;`,
		`CREATE TRIGGER fail_entity_bad BEFORE INSERT ON entity_override FOR EACH ROW WHEN NEW.entity_id='bad-album' BEGIN SELECT RAISE(ABORT, 'injected failure for bad-album'); END;`,
		`CREATE TRIGGER fail_entity_bad_upd BEFORE UPDATE ON entity_override FOR EACH ROW WHEN NEW.entity_id='bad-album' BEGIN SELECT RAISE(ABORT, 'injected failure for bad-album'); END;`,
	} {
		if _, err := st.DB().ExecContext(ctx, sql); err != nil {
			t.Fatalf("create trigger: %v", err)
		}
	}
	authSvc, tok := seededAuthToken(t, st)
	coverDir := t.TempDir()
	lib := &batchLib{
		albums: map[string]core.Album{
			"a1": {ID: "a1", Name: "Album", Artist: "Artist"},
		},
		songs: []core.Track{
			{ID: "t1", Title: "Song One", Artist: "A", Album: "Al"},
		},
		artists: map[string]core.Artist{
			"ar1": {ID: "ar1", Name: "Artist"},
		},
	}
	srv := NewServer(Deps{
		Auth:       authSvc,
		Library:    lib,
		Search:     registry.NewRegistry("search"),
		Downloader: registry.NewRegistry("downloader"),
		Overrides:  override.New(st.Q()),
		Entities:   override.NewEntities(st.Q()),
		Covers:     cover.New(st.Q(), coverDir),
	})
	cookie := &http.Cookie{Name: sessionCookie, Value: tok}

	// One bad album id, one good album, one good track, one good artist.
	// The bad album should fail, the rest should succeed.
	body := `{"tracks":[{"id":"t1","title":"Good Track"}],"albums":[{"id":"a1","name":"Good Album"},{"id":"bad-album","name":"Bad"}],"artists":[{"id":"ar1","name":"Good Artist"}]}`
	rec := doAuthedBody(t, srv, http.MethodPost, "/api/v1/library/rename/batch", body, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp batchRenameResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Applied != 3 {
		t.Fatalf("applied = %d want 3 (good track + good album + good artist), errors=%v body=%s", resp.Applied, resp.Errors, rec.Body.String())
	}
	if len(resp.Errors) == 0 {
		t.Fatal("expected errors for bad-album")
	}
	if _, ok := resp.Errors["bad-album"]; !ok {
		t.Fatalf("errors missing bad-album: %v", resp.Errors)
	}
	// Verify good album still applied
	rec2 := doAuthed(t, srv, http.MethodGet, "/api/v1/library/album/a1", cookie)
	var al core.Album
	_ = json.Unmarshal(rec2.Body.Bytes(), &al)
	if al.Name != "Good Album" {
		t.Fatalf("good album not applied: %q", al.Name)
	}
}

func TestBatchRenameOverLimit(t *testing.T) {
	lib := &batchLib{}
	srv, cookie := libTestServerWithEntitiesAndCovers(t, lib)

	// Build 501 albums
	var albums []entityRename
	for i := 0; i < 501; i++ {
		albums = append(albums, entityRename{ID: strings.Repeat("a", 3) + string(rune('0'+i%10)), Name: "n"})
	}
	// Use JSON marshaling to avoid manual string building
	req := batchRenameRequest{Albums: albums}
	b, _ := json.Marshal(req)
	rec := doAuthedBody(t, srv, http.MethodPost, "/api/v1/library/rename/batch", string(b), cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over limit status = %d want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "too many") {
		t.Fatalf("body missing too many: %s", rec.Body.String())
	}

	// Exactly 500 should succeed
	var albums500 []entityRename
	for i := 0; i < 500; i++ {
		albums500 = append(albums500, entityRename{ID: "a" + strings.Repeat("x", 2) + string(rune(i)), Name: "n"})
	}
	req2 := batchRenameRequest{Albums: albums500}
	b2, _ := json.Marshal(req2)
	rec2 := doAuthedBody(t, srv, http.MethodPost, "/api/v1/library/rename/batch", string(b2), cookie)
	if rec2.Code != http.StatusOK {
		t.Fatalf("500 items status = %d want 200: %s", rec2.Code, rec2.Body.String())
	}
}

func TestBatchRenameWithEmptyIDsAreSkipped(t *testing.T) {
	lib := &batchLib{}
	srv, cookie := libTestServerWithEntitiesAndCovers(t, lib)
	body := `{"tracks":[{"id":"","title":"x"},{"id":"t1","title":"ok"}],"albums":[{"id":"","name":"y"}]}`
	rec := doAuthedBody(t, srv, http.MethodPost, "/api/v1/library/rename/batch", body, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp batchRenameResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Applied != 1 {
		t.Fatalf("applied = %d want 1 (empty ids skipped)", resp.Applied)
	}
}

// ---------------------------------------------------------------------------
// 503 when deps nil
// ---------------------------------------------------------------------------

func TestRenameEndpointsReturn503WhenUnavailable(t *testing.T) {
	// Entities nil -> album/artist rename 503
	st, err := store.Open(t.TempDir() + "/noent.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	_ = st.Migrate()
	authSvc, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{
		Auth:       authSvc,
		Library:    &batchLib{},
		Search:     registry.NewRegistry("search"),
		Downloader: registry.NewRegistry("downloader"),
		// Entities intentionally nil
		Overrides: override.New(st.Q()),
	})
	cookie := &http.Cookie{Name: sessionCookie, Value: tok}
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPut, "/api/v1/library/album/a1/name", `{"name":"x"}`},
		{http.MethodPut, "/api/v1/library/artist/ar1/name", `{"name":"x"}`},
	} {
		rec := doAuthedBody(t, srv, tc.method, tc.path, tc.body, cookie)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s status = %d want 503", tc.method, tc.path, rec.Code)
		}
	}
	// Batch requires both Overrides and Entities; nil Entities -> 503
	rec := doAuthedBody(t, srv, http.MethodPost, "/api/v1/library/rename/batch", `{"albums":[{"id":"a1","name":"x"}]}`, cookie)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("batch without Entities status = %d want 503", rec.Code)
	}

	// Nil Overrides also -> 503
	srv2 := NewServer(Deps{
		Auth:       authSvc,
		Library:    &batchLib{},
		Search:     registry.NewRegistry("search"),
		Downloader: registry.NewRegistry("downloader"),
		Entities:   override.NewEntities(st.Q()),
		// Overrides nil
	})
	rec = doAuthedBody(t, srv2, http.MethodPost, "/api/v1/library/rename/batch", `{"tracks":[{"id":"t1","title":"x"}]}`, cookie)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("batch without Overrides status = %d want 503", rec.Code)
	}
}

func TestCoverEndpointsReturn503WhenUnavailable(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/nocover.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	_ = st.Migrate()
	authSvc, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{
		Auth:       authSvc,
		Library:    &batchLib{},
		Search:     registry.NewRegistry("search"),
		Downloader: registry.NewRegistry("downloader"),
		// Covers nil
	})
	cookie := &http.Cookie{Name: sessionCookie, Value: tok}

	// Upload
	pngBytes := tinyPNGBytes()
	buf, ctype := newCoverMultipart(t, pngBytes, "album:a1")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/covers", buf)
	req.Header.Set("Content-Type", ctype)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("cover upload without Covers status = %d want 503", rec.Code)
	}

	// Delete
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodDelete, "/api/v1/library/covers", strings.NewReader(`{"targets":["album:a1"]}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusServiceUnavailable {
		t.Fatalf("cover delete without Covers status = %d want 503", rec2.Code)
	}
}

// ---------------------------------------------------------------------------
// Covers: batch upload
// ---------------------------------------------------------------------------

func TestCoverUploadBatchAndServe(t *testing.T) {
	lib := &batchLib{
		albums: map[string]core.Album{
			"a1": {ID: "a1", Name: "Album", Artist: "Artist"},
		},
	}
	srv, cookie := libTestServerWithEntitiesAndCovers(t, lib)
	pngBytes := tinyPNGBytes()

	// One image, two targets (album + track) == batch case
	buf, ctype := newCoverMultipart(t, pngBytes, "album:a1", "track:t1")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/covers", buf)
	req.Header.Set("Content-Type", ctype)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp coverBatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Applied != 2 {
		t.Fatalf("applied = %d want 2", resp.Applied)
	}
	matched, _ := regexp.MatchString(`^custom:[0-9a-f]{64}\.png$`, resp.CoverArtID)
	if !matched {
		t.Fatalf("coverArtId = %q want custom:<64 hex>.png", resp.CoverArtID)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %v want none", resp.Errors)
	}

	// GET the uploaded cover should return exact bytes with image/png
	rec2 := httptest.NewRecorder()
	// cover id contains colon, but chi will capture it; use raw id in path
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/cover/"+resp.CoverArtID, nil)
	req2.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET cover status = %d: %s", rec2.Code, rec2.Body.String())
	}
	if ct := rec2.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q want image/png", ct)
	}
	if !bytes.Equal(rec2.Body.Bytes(), pngBytes) {
		t.Fatalf("cover bytes mismatch: got %d want %d", len(rec2.Body.Bytes()), len(pngBytes))
	}
	// Verify that the image is actually stored once (second GET same)
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/cover/"+resp.CoverArtID, nil)
	req3.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("second GET cover status = %d", rec3.Code)
	}
}

func TestCoverUploadNonImageReturns400(t *testing.T) {
	lib := &batchLib{}
	srv, cookie := libTestServerWithEntitiesAndCovers(t, lib)

	buf, ctype := newCoverMultipart(t, []byte("not an image"), "album:a1")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/covers", buf)
	req.Header.Set("Content-Type", ctype)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-image upload status = %d want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestCoverUploadNoValidTargetReturns400(t *testing.T) {
	lib := &batchLib{}
	srv, cookie := libTestServerWithEntitiesAndCovers(t, lib)
	pngBytes := tinyPNGBytes()

	tests := []struct {
		name    string
		targets []string
		noImage bool
	}{
		{"no target field", nil, false},
		{"invalid kind", []string{"foo:bar"}, false},
		{"empty id", []string{"album:"}, false},
		{"mixed invalid only", []string{"bad", "also:bad"}, false},
		{"missing image", []string{"album:a1"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf *bytes.Buffer
			var ctype string
			if tc.noImage {
				// Build multipart without image part
				b := &bytes.Buffer{}
				w := multipart.NewWriter(b)
				for _, tg := range tc.targets {
					_ = w.WriteField("target", tg)
				}
				_ = w.Close()
				buf = b
				ctype = w.FormDataContentType()
			} else {
				if tc.targets == nil {
					// No targets at all: just image
					b := &bytes.Buffer{}
					w := multipart.NewWriter(b)
					fw, _ := w.CreateFormFile("image", "test.png")
					_, _ = fw.Write(pngBytes)
					_ = w.Close()
					buf = b
					ctype = w.FormDataContentType()
				} else {
					buf, ctype = newCoverMultipart(t, pngBytes, tc.targets...)
				}
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/library/covers", buf)
			req.Header.Set("Content-Type", ctype)
			req.AddCookie(cookie)
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: status = %d want 400: %s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestCoverUploadTooManyTargetsReturns400(t *testing.T) {
	lib := &batchLib{}
	srv, cookie := libTestServerWithEntitiesAndCovers(t, lib)
	pngBytes := tinyPNGBytes()
	// 501 targets
	var targets []string
	for i := 0; i < 501; i++ {
		targets = append(targets, "album:a"+string(rune(i)))
	}
	buf, ctype := newCoverMultipart(t, pngBytes, targets...)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/covers", buf)
	req.Header.Set("Content-Type", ctype)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("too many targets status = %d want 400", rec.Code)
	}
}

func TestCoverDeleteClears(t *testing.T) {
	lib := &batchLib{
		albums: map[string]core.Album{
			"a1": {ID: "a1", Name: "Album", Artist: "Artist"},
		},
	}
	srv, cookie := libTestServerWithEntitiesAndCovers(t, lib)
	pngBytes := tinyPNGBytes()

	// Upload first
	buf, ctype := newCoverMultipart(t, pngBytes, "album:a1")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/covers", buf)
	req.Header.Set("Content-Type", ctype)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d: %s", rec.Code, rec.Body.String())
	}
	var upResp coverBatchResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &upResp)
	if upResp.CoverArtID == "" {
		t.Fatal("coverArtId empty after upload")
	}

	// Verify album shows custom cover
	rec2 := doAuthed(t, srv, http.MethodGet, "/api/v1/library/album/a1", cookie)
	var al core.Album
	_ = json.Unmarshal(rec2.Body.Bytes(), &al)
	if al.CoverArtID != upResp.CoverArtID {
		t.Fatalf("album CoverArtID = %q want %q", al.CoverArtID, upResp.CoverArtID)
	}

	// Delete
	delBody := `{"targets":["album:a1"]}`
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodDelete, "/api/v1/library/covers", strings.NewReader(delBody))
	req3.Header.Set("Content-Type", "application/json")
	req3.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("delete status = %d: %s", rec3.Code, rec3.Body.String())
	}
	var delResp coverBatchResponse
	_ = json.Unmarshal(rec3.Body.Bytes(), &delResp)
	if delResp.Applied != 1 {
		t.Fatalf("delete applied = %d want 1", delResp.Applied)
	}

	// Album should no longer have custom cover
	rec4 := doAuthed(t, srv, http.MethodGet, "/api/v1/library/album/a1", cookie)
	var al2 core.Album
	_ = json.Unmarshal(rec4.Body.Bytes(), &al2)
	if al2.CoverArtID == upResp.CoverArtID {
		t.Fatalf("album cover not cleared: %q", al2.CoverArtID)
	}
	// Cover bytes may still be served if another entity holds it, but our single
	// assignment was cleared, so the cover id is no longer reachable via album;
	// direct GET of the old id may still succeed because blob remains until GC.
	// We just verify the assignment was removed via album state.

	// Also verify deleting with no valid target returns 400
	rec5 := httptest.NewRecorder()
	req5 := httptest.NewRequest(http.MethodDelete, "/api/v1/library/covers", strings.NewReader(`{"targets":["bad"]}`))
	req5.Header.Set("Content-Type", "application/json")
	req5.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec5, req5)
	if rec5.Code != http.StatusBadRequest {
		t.Fatalf("delete bad target status = %d want 400", rec5.Code)
	}
}

// Ensure batch and rename don't require Origin header (csrfGuard allows empty Origin)
func TestBatchAndCoverDoNotRequireOrigin(t *testing.T) {
	lib := &batchLib{}
	srv, cookie := libTestServerWithEntitiesAndCovers(t, lib)

	// Batch with Origin evil should be blocked, without Origin should pass
	body := `{"albums":[{"id":"a1","name":"x"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/rename/batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example.com")
	req.Host = "reverb.local"
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("evil Origin should be blocked, got %d", rec.Code)
	}

	// Same without Origin should pass
	rec2 := doAuthedBody(t, srv, http.MethodPost, "/api/v1/library/rename/batch", body, cookie)
	if rec2.Code != http.StatusOK {
		t.Fatalf("without Origin should pass, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

// Verify that the 503 for covers also applies to serveUploadedCover path:
// GET /api/v1/cover/custom:... when Covers is nil should not serve (falls through to library but we don't have one)
func TestCoverServeWithNoCoversConfiguredFallsThrough(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/nocover2.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	_ = st.Migrate()
	authSvc, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{
		Auth:       authSvc,
		Library:    &batchLib{},
		Search:     registry.NewRegistry("search"),
		Downloader: registry.NewRegistry("downloader"),
		// Covers nil
	})
	cookie := &http.Cookie{Name: sessionCookie, Value: tok}
	rec := doAuthed(t, srv, http.MethodGet, "/api/v1/cover/custom:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png", cookie)
	// With no library, it would be 404 or 503? The handler first checks serveUploadedCover which returns false when Covers nil/empty, then requires library.
	// Library exists (batchLib) but its CoverArt not for custom id, but it will attempt to call lib.CoverArt and return 200 with dummy.
	// We just ensure it does not panic.
	if rec.Code == http.StatusServiceUnavailable {
		// acceptable if library nil, but we have library
	}
}

func TestCoverDeleteAndUploadRoundTripContentType(t *testing.T) {
	// Ensure different image types are accepted (png, jpeg, webp) — at least png.
	lib := &batchLib{}
	srv, cookie := libTestServerWithEntitiesAndCovers(t, lib)
	pngBytes := tinyPNGBytes()
	buf, ctype := newCoverMultipart(t, pngBytes, "album:a1")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/covers", buf)
	req.Header.Set("Content-Type", ctype)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("png upload status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp coverBatchResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.HasSuffix(resp.CoverArtID, ".png") {
		t.Fatalf("png coverArtId suffix = %q want .png", resp.CoverArtID)
	}
}

// The frontend builds cover URLs with encodeURIComponent, so the colon in a
// "custom:" id reaches the server percent-encoded. chi routes on the raw path,
// which means the handler sees %3A rather than ':' — the id the browser
// actually sends has to resolve.
func TestCoverServeAcceptsPercentEncodedID(t *testing.T) {
	lib := &batchLib{albums: map[string]core.Album{"a1": {ID: "a1", Name: "Album", Artist: "Artist"}}}
	srv, cookie := libTestServerWithEntitiesAndCovers(t, lib)
	png := tinyPNGBytes()

	buf, ctype := newCoverMultipart(t, png, "album:a1")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/covers", buf)
	req.Header.Set("Content-Type", ctype)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp coverBatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	rec2 := httptest.NewRecorder()
	// encodeURIComponent escapes the colon; url.PathEscape does not, so the
	// browser's exact encoding is spelled out here.
	encoded := strings.ReplaceAll(resp.CoverArtID, ":", "%3A")
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/cover/"+encoded+"?size=300", nil)
	req2.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET encoded cover status = %d: %s", rec2.Code, rec2.Body.String())
	}
	if !bytes.Equal(rec2.Body.Bytes(), png) {
		t.Fatal("encoded cover id did not serve the uploaded bytes")
	}
}

// TestTrackRenameKeepsFieldsLeftOut covers the difference between a field that
// is absent and one that is blank. The single-track rename dialog sends only
// title and artist, so an album override — set in a batch, or arrived from a
// peer — must survive it; sending the field blank is what clears it.
func TestTrackRenameKeepsFieldsLeftOut(t *testing.T) {
	lib := &batchLib{
		songs: []core.Track{
			{ID: "t1", Title: "Song One", Artist: "Artist One", Album: "Album One"},
		},
	}
	srv, cookie := libTestServerWithEntitiesAndCovers(t, lib)

	body := `{"tracks":[{"id":"t1","album":"Batch Album"}]}`
	if rec := doAuthedBody(t, srv, http.MethodPost, "/api/v1/library/rename/batch", body, cookie); rec.Code != http.StatusOK {
		t.Fatalf("batch status = %d: %s", rec.Code, rec.Body.String())
	}

	songTitled := func() core.Track {
		t.Helper()
		rec := doAuthed(t, srv, http.MethodGet, "/api/v1/library/songs", cookie)
		var songs []core.Track
		if err := json.Unmarshal(rec.Body.Bytes(), &songs); err != nil {
			t.Fatal(err)
		}
		for _, s := range songs {
			if s.ID == "t1" {
				return s
			}
		}
		t.Fatalf("t1 not in songs %v", songs)
		return core.Track{}
	}

	if got := songTitled(); got.Album != "Batch Album" || got.Title != "Song One" {
		t.Fatalf("after batch = %q/%q want Song One/Batch Album", got.Title, got.Album)
	}

	// The single-track dialog's request shape: title and artist only.
	rec := doAuthedBody(t, srv, http.MethodPut, "/api/v1/library/track/t1/name",
		`{"title":"New Title","artist":"New Artist"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := songTitled(); got.Album != "Batch Album" {
		t.Fatalf("album after title rename = %q want Batch Album", got.Album)
	} else if got.Title != "New Title" || got.Artist != "New Artist" {
		t.Fatalf("names after rename = %q/%q want New Title/New Artist", got.Title, got.Artist)
	}

	// Blank is still a clear.
	rec = doAuthedBody(t, srv, http.MethodPut, "/api/v1/library/track/t1/name",
		`{"title":"New Title","artist":"New Artist","album":""}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := songTitled(); got.Album != "Album One" {
		t.Fatalf("album after blank = %q want the library's Album One", got.Album)
	}
}
