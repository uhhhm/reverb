package recommend_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/search"
)

// plainSource is a search source with no similarity capability.
type plainSource struct {
	name    string
	artists []core.ExternalResult
}

func (p *plainSource) Type() string                         { return "search" }
func (p *plainSource) Name() string                         { return p.name }
func (p *plainSource) ConfigSchema() registry.ConfigSchema  { return registry.ConfigSchema{} }
func (p *plainSource) Init(map[string]any) error            { return nil }
func (p *plainSource) TestConnection(context.Context) error { return nil }
func (p *plainSource) Search(_ context.Context, _ string, t core.EntityType) ([]core.ExternalResult, error) {
	if t == core.EntityArtist {
		return p.artists, nil
	}
	return nil, nil
}
func (p *plainSource) GetAlbum(context.Context, string) (core.ExternalAlbum, error) {
	return core.ExternalAlbum{}, nil
}

// similarSource relates every artist to a fixed list and counts its calls.
type similarSource struct {
	plainSource
	related []core.ExternalArtist
	err     error
	delay   time.Duration
	calls   atomic.Int32
	lastID  string
}

type artistSimilarity struct {
	name    string
	related []recommend.ArtistCandidate
	err     error
}

func (s artistSimilarity) Name() string { return s.name }
func (s artistSimilarity) SimilarArtists(context.Context, recommend.ArtistSeed, int) ([]recommend.ArtistCandidate, error) {
	return s.related, s.err
}

func (s *similarSource) SimilarArtists(ctx context.Context, id string, limit int) ([]core.ExternalArtist, error) {
	s.calls.Add(1)
	s.lastID = id
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	if len(s.related) > limit {
		return s.related[:limit], nil
	}
	return s.related, nil
}

type library map[string]core.Artist

func (l library) GetArtist(_ context.Context, id string) (core.Artist, error) {
	if a, ok := l[id]; ok {
		return a, nil
	}
	return core.Artist{}, errors.New("no such artist")
}

func relatedN(n int) []core.ExternalArtist {
	out := make([]core.ExternalArtist, n)
	for i := range out {
		out[i] = core.ExternalArtist{Source: "deezer", ExternalID: fmt.Sprint(100 + i), Name: fmt.Sprintf("Artist %d", i)}
	}
	return out
}

func newService(lib library, sources ...search.SearchSource) *recommend.Service {
	return newServiceWithArtistSources(lib, nil, sources...)
}

func newServiceWithArtistSources(lib library, artistSources []recommend.ArtistSimilarity, sources ...search.SearchSource) *recommend.Service {
	options := []recommend.Option{
		recommend.WithLibrary(func() recommend.Library { return lib }),
		recommend.WithTimeout(50 * time.Millisecond),
	}
	for _, source := range artistSources {
		options = append(options, recommend.WithArtistSource(source))
	}
	return recommend.New(
		func() []search.SearchSource { return sources },
		options...,
	)
}

func TestSimilarArtistsForSourceArtist(t *testing.T) {
	deezer := &similarSource{plainSource: plainSource{name: "deezer"}, related: relatedN(25)}
	svc := newService(nil, &plainSource{name: "spotify"}, deezer)

	got := svc.SimilarArtists(context.Background(), "deezer", "27")
	if !got.Available {
		t.Fatal("want available: deezer provides similar artists")
	}
	if len(got.Artists) != 10 {
		t.Fatalf("got %d artists, want the first 10", len(got.Artists))
	}
	if deezer.lastID != "27" || got.Artists[0].Name != "Artist 0" {
		t.Fatalf("asked for %q, first %+v", deezer.lastID, got.Artists[0])
	}
}

func TestSimilarArtistsUnavailableWithoutCapability(t *testing.T) {
	svc := newService(nil, &plainSource{name: "spotify"})
	got := svc.SimilarArtists(context.Background(), "spotify", "x")
	if got.Available || len(got.Artists) != 0 {
		t.Fatalf("got %+v, want unavailable", got)
	}
}

