package linkresolve

import (
	"net"
	"net/url"
	"strings"
)

// allowedSourceHosts are the registrable domains a user-supplied media URL may
// name. yt-dlp and spotDL fetch whatever URL they are handed, so an unchecked
// URL turns a download into a server-side request to an arbitrary host --
// cloud metadata endpoints, services on the loopback interface, or other hosts
// on the deployment's private network.
var allowedSourceHosts = []string{
	"youtube.com",
	"youtube-nocookie.com",
	"youtu.be",
	"spotify.com",
	"soundcloud.com",
	"bandcamp.com",
}

// IsAllowedSourceURL reports whether raw is a media URL safe to hand to an
// external downloader. It requires an http(s) URL naming an allowlisted host by
// name; IP literals are always refused, since an allowlist cannot vouch for one.
//
// A bare "youtube.com/watch?v=x" is accepted the same way ParseYouTubeURL
// accepts it: a missing scheme is read as https rather than rejected.
func IsAllowedSourceURL(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" {
		return false
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || net.ParseIP(host) != nil {
		return false
	}
	for _, allowed := range allowedSourceHosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}
