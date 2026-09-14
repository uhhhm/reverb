package recommend

import (
	"context"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
	"github.com/uhhhm/reverb/internal/search"
)

const (
	// An artist is Tracked with at least trackedMinPlays qualified plays in
	// the trackedPlayWindow before the period, or trackedMinLibraryTracks
	// tracks in the library.
	trackedPlayWindow       = 90 * 24 * time.Hour
	trackedMinPlays         = 3
	trackedMinLibraryTracks = 5
	trackedArtistLimit      = 40
	// trackedCandidates bounds the play and library counts read to find them.
	trackedCandidates = 1000

	// releaseWindowDays is how far back a release counts as new.
	releaseWindowDays     = 14
	releaseTracks         = 3
	releaseRadarSize      = 30
	releaseRadarArtistCap = 4
	discographyWorkers    = 4
)

// TrackedArtists derives the Tracked artists from play and library counts:
// anyone with at least trackedMinPlays plays or trackedMinLibraryTracks
// library tracks, played artists first. They are never stored, so they
// follow the inputs. marked artists never qualify.
func TrackedArtists(plays, library []PlayedCount, marked func(artist string) bool) []string {
	var out []string
	seen := map[string]bool{}
	add := func(artist string) {
		key := matching.Normalize(matching.PrimaryArtist(artist))
		if key == "" || seen[key] || marked != nil && marked(artist) || len(out) == trackedArtistLimit {
			return
		}
		seen[key] = true
		out = append(out, artist)
	}
	for _, p := range plays {
		if p.Count >= trackedMinPlays {
			add(p.Artist)
		}
	}
	for _, l := range library {
		if l.Count >= trackedMinLibraryTracks {
			add(l.Artist)
		}
	}
	return out
}

type discographySource struct {
	search.SearchSource
	provider search.DiscographyProvider
}

// release is one new release, found on the source that listed it first.
type release struct {
	album  core.ExternalAlbum
	source discographySource
	rank   int // the Tracked artist's position
}

// releaseRadar collects recent releases by Tracked artists from every source
// with a discography, a few tracks from each. It is empty when there are none.
func (s *Service) releaseRadar(ctx context.Context, start time.Time) Mix {
	m := Mix{Tracks: []core.ExternalResult{}}
	if !s.settings(ctx).Online || s.listening == nil {
		return m
	}
	var sources []discographySource
	for _, src := range s.liveSources() {
		if provider, ok := src.(search.DiscographyProvider); ok {
			sources = append(sources, discographySource{SearchSource: src, provider: provider})
		}
	}
	if len(sources) == 0 {
		return m
	}
	// A failed read leaves the Mix unavailable, so it is retried rather than
	// stored empty until next Friday.
	ctx, ok := s.withMarks(ctx)
	if !ok {
		return m
	}
	ex, _ := s.excluded(ctx)
	plays, err := s.listening.TopArtists(ctx, start.Add(-trackedPlayWindow), start, trackedCandidates)
	if err != nil {
		log.Printf("recommend: reading plays for Tracked artists: %v", err)
		return m
	}
	library, err := s.listening.LibraryArtists(ctx, trackedCandidates)
	if err != nil {
		log.Printf("recommend: reading the library for Tracked artists: %v", err)
		return m
	}
	m.Available = true
	artists := TrackedArtists(plays, library, func(a string) bool { return markedSeed(ex, a, "") })
	since := start.AddDate(0, 0, -releaseWindowDays).Format(periodLayout)
	until := start.Format(periodLayout)
	releases, answered := s.newReleases(ctx, artists, sources, since, until)
	tracks, fetched := s.releaseTracks(ctx, releases)
	// Every lookup failing is an outage, not a quiet week: retry rather than
	// store an empty Mix until next Friday.
	if len(artists) > 0 && !answered || len(releases) > 0 && !fetched {
		m.Available = false
		return m
	}
	kept := make([]core.ExternalResult, 0, len(tracks))
	for _, t := range tracks {
		if !markedSeed(ex, t.Artist, "") {
			kept = append(kept, t)
		}
	}
	m.Tracks = firstN(capPerArtist(s.withoutMarkedTracks(ctx, kept), releaseRadarArtistCap), releaseRadarSize)
	return m
}

