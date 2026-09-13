package core

// Reason kinds. The client words each one; Artist and Title name the seed.
const (
	// ReasonPlayed: "Because you played <Title>" — the seed is a track the
	// household has played.
	ReasonPlayed = "played"
	// ReasonSimilar: "Similar to <Title>" — the seed is a track not played.
	ReasonSimilar = "similar"
	// ReasonFansAlsoLike: "Fans of <Artist> also like".
	ReasonFansAlsoLike = "fansAlsoLike"
	// ReasonRadioArtist: "Radio from <Artist>" — one of the seed artist's
	// own tracks, leading a Radio started from that artist.
	ReasonRadioArtist = "radioArtist"
)

// RecommendationReason is the short reason shown with a recommendation.
type RecommendationReason struct {
	Kind   string `json:"kind"`
	Artist string `json:"artist"`
	Title  string `json:"title,omitempty"`
}
