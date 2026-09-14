package recommend_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
)

func TestTrackedArtistsThresholds(t *testing.T) {
	plays := []recommend.PlayedCount{{Artist: "Justice", Count: 3}, {Artist: "Air", Count: 2}}
	library := []recommend.PlayedCount{{Artist: "Daft Punk", Count: 5}, {Artist: "Moby", Count: 4}, {Artist: "justice", Count: 9}}

	got := recommend.TrackedArtists(plays, library, nil)
	if want := []string{"Justice", "Daft Punk"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tracked %v, want %v (3 plays or 5 library tracks, played first, once each)", got, want)
	}
	got = recommend.TrackedArtists(plays, library, func(a string) bool { return a == "Justice" || a == "justice" })
	if want := []string{"Daft Punk"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tracked %v with Justice marked, want %v", got, want)
	}
}

// radarSource lists fixed discographies; each album holds four tracks.
type radarSource struct {
	plainSource
	discographies map[string][]core.ExternalAlbum
	// err fails every discography lookup and albumErr every album read, as
	// an outage would.
	err, albumErr error
}

func (s *radarSource) Search(_ context.Context, q string, t core.EntityType) ([]core.ExternalResult, error) {
	if t != core.EntityArtist {
		return nil, nil
	}
	if _, ok := s.discographies[q]; !ok {
		return nil, nil
	}
	return []core.ExternalResult{{Source: s.name, ExternalID: q, Title: q, Type: core.EntityArtist}}, nil
}

func (s *radarSource) GetArtistDiscography(_ context.Context, id string) ([]core.ExternalAlbum, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.discographies[id], nil
}

func (s *radarSource) GetAlbum(_ context.Context, id string) (core.ExternalAlbum, error) {
	if s.albumErr != nil {
		return core.ExternalAlbum{}, s.albumErr
	}
	for _, albums := range s.discographies {
		for _, al := range albums {
			if al.ExternalID == id {
				for i := 1; i <= 4; i++ {
					al.Tracks = append(al.Tracks, core.ExternalResult{
						Source: s.name, ExternalID: fmt.Sprintf("%s-%d", id, i),
						Title: fmt.Sprintf("%s Cut %d", al.Name, i), Artist: al.Artist, Type: core.EntityTrack,
					})
				}
				return al, nil
			}
		}
	}
	return core.ExternalAlbum{}, fmt.Errorf("no album %s", id)
}

func album(source, name, artist, date string) core.ExternalAlbum {
	return core.ExternalAlbum{Source: source, ExternalID: source + ":" + strings.ToLower(name), Name: name, Artist: artist, ReleaseDate: date}
}

// Saturday 12 September: Release Radar's period began Friday 11 September,
// and releases from 28 August onwards are new.
var saturday = time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)

func radarService(extra ...recommend.Option) *recommend.Service {
	l := &fakeListening{plays: append(
		playsOf("Justice", "Genesis", 3, saturday.Add(-10*24*time.Hour)),
		playsOf("Air", "Sexy Boy", 2, saturday.Add(-10*24*time.Hour))...)}
	for i := range 5 {
		l.library = append(l.library, libraryTrack(fmt.Sprint("dp", i), fmt.Sprint("DP ", i), "Daft Punk"))
	}
	for i := range 4 {
		l.library = append(l.library, libraryTrack(fmt.Sprint("mo", i), fmt.Sprint("Moby ", i), "Moby"))
	}
	deezer := &radarSource{plainSource: plainSource{name: "deezer"}, discographies: map[string][]core.ExternalAlbum{
		"Justice":   {album("deezer", "Hyperdrama", "Justice", "2026-09-05"), album("deezer", "Old", "Justice", "2025-01-01")},
		"Daft Punk": {album("deezer", "New DP", "Daft Punk", "2026-09-01")},
		"Air":       {album("deezer", "Air New", "Air", "2026-09-05")},
		"Moby":      {album("deezer", "Moby New", "Moby", "2026-09-05")},
	}}
	spotify := &radarSource{plainSource: plainSource{name: "spotify"}, discographies: map[string][]core.ExternalAlbum{
		"Justice": {
			album("spotify", "Hyperdrama", "Justice", "2026-09-05"),
			album("spotify", "Justice Single", "Justice", "2026-09-11"),
			album("spotify", "Too New", "Justice", "2026-09-12"),
		},
	}}
	return homeService(&clock{t: saturday}, l, nil, libraryMatcher{}, extra, deezer, spotify)
}

