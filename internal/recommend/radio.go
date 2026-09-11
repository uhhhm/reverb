package recommend

import (
	"context"
	"sync"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
)

const (
	// radioSeedLimit bounds how many seeds one Radio request looks up.
	radioSeedLimit = 5
	// radioArtistTracks is how many of an artist's own tracks start a Radio
	// seeded by that artist.
	radioArtistTracks = 3
	radioLimit        = 50
)

// Seed is what a recommendation is asked for: a track, or an artist when
// Title is empty.
type Seed struct {
	Artist string `json:"artist"`
	Title  string `json:"title,omitempty"`
}

// Radio returns the next tracks for a Radio session. An artist seed is first
// turned into a few of that artist's own tracks, which lead the result; after
// them come tracks similar to every seed, interleaved in seed order so no one
// seed crowds out the rest. There is no personal ranking: candidates keep the
// order their sources gave. Radio is not a discovery surface, so owned tracks
// stay, but other versions and duplicate recordings are dropped.
func (s *Service) Radio(ctx context.Context, seeds []Seed) TrackResult {
	if s.tracks == nil {
		return TrackResult{Tracks: []core.ExternalResult{}}
	}
	if len(seeds) > radioSeedLimit {
		seeds = seeds[:radioSeedLimit]
	}

	var lead []core.ExternalResult
	var titled []Seed
	for _, sd := range seeds {
		if sd.Title != "" {
			titled = append(titled, sd)
			continue
		}
		for _, t := range s.artistTracks(ctx, sd.Artist) {
			lead = append(lead, t)
			titled = append(titled, Seed{Artist: t.Artist, Title: t.Title})
		}
	}

	lists := make([]TrackResult, len(titled))
	var wg sync.WaitGroup
	for i, sd := range titled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lists[i] = s.SimilarTracks(ctx, sd.Artist, sd.Title)
		}()
	}
	wg.Wait()

	result := TrackResult{Tracks: []core.ExternalResult{}}
	var merged []core.ExternalResult
	for i := 0; ; i++ {
		more := false
		for _, l := range lists {
			if l.Available {
				result.Available = true
			}
			if i < len(l.Tracks) {
				merged = append(merged, l.Tracks[i])
				more = true
			}
		}
		if !more {
			break
		}
	}
	if !result.Available && len(lead) == 0 {
		return result
	}
	result.Available = true
	tracks := append(s.withoutMarkedTracks(ctx, lead), filterTracks(merged, titled, radioSurface, nil)...)
	if len(tracks) > radioLimit {
		tracks = tracks[:radioLimit]
	}
	result.Tracks = tracks
	return result
}

// artistTracks finds a few tracks by an artist in the first source that has
// any, as the library copy where one is owned.
func (s *Service) artistTracks(ctx context.Context, artist string) []core.ExternalResult {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	matcher := s.liveMatcher()
	for _, src := range s.liveSources() {
		hits, err := src.Search(ctx, artist, core.EntityTrack)
		if err != nil {
			continue
		}
		var out []core.ExternalResult
		seen := map[string]bool{}
		for _, hit := range hits {
			if hit.Type != "" && hit.Type != core.EntityTrack || !matching.ArtistMatches(hit.Artist, artist) {
				continue
			}
			if versionKinds(hit.Title) != 0 {
				continue
			}
			key := recordingKey(hit.Title, hit.Artist)
			if seen[key] {
				continue
			}
			seen[key] = true
			if owned, ok := ownedCopy(ctx, matcher, hit); ok {
				hit = owned
			}
			out = append(out, hit)
			if len(out) == radioArtistTracks {
				break
			}
		}
		if len(out) > 0 {
			s.assignCatalogIDs(ctx, out)
			return out
		}
	}
	return nil
}