func TestSimilarArtistsForLibraryArtistResolvesByName(t *testing.T) {
	deezer := &similarSource{
		plainSource: plainSource{name: "deezer", artists: []core.ExternalResult{
			{Source: "deezer", ExternalID: "999", Title: "Daft Punk Tribute", Type: core.EntityArtist},
			{Source: "deezer", ExternalID: "27", Title: "Daft Punk", Type: core.EntityArtist},
		}},
		related: relatedN(3),
	}
	svc := newService(library{"ar-1": {ID: "ar-1", Name: "Daft Punk"}}, deezer)

	got := svc.SimilarArtists(context.Background(), "library", "ar-1")
	if len(got.Artists) != 3 || deezer.lastID != "27" {
		t.Fatalf("got %d artists for deezer id %q, want 3 for 27", len(got.Artists), deezer.lastID)
	}
}

func TestSimilarArtistsAreCached(t *testing.T) {
	deezer := &similarSource{plainSource: plainSource{name: "deezer"}, related: relatedN(3)}
	svc := newService(nil, deezer)
	svc.SimilarArtists(context.Background(), "deezer", "27")
	svc.SimilarArtists(context.Background(), "deezer", "27")
	if n := deezer.calls.Load(); n != 1 {
		t.Fatalf("deezer queried %d times, want 1", n)
	}
}

func TestSimilarArtistsFailureAndTimeoutAreEmpty(t *testing.T) {
	failing := &similarSource{plainSource: plainSource{name: "deezer"}, err: errors.New("boom")}
	if got := newService(nil, failing).SimilarArtists(context.Background(), "deezer", "27"); len(got.Artists) != 0 {
		t.Fatalf("failure: got %+v", got)
	}
	slow := &similarSource{plainSource: plainSource{name: "deezer"}, related: relatedN(3), delay: time.Second}
	start := time.Now()
	if got := newService(nil, slow).SimilarArtists(context.Background(), "deezer", "27"); len(got.Artists) != 0 {
		t.Fatalf("timeout: got %+v", got)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("a slow source held the request past its timeout")
	}
}

func TestSimilarArtistsMergeDedicatedAndSearchSources(t *testing.T) {
	deezer := &similarSource{
		plainSource: plainSource{name: "deezer", artists: []core.ExternalResult{
			{Source: "deezer", ExternalID: "seed", Title: "Seed Artist", Type: core.EntityArtist},
			{Source: "deezer", ExternalID: "shared", Title: "Shared Artist", Type: core.EntityArtist},
			{Source: "deezer", ExternalID: "only-lb", Title: "ListenBrainz Only", Type: core.EntityArtist},
		}},
		related: []core.ExternalArtist{
			{Source: "deezer", ExternalID: "deezer-only", Name: "Deezer Only"},
			{Source: "deezer", ExternalID: "shared", Name: "Shared Artist", MBID: "artist-mbid"},
		},
	}
	svc := newServiceWithArtistSources(library{"ar-1": {ID: "ar-1", Name: "Seed Artist"}}, []recommend.ArtistSimilarity{artistSimilarity{name: "listenbrainz", related: []recommend.ArtistCandidate{
		{Name: "Shared Artist", MBID: "artist-mbid"},
		{Name: "ListenBrainz Only", MBID: "lb-only-mbid"},
	}}}, deezer)

	got := svc.SimilarArtists(context.Background(), "library", "ar-1")
	if len(got.Artists) != 3 {
		t.Fatalf("got %d artists, want 3: %+v", len(got.Artists), got.Artists)
	}
	if got.Artists[0].Name != "Shared Artist" || strings.Join(got.Artists[0].RecommendationSources, ",") != "deezer,listenbrainz" {
		t.Fatalf("first artist = %+v, want shared artist with both sources", got.Artists[0])
	}
	if got.Artists[2].Source != "deezer" || got.Artists[2].ExternalID != "only-lb" {
		t.Fatalf("ListenBrainz-only artist was not resolved to a browseable source: %+v", got.Artists[2])
	}
}