func TestReleaseRadarCollectsNewReleasesByTrackedArtists(t *testing.T) {
	got := radarService().RefreshMix(context.Background(), recommend.MixReleaseRadar)

	want := []string{
		"Justice Single Cut 1", "Justice Single Cut 2", "Justice Single Cut 3", "Hyperdrama Cut 1",
		"New DP Cut 1", "New DP Cut 2", "New DP Cut 3",
	}
	if !got.Available || got.Period != "2026-09-11" || !reflect.DeepEqual(titles(got.Tracks), want) {
		t.Fatalf("radar %s %v, want %v", got.Period, titles(got.Tracks), want)
	}
	// Hyperdrama is on both sources and is taken once, from the first.
	for _, tr := range got.Tracks {
		if strings.HasPrefix(tr.Title, "Hyperdrama") && tr.Source != "deezer" {
			t.Fatalf("Hyperdrama came from %s, want deezer", tr.Source)
		}
		if tr.Reason == nil || tr.Reason.Kind != core.ReasonNewRelease {
			t.Fatalf("%q has reason %+v", tr.Title, tr.Reason)
		}
	}
}

func TestReleaseRadarNeverShowsNotInterestedArtists(t *testing.T) {
	m := &marks{artists: map[string]bool{"daft punk": true}}
	got := radarService(withMarks(m)).RefreshMix(context.Background(), recommend.MixReleaseRadar)
	for _, tr := range got.Tracks {
		if tr.Artist == "Daft Punk" {
			t.Fatalf("marked artist's release %q", tr.Title)
		}
	}
	if len(got.Tracks) == 0 {
		t.Fatal("no releases left for the unmarked artist")
	}
}

func TestReleaseRadarRetriesAfterAFailedRead(t *testing.T) {
	l := &fakeListening{artistErr: errors.New("database is locked")}
	deezer := &radarSource{plainSource: plainSource{name: "deezer"}}
	got := homeService(&clock{t: saturday}, l, nil, libraryMatcher{}, nil, deezer).RefreshMix(context.Background(), recommend.MixReleaseRadar)
	// No period is recorded, so the next check generates it again.
	if got.Available || got.Period != "" {
		t.Fatalf("radar %+v, want unavailable with no period after a failed read", got)
	}
}

func TestReleaseRadarRetriesWhenNoSourceAnswers(t *testing.T) {
	l := &fakeListening{plays: playsOf("Justice", "Genesis", 3, saturday.Add(-10*24*time.Hour))}
	deezer := &radarSource{plainSource: plainSource{name: "deezer"}, err: errors.New("503"), discographies: map[string][]core.ExternalAlbum{"Justice": nil}}
	got := homeService(&clock{t: saturday}, l, nil, libraryMatcher{}, nil, deezer).RefreshMix(context.Background(), recommend.MixReleaseRadar)
	if got.Available || got.Period != "" {
		t.Fatalf("radar %+v, want an outage retried rather than stored empty", got)
	}
}

func TestReleaseRadarRetriesWhenNoReleaseCanBeRead(t *testing.T) {
	l := &fakeListening{plays: playsOf("Justice", "Genesis", 3, saturday.Add(-10*24*time.Hour))}
	deezer := &radarSource{plainSource: plainSource{name: "deezer"}, albumErr: errors.New("503"), discographies: map[string][]core.ExternalAlbum{
		"Justice": {album("deezer", "Hyperdrama", "Justice", "2026-09-05")},
	}}
	got := homeService(&clock{t: saturday}, l, nil, libraryMatcher{}, nil, deezer).RefreshMix(context.Background(), recommend.MixReleaseRadar)
	if got.Available || got.Period != "" {
		t.Fatalf("radar %+v, want unreadable releases retried rather than stored empty", got)
	}
}

func TestReleaseRadarStoresAQuietWeekWhenArtistsAreNotOnTheSources(t *testing.T) {
	l := &fakeListening{plays: playsOf("Local Band", "Demo", 3, saturday.Add(-10*24*time.Hour))}
	deezer := &radarSource{plainSource: plainSource{name: "deezer"}, discographies: map[string][]core.ExternalAlbum{}}
	got := homeService(&clock{t: saturday}, l, nil, libraryMatcher{}, nil, deezer).RefreshMix(context.Background(), recommend.MixReleaseRadar)
	if !got.Available || got.Period != "2026-09-11" || len(got.Tracks) != 0 {
		t.Fatalf("radar %+v, want an available empty Mix stored for the week", got)
	}
}

func TestReleaseRadarIsEmptyWithoutNewReleases(t *testing.T) {
	// Justice is Tracked (three plays before Friday) but released nothing new.
	l := &fakeListening{plays: playsOf("Justice", "Genesis", 3, saturday.Add(-10*24*time.Hour))}
	deezer := &radarSource{plainSource: plainSource{name: "deezer"}, discographies: map[string][]core.ExternalAlbum{
		"Justice": {album("deezer", "Old", "Justice", "2025-01-01")},
	}}
	got := homeService(&clock{t: saturday}, l, nil, libraryMatcher{}, nil, deezer).RefreshMix(context.Background(), recommend.MixReleaseRadar)
	if !got.Available || len(got.Tracks) != 0 {
		t.Fatalf("radar %+v, want available and empty", got)
	}
}