// newReleases lists each artist's releases dated in [since, until] across the
// sources, deduplicated by artist and title with the first source winning,
// newest first. answered reports whether any source answered: a discography
// lookup succeeded, or a search found the source has no such artist.
func (s *Service) newReleases(ctx context.Context, artists []string, sources []discographySource, since, until string) ([]release, bool) {
	var answered atomic.Bool
	perArtist := make([][]release, len(artists))
	sem := make(chan struct{}, discographyWorkers)
	var wg sync.WaitGroup
	for i, artist := range artists {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			for _, src := range sources {
				lookupCtx, cancel := context.WithTimeout(ctx, s.timeout)
				id, err := s.resolveArtistID(lookupCtx, ArtistSeed{Name: artist}, src.SearchSource)
				if err == nil && id == "" {
					// The source answered: it just does not have this artist.
					answered.Store(true)
				}
				var albums []core.ExternalAlbum
				if id != "" {
					var err error
					if albums, err = src.provider.GetArtistDiscography(lookupCtx, id); err == nil {
						answered.Store(true)
					}
				}
				cancel()
				for _, al := range albums {
					if al.ReleaseDate < since || al.ReleaseDate > until || len(al.ReleaseDate) != len(periodLayout) {
						continue
					}
					if al.Artist != "" && !matching.ArtistMatches(al.Artist, artist) {
						continue
					}
					if al.Artist == "" {
						al.Artist = artist
					}
					perArtist[i] = append(perArtist[i], release{album: al, source: src, rank: i})
				}
			}
		}()
	}
	wg.Wait()

	var out []release
	seen := map[string]bool{}
	for _, list := range perArtist {
		for _, r := range list {
			key := artistKey(r.album.Artist) + "\x1f" + matching.Normalize(stripReissue(r.album.Name))
			if !seen[key] {
				seen[key] = true
				out = append(out, r)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].album.ReleaseDate != out[j].album.ReleaseDate {
			return out[i].album.ReleaseDate > out[j].album.ReleaseDate
		}
		return out[i].rank < out[j].rank
	})
	return out, answered.Load()
}

// releaseTracks takes the first few tracks of each release, as the library
// copy where one is owned, dropping recordings already taken. fetched
// reports whether any release could be read.
func (s *Service) releaseTracks(ctx context.Context, releases []release) ([]core.ExternalResult, bool) {
	var fetched atomic.Bool
	lists := make([][]core.ExternalResult, len(releases))
	matcher := s.liveMatcher()
	sem := make(chan struct{}, discographyWorkers)
	var wg sync.WaitGroup
	for i, r := range releases {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			lookupCtx, cancel := context.WithTimeout(ctx, s.timeout)
			defer cancel()
			album, err := r.source.GetAlbum(lookupCtx, r.album.ExternalID)
			if err != nil {
				return
			}
			fetched.Store(true)
			reason := &core.RecommendationReason{Kind: core.ReasonNewRelease, Artist: r.album.Artist, Title: r.album.Name}
			for _, t := range firstN(album.Tracks, releaseTracks) {
				t.Type = core.EntityTrack
				if t.CoverURL == "" {
					t.CoverURL = album.CoverURL
				}
				if owned, ok := ownedCopy(lookupCtx, matcher, t); ok {
					t = owned
				}
				t.RecommendationSources = []string{r.source.Name()}
				t.Reason = reason
				lists[i] = append(lists[i], t)
			}
		}()
	}
	wg.Wait()

	var out []core.ExternalResult
	seen := map[string]bool{}
	for _, list := range lists {
		for _, t := range list {
			if key := recordingKey(t.Title, t.Artist); !seen[key] {
				seen[key] = true
				out = append(out, t)
			}
		}
	}
	s.assignCatalogIDs(ctx, out)
	return out, fetched.Load()
}
