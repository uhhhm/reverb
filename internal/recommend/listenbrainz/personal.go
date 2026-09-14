package listenbrainz

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/uhhhm/reverb/internal/recommend"
)

var _ recommend.PersonalSource = (*Personal)(nil)

// Personal reads ListenBrainz's collaborative-filtering recommendations for
// the connected account. They are public, so no token is sent; the account
// name is read on every call, so connecting or disconnecting applies at once.
type Personal struct {
	src  *Source
	user func(ctx context.Context) (string, error)
}

// Personal returns the personal source for the account user names. It
// shares the Source's request limiter.
func (s *Source) Personal(user func(ctx context.Context) (string, error)) *Personal {
	return &Personal{src: s, user: user}
}

func (*Personal) Name() string { return "listenbrainz" }

// Connected reports whether an account is connected.
func (p *Personal) Connected(ctx context.Context) bool {
	user, err := p.user(ctx)
	return err == nil && user != ""
}

// Recommendations returns up to limit recordings, best first, with names
// read from ListenBrainz's metadata so they can be matched to something
// playable. An account with no recommendations yet returns none.
func (p *Personal) Recommendations(ctx context.Context, limit int) ([]recommend.TrackCandidate, error) {
	user, err := p.user(ctx)
	if err != nil {
		return nil, err
	}
	if user == "" {
		return nil, recommend.ErrNotConfigured
	}
	q := url.Values{}
	q.Set("count", strconv.Itoa(limit))
	var recs struct {
		Payload struct {
			MBIDs []struct {
				MBID string `json:"recording_mbid"`
			} `json:"mbids"`
		} `json:"payload"`
	}
	endpoint := p.src.apiURL + "/1/cf/recommendation/user/" + url.PathEscape(user) + "/recording?" + q.Encode()
	if err := p.src.getJSON(ctx, endpoint, &recs); err != nil {
		return nil, err
	}
	mbids := make([]string, 0, len(recs.Payload.MBIDs))
	for _, r := range recs.Payload.MBIDs {
		if r.MBID != "" {
			mbids = append(mbids, r.MBID)
		}
	}
	if len(mbids) == 0 {
		return []recommend.TrackCandidate{}, nil
	}
	q = url.Values{}
	q.Set("recording_mbids", strings.Join(mbids, ","))
	q.Set("inc", "artist")
	var meta map[string]struct {
		Recording struct {
			Name   string `json:"name"`
			Length int    `json:"length"`
		} `json:"recording"`
		Artist struct {
			Name string `json:"name"`
		} `json:"artist"`
	}
	if err := p.src.getJSON(ctx, p.src.apiURL+"/1/metadata/recording/?"+q.Encode(), &meta); err != nil {
		return nil, err
	}
	out := make([]recommend.TrackCandidate, 0, len(mbids))
	for _, mbid := range mbids {
		m, ok := meta[mbid]
		if !ok || m.Recording.Name == "" || m.Artist.Name == "" {
			continue
		}
		out = append(out, recommend.TrackCandidate{Artist: m.Artist.Name, Title: m.Recording.Name, DurationMs: m.Recording.Length, MBID: mbid})
	}
	return out, nil
}
