package recommend_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
)

// Paths that return a result without passing the read-time filter: a refresh
// that falls back to what it stored, a freshly generated Mix holding a track
// marked after its period began, and a refresh whose marks cannot be read.

func hasArtist(tracks []core.ExternalResult, artist string) bool {
	for _, tr := range tracks {
		if tr.Artist == artist {
			return true
		}
	}
	return false
}

func TestRefreshedShelvesFallingBackHonorCurrentMarks(t *testing.T) {
	l, sim, deezer, related := shelfFixture()
	m := &marks{artists: map[string]bool{}}
	c := &clock{t: wednesday}
	svc := homeService(c, l, sim, libraryMatcher{"owned song": "lib-1"}, []recommend.Option{related, withMarks(m)}, deezer)
	ctx := context.Background()
	first := svc.RefreshShelves(ctx)

	m.artists["squarepusher"] = true
	l.plays = nil // the refresh finds nothing and keeps the last shelves
	c.advance(7 * time.Hour)
	got := svc.RefreshShelves(ctx)
	if got.UpdatedAt != first.UpdatedAt {
		t.Fatalf("updated %d, want the kept shelves from %d", got.UpdatedAt, first.UpdatedAt)
	}
	for _, sh := range got.Shelves {
		if hasArtist(sh.Tracks, "Squarepusher") {
			t.Fatalf("kept %s shelf returned a marked artist's track", sh.Kind)
		}
		for _, a := range sh.Artists {
			if a.Name == "Squarepusher" {
				t.Fatal("kept artists shelf returned a marked artist")
			}
		}
	}

	// The mark was presentation only: the stored shelves still hold it.
	delete(m.artists, "squarepusher")
	if again := svc.Shelves(ctx); !hasArtist(again.Shelves[0].Tracks, "Squarepusher") {
		t.Fatalf("undo did not restore the stored shelf: %+v", again.Shelves[0].Tracks)
	}
}

func TestRefreshedMixHonorsCurrentMarks(t *testing.T) {
	l, taste, sim, deezer := discoverFixture()
	deezer.tracks = append(deezer.tracks, deezerTrack("7", "Zed", "Zulu"))
	personal := recommend.WithPersonalSource(fakePersonal{cands: []recommend.TrackCandidate{{Artist: "Zulu", Title: "Zed"}}})
	m := &marks{artists: map[string]bool{}}
	svc := homeService(&clock{t: wednesday}, l, sim, libraryMatcher{"owned song": "lib-1"},
		[]recommend.Option{recommend.WithTaste(taste), personal, withMarks(m)}, deezer)
	ctx := context.Background()
	if first := svc.RefreshMix(ctx, recommend.MixDiscoverWeekly); !hasArtist(first.Tracks, "Zulu") || !hasArtist(first.Tracks, "Xenon") {
		t.Fatalf("mix %v, want Zulu's personal recommendation and Xenon", titles(first.Tracks))
	}

	// A personal source's list is not filtered while it is generated, so the
	// regenerated Mix returned here holds the marked artist unless the result
	// itself is filtered.
	m.artists["zulu"] = true
	if fresh := svc.RefreshMix(ctx, recommend.MixDiscoverWeekly); len(fresh.Tracks) == 0 || hasArtist(fresh.Tracks, "Zulu") {
		t.Fatalf("regenerated Mix %v, want tracks without the marked artist", titles(fresh.Tracks))
	}

	// A failed regeneration keeps the stored Mix and still honors marks.
	m.artists["xenon"] = true
	l.trackErr = errors.New("database is locked")
	kept := svc.RefreshMix(ctx, recommend.MixDiscoverWeekly)
	if len(kept.Tracks) == 0 || hasArtist(kept.Tracks, "Xenon") || hasArtist(kept.Tracks, "Zulu") {
		t.Fatalf("kept Mix %v, want the stored tracks without either marked artist", titles(kept.Tracks))
	}

	// The marks were presentation only: undo restores from the stored Mix
	// without regenerating it.
	delete(m.artists, "zulu")
	delete(m.artists, "xenon")
	if got := svc.Mix(ctx, recommend.MixDiscoverWeekly); !hasArtist(got.Tracks, "Zulu") || !hasArtist(got.Tracks, "Xenon") {
		t.Fatalf("after undo %v, want Zulu and Xenon back from the stored Mix", titles(got.Tracks))
	}
}

func TestRefreshMixWhoseMarksCannotBeReadReturnsAndStoresNothing(t *testing.T) {
	l, taste, sim, deezer := discoverFixture()
	f := &flakyMarks{marks: marks{artists: map[string]bool{}}}
	svc := homeService(&clock{t: wednesday}, l, sim, libraryMatcher{"owned song": "lib-1"},
		[]recommend.Option{recommend.WithTaste(taste), f.option()}, deezer)
	ctx := context.Background()
	generated := svc.RefreshMix(ctx, recommend.MixDiscoverWeekly)
	if !generated.Available || len(generated.Tracks) == 0 {
		t.Fatalf("mix %+v, want generated", generated)
	}

	f.err = errMarksLocked
	if got := svc.RefreshMix(ctx, recommend.MixDiscoverWeekly); got.Available || len(got.Tracks) != 0 {
		t.Fatalf("refresh %+v, want unavailable with no tracks while marks cannot be read", got)
	}

	// The prior Mix survives the failure exactly as it was stored.
	f.err = nil
	got := svc.Mix(ctx, recommend.MixDiscoverWeekly)
	if got.Period != generated.Period || got.UpdatedAt != generated.UpdatedAt || got.Offline || len(got.Tracks) != len(generated.Tracks) {
		t.Fatalf("after recovery %+v, want the Mix generated before the failure", got)
	}
}

// Undo restores a track from the retained candidates once the sources that
// produced them are down and the cache has gone stale.
func TestUndoRestoresFromRetainedCandidatesWithSourcesDown(t *testing.T) {
	c := &clock{t: wednesday}
	ts := &similarTracks{cands: []recommend.TrackCandidate{
		{Artist: "Stardust", Title: "Music Sounds Better with You"},
		{Artist: "Cassius", Title: "1999"},
	}}
	deezer := &trackSource{plainSource: plainSource{name: "deezer"}, tracks: []core.ExternalResult{
		deezerTrack("2", "Music Sounds Better with You", "Stardust"),
		deezerTrack("3", "1999", "Cassius"),
	}}
	m := &marks{artists: map[string]bool{}, tracks: map[string]bool{}}
	svc := homeService(c, &fakeListening{}, ts, libraryMatcher{}, []recommend.Option{withMarks(m)}, deezer)
	ctx := context.Background()
	if got := svc.SimilarTracks(ctx, "Daft Punk", "One More Time"); len(got.Tracks) != 2 {
		t.Fatalf("warm %+v, want both tracks", got.Tracks)
	}

	ts.cands, ts.err = nil, errors.New("offline")
	c.advance(25 * time.Hour)
	m.artists["stardust"] = true
	m.tracks["deezer:3"] = true
	if got := svc.SimilarTracks(ctx, "Daft Punk", "One More Time"); !got.Offline || len(got.Tracks) != 0 {
		t.Fatalf("got %+v (offline %v), want the stale result with both marked tracks removed", got.Tracks, got.Offline)
	}
	delete(m.tracks, "deezer:3")
	got := svc.SimilarTracks(ctx, "Daft Punk", "One More Time")
	if !got.Offline || len(got.Tracks) != 1 || got.Tracks[0].Artist != "Cassius" {
		t.Fatalf("after undo %+v, want Cassius back from the stale cache", got.Tracks)
	}
}
