package linkresolve

import (
	"net/url"
	"regexp"
	"strings"
)

// spotifyIDRe validates a Spotify ID (base62, at least one char).
var spotifyIDRe = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// ParseSpotifyURL parses Spotify track/album/playlist URLs and URIs.
// Supported forms:
//
//	https://open.spotify.com/track/{id}
//	https://open.spotify.com/album/{id}
//	https://open.spotify.com/playlist/{id}
//
// with or without query params, with or without http(s) scheme,
// and spotify:track:{id} style URIs.
func ParseSpotifyURL(raw string) (kind, id string, ok bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", false
	}
	// URI form: spotify:track:xxx etc.
	if strings.HasPrefix(s, "spotify:") {
		parts := strings.Split(s, ":")
		if len(parts) != 3 {
			return "", "", false
		}
		k := strings.ToLower(parts[1])
		if k != "track" && k != "album" && k != "playlist" {
			return "", "", false
		}
		i := strings.TrimSpace(parts[2])
		// Strip any trailing query-like? Not needed; ID should be pure.
		// But handle if URI contains query? e.g. spotify:track:xxx?si=... not standard.
		if idx := strings.Index(i, "?"); idx >= 0 {
			i = i[:idx]
		}
		if idx := strings.Index(i, "#"); idx >= 0 {
			i = i[:idx]
		}
		i = strings.Trim(i, "/")
		if i == "" || !spotifyIDRe.MatchString(i) {
			return "", "", false
		}
		return k, i, true
	}

	// URL form: normalize scheme.
	tmp := s
	if !strings.Contains(tmp, "://") {
		tmp = "https://" + tmp
	}
	u, err := url.Parse(tmp)
	if err != nil {
		return "", "", false
	}
	host := strings.ToLower(u.Hostname())
	if host != "open.spotify.com" {
		return "", "", false
	}
	// Path expected: /<kind>/<id>
	p := strings.Trim(u.Path, "/")
	if p == "" {
		return "", "", false
	}
	parts := strings.Split(p, "/")
	if len(parts) < 2 {
		return "", "", false
	}
	k := strings.ToLower(parts[0])
	if k != "track" && k != "album" && k != "playlist" {
		return "", "", false
	}
	i := parts[1]
	if i == "" || !spotifyIDRe.MatchString(i) {
		return "", "", false
	}
	return k, i, true
}
