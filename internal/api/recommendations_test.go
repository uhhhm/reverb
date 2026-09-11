package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
)

type fakeRecommendations struct {
	artists          recommend.ArtistResult
	gotSource, gotID string
	tracks           recommend.TrackResult
	gotArtist        string
	gotTitle         string
	gotSeeds         []recommend.Seed
}

func (f *fakeRecommendations) SimilarTracks(_ context.Context, artist, title string) recommend.TrackResult {
	f.gotArtist, f.gotTitle = artist, title
	return f.tracks
}

func (f *fakeRecommendations) Radio(_ context.Context, seeds []recommend.Seed) recommend.TrackResult {
	f.gotSeeds = seeds
	return f.tracks
}

func postRadio(t *testing.T, srv *Server, body string, out any) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recommendations/radio", strings.NewReader(body))
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

func TestRadioEndpoint(t *testing.T) {
	fake := &fakeRecommendations{tracks: recommend.TrackResult{Available: true, Tracks: []core.ExternalResult{
		{Source: "deezer", ExternalID: "1", Title: "Genesis", Artist: "Justice", Type: core.EntityTrack},
	}}}
	srv := recommendationServer(t, fake)

	var body recommend.TrackResult
	code := postRadio(t, srv, `{"seeds":[{"artist":" Daft Punk ","title":"One More Time"},{"artist":"Air"}]}`, &body)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	want := []recommend.Seed{{Artist: "Daft Punk", Title: "One More Time"}, {Artist: "Air"}}
	if !reflect.DeepEqual(fake.gotSeeds, want) {
		t.Fatalf("seeds %+v, want %+v", fake.gotSeeds, want)
	}
	if !body.Available || len(body.Tracks) != 1 {
		t.Fatalf("body %+v", body)
	}
	for _, bad := range []string{`{"seeds":[]}`, `{"seeds":[{"title":"No Artist"}]}`, `nope`} {
		if code := postRadio(t, srv, bad, nil); code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", bad, code)
		}
	}
}

func (f *fakeRecommendations) SimilarArtists(_ context.Context, source, id string) recommend.ArtistResult {
	f.gotSource, f.gotID = source, id
	return f.artists
}

func recommendationServer(t *testing.T, rec Recommendations) *Server {
	t.Helper()
	srv := newTestServer(t)
	srv.deps.Recommend = rec
	return srv
}

func getJSON(t *testing.T, srv *Server, path string, out any) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1"+path, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if out != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
	}
	return rec.Code
}

func TestSimilarArtistsEndpoint(t *testing.T) {
	fake := &fakeRecommendations{artists: recommend.ArtistResult{Available: true, Artists: []core.ExternalArtist{
		{Source: "deezer", ExternalID: "6404", Name: "Justice"},
	}}}
	srv := recommendationServer(t, fake)

	var body recommend.ArtistResult
	if code := getJSON(t, srv, "/recommendations/artists/library/ar-1", &body); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if fake.gotSource != "library" || fake.gotID != "ar-1" {
		t.Fatalf("asked for %s/%s", fake.gotSource, fake.gotID)
	}
	if !body.Available || len(body.Artists) != 1 || body.Artists[0].Name != "Justice" {
		t.Fatalf("body %+v", body)
	}
}

func TestSimilarTracksEndpoint(t *testing.T) {
	fake := &fakeRecommendations{tracks: recommend.TrackResult{Available: true, Tracks: []core.ExternalResult{
		{Source: "deezer", ExternalID: "1", Title: "D.A.N.C.E.", Artist: "Justice", Type: core.EntityTrack},
	}}}
	srv := recommendationServer(t, fake)

	var body recommend.TrackResult
	if code := getJSON(t, srv, "/recommendations/similar-tracks?artist=Daft+Punk&title=One+More+Time", &body); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if fake.gotArtist != "Daft Punk" || fake.gotTitle != "One More Time" {
		t.Fatalf("asked for %q by %q", fake.gotTitle, fake.gotArtist)
	}
	if !body.Available || len(body.Tracks) != 1 || body.Tracks[0].ExternalID != "1" {
		t.Fatalf("body %+v", body)
	}
	if code := getJSON(t, srv, "/recommendations/similar-tracks?artist=Daft+Punk", nil); code != http.StatusBadRequest {
		t.Fatalf("missing title: status %d, want 400", code)
	}
}

func TestSimilarTracksEndpointWithoutModule(t *testing.T) {
	srv := recommendationServer(t, nil)
	var body recommend.TrackResult
	if code := getJSON(t, srv, "/recommendations/similar-tracks?artist=A&title=B", &body); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if body.Available || body.Tracks == nil {
		t.Fatalf("body %+v, want unavailable with an empty list", body)
	}
}

// With no recommendation module the section is simply absent, not an error
// the artist page has to handle.
func TestSimilarArtistsEndpointWithoutModule(t *testing.T) {
	srv := recommendationServer(t, nil)
	var body recommend.ArtistResult
	if code := getJSON(t, srv, "/recommendations/artists/deezer/27", &body); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if body.Available || body.Artists == nil {
		t.Fatalf("body %+v, want unavailable with an empty list", body)
	}
}
