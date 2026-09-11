package lastfm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/uhhhm/reverb/internal/recommend"
)

// testdata/track_similar.json follows the documented track.getSimilar JSON
// response (https://www.last.fm/api/show/track.getSimilar).
func TestSimilarTracksFromFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/track_similar.json")
	if err != nil {
		t.Fatal(err)
	}
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.Write(body)
	}))
	defer srv.Close()

	var src recommend.TrackSimilarity = NewSimilarity(newTestAdapter(srv.URL), func() string { return "key1" })
	cands, err := src.SimilarTracks(context.Background(), recommend.TrackSeed{Artist: "Daft Punk", Title: "One More Time"}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodGet {
		t.Fatalf("method %s: similar tracks is a read and needs no signature", got.Method)
	}
	q := got.URL.Query()
	for k, want := range map[string]string{
		"method": "track.getSimilar", "artist": "Daft Punk", "track": "One More Time",
		"api_key": "key1", "limit": "30", "autocorrect": "1", "format": "json",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	want := []recommend.TrackCandidate{
		{Artist: "Daft Punk", Title: "Around the World", DurationMs: 429000, MBID: "7a2cba5e-8c4d-4b5b-a5a9-4b3f8b5f1b2c"},
		{Artist: "Justice", Title: "D.A.N.C.E.", DurationMs: 242000},
		{Artist: "Stardust", Title: "Music Sounds Better with You"},
	}
	if len(cands) != len(want) {
		t.Fatalf("got %d candidates, want %d", len(cands), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(cands[i], want[i]) {
			t.Errorf("candidate %d = %+v, want %+v", i, cands[i], want[i])
		}
	}
}

func TestSimilarTracksNotConfiguredWithoutKey(t *testing.T) {
	src := NewSimilarity(newTestAdapter("http://127.0.0.1:0"), func() string { return "" })
	if _, err := src.SimilarTracks(context.Background(), recommend.TrackSeed{Artist: "A", Title: "B"}, 10); !errors.Is(err, recommend.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

// Last.fm reports errors in-band, rate limiting (29) included.
func TestSimilarTracksSurfacesInBandError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error": 29, "message": "Rate Limit Exceeded"}`))
	}))
	defer srv.Close()
	src := NewSimilarity(newTestAdapter(srv.URL), func() string { return "key1" })
	if _, err := src.SimilarTracks(context.Background(), recommend.TrackSeed{Artist: "A", Title: "B"}, 10); err == nil {
		t.Fatal("want an error for Last.fm's in-band error body")
	}
}
