package core

// RecommendationOrigin identifies the product surface that supplied a track.
// These values cross HTTP, persistence, and replication boundaries.
type RecommendationOrigin string

const (
	RecommendationRadio          RecommendationOrigin = "radio"
	RecommendationMix            RecommendationOrigin = "mix"
	RecommendationShelf          RecommendationOrigin = "shelf"
	RecommendationSimilarTracks  RecommendationOrigin = "similarTracks"
	RecommendationSimilarArtists RecommendationOrigin = "similarArtists"
)

// Valid reports whether origin names a recommendation surface.
func (origin RecommendationOrigin) Valid() bool {
	switch origin {
	case RecommendationRadio, RecommendationMix, RecommendationShelf, RecommendationSimilarTracks, RecommendationSimilarArtists:
		return true
	default:
		return false
	}
}
