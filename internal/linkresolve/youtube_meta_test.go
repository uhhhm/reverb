package linkresolve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSplitArtistTitle(t *testing.T) {
	cases := []struct{ title, channel, wantArtist, wantTitle string }{
		{"Radiohead - Creep (Official Music Video)", "Radiohead", "Radiohead", "Creep"},
		{"Creep", "Radiohead - Topic", "Radiohead", "Creep"},
		{"Artist – Song [HD]", "Chan", "Artist", "Song"},
		{"Song (feat. Someone)", "Band", "Band", "Song (feat. Someone)"},
		{"Song", "", "Unknown", "Song"},
	}
	for _, c := range cases {
		artist, title := splitArtistTitle(c.title, c.channel)
		if artist != c.wantArtist || title != c.wantTitle {
			t.Errorf("splitArtistTitle(%q, %q) = %q/%q, want %q/%q", c.title, c.channel, artist, title, c.wantArtist, c.wantTitle)
		}
	}
}

func TestResolveURLUsesOembedMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("url") == "" {
			t.Error("oembed called without url param")
		}
		_, _ = w.Write([]byte(`{"title":"Radiohead - Creep (Official Music Video)","author_name":"Radiohead","thumbnail_url":"https://img/x.jpg"}`))
	}))
	defer srv.Close()
	old := oembedEndpoint
	oembedEndpoint = srv.URL
	defer func() { oembedEndpoint = old }()

	res, err := ResolveURL(context.Background(), "https://www.youtube.com/watch?v=abc123")
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "Creep" || res.Artist != "Radiohead" || res.CoverUrl != "https://img/x.jpg" {
		t.Fatalf("got %+v", res)
	}
}

// A failing oEmbed must not fail the resolve — the synthetic fallback stands so
// the download can still be attempted from the URL itself.
func TestResolveURLFallsBackWhenOembedFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	old := oembedEndpoint
	oembedEndpoint = srv.URL
	defer func() { oembedEndpoint = old }()

	res, err := ResolveURL(context.Background(), "https://www.youtube.com/watch?v=abc123")
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "YouTube track abc123" || res.Artist != "Unknown" {
		t.Fatalf("got %+v", res)
	}
}
