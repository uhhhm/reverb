package recommend

import (
	"context"
	"strings"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
	"github.com/uhhhm/reverb/internal/store/db"
)

// LocalQuerier is the persistence seam for offline similarity. The generated
// queries score only tracks with a live backend binding, so every returned
// recommendation is playable without resolving an external stream.
type LocalQuerier interface {
	LocalRecommendationTracks(context.Context, db.LocalRecommendationTracksParams) ([]db.LocalRecommendationTracksRow, error)
	LocalRecommendationArtists(context.Context, db.LocalRecommendationArtistsParams) ([]db.LocalRecommendationArtistsRow, error)
}

// LocalLibrary resolves an artist name to the library adapter's browseable id.
type LocalLibrary interface {
	Search(ctx context.Context, query string, types []core.EntityType) (core.SearchResults, error)
}

type localSimilarity struct {
	q       LocalQuerier
	library func() LocalLibrary
}

// NewLocalSimilarity builds the library-only fallback used when online
// recommendation sources are disabled or unavailable.
func NewLocalSimilarity(q LocalQuerier, library func() LocalLibrary) LocalSimilarity {
	return &localSimilarity{q: q, library: library}
}

func (l *localSimilarity) SimilarLocalTracks(ctx context.Context, seed TrackSeed, limit int) ([]core.ExternalResult, error) {
	rows, err := l.q.LocalRecommendationTracks(ctx, db.LocalRecommendationTracksParams{
		SeedArtist: seed.Artist, SeedTitle: seed.Title, ResultLimit: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]core.ExternalResult, len(rows))
	for i, row := range rows {
		out[i] = core.ExternalResult{
			Source: "library", ExternalID: row.BackendID, CanonicalID: row.ID,
			Title: row.Title, Artist: row.Artist, Album: row.Album,
			DurationMs: int(row.DurationMs), ISRC: row.Isrc, MBID: row.Mbid,
			CoverArtID: row.CoverArtID, Type: core.EntityTrack,
			RecommendationSources: []string{"local"},
			Reason:                &core.RecommendationReason{Kind: core.ReasonSimilar, Artist: seed.Artist, Title: seed.Title},
			Match:                 &core.MatchResult{Status: core.MatchInLibrary, LibraryTrackID: row.BackendID, Method: core.MatchFuzzy, Confidence: 1},
		}
	}
	return out, nil
}

func (l *localSimilarity) SimilarLocalArtists(ctx context.Context, seed ArtistSeed, limit int) ([]core.ExternalArtist, error) {
	rows, err := l.q.LocalRecommendationArtists(ctx, db.LocalRecommendationArtistsParams{SeedArtist: seed.Name, ResultLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	if l.library == nil {
		return []core.ExternalArtist{}, nil
	}
	lib := l.library()
	if lib == nil {
		return []core.ExternalArtist{}, nil
	}
	out := make([]core.ExternalArtist, 0, len(rows))
	for _, row := range rows {
		results, err := lib.Search(ctx, row.Artist, []core.EntityType{core.EntityArtist})
		if err != nil {
			continue
		}
		want := matching.Normalize(row.Artist)
		for _, artist := range results.Artists {
			if matching.Normalize(artist.Name) != want {
				continue
			}
			out = append(out, core.ExternalArtist{
				Source: "library", ExternalID: artist.ID, Name: artist.Name,
				CoverArtID: artist.CoverArtID, RecommendationSources: []string{"local"},
				Reason: &core.RecommendationReason{Kind: core.ReasonFansAlsoLike, Artist: strings.TrimSpace(seed.Name)},
			})
			break
		}
	}
	return out, nil
}
