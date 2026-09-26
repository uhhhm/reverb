package recommend

import (
	"context"
	"strings"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
	"github.com/uhhhm/reverb/internal/store/db"
)

// LocalQuerier is the persistence seam for offline similarity. The queries
// score only tracks with a live backend binding, or the tracks a playable
// library lists, so every returned recommendation is playable without
// resolving an external stream.
type LocalQuerier interface {
	LocalRecommendationTracks(context.Context, db.LocalRecommendationTracksParams) ([]db.LocalRecommendationTracksRow, error)
	LocalRecommendationPlayableTracks(context.Context, db.LocalRecommendationPlayableTracksParams) ([]db.LocalRecommendationPlayableTracksRow, error)
	LocalRecommendationArtists(context.Context, db.LocalRecommendationArtistsParams) ([]db.LocalRecommendationArtistsRow, error)
}

// LocalLibrary resolves an artist name to the library adapter's browseable id.
type LocalLibrary interface {
	Search(ctx context.Context, query string, types []core.EntityType) (core.SearchResults, error)
}

// PlayableLibrary is a library that lists every track it can play.
type PlayableLibrary interface {
	LocalLibrary
	GetSongsBrowse(ctx context.Context, size, offset int) ([]core.Track, error)
}

// playablePageSize is how many tracks one browse page reads.
const playablePageSize = 500

type localSimilarity struct {
	q       LocalQuerier
	library func() LocalLibrary
	// playable, when set, supplies the candidates instead of bound catalog
	// entities.
	playable func() PlayableLibrary
}

// NewLocalSimilarity builds the library-only fallback used when online
// recommendation sources are disabled or unavailable.
func NewLocalSimilarity(q LocalQuerier, library func() LocalLibrary) LocalSimilarity {
	return &localSimilarity{q: q, library: library}
}

// NewPlayableSimilarity is NewLocalSimilarity for a phone (ADR 0003). Its
// offline set is copies of a peer's files, not library membership, so nothing
// binds them to catalog entities; the candidates are instead the tracks its
// library can play, scored on the same signals.
func NewPlayableSimilarity(q LocalQuerier, library func() PlayableLibrary) LocalSimilarity {
	return &localSimilarity{q: q, playable: library, library: func() LocalLibrary {
		if lib := library(); lib != nil {
			return lib
		}
		return nil
	}}
}

func (l *localSimilarity) SimilarLocalTracks(ctx context.Context, seed TrackSeed, limit int) ([]core.ExternalResult, error) {
	if l.playable != nil {
		return l.similarPlayableTracks(ctx, seed, limit)
	}
	rows, err := l.q.LocalRecommendationTracks(ctx, db.LocalRecommendationTracksParams{
		SeedArtist: seed.Artist, SeedTitle: seed.Title, ResultLimit: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]core.ExternalResult, len(rows))
	for i, row := range rows {
		out[i] = localResult(seed, core.ExternalResult{
			ExternalID: row.BackendID, CanonicalID: row.ID,
			Title: row.Title, Artist: row.Artist, Album: row.Album,
			DurationMs: int(row.DurationMs), ISRC: row.Isrc, MBID: row.Mbid,
			CoverArtID: row.CoverArtID,
		})
	}
	return out, nil
}

func (l *localSimilarity) similarPlayableTracks(ctx context.Context, seed TrackSeed, limit int) ([]core.ExternalResult, error) {
	lib := l.playable()
	if lib == nil {
		return []core.ExternalResult{}, nil
	}
	var candidates []db.PlayableTrack
	for offset := 0; ; offset += playablePageSize {
		page, err := lib.GetSongsBrowse(ctx, playablePageSize, offset)
		if err != nil {
			return nil, err
		}
		for _, t := range page {
			candidates = append(candidates, db.PlayableTrack{
				ID: t.ID, Title: t.Title, Artist: t.Artist, Album: t.Album,
				DurationMs: int64(t.DurationMs), CoverArtID: t.CoverArtID,
			})
		}
		if len(page) < playablePageSize {
			break
		}
	}
	rows, err := l.q.LocalRecommendationPlayableTracks(ctx, db.LocalRecommendationPlayableTracksParams{
		Candidates: candidates, SeedArtist: seed.Artist, SeedTitle: seed.Title, ResultLimit: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]core.ExternalResult, len(rows))
	for i, row := range rows {
		out[i] = localResult(seed, core.ExternalResult{
			ExternalID: row.BackendID, CanonicalID: row.CatalogID,
			Title: row.Title, Artist: row.Artist, Album: row.Album,
			DurationMs: int(row.DurationMs), CoverArtID: row.CoverArtID,
		})
	}
	return out, nil
}

// localResult marks a track as the library copy this device plays.
func localResult(seed TrackSeed, r core.ExternalResult) core.ExternalResult {
	r.Source = "library"
	r.Type = core.EntityTrack
	r.RecommendationSources = []string{"local"}
	r.Reason = &core.RecommendationReason{Kind: core.ReasonSimilar, Artist: seed.Artist, Title: seed.Title}
	r.Match = &core.MatchResult{Status: core.MatchInLibrary, LibraryTrackID: r.ExternalID, Method: core.MatchFuzzy, Confidence: 1}
	return r
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
