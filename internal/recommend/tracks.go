package recommend

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
	"github.com/uhhhm/reverb/internal/search"
)

// ErrNotConfigured is returned by a source that cannot run without setup (a
// missing API key). The surfaces it feeds are hidden rather than shown empty.
var ErrNotConfigured = errors.New("recommend: source not configured")

// TrackCandidate is a similar track as a similarity source names it: artist
// and title only, not yet something that can be played.
type TrackCandidate struct {
	Artist     string
	Title      string
	DurationMs int
}

// TrackSimilarity is a source of tracks similar to a seed, most similar first.
type TrackSimilarity interface {
	Name() string
	SimilarTracks(ctx context.Context, artist, title string, limit int) ([]TrackCandidate, error)
}

const (
	// similarTrackCandidates is how many candidates are asked for: some never
	// match anything playable, so more are fetched than are shown.
	similarTrackCandidates = 30
	similarTrackLimit      = 20
	// matchWorkers bounds concurrent lookups while matching candidates, so one
	// request cannot flood a search source.
	matchWorkers = 4
)

// TrackResult is the "Similar tracks" list for a seed track. Every entry is
// playable: a library track (Source "library", ExternalID the backend id,
// CanonicalID the catalog id, Match set) or a search-source result that plays
// through external playback. Available is false when no source is configured.
type TrackResult struct {
	Available bool                  `json:"available"`
	Tracks    []core.ExternalResult `json:"tracks"`
}

// SimilarTracks returns playable tracks similar to the seed.
func (s *Service) SimilarTracks(ctx context.Context, artist, title string) TrackResult {
	if s.tracks == nil {
		return TrackResult{Tracks: []core.ExternalResult{}}
	}
	result := TrackResult{Available: true, Tracks: []core.ExternalResult{}}
	key := "tracks\x1f" + matching.Normalize(matching.PrimaryArtist(artist)) + "\x1f" + matching.Normalize(title)
	if cached, ok := s.cache.get(key); ok {
		result.Tracks = s.withoutMarkedTracks(ctx, cached.([]core.ExternalResult))
		return result
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if err := s.trackGate.wait(ctx, s.now, s.sleep); err != nil {
		return result
	}
	cands, err := s.tracks.SimilarTracks(ctx, artist, title, similarTrackCandidates)
	if errors.Is(err, ErrNotConfigured) {
		return TrackResult{Tracks: []core.ExternalResult{}}
	}
	if err != nil {
		// Our own deadline or a caller that went away says nothing about the
		// source, so only a real failure backs off.
		if ctx.Err() == nil {
			s.trackGate.failed(s.now())
		}
		log.Printf("recommend: similar tracks from %s for %q by %q: %v", s.tracks.Name(), title, artist, err)
		return result
	}
	s.trackGate.succeeded()

	seed := TrackCandidate{Artist: artist, Title: title}
	kept := cands[:0:0]
	for _, c := range cands {
		if !sameRecording(seed, c.Title, c.Artist) {
			kept = append(kept, c)
		}
	}
	tracks := filterTracks(s.matchCandidates(ctx, kept), []Seed{{Artist: artist, Title: title}}, similarTracksSurface, nil)
	// A lookup cut short by the deadline keeps what it matched: re-asking the
	// source on every reopen would be worse than a shorter list.
	if ctx.Err() == nil || len(tracks) > 0 {
		s.cache.put(key, tracks)
	}
	result.Tracks = s.withoutMarkedTracks(ctx, tracks)
	return result
}

// matchCandidates turns candidates into playable tracks, keeping the source's
// order and dropping candidates nothing playable matches.
func (s *Service) matchCandidates(ctx context.Context, cands []TrackCandidate) []core.ExternalResult {
	sources := s.liveSources()
	matcher := s.liveMatcher()
	resolved := make([]*core.ExternalResult, len(cands))
	sem := make(chan struct{}, matchWorkers)
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if r, ok := resolveCandidate(ctx, c, sources, matcher); ok {
				resolved[i] = &r
			}
		}()
	}
	wg.Wait()

	out := []core.ExternalResult{}
	seen := map[string]bool{}
	var owned []string
	for _, r := range resolved {
		if r == nil {
			continue
		}
		id := r.Source + "\x00" + r.ExternalID
		if seen[id] {
			continue
		}
		seen[id] = true
		if r.Source == "library" {
			owned = append(owned, r.ExternalID)
		}
		out = append(out, *r)
		if len(out) == similarTrackLimit {
			break
		}
	}
	if len(owned) > 0 {
		s.assignCatalogIDs(ctx, out)
	}
	return out
}

// assignCatalogIDs sets the catalog id on every library track in place.
func (s *Service) assignCatalogIDs(ctx context.Context, tracks []core.ExternalResult) {
	if s.catalogIDs == nil {
		return
	}
	var owned []string
	for _, t := range tracks {
		if t.Source == "library" {
			owned = append(owned, t.ExternalID)
		}
	}
	if len(owned) == 0 {
		return
	}
	ids := s.catalogIDs(ctx, owned)
	for i := range tracks {
		if tracks[i].Source == "library" {
			tracks[i].CanonicalID = ids[tracks[i].ExternalID]
		}
	}
}

// resolveCandidate finds what a candidate plays as: the library copy when one
// is owned, otherwise the first search result that is the same recording.
func resolveCandidate(ctx context.Context, c TrackCandidate, sources []search.SearchSource, matcher Matcher) (core.ExternalResult, bool) {
	probe := core.ExternalResult{Title: c.Title, Artist: c.Artist, DurationMs: c.DurationMs, Type: core.EntityTrack}
	if owned, ok := ownedCopy(ctx, matcher, probe); ok {
		return owned, true
	}
	query := matching.PrimaryArtist(c.Artist) + " " + c.Title
	for _, src := range sources {
		hits, err := src.Search(ctx, query, core.EntityTrack)
		if err != nil {
			continue
		}
		for _, hit := range hits {
			if hit.Type != "" && hit.Type != core.EntityTrack {
				continue
			}
			if !sameRecording(c, hit.Title, hit.Artist) {
				continue
			}
			// The hit carries an album and duration the bare candidate lacked,
			// which can confirm a library copy the first match missed.
			if owned, ok := ownedCopy(ctx, matcher, hit); ok {
				return owned, true
			}
			return hit, true
		}
	}
	return core.ExternalResult{}, false
}

func ownedCopy(ctx context.Context, matcher Matcher, ext core.ExternalResult) (core.ExternalResult, bool) {
	if matcher == nil {
		return core.ExternalResult{}, false
	}
	m, err := matcher.Match(ctx, ext)
	if err != nil || m.Status != core.MatchInLibrary || m.LibraryTrackID == "" {
		return core.ExternalResult{}, false
	}
	return core.ExternalResult{
		Source: "library", ExternalID: m.LibraryTrackID,
		Title: ext.Title, Artist: ext.Artist, Album: ext.Album, DurationMs: ext.DurationMs,
		CoverArtID: m.CoverArtID, Type: core.EntityTrack, Match: &m,
	}, true
}

// sameRecording reports whether a title and artist name the candidate. The
// title must be equal after normalisation, which keeps version qualifiers, so
// a live cut or a remix is a different recording; the artist uses the
// matcher's composite-credit rules.
func sameRecording(c TrackCandidate, title, artist string) bool {
	return matching.Normalize(title) == matching.Normalize(c.Title) && matching.ArtistMatches(artist, c.Artist)
}
