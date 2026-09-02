package linkresolve

import (
	"net/url"
	"regexp"
	"strings"
)

var youtubeIDRe = regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)

// ParseYouTubeURL parses YouTube watch, youtu.be, and playlist URLs.
// Supported:
//
//	https://www.youtube.com/watch?v={id}
//	https://youtu.be/{id}
//	https://www.youtube.com/playlist?list={id}
//	https://music.youtube.com/watch?v={id}
//
// with or without http(s) scheme, with extra query params.
func ParseYouTubeURL(raw string) (kind, id string, ok bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", false
	}
	tmp := s
	if !strings.Contains(tmp, "://") {
		tmp = "https://" + tmp
	}
	u, err := url.Parse(tmp)
	if err != nil {
		return "", "", false
	}
	host := strings.ToLower(u.Hostname())
	// normalize by stripping leading www.
	// Keep original for youtu.be handling.
	path := u.Path
	query := u.Query()

	switch host {
	case "youtu.be", "www.youtu.be":
		// Path is /{id} possibly with trailing slash or extra segment.
		p := strings.Trim(path, "/")
		if p == "" {
			return "", "", false
		}
		// Take first segment before "/" or "?" (query already separated)
		if idx := strings.Index(p, "/"); idx >= 0 {
			p = p[:idx]
		}
		// youtu.be IDs may include ? params already in query, but path is clean.
		// Validate.
		if !youtubeIDRe.MatchString(p) {
			return "", "", false
		}
		return "track", p, true
	}

	// Accept youtube.com and youtube-nocookie.com plus their subdomains. The
	// suffix match is label-bounded so a host like "evilyoutube.com" does not
	// pass as a subdomain.
	isYouTubeHost := host == "youtube.com" ||
		host == "youtube-nocookie.com" ||
		strings.HasSuffix(host, ".youtube.com") ||
		strings.HasSuffix(host, ".youtube-nocookie.com")

	if !isYouTubeHost {
		return "", "", false
	}

	// IDs are case-sensitive; only the path is compared case-insensitively.
	lowerPath := strings.ToLower(path)

	if lowerPath == "/watch" {
		v := query.Get("v")
		if !youtubeIDRe.MatchString(v) {
			return "", "", false
		}
		return "track", v, true
	}
	if lowerPath == "/playlist" {
		// Playlist IDs use the same alphabet as video IDs but vary in length.
		list := query.Get("list")
		if !youtubeIDRe.MatchString(list) {
			return "", "", false
		}
		return "playlist", list, true
	}
	// Optional: handle /shorts/{id} as track
	if strings.HasPrefix(lowerPath, "/shorts/") {
		rest := strings.TrimPrefix(path, "/shorts/")
		// path preserves case for ID; split.
		rest = strings.Trim(rest, "/")
		if idx := strings.Index(rest, "/"); idx >= 0 {
			rest = rest[:idx]
		}
		if rest != "" && youtubeIDRe.MatchString(rest) {
			return "track", rest, true
		}
	}
	// Could also handle /embed/{id}
	if strings.HasPrefix(lowerPath, "/embed/") {
		rest := strings.TrimPrefix(path, "/embed/")
		rest = strings.Trim(rest, "/")
		if idx := strings.Index(rest, "/"); idx >= 0 {
			rest = rest[:idx]
		}
		if rest != "" && youtubeIDRe.MatchString(rest) {
			return "track", rest, true
		}
	}
	return "", "", false
}
