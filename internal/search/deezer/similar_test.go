package deezer_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/uhhhm/reverb/internal/search"
	"github.com/uhhhm/reverb/internal/search/deezer"
)

// testdata/artist_related.json is a recorded response from
// GET https://api.deezer.com/artist/27/related (Daft Punk).
func TestSimilarArtistsFromRecordedFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/artist_related.json")
	if err != nil {
		t.Fatal(err)
	}
	var gotPath, gotLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotLimit = r.URL.Path, r.URL.Query().Get("limit")
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	a := deezer.New().WithBaseURL(srv.URL).WithHTTPClient(srv.Client())
	if err := a.Init(map[string]any{}); err != nil {
		t.Fatal(err)
	}

	var p search.SimilarArtistsProvider = a
	artists, err := p.SimilarArtists(context.Background(), "27", 10)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/artist/27/related" || gotLimit != "10" {
		t.Fatalf("requested %s?limit=%s", gotPath, gotLimit)
	}
	if len(artists) != 3 {
		t.Fatalf("got %d artists, want 3", len(artists))
	}
	first := artists[0]
	if first.Source != "deezer" || first.ExternalID != "6404" || first.Name != "Justice" {
		t.Fatalf("first = %+v", first)
	}
	if first.CoverURL == "" {
		t.Fatal("cover url missing")
	}
}

func TestSimilarArtistsSurfacesInBandError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error": {"type": "DataException", "message": "no data", "code": 800}}`))
	}))
	t.Cleanup(srv.Close)
	a := deezer.New().WithBaseURL(srv.URL).WithHTTPClient(srv.Client())
	_ = a.Init(map[string]any{})
	if _, err := a.SimilarArtists(context.Background(), "0", 10); err == nil {
		t.Fatal("want an error for Deezer's in-band error body")
	}
}
