package listenbrainz

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/recommend"
)

func TestPersonalRecommendationsFromRecordedFixtures(t *testing.T) {
	recsBody, err := os.ReadFile("testdata/cf_recommendations.json")
	if err != nil {
		t.Fatal(err)
	}
	metaBody, err := os.ReadFile("testdata/recording_metadata.json")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		if r.Header.Get("Authorization") != "" {
			t.Errorf("personal recommendations sent credentials")
		}
		switch r.URL.Path {
		case "/1/cf/recommendation/user/lb user/recording":
			w.Write(recsBody)
		case "/1/metadata/recording/":
			w.Write(metaBody)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	src := newTestSource(srv.URL, srv.URL, srv.Client())
	p := src.Personal(func(context.Context) (string, error) { return "lb user", nil })
	cands, err := p.Recommendations(context.Background(), 25)
	if err != nil {
		t.Fatal(err)
	}
	// The third recommendation has no metadata and is dropped; order is kept.
	if len(cands) != 2 || cands[0].Title != "D.A.N.C.E." || cands[0].Artist != "Justice" || cands[0].DurationMs != 242000 ||
		cands[0].MBID != "d18a1284-d6a1-42d9-b0c1-b247e9f27b80" || cands[1].Title != "Genesis" {
		t.Fatalf("candidates = %+v", cands)
	}
	if len(paths) != 2 || !strings.Contains(paths[0], "count=25") || !strings.Contains(paths[1], "inc=artist") {
		t.Fatalf("requests = %v", paths)
	}
}

func TestPersonalRecommendationsWithoutAccountAreNotConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	}))
	defer srv.Close()
	src := newTestSource(srv.URL, srv.URL, srv.Client())
	p := src.Personal(func(context.Context) (string, error) { return "", nil })

	if _, err := p.Recommendations(context.Background(), 25); !errors.Is(err, recommend.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

func TestPersonalRecommendationsNotYetGenerated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ListenBrainz answers 204 until an account has recommendations.
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	src := newTestSource(srv.URL, srv.URL, srv.Client())
	p := src.Personal(func(context.Context) (string, error) { return "new", nil })

	cands, err := p.Recommendations(context.Background(), 25)
	if err != nil || len(cands) != 0 {
		t.Fatalf("Recommendations = %+v, %v", cands, err)
	}
}
