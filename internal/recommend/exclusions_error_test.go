package recommend_test

import (
	"context"
	"errors"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/search"
)

var errMarksLocked = errors.New("database is locked")

// flakyMarks is a Not interested loader whose reads can start failing.
type flakyMarks struct {
	marks
	err error
}

func (f *flakyMarks) option() recommend.Option {
	return recommend.WithExclusions(func(context.Context) (recommend.Exclusions, error) {
		if f.err != nil {
			return nil, f.err
		}
		return &f.marks, nil
	})
}

func markedStardust() *flakyMarks {
	return &flakyMarks{marks: marks{artists: map[string]bool{"stardust": true}, tracks: map[string]bool{}}}
}

func stardustTrackService(f *flakyMarks) *recommend.Service {
	ts := &similarTracks{cands: []recommend.TrackCandidate{
		{Artist: "Stardust", Title: "Music Sounds Better with You"},
		{Artist: "Cassius", Title: "1999"},
	}}
	deezer := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		deezerTrack("2", "Music Sounds Better with You", "Stardust"),
		deezerTrack("3", "1999", "Cassius"),
	}}
	return recommend.New(func() []search.SearchSource { return []search.SearchSource{deezer} },
		recommend.WithTrackSource(ts), f.option())
}

func TestSimilarTracksAreUnavailableWhenMarksCannotBeRead(t *testing.T) {
	f := markedStardust()
	svc := stardustTrackService(f)
	ctx := context.Background()
	if got := svc.SimilarTracks(ctx, "Daft Punk", "One More Time"); len(got.Tracks) != 1 {
		t.Fatalf("got %+v before the failure, want only the unmarked track", got.Tracks)
	}

	// The result is cached now; a failed read must not re-admit Stardust.
	f.err = errMarksLocked
	got := svc.SimilarTracks(ctx, "Daft Punk", "One More Time")
	if got.Available || len(got.Tracks) != 0 {
		t.Fatalf("got %+v (available %v), want unavailable with no tracks", got.Tracks, got.Available)
	}
}

func TestRadioAndSuggestionsAreUnavailableWhenMarksCannotBeRead(t *testing.T) {
	f := markedStardust()
	f.err = errMarksLocked
	svc := stardustTrackService(f)
	ctx := context.Background()
	seeds := []recommend.Seed{{Artist: "Daft Punk", Title: "One More Time"}}

	if got := svc.Radio(ctx, seeds); got.Available || len(got.Tracks) != 0 {
		t.Fatalf("radio %+v (available %v), want unavailable with no tracks", got.Tracks, got.Available)
	}
	if got := svc.PlaylistSuggestions(ctx, seeds, 0); got.Available || len(got.Tracks) != 0 {
		t.Fatalf("suggestions %+v (available %v), want unavailable with no tracks", got.Tracks, got.Available)
	}
}

func TestSimilarArtistsAreUnavailableWhenMarksCannotBeRead(t *testing.T) {
	deezer := &similarSource{plainSource: plainSource{name: "deezer"}, related: relatedN(3)}
	f := &flakyMarks{marks: marks{artists: map[string]bool{"artist 1": true}}}
	svc := recommend.New(func() []search.SearchSource { return []search.SearchSource{deezer} }, f.option())
	ctx := context.Background()
	if got := svc.SimilarArtists(ctx, "deezer", "27"); len(got.Artists) != 2 {
		t.Fatalf("got %d artists before the failure, want 2", len(got.Artists))
	}

	f.err = errMarksLocked
	got := svc.SimilarArtists(ctx, "deezer", "27")
	if got.Available || len(got.Artists) != 0 {
		t.Fatalf("got %+v (available %v), want unavailable with no artists", got.Artists, got.Available)
	}
}

func TestShelvesShowNothingWhenMarksCannotBeRead(t *testing.T) {
	l, sim, deezer, related := shelfFixture()
	f := &flakyMarks{marks: marks{artists: map[string]bool{"squarepusher": true}}}
	svc := homeService(&clock{t: wednesday}, l, sim, libraryMatcher{"owned song": "lib-1"}, []recommend.Option{related, f.option()}, deezer)
	ctx := context.Background()
	first := svc.RefreshShelves(ctx)
	if len(first.Shelves) == 0 {
		t.Fatal("no shelves before the failure")
	}

	f.err = errMarksLocked
	if got := svc.Shelves(ctx); len(got.Shelves) != 0 {
		t.Fatalf("read kinds %v, want no shelves while marks cannot be read", shelfKinds(got))
	}
	if got := svc.RefreshShelves(ctx); len(got.Shelves) != 0 {
		t.Fatalf("refresh kinds %v, want no shelves while marks cannot be read", shelfKinds(got))
	}

	// The failed refresh stored nothing, so the earlier shelves are still there.
	f.err = nil
	if got := svc.Shelves(ctx); len(got.Shelves) != len(first.Shelves) || got.UpdatedAt != first.UpdatedAt {
		t.Fatalf("got %d shelves updated %d, want the %d from before the failure", len(got.Shelves), got.UpdatedAt, len(first.Shelves))
	}
}

func TestDiscoverWeeklyRetriesWhenMarksCannotBeRead(t *testing.T) {
	l, taste, sim, deezer := discoverFixture()
	f := &flakyMarks{marks: marks{artists: map[string]bool{}}}
	svc := homeService(&clock{t: wednesday}, l, sim, libraryMatcher{"owned song": "lib-1"},
		[]recommend.Option{recommend.WithTaste(taste), f.option()}, deezer)
	ctx := context.Background()

	f.err = errMarksLocked
	if got := svc.RefreshMix(ctx, recommend.MixDiscoverWeekly); got.Available || got.Period != "" {
		t.Fatalf("mix %+v, want unavailable with no period so it is retried", got)
	}

	f.err = nil
	if got := svc.RefreshMix(ctx, recommend.MixDiscoverWeekly); !got.Available || len(got.Tracks) == 0 {
		t.Fatalf("mix %+v, want generated once marks can be read", got)
	}
	f.err = errMarksLocked
	if got := svc.Mix(ctx, recommend.MixDiscoverWeekly); got.Available || len(got.Tracks) != 0 {
		t.Fatalf("read %+v, want unavailable with no tracks while marks cannot be read", got)
	}
}

func TestReleaseRadarRetriesWhenMarksCannotBeRead(t *testing.T) {
	f := &flakyMarks{marks: marks{artists: map[string]bool{}}, err: errMarksLocked}
	got := radarService(f.option()).RefreshMix(context.Background(), recommend.MixReleaseRadar)
	if got.Available || got.Period != "" || len(got.Tracks) != 0 {
		t.Fatalf("radar %+v, want unavailable with no period so it is retried", got)
	}
}
