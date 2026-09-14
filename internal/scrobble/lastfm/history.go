package lastfm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/uhhhm/reverb/internal/tastehistory"
)

var _ tastehistory.Fetcher = (*History)(nil)

// History reads a Last.fm user's public listening history. Like Similarity it
// reuses the scrobbling API key with unsigned reads, so it needs neither the
// secret nor the user's session.
type History struct {
	a   *Adapter
	key func() string
}

// NewHistory reads the API key through key on every call.
func NewHistory(a *Adapter, key func() string) *History { return &History{a: a, key: key} }

const recentPage = 200

// TopTracks returns the user's most scrobbled tracks of all time.
func (h *History) TopTracks(ctx context.Context, user string, limit int) ([]tastehistory.Count, error) {
	var out struct {
		TopTracks struct {
			Track oneOrMany[struct {
				Name      string      `json:"name"`
				PlayCount json.Number `json:"playcount"`
				Artist    struct {
					Name string `json:"name"`
				} `json:"artist"`
			}] `json:"track"`
		} `json:"toptracks"`
	}
	if err := h.get(ctx, "user.getTopTracks", url.Values{"user": {user}, "period": {"overall"}, "limit": {strconv.Itoa(limit)}}, &out); err != nil {
		return nil, err
	}
	counts := make([]tastehistory.Count, 0, len(out.TopTracks.Track))
	for _, t := range out.TopTracks.Track {
		n, _ := t.PlayCount.Int64()
		if t.Name != "" && t.Artist.Name != "" && n > 0 {
			counts = append(counts, tastehistory.Count{Artist: t.Artist.Name, Title: t.Name, Plays: int(n)})
		}
	}
	return counts, nil
}

// TopArtists returns the user's most scrobbled artists of all time.
func (h *History) TopArtists(ctx context.Context, user string, limit int) ([]tastehistory.Count, error) {
	var out struct {
		TopArtists struct {
			Artist oneOrMany[struct {
				Name      string      `json:"name"`
				PlayCount json.Number `json:"playcount"`
			}] `json:"artist"`
		} `json:"topartists"`
	}
	if err := h.get(ctx, "user.getTopArtists", url.Values{"user": {user}, "period": {"overall"}, "limit": {strconv.Itoa(limit)}}, &out); err != nil {
		return nil, err
	}
	counts := make([]tastehistory.Count, 0, len(out.TopArtists.Artist))
	for _, a := range out.TopArtists.Artist {
		n, _ := a.PlayCount.Int64()
		if a.Name != "" && n > 0 {
			counts = append(counts, tastehistory.Count{Artist: a.Name, Plays: int(n)})
		}
	}
	return counts, nil
}

// RecentTracks returns up to limit scrobbles dated before the given time,
// newest first. A track playing now has no date and is skipped.
func (h *History) RecentTracks(ctx context.Context, user string, before int64, limit int) ([]tastehistory.Scrobble, error) {
	var scrobbles []tastehistory.Scrobble
	for page := 1; len(scrobbles) < limit; page++ {
		var out struct {
			RecentTracks struct {
				Track oneOrMany[struct {
					Name   string `json:"name"`
					Artist struct {
						Text string `json:"#text"`
					} `json:"artist"`
					Date struct {
						UTS json.Number `json:"uts"`
					} `json:"date"`
				}] `json:"track"`
				Attr struct {
					TotalPages json.Number `json:"totalPages"`
				} `json:"@attr"`
			} `json:"recenttracks"`
		}
		q := url.Values{"user": {user}, "limit": {strconv.Itoa(recentPage)}, "page": {strconv.Itoa(page)}}
		// Last.fm's to is inclusive.
		q.Set("to", strconv.FormatInt(before-1, 10))
		if err := h.get(ctx, "user.getRecentTracks", q, &out); err != nil {
			return nil, err
		}
		for _, t := range out.RecentTracks.Track {
			at, _ := t.Date.UTS.Int64()
			if t.Name != "" && t.Artist.Text != "" && at > 0 && at < before && len(scrobbles) < limit {
				scrobbles = append(scrobbles, tastehistory.Scrobble{Artist: t.Artist.Text, Title: t.Name, At: at})
			}
		}
		total, _ := out.RecentTracks.Attr.TotalPages.Int64()
		if int64(page) >= total || len(out.RecentTracks.Track) == 0 {
			break
		}
	}
	return scrobbles, nil
}

func (h *History) get(ctx context.Context, method string, q url.Values, dst any) error {
	key := ""
	if h.key != nil {
		key = h.key()
	}
	if key == "" {
		return fmt.Errorf("lastfm: %s: no API key configured", method)
	}
	return h.a.getJSON(ctx, method, key, q, dst)
}

// oneOrMany decodes a Last.fm list, which comes back as a bare object when it
// holds a single item.
type oneOrMany[T any] []T

func (o *oneOrMany[T]) UnmarshalJSON(b []byte) error {
	if b = bytes.TrimSpace(b); len(b) > 0 && b[0] == '{' {
		var one T
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*o = []T{one}
		return nil
	}
	var many []T
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*o = many
	return nil
}
