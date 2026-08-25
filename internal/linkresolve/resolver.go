package linkresolve

import (
	"context"
	"errors"
	"strings"
)

// ResolveResult is the resolved metadata for a pasted URL.
type ResolveResult struct {
	Kind       string `json:"kind"`   // "track" | "album" | "playlist"
	Source     string `json:"source"` // "spotify" | "youtube"
	ExternalID string `json:"externalId"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	CoverUrl   string `json:"coverUrl,omitempty"`
	URL        string `json:"url"`
}

// ErrUnsupportedURL is returned when no parser matches rawURL.
var ErrUnsupportedURL = errors.New("unsupported URL")

// ResolveURL tries Spotify then YouTube. If neither matches it returns ErrUnsupportedURL.
// It does not perform network calls; returned titles are synthetic.
func ResolveURL(ctx context.Context, rawURL string) (*ResolveResult, error) {
	_ = ctx
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return nil, ErrUnsupportedURL
	}
	if kind, id, ok := ParseSpotifyURL(raw); ok {
		title := "Spotify " + kind + " " + id
		artist := "Unknown"
		album := ""
		if kind == "album" {
			title = "Spotify album " + id
		} else if kind == "playlist" {
			title = "Spotify playlist " + id
		}
		return &ResolveResult{
			Kind:       kind,
			Source:     "spotify",
			ExternalID: id,
			Title:      title,
			Artist:     artist,
			Album:      album,
			URL:        raw,
		}, nil
	}
	if kind, id, ok := ParseYouTubeURL(raw); ok {
		title := "YouTube " + kind + " " + id
		return &ResolveResult{
			Kind:       kind,
			Source:     "youtube",
			ExternalID: id,
			Title:      title,
			Artist:     "Unknown",
			Album:      "",
			URL:        raw,
		}, nil
	}
	return nil, ErrUnsupportedURL
}
