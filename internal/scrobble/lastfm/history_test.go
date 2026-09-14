package lastfm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/uhhhm/reverb/internal/tastehistory"
)

func historyServer(t *testing.T, bodies map[string]func(r *http.Request) string) *History {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "key" || r.URL.Query().Get("user") != "alice" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		body, ok := bodies[r.URL.Query().Get("method")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body(r)))
	}))
	t.Cleanup(srv.Close)
	return NewHistory(&Adapter{baseURL: srv.URL, client: srv.Client()}, func() string { return "key" })
}

func TestHistoryTopTracksAndArtists(t *testing.T) {
	h := historyServer(t, map[string]func(*http.Request) string{
		"user.getTopTracks": func(*http.Request) string {
			return `{"toptracks":{"track":[{"name":"Xtal","playcount":"12","artist":{"name":"Aphex Twin"}},{"name":"","playcount":"3","artist":{"name":"X"}}]}}`
		},
		// A single artist comes back as an object, not a list.
		"user.getTopArtists": func(*http.Request) string {
			return `{"topartists":{"artist":{"name":"Aphex Twin","playcount":"40"}}}`
		},
	})
	tracks, err := h.TopTracks(context.Background(), "alice", 200)
	if err != nil {
		t.Fatal(err)
	}
	if want := []tastehistory.Count{{Artist: "Aphex Twin", Title: "Xtal", Plays: 12}}; !reflect.DeepEqual(tracks, want) {
		t.Fatalf("top tracks = %+v", tracks)
	}
	artists, err := h.TopArtists(context.Background(), "alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	if want := []tastehistory.Count{{Artist: "Aphex Twin", Plays: 40}}; !reflect.DeepEqual(artists, want) {
		t.Fatalf("top artists = %+v", artists)
	}
}

func TestHistoryRecentTracksPagesUntilLimitAndSkipsNowPlaying(t *testing.T) {
	var pages []string
	h := historyServer(t, map[string]func(*http.Request) string{
		"user.getRecentTracks": func(r *http.Request) string {
			if r.URL.Query().Get("to") != "999" {
				t.Errorf("to = %q, want before-1", r.URL.Query().Get("to"))
			}
			pages = append(pages, r.URL.Query().Get("page"))
			if r.URL.Query().Get("page") == "1" {
				return `{"recenttracks":{"track":[
					{"name":"Now","artist":{"#text":"A"},"@attr":{"nowplaying":"true"}},
					{"name":"One","artist":{"#text":"A"},"date":{"uts":"900"}}
				],"@attr":{"page":"1","totalPages":"3"}}}`
			}
			return `{"recenttracks":{"track":[{"name":"Two","artist":{"#text":"B"},"date":{"uts":"800"}},{"name":"Three","artist":{"#text":"C"},"date":{"uts":"700"}}],"@attr":{"page":"2","totalPages":"3"}}}`
		},
	})
	got, err := h.RecentTracks(context.Background(), "alice", 1000, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []tastehistory.Scrobble{{Artist: "A", Title: "One", At: 900}, {Artist: "B", Title: "Two", At: 800}}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(pages, []string{"1", "2"}) {
		t.Fatalf("recent = %+v after pages %v", got, pages)
	}
}

func TestHistoryReportsLastfmErrors(t *testing.T) {
	h := historyServer(t, map[string]func(*http.Request) string{
		"user.getTopTracks": func(*http.Request) string { return `{"error":6,"message":"User not found"}` },
	})
	if _, err := h.TopTracks(context.Background(), "alice", 10); err == nil {
		t.Fatal("want an error for an unknown user")
	}
	noKey := NewHistory(New(), func() string { return "" })
	if _, err := noKey.TopArtists(context.Background(), "alice", 10); err == nil {
		t.Fatal("want an error without an API key")
	}
}
