// Package listenbrainz uploads listens to a user's ListenBrainz account. The
// account is linked by pasting its user token, which is sent only in the
// Authorization header and never appears in an error.
package listenbrainz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/uhhhm/reverb/internal/scrobble"
)

const (
	defaultBase = "https://api.listenbrainz.org"
	// maxListens is ListenBrainz's limit on listens in one submission.
	maxListens = 1000
)

var _ scrobble.TokenProvider = (*Adapter)(nil)

// Adapter implements scrobble.TokenProvider for ListenBrainz.
type Adapter struct {
	baseURL string
	client  *http.Client
}

// New returns an Adapter for the public ListenBrainz service.
func New() *Adapter { return &Adapter{baseURL: defaultBase, client: http.DefaultClient} }

func newTestAdapter(baseURL string, client *http.Client) *Adapter {
	return &Adapter{baseURL: baseURL, client: client}
}

type trackMetadata struct {
	ArtistName     string         `json:"artist_name"`
	TrackName      string         `json:"track_name"`
	ReleaseName    string         `json:"release_name,omitempty"`
	AdditionalInfo map[string]any `json:"additional_info"`
}

type listen struct {
	ListenedAt    int64         `json:"listened_at,omitempty"`
	TrackMetadata trackMetadata `json:"track_metadata"`
}

func metadata(t scrobble.Track) trackMetadata {
	info := map[string]any{"submission_client": "Reverb"}
	if t.DurationMs > 0 {
		info["duration_ms"] = t.DurationMs
	}
	// ListenBrainz maps the full credit to MusicBrainz artists itself, so a
	// composite credit is sent as it is.
	return trackMetadata{ArtistName: t.Artist, TrackName: t.Title, ReleaseName: t.Album, AdditionalInfo: info}
}

// NowPlaying marks a track as playing now.
func (a *Adapter) NowPlaying(ctx context.Context, c scrobble.Creds, t scrobble.Track) error {
	return a.submit(ctx, c.SessionKey, "playing_now", []listen{{TrackMetadata: metadata(t)}})
}

// Scrobble submits plays: one as a single listen, several as an import.
func (a *Adapter) Scrobble(ctx context.Context, c scrobble.Creds, plays []scrobble.ScrobblePlay) (int, error) {
	accepted := 0
	for start := 0; start < len(plays); start += maxListens {
		batch := plays[start:min(start+maxListens, len(plays))]
		listens := make([]listen, len(batch))
		for i, p := range batch {
			listens[i] = listen{ListenedAt: p.PlayedAt, TrackMetadata: metadata(p.Track)}
		}
		kind := "import"
		if len(plays) == 1 {
			kind = "single"
		}
		if err := a.submit(ctx, c.SessionKey, kind, listens); err != nil {
			return accepted, err
		}
		accepted += len(batch)
	}
	return accepted, nil
}

// ValidateToken returns the user name the token belongs to.
func (a *Adapter) ValidateToken(ctx context.Context, token string) (string, error) {
	var out struct {
		Valid    bool   `json:"valid"`
		UserName string `json:"user_name"`
	}
	if err := a.do(ctx, http.MethodGet, "/1/validate-token", token, nil, &out); err != nil {
		return "", err
	}
	if !out.Valid || out.UserName == "" {
		return "", fmt.Errorf("%w: listenbrainz token is not valid", scrobble.ErrAuth)
	}
	return out.UserName, nil
}

func (a *Adapter) submit(ctx context.Context, token, kind string, listens []listen) error {
	body, err := json.Marshal(map[string]any{"listen_type": kind, "payload": listens})
	if err != nil {
		return fmt.Errorf("listenbrainz: encode listens: %w", err)
	}
	return a.do(ctx, http.MethodPost, "/1/submit-listens", token, body, nil)
}

// do sends one request. Errors name only the status: the response body can
// echo the request, and the token must never reach a log.
func (a *Adapter) do(ctx context.Context, method, path, token string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("listenbrainz: build request: %w", err)
	}
	req.Header.Set("Authorization", "Token "+token)
	req.Header.Set("User-Agent", "Reverb/dev (https://github.com/uhhhm/reverb)")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		// A transport error names the URL, which holds no credentials.
		return fmt.Errorf("listenbrainz: request: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: listenbrainz HTTP %d", scrobble.ErrAuth, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("listenbrainz: HTTP %d", resp.StatusCode)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("listenbrainz: decode response: %w", err)
	}
	return nil
}
