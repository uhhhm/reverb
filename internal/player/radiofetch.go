package player

import (
	"context"
	"encoding/json"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/trackref"
)

// radioLookupTimeout bounds one recommendation lookup, which reaches for
// several online sources.
const radioLookupTimeout = 15 * time.Second

// RadioSource looks up recommendations for a Radio session's seeds.
// *recommend.Service fits.
type RadioSource interface {
	Radio(ctx context.Context, seeds []recommend.Seed) recommend.TrackResult
}

// RadioFrom answers Radio's lookups from a recommendation source, as playable
// queue tracks: the shape every player already understands. Recommendation
// policy stays in recommend. With no source a lookup finds nothing, which ends
// the session once its queue runs out.
func RadioFrom(src RadioSource) RadioFetch {
	return func(ctx context.Context, seeds []recommend.Seed) ([]json.RawMessage, error) {
		if src == nil {
			return nil, nil
		}
		ctx, cancel := context.WithTimeout(ctx, radioLookupTimeout)
		defer cancel()
		result := src.Radio(ctx, seeds)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out := make([]json.RawMessage, 0, len(result.Tracks))
		for _, t := range result.Tracks {
			raw, err := json.Marshal(radioQueueTrack(t))
			if err != nil {
				return nil, err
			}
			out = append(out, raw)
		}
		return out, nil
	}
}

// queueTrack is a track as a player's queue carries it.
type queueTrack struct {
	ID                   string                     `json:"id"`
	Title                string                     `json:"title"`
	Artist               string                     `json:"artist"`
	Album                string                     `json:"album"`
	DurationMS           int                        `json:"durationMs"`
	AlbumID              string                     `json:"albumId"`
	ArtistID             string                     `json:"artistId"`
	CoverArtID           string                     `json:"coverArtId"`
	TrackNumber          int                        `json:"trackNumber"`
	DiscNumber           int                        `json:"discNumber"`
	BitRate              int                        `json:"bitRate"`
	Suffix               string                     `json:"suffix"`
	ContentType          string                     `json:"contentType"`
	RecommendationOrigin core.RecommendationOrigin  `json:"recommendationOrigin"`
	Reason               *core.RecommendationReason `json:"reason"`
	ISRC                 string                     `json:"isrc"`
	MBID                 string                     `json:"mbid"`
	ExternalStream       *externalStream            `json:"externalStream,omitempty"`
}

type externalStream struct {
	Source     string `json:"source"`
	ExternalID string `json:"externalId"`
}

// radioQueueTrack is a recommended track as Radio queues it: a library track
// plays by its own id, any other through its source's stream.
func radioQueueTrack(t core.ExternalResult) queueTrack {
	q := queueTrack{
		ID: t.ExternalID, Title: t.Title, Artist: t.Artist, Album: t.Album, DurationMS: t.DurationMs,
		CoverArtID: t.CoverArtID, RecommendationOrigin: core.RecommendationRadio, Reason: t.Reason,
		ISRC: t.ISRC, MBID: t.MBID,
	}
	if t.Source != "library" {
		q.ID = trackref.EncodeExternalID(t.Source, t.ExternalID)
		q.ExternalStream = &externalStream{Source: t.Source, ExternalID: t.ExternalID}
	}
	return q
}
