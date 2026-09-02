package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// contentSecurityPolicy is deliberately permissive where the app genuinely needs
// it and locked down everywhere else:
//   - script-src 'self': the built SPA loads a single same-origin module bundle;
//     there are no inline scripts, so no 'unsafe-inline' is granted to scripts.
//   - style-src 'unsafe-inline': the player's dynamic album-palette theming sets
//     inline style attributes at runtime, which requires this.
//   - img-src https:: album/artist art is frequently served straight from external
//     provider CDNs (e.g. Spotify), so remote https images must be allowed.
//   - connect-src includes ws/wss for the live-progress WebSocket.
//   - frame-ancestors 'none' + X-Frame-Options: DENY: this app is never framed.
const contentSecurityPolicy = "default-src 'self'; " +
	"img-src 'self' data: https:; " +
	"media-src 'self' blob:; " +
	"style-src 'self' 'unsafe-inline'; " +
	"script-src 'self'; " +
	"font-src 'self' data:; " +
	"connect-src 'self' ws: wss:; " +
	"frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'"

// The desktop policy additionally allows the loopback listener as a media
// source: the SPA is served from the wails: scheme, whose custom URI handler
// WebKitGTK's GStreamer media pipeline cannot read, so audio is loaded from
// http://127.0.0.1:<port> instead — see web/src/lib/mediaBase.ts.
const desktopContentSecurityPolicy = "default-src 'self'; " +
	"img-src 'self' data: https:; " +
	"media-src 'self' blob: http://127.0.0.1:* http://localhost:*; " +
	"style-src 'self' 'unsafe-inline'; " +
	"script-src 'self'; " +
	"font-src 'self' data:; " +
	"connect-src 'self' ws: wss: wails: http://localhost:* ws://localhost:*; " +
	"frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'"

// securityHeaders sets defensive response headers on every response (SPA + API).
// These are safe defaults for a same-origin SPA served over a trusted reverse
// proxy plus paired-device sync; see contentSecurityPolicy for the CSP rationale.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		csp := contentSecurityPolicy
		if s.deps.Desktop {
			csp = desktopContentSecurityPolicy
		}
		h.Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

