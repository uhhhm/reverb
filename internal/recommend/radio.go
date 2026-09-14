package recommend

import (
	"context"

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
	MBID   string `json:"mbid,omitempty"`
}

// Radio returns the next tracks for a Radio session. An artist seed is first
// turned into a few of that artist's own tracks, which lead the result; after
// them come tracks similar to every seed, ranked by the taste profile (see
// rankWeights) and then balanced between new and known music by the
// surface's share and Adventurousness. Radio is not a discovery surface, so
// owned tracks stay, but other versions and duplicate recordings are dropped.
// With online recommendations off, only local-library similarity is queried.
func (s *Service) Radio(ctx context.Context, seeds []Seed) TrackResult {
	ctx, ok := s.withMarks(ctx)
	if !ok {
		return TrackResult{Tracks: []core.ExternalResult{}}
	}
	settings := s.settings(ctx)
	if !settings.Online || len(s.tracks) == 0 {
		return s.localRadio(ctx, seeds)
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
		for _, t := range s.artistTracks(ctx, sd.Artist, radioArtistTracks) {
			t.Reason = &core.RecommendationReason{Kind: core.ReasonRadioArtist, Artist: sd.Artist}
			lead = append(lead, t)
			titled = append(titled, Seed{Artist: t.Artist, Title: t.Title})
		}
	}

	profile := s.profile(ctx)
	rest, available := s.fromSeeds(ctx, titled, titled, radioSurface, nil, profile)
	if !available && len(lead) == 0 {
		return s.localRadio(ctx, seeds)
	}
	result := TrackResult{Available: true, Tracks: []core.ExternalResult{}}
	rest = mixNewAndKnown(rest, newShare(radioSurface.newShare, settings.Adventurousness), profile)
	tracks := append(s.withoutMarkedTracks(ctx, lead), rest...)
	if len(tracks) > radioLimit {
		tracks = tracks[:radioLimit]
	}
	result.Tracks = tracks
	if len(result.Tracks) == 0 {
		return s.localRadio(ctx, seeds)
	}
	return result
}

func (s *Service) localRadio(ctx context.Context, seeds []Seed) TrackResult {
	result := TrackResult{Tracks: []core.ExternalResult{}, Offline: true, UpdatedAt: s.now().Unix()}
	if s.local == nil {
		return result
	}
	seen := map[string]bool{}
	for _, seed := range seeds {
		tracks, err := s.local.SimilarLocalTracks(ctx, TrackSeed{Artist: seed.Artist, Title: seed.Title, MBID: seed.MBID}, radioLimit)
		if err != nil {
			continue
		}
		result.Available = true
		for _, track := range tracks {
			key := recordingKey(track.Title, track.Artist)
			if !seen[key] {
				seen[key] = true
				result.Tracks = append(result.Tracks, track)
			}
		}
	}
	if len(result.Tracks) > radioLimit {
		result.Tracks = result.Tracks[:radioLimit]
	}
	result.Tracks = s.withoutMarkedTracks(ctx, result.Tracks)
	return result
}

// artistTracks finds up to n tracks by an artist in the first source that
// has any, as the library copy where one is owned.
func (s *Service) artistTracks(ctx context.Context, artist string, n int) []core.ExternalResult {
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
			if len(out) == n {
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
