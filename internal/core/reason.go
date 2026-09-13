package core

// ReasonKind picks how the client words a recommendation's reason.
type ReasonKind string

// Reason kinds. Artist and Title on the reason name the seed.
const (
	// ReasonPlayed: "Because you played <Title>" — the seed is a track the
	// household has played.
	ReasonPlayed ReasonKind = "played"
	// ReasonSimilar: "Similar to <Title>" — the seed is a track not played.
	ReasonSimilar ReasonKind = "similar"
	// ReasonFansAlsoLike: "Fans of <Artist> also like".
	ReasonFansAlsoLike ReasonKind = "fansAlsoLike"
	// ReasonRadioArtist: "Radio from <Artist>" — one of the seed artist's
	// own tracks, leading a Radio started from that artist.
	ReasonRadioArtist ReasonKind = "radioArtist"
)

// RecommendationReason is the short reason shown with a recommendation.
type RecommendationReason struct {
	Kind   ReasonKind `json:"kind"`
	Artist string     `json:"artist"`
	Title  string     `json:"title,omitempty"`
}