// isStateChanging reports whether the method mutates server state (and therefore
// warrants CSRF scrutiny). Safe/idempotent methods are exempt.
func isStateChanging(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

// csrfGuard rejects state-changing requests whose Origin (or, absent that,
// Referer) names a host other than the one the request targeted.
//
// There is no session cookie, which makes it tempting to conclude CSRF cannot
// apply. It can: the browser UI is authenticated as the household owner purely
// by reaching the loopback listener, and that reachability is itself an ambient
// credential every page in the user's browser holds. Without this check, any
// site the user visits could POST to 127.0.0.1 and be served as the owner. The
// Origin check is the only thing standing in the way. Paired-device /sync calls use Bearer tokens and are
// exempted (see the sync prefix bypass below). A browser always attaches Origin
// to a cross-site POST/PUT/DELETE, so a forged request is caught here.
//
// Requests that carry neither header (curl, native apps, server-to-server) are
// allowed through: those clients cannot be driven by a malicious web page, which
// is the only threat CSRF describes. Dev mode is skipped because the Vite dev
// server issues cross-origin XHRs during local development.
func (s *Server) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.deps.Dev || !isStateChanging(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if isBearerSync(r) {
			next.ServeHTTP(w, r)
			return
		}
		src := r.Header.Get("Origin")
		if src == "" {
			src = r.Header.Get("Referer")
		}
		if src != "" && !sameHost(src, r.Host) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin request blocked"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameHost reports whether rawURL's host (host:port) equals the request Host.
func sameHost(rawURL, host string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

// isBearerSync reports whether r is a paired device calling the sync API with a
// Bearer token rather than a browser riding the ambient loopback credential.
//
// Both browser guards exempt it for the same reason: a web page cannot attach
// an Authorization header to a cross-origin request without a CORS preflight,
// and Reverb sends no CORS headers, so a request in this shape cannot have been
// driven by a hostile page. The path match is exact (or a sub-path) rather than
// a bare prefix so a future route like /api/v1/sync-anything does not inherit
// the exemption by name alone.
func isBearerSync(r *http.Request) bool {
	p := r.URL.Path
	if p != "/api/v1/sync" && !strings.HasPrefix(p, "/api/v1/sync/") {
		return false
	}
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// isLoopbackHost reports whether host (with or without a port, IPv6 bracketed
// or not) names the loopback interface.
func isLoopbackHost(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// desktopWindowHosts are the Host values a request carries when it comes from
// the Wails window rather than the network. The SPA there is served by the same
// api.Server handler through the AssetServer under a custom scheme
// (wails://wails, http://wails.localhost on Windows), so requests it makes to
// /api/v1 arrive in-process with that scheme's host and no real peer address.
// ws.go allows the same two values as WebSocket origins.
var desktopWindowHosts = []string{"wails", "wails.localhost"}

// isDesktopWindowHost reports whether host names the Wails asset server.
func isDesktopWindowHost(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	for _, w := range desktopWindowHosts {
		if strings.EqualFold(h, w) {
			return true
		}
	}
	return false
}

// isDesktopWindowRequest reports whether r came from the desktop window's asset
// server. Only the desktop build serves that origin, so the check is gated on
// Desktop: in server mode "wails.localhost" is a name a browser can resolve to
// loopback, and nothing should treat it as trusted there.
//
// A page in the user's browser cannot exploit this in the desktop build either:
// csrfGuard still requires Origin to match Host, and a cross-site page sends its
// own Origin.
func (s *Server) isDesktopWindowRequest(r *http.Request) bool {
	return s.deps.Desktop && isDesktopWindowHost(r.Host)
}

// hostGuard rejects state-changing requests whose Host header names something
// other than loopback or a host the operator configured.
//
// This is the DNS-rebinding defence, and it is what csrfGuard cannot do alone.
// csrfGuard compares Origin against Host, but under rebinding the attacker
// controls both and makes them agree: a page on evil.example whose DNS record
// flips to 127.0.0.1 sends Origin: http://evil.example and Host: evil.example,
// they match, and the request is served as the household owner. Checking Host
// against a fixed allowlist breaks that, because the one thing the attacker
// cannot do is make the browser send a Host it does not believe it is talking
// to.
//
// The payoff being denied is concrete: POST /api/v1/adapters/test runs the
// binary named by binary_path, so without this guard a visited web page could
// execute a local program.
//
// Reads are exempt for the same reason they are exempt from CSRF, and dev mode
// is skipped because the Vite dev server issues requests under whatever host
// the developer is browsing.
func (s *Server) hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.deps.Dev || !isStateChanging(r.Method) || isBearerSync(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !s.hostAllowed(r.Host) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "host not allowed"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostAllowed reports whether host may be used to reach the API.
func (s *Server) hostAllowed(host string) bool {
	// An empty Host is not reachable from a browser -- Go's HTTP/1.1 server
	// rejects a request without one and HTTP/2 always carries :authority -- but
	// a deny-list check should not treat "absent" as "permitted".
	if host == "" {
		return false
	}
	if isLoopbackHost(host) {
		return true
	}
	// The desktop window reaches the same handler in-process under the Wails
	// scheme, so its Host is "wails" and never loopback. Without this every
	// mutation from the packaged app is rejected.
	if s.deps.Desktop && isDesktopWindowHost(host) {
		return true
	}
	bare := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		bare = h
	}
	bare = strings.Trim(bare, "[]")
	for _, allowed := range s.deps.AllowedHosts {
		allowed = strings.Trim(strings.TrimSpace(allowed), "[]")
		if allowed == "" {
			continue
		}
		// Compare on the bare host: the port a proxy forwards on is its own
		// business and is not knowable when the allowlist is written.
		if a, _, err := net.SplitHostPort(allowed); err == nil {
			allowed = strings.Trim(a, "[]")
		}
		if strings.EqualFold(allowed, bare) {
			return true
		}
	}
	return false
}
