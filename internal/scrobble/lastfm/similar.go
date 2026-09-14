package lastfm

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/uhhhm/reverb/internal/matching"
	"github.com/uhhhm/reverb/internal/recommend"
)

var _ recommend.TrackSimilarity = (*Similarity)(nil)

// Similarity is Last.fm's track.getSimilar as a recommendation source. It
// reuses the API key configured for scrobbling; the call is an unsigned read,
// so it needs neither the secret nor a user session.
type Similarity struct {
	a   *Adapter
	key func() string
}

// NewSimilarity reads the API key through key on every call, so a key entered
// in the admin UI takes effect without a restart.
func NewSimilarity(a *Adapter, key func() string) *Similarity {
	return &Similarity{a: a, key: key}
}

func (s *Similarity) Name() string { return "lastfm" }

// SimilarTracks returns tracks Last.fm considers similar, most similar first.
func (s *Similarity) SimilarTracks(ctx context.Context, seed recommend.TrackSeed, limit int) ([]recommend.TrackCandidate, error) {
	key := ""
	if s.key != nil {
		key = s.key()
	}
	if key == "" {
		return nil, recommend.ErrNotConfigured
	}
	q := url.Values{}
	q.Set("method", "track.getSimilar")
	if seed.MBID != "" {
		q.Set("mbid", seed.MBID)
	} else {
		// Last.fm knows one canonical artist; a composite library credit would
		// look up an unknown artist and return nothing.
		q.Set("artist", matching.PrimaryArtist(seed.Artist))
		q.Set("track", seed.Title)
	}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("autocorrect", "1")

	var out struct {
		SimilarTracks struct {
			Track []struct {
				Name     string      `json:"name"`
				MBID     string      `json:"mbid"`
				Duration json.Number `json:"duration"`
				Artist   struct {
					Name string `json:"name"`
				} `json:"artist"`
			} `json:"track"`
		} `json:"similartracks"`
	}
	if err := s.a.getJSON(ctx, "track.getSimilar", key, q, &out); err != nil {
		return nil, err
	}
	cands := make([]recommend.TrackCandidate, 0, len(out.SimilarTracks.Track))
	for _, t := range out.SimilarTracks.Track {
		if t.Name == "" || t.Artist.Name == "" {
			continue
		}
		seconds, _ := t.Duration.Int64()
		cands = append(cands, recommend.TrackCandidate{Artist: t.Artist.Name, Title: t.Name, DurationMs: int(seconds) * 1000, MBID: t.MBID})
	}
	return cands, nil
}
