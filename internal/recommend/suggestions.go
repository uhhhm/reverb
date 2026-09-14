package recommend

import (
	"context"

	"github.com/uhhhm/reverb/internal/core"
)

const (
	// suggestionPage is how many suggestions show at once; Refresh asks for
	// the next page.
	suggestionPage = 10
	// suggestionSeeds are spread across the playlist, so a long one is not
	// judged by its first few tracks.
	suggestionSeeds = 5
)

// PlaylistSuggestions suggests songs for a managed playlist, seeded from its
// tracks and ranked by the taste profile. Nothing already in the playlist is
// suggested, so an added suggestion drops off the next list. page picks the
// next best candidates and wraps round when they run out. Owned tracks stay:
// adding one costs no download.
func (s *Service) PlaylistSuggestions(ctx context.Context, playlist []Seed, page int) TrackResult {
	result := TrackResult{Tracks: []core.ExternalResult{}}
	lookup := spreadSeeds(playlist, suggestionSeeds)
	if len(lookup) == 0 {
		return result
	}
	var tracks []core.ExternalResult
	if s.settings(ctx).Online && len(s.tracks) > 0 {
		tracks, result.Available = s.fromSeeds(ctx, lookup, playlist, similarTracksSurface, nil, s.profile(ctx))
	}
	if len(tracks) == 0 {
		local := s.localRadio(ctx, lookup)
		tracks = filterTracks(local.Tracks, playlist, similarTracksSurface, nil)
		result.Available = result.Available || local.Available
		result.Offline, result.UpdatedAt = true, local.UpdatedAt
	}
	tracks = s.withoutMarkedTracks(ctx, tracks)
	if len(tracks) == 0 {
		return result
	}
	pages := (len(tracks) + suggestionPage - 1) / suggestionPage
	from := (max(page, 0) % pages) * suggestionPage
	result.Tracks = tracks[from:min(from+suggestionPage, len(tracks))]
	return result
}

// spreadSeeds picks up to n seeds evenly spaced through the list.
func spreadSeeds(list []Seed, n int) []Seed {
	var out []Seed
	for _, sd := range list {
		if sd.Artist != "" && sd.Title != "" {
			out = append(out, sd)
		}
	}
	if len(out) <= n {
		return out
	}
	spread := make([]Seed, n)
	for i := range spread {
		spread[i] = out[i*len(out)/n]
	}
	return spread
}
