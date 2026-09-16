package listenbrainz

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/recommend"
)

func TestSimilarTracksFromRecordedFixtures(t *testing.T) {
	searchBody, err := os.ReadFile("testdata/recording_search.json")
	if err != nil {
		t.Fatal(err)
	}
	similarBody, err := os.ReadFile("testdata/similar_recordings.json")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Path {
		case "/recording-search/json":
			w.Write(searchBody)
		case "/similar-recordings/json":
			w.Write(similarBody)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	src := newTestSource(srv.URL, srv.URL, srv.Client())
	cands, err := src.SimilarTracks(context.Background(), recommend.TrackSeed{Artist: "Daft Punk", Title: "Around the World"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 || cands[0].Title != "D.A.N.C.E." || cands[0].Artist != "Justice" || cands[0].MBID != "d18a1284-d6a1-42d9-b0c1-b247e9f27b80" {
		t.Fatalf("candidates = %+v", cands)
	}
	if len(paths) != 2 || !strings.Contains(paths[0], "query=Daft+Punk+Around+the+World") || !strings.Contains(paths[1], "recording_mbids=7813d6e5-fe21-4cd0-8f8b-82b7c15662ae") {
		t.Fatalf("requests = %v", paths)
	}
}

func TestSimilarTracksUseSeedMBIDWithoutSearch(t *testing.T) {
	body, err := os.ReadFile("testdata/similar_recordings.json")
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/similar-recordings/json" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Write(body)
	}))
	defer srv.Close()

	src := newTestSource(srv.URL, srv.URL, srv.Client())
	_, err = src.SimilarTracks(context.Background(), recommend.TrackSeed{Artist: "Daft Punk", Title: "Around the World", MBID: "7813d6e5-fe21-4cd0-8f8b-82b7c15662ae"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want only the similarity lookup", requests)
	}
}

func TestSimilarArtistsFromRecordedFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/similar_artists.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/similar-artists/json" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Write(body)
	}))
	defer srv.Close()

	src := newTestSource(srv.URL, srv.URL, srv.Client())
	cands, err := src.SimilarArtists(context.Background(), recommend.ArtistSeed{Name: "Daft Punk", MBID: "056e4f3e-d505-4dad-8ec1-d04f521cbb56"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 || cands[0].Name != "Nile Rodgers" || cands[0].MBID != "c6d571dd-c0ae-4ac8-9500-780b1b9b25e5" {
		t.Fatalf("candidates = %+v", cands)
	}
}

func TestSimilarArtistsFallBackToAnExactNameAndSpaceRequests(t *testing.T) {
	artistSearch, err := os.ReadFile("testdata/artist_search.json")
	if err != nil {
		t.Fatal(err)
	}
	similar, err := os.ReadFile("testdata/similar_artists.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/artist/":
			w.Write(artistSearch)
		case "/similar-artists/json":
			w.Write(similar)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	type fakeClock struct {
		sync.Mutex
		n      time.Time
		sleeps []time.Duration
	}
	clock := &fakeClock{n: time.Unix(1_700_000_000, 0)}
	src := newTestSource(srv.URL, srv.URL, srv.Client())
	src.interval = time.Second
	src.now = func() time.Time { clock.Lock(); defer clock.Unlock(); return clock.n }
	src.sleep = func(_ context.Context, delay time.Duration) error {
		clock.Lock()
		defer clock.Unlock()
		clock.sleeps = append(clock.sleeps, delay)
		clock.n = clock.n.Add(delay)
		return nil
	}

	candidates, err := src.SimilarArtists(context.Background(), recommend.ArtistSeed{Name: "Daft Punk"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || len(clock.sleeps) != 1 || clock.sleeps[0] != time.Second {
		t.Fatalf("candidates=%d sleeps=%v, want 2 candidates and one 1s rate-limit wait", len(candidates), clock.sleeps)
	}
}

func TestNewSourceBoundsAStalledRequest(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)

	src := New()
	if src.client == http.DefaultClient || src.client.Timeout <= 0 {
		t.Fatalf("client %+v, want its own client with a timeout", src.client)
	}
	src.labsURL = srv.URL
	src.client.Timeout = 50 * time.Millisecond

	start := time.Now()
	_, err := src.SimilarTracks(context.Background(), recommend.TrackSeed{MBID: "7813d6e5-fe21-4cd0-8f8b-82b7c15662ae"}, 1)
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("error = %v, want a timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("stalled request took %s, want it bounded by the client timeout", elapsed)
	}
}

// A recommendation endpoint is an outside service. However much it sends, the
// client buffers a bounded amount and fails, rather than growing until the
// process is killed.
func TestOversizedResponseIsBoundedAndSurfacesAnError(t *testing.T) {
	chunk := []byte(strings.Repeat("x", 1<<20))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("["))
		for sent := 0; sent < 4*maxResponseBytes; sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	src := newTestSource(srv.URL, srv.URL, srv.Client())
	_, err := src.SimilarTracks(context.Background(), recommend.TrackSeed{MBID: "3fa4b4e0-0000-0000-0000-000000000000", Artist: "Band", Title: "Song"}, 5)
	if err == nil {
		t.Fatal("an unbounded response was accepted")
	}
	if !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("error = %v, want the response size cap to be reported", err)
	}
}
