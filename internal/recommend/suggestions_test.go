package recommend_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/uhhhm/reverb/internal/recommend"
)

func TestPlaylistSuggestionsExcludePlaylistTracksAndPage(t *testing.T) {
	var cands []recommend.TrackCandidate
	deezer := &trackSource{plainSource: plainSource{name: "deezer"}}
	for i := 1; i <= 12; i++ {
		title := fmt.Sprintf("S%02d", i)
		cands = append(cands, recommend.TrackCandidate{Artist: "Cassius", Title: title})
		deezer.tracks = append(deezer.tracks, deezerTrack(title, title, "Cassius"))
	}
	cands = append(cands[:3:3], append([]recommend.TrackCandidate{{Artist: "Boards", Title: "Beta Song"}}, cands[3:]...)...)
	deezer.tracks = append(deezer.tracks, deezerTrack("beta", "Beta Song", "Boards"))
	sim := seededSimilarity{"Alpha Song": cands}
	svc := newTrackService(sim, libraryMatcher{}, nil, deezer)
	playlist := []recommend.Seed{{Artist: "Aphex", Title: "Alpha Song"}, {Artist: "Boards", Title: "Beta Song"}}
	ctx := context.Background()

	first := svc.PlaylistSuggestions(ctx, playlist, 0)
	if !first.Available || len(first.Tracks) != 10 {
		t.Fatalf("first page %v", titles(first.Tracks))
	}
	for _, tr := range first.Tracks {
		if tr.Title == "Beta Song" {
			t.Fatal("suggested a track already in the playlist")
		}
		if tr.Reason == nil {
			t.Fatalf("%q has no reason", tr.Title)
		}
	}
	second := svc.PlaylistSuggestions(ctx, playlist, 1)
	if len(second.Tracks) != 2 {
		t.Fatalf("second page %v, want the two next best", titles(second.Tracks))
	}
	if wrapped := svc.PlaylistSuggestions(ctx, playlist, 2); !reflect.DeepEqual(titles(wrapped.Tracks), titles(first.Tracks)) {
		t.Fatalf("third page %v, want it to wrap to the first", titles(wrapped.Tracks))
	}

	// Adding a suggestion drops it off the list.
	added := append(playlist, recommend.Seed{Artist: "Cassius", Title: first.Tracks[0].Title})
	for _, tr := range svc.PlaylistSuggestions(ctx, added, 0).Tracks {
		if tr.Title == first.Tracks[0].Title {
			t.Fatal("added suggestion is still suggested")
		}
	}
}

func TestPlaylistSuggestionsEmptyForEmptyPlaylist(t *testing.T) {
	svc := newTrackService(seededSimilarity{}, libraryMatcher{}, nil, &trackSource{plainSource: plainSource{name: "deezer"}})
	if got := svc.PlaylistSuggestions(context.Background(), nil, 0); len(got.Tracks) != 0 || got.Tracks == nil {
		t.Fatalf("got %+v", got)
	}
}
