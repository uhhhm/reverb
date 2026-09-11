package lastfm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
func (s *Similarity) SimilarTracks(ctx context.Context, artist, title string, limit int) ([]recommend.TrackCandidate, error) {
	key := ""
	if s.key != nil {
		key = s.key()
	}
	if key == "" {
		return nil, recommend.ErrNotConfigured
	}
	q := url.Values{}
	q.Set("method", "track.getSimilar")
	// Last.fm knows one canonical artist; a composite library credit would
	// look up an unknown artist and return nothing.
	q.Set("artist", matching.PrimaryArtist(artist))
	q.Set("track", title)
	q.Set("api_key", key)
	q.Set("limit", strconv.Itoa(limit))
	q.Set("autocorrect", "1")
	q.Set("format", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.a.baseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("lastfm: build request: %w", err)
	}
	resp, err := s.a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lastfm: http: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("lastfm: read body: %w", err)
	}

	var out struct {
		SimilarTracks struct {
			Track []struct {
				Name     string      `json:"name"`
				Duration json.Number `json:"duration"`
				Artist   struct {
					Name string `json:"name"`
				} `json:"artist"`
			} `json:"track"`
		} `json:"similartracks"`
		Error   int    `json:"error"`
		Message string `json:"message"`
	}
	// Last.fm reports errors in the body, sometimes under a non-200 status, so
	// the body is read before the status decides anything.
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("lastfm: decode response (HTTP %d): %w", resp.StatusCode, err)
	}
	if out.Error != 0 {
		return nil, lastfmError(out.Error, out.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lastfm: track.getSimilar: HTTP %d", resp.StatusCode)
	}
	cands := make([]recommend.TrackCandidate, 0, len(out.SimilarTracks.Track))
	for _, t := range out.SimilarTracks.Track {
		if t.Name == "" || t.Artist.Name == "" {
			continue
		}
		seconds, _ := t.Duration.Int64()
		cands = append(cands, recommend.TrackCandidate{Artist: t.Artist.Name, Title: t.Name, DurationMs: int(seconds) * 1000})
	}
	return cands, nil
}
