// Package extstream plays a search result that is not in the library, without
// downloading it. Clicking a track and adding it to the library are separate
// actions: this path writes no file, creates no download job, and triggers no
// library scan.
//
// The audio cannot come from the search source itself — Deezer's API exposes
// only a 30s preview and Spotify's full playback requires their proprietary SDK
// — so a track is resolved to a direct YouTube audio URL via yt-dlp, the same
// origin spotDL ultimately downloads from. The URL is then proxied (see
// internal/api) so the browser talks only to Reverb.
package extstream

import (
	"context"
	"fmt"
	neturl "net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store/db"
	"github.com/uhhhm/reverb/internal/trackref"
)

// DefaultTTL is how long a resolved URL is reused. yt-dlp's URLs carry their own
// expiry (typically several hours); this is deliberately well short of that, so
// a cached URL never goes stale mid-playback.
const DefaultTTL = 30 * time.Minute

// DefaultBinary is the yt-dlp executable name when REVERB_YTDLP_PATH is unset.
const DefaultBinary = "yt-dlp"

// resolveTimeout bounds one yt-dlp resolve. It runs on the request path, ahead
// of the first audio byte, so a wedged process must not hang the player.
const resolveTimeout = 45 * time.Second

// storedURLMargin keeps a URL that is about to lapse from being handed out: a
// listener would get audio that dies partway through the track.
const storedURLMargin = 10 * time.Minute

// fallbackURLLifetime is how long a URL carrying no readable expiry is trusted.
// Deliberately short — guessing wrong the other way strands a listener.
const fallbackURLLifetime = 30 * time.Minute

// TrackLookup fetches a durable source track by id. *search.Aggregator fits.
type TrackLookup interface {
	GetTrack(ctx context.Context, source, externalID string) (core.ExternalResult, error)
}

// Store persists what a track resolved to across restarts. *db.Queries fits.
// Optional: a nil Store leaves the in-memory cache as the only one, which is
// correct but forgets everything when the process exits.
type Store interface {
	GetExtstreamResolve(ctx context.Context, arg db.GetExtstreamResolveParams) (db.ExtstreamResolve, error)
	UpsertExtstreamVideoID(ctx context.Context, arg db.UpsertExtstreamVideoIDParams) error
	UpsertExtstreamURL(ctx context.Context, arg db.UpsertExtstreamURLParams) error
	ClearExtstreamURL(ctx context.Context, arg db.ClearExtstreamURLParams) error
}

// Runner streams a process's combined output line-by-line. Abstracted so tests
// need no yt-dlp binary and no network.
type Runner interface {
	Run(ctx context.Context, name string, args []string, onLine func(string)) error
}

type entry struct {
	url       string
	expiresAt time.Time
}

// Service resolves external tracks to direct audio URLs, caching by source+id.
type Service struct {
	lookup      TrackLookup
	runner      Runner
	binary      string
	cookiesFile string
	jsRuntime   string
	ttl         time.Duration
	now         func() time.Time
	store       Store

	mu    sync.Mutex
	cache map[string]entry
	sf    singleflight.Group
}

// Option configures a Service.
type Option func(*Service)

// WithRunner injects a Runner (test seam).
func WithRunner(r Runner) Option { return func(s *Service) { s.runner = r } }

// WithBinary sets the yt-dlp executable path.
func WithBinary(path string) Option {
	return func(s *Service) {
		if path != "" {
			s.binary = path
		}
	}
}

// WithStore persists resolves across restarts.
func WithStore(st Store) Option { return func(s *Service) { s.store = st } }

// WithJSRuntime points yt-dlp at a JavaScript runtime (the bundled deno).
// Without one, YouTube extraction falls back through extra player clients,
// which is both slower and misses formats.
func WithJSRuntime(path string) Option { return func(s *Service) { s.jsRuntime = path } }

// WithCookiesFile points yt-dlp at a cookies.txt, which is what gets past
// YouTube's bot checks on the resolve.
func WithCookiesFile(path string) Option { return func(s *Service) { s.cookiesFile = path } }

// WithTTL overrides how long a resolved URL is cached.
func WithTTL(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.ttl = d
		}
	}
}

// WithClock injects a time source (test seam).
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// NewFromEnv builds the Service the way both composition roots want it: yt-dlp's
// binary from REVERB_YTDLP_PATH (the desktop bundle sets this to its vendored
// copy) and the downloader's own cookies.txt when one has been written. Cookies
// are what get a resolve past YouTube's bot checks, so sharing the file means an
// operator configures them once.
func NewFromEnv(lookup TrackLookup, getenv func(string) string, opts ...Option) *Service {
	base := []Option{
		WithBinary(getenv("REVERB_YTDLP_PATH")),
		WithCookiesFile(existingPath(ytdlpCookiesPath())),
		WithJSRuntime(existingPath(getenv("REVERB_DENO_PATH"))),
	}
	return New(lookup, append(base, opts...)...)
}

// ytdlpCookiesPath is where the yt-dlp downloader adapter writes the operator's
// cookies.txt.
func ytdlpCookiesPath() string {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cfg, "yt-dlp", "cookies.txt")
}

// existingPath returns path if it exists, else "" — an absent cookies file means
// "no cookies configured", not a path to hand yt-dlp so it can reject it.
func existingPath(path string) string {
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func New(lookup TrackLookup, opts ...Option) *Service {
	s := &Service{
		lookup: lookup,
		runner: ExecRunner{},
		binary: DefaultBinary,
		ttl:    DefaultTTL,
		now:    time.Now,
		cache:  map[string]entry{},
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Resolve returns a direct audio URL for one external track.
//
// Concurrent callers for the same track collapse onto a single yt-dlp run:
// resolving is the expensive part (seconds), and a player that retries or a
// second listener would otherwise spawn duplicate processes.
func (s *Service) Resolve(ctx context.Context, source, externalID string) (string, error) {
	return s.ResolveHinted(ctx, source, externalID, "", "")
}

// ResolveHinted is Resolve with the artist and title the caller already knows.
// The search-source lookup exists only to obtain those two strings, so a caller
// holding them (the SPA always does — it rendered the row) skips a network
// round trip on the critical path before the first audio byte. Empty hints fall
// back to the lookup.
func (s *Service) ResolveHinted(ctx context.Context, source, externalID, artist, title string) (string, error) {
	source, externalID = strings.TrimSpace(source), strings.TrimSpace(externalID)
	if source == "" || externalID == "" {
		return "", fmt.Errorf("extstream: source and id are required")
	}
	key := trackref.ExternalCacheKey(source, externalID)

	s.mu.Lock()
	e, ok := s.cache[key]
	s.mu.Unlock()
	if ok && s.now().Before(e.expiresAt) {
		return e.url, nil
	}

	// Detached like the resolver's singleflight: only the first caller's
	// goroutine runs the closure, so its cancellation would otherwise propagate
	// to every collapsed waiter that still has a live request.
	detached := context.WithoutCancel(ctx)
	v, err, _ := s.sf.Do(key, func() (any, error) {
		// A persisted URL outlives this process, and the upstream honours one
		// for hours. Checking the store before spending seconds on yt-dlp is
		// what makes replaying a recent track instant after a restart.
		url, videoID, fresh := s.storedURL(detached, source, externalID)
		if fresh {
			s.memoize(key, url)
			return url, nil
		}

		url, rerr := s.resolveUncached(detached, source, externalID, artist, title, videoID)
		if rerr != nil {
			return "", rerr
		}
		s.memoize(key, url)
		return url, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

func (s *Service) memoize(key, url string) {
	s.mu.Lock()
	s.cache[key] = entry{url: url, expiresAt: s.now().Add(s.ttl)}
	s.mu.Unlock()
}

// storedURL returns a persisted URL that is still valid, plus the video id the
// row holds either way — the id is permanent, so it is worth having even when
// the URL has expired.
func (s *Service) storedURL(ctx context.Context, source, externalID string) (url, videoID string, ok bool) {
	if s.store == nil {
		return "", "", false
	}
	row, err := s.store.GetExtstreamResolve(ctx, db.GetExtstreamResolveParams{Source: source, ExternalID: externalID})
	if err != nil {
		return "", "", false
	}
	// The stored expiry is the upstream's, not this process's cache TTL. A
	// margin keeps a URL that is about to lapse from being handed to a listener
	// who would then stall mid-track.
	if row.Url != "" && s.now().Add(storedURLMargin).Unix() < row.UrlExpiresAt {
		return row.Url, row.VideoID, true
	}
	return "", row.VideoID, false
}

// Invalidate drops any cached URL for a track. Called when a proxied stream is
// rejected upstream, so the next play re-resolves instead of replaying a URL
// that has expired early.
func (s *Service) Invalidate(source, externalID string) {
	s.mu.Lock()
	delete(s.cache, trackref.ExternalCacheKey(source, externalID))
	s.mu.Unlock()
	if s.store != nil {
		// Only the URL goes: the video id is still the right track, and keeping
		// it means the re-resolve skips the search stage.
		_ = s.store.ClearExtstreamURL(context.Background(), db.ClearExtstreamURLParams{
			Source: source, ExternalID: externalID, UpdatedAt: s.now().Unix(),
		})
	}
}

// resolveUncached does the expensive work: find which upstream track this is,
// then get a playable URL for it. knownVideoID skips the first stage — the
// search answer never changes for a given track, so it is stored permanently
// and is worth roughly half the total time.
func (s *Service) resolveUncached(ctx context.Context, source, externalID, artist, title, knownVideoID string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	videoID := strings.TrimSpace(knownVideoID)
	if videoID == "" {
		query, err := s.searchQuery(ctx, source, externalID, artist, title)
		if err != nil {
			return "", err
		}
		if videoID, err = s.searchVideoID(runCtx, query); err != nil {
			return "", err
		}
		s.persistVideoID(ctx, source, externalID, videoID)
	}

	url, err := s.mediaURL(runCtx, videoID)
	if err != nil {
		// A stored id that no longer resolves (video pulled, region-locked)
		// must not wedge the track forever: drop it and search again.
		if knownVideoID != "" {
			return s.resolveUncached(ctx, source, externalID, artist, title, "")
		}
		return "", err
	}
	s.persistURL(ctx, source, externalID, videoID, url)
	return url, nil
}

// searchQuery is the "artist - title" yt-dlp searches for. The lookup at the
// search source exists only to obtain those two strings, so hints skip it.
func (s *Service) searchQuery(ctx context.Context, source, externalID, artist, title string) (string, error) {
	artist, title = strings.TrimSpace(artist), strings.TrimSpace(title)
	if title == "" {
		if s.lookup == nil {
			return "", fmt.Errorf("extstream: no search source configured")
		}
		track, err := s.lookup.GetTrack(ctx, source, externalID)
		if err != nil {
			return "", fmt.Errorf("extstream: looking up %s:%s: %w", source, externalID, err)
		}
		artist, title = strings.TrimSpace(track.Artist), strings.TrimSpace(track.Title)
	}
	if title == "" {
		return "", fmt.Errorf("extstream: %s:%s has no title to search for", source, externalID)
	}
	return strings.TrimSpace(artist + " - " + title), nil
}

// searchVideoID runs only the search. --flat-playlist stops yt-dlp extracting
// the hit's formats, which is the other half of the work and is redundant here.
func (s *Service) searchVideoID(ctx context.Context, query string) (string, error) {
	args := append(s.baseArgs(), "--flat-playlist", "--print", "id", "ytsearch1:"+query)
	lines, lastLine, err := s.run(ctx, args)
	if err != nil {
		return "", fmt.Errorf("extstream: yt-dlp search for %q failed: %w (%s)", query, err, lastLine)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "http") {
			return l, nil
		}
	}
	return "", fmt.Errorf("extstream: no playable source found for %q (%s)", query, lastLine)
}

// mediaURL extracts a direct audio URL for one known upstream track. -g prints
// it instead of downloading; bestaudio keeps the proxy off video bytes.
func (s *Service) mediaURL(ctx context.Context, videoID string) (string, error) {
	args := append(s.baseArgs(), "-f", "bestaudio", "--no-playlist", "-g", watchURL(videoID))
	lines, lastLine, err := s.run(ctx, args)
	if err != nil {
		return "", fmt.Errorf("extstream: yt-dlp resolve for %s failed: %w (%s)", videoID, err, lastLine)
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
			return l, nil
		}
	}
	return "", fmt.Errorf("extstream: no playable source found for %s (%s)", videoID, lastLine)
}

func watchURL(videoID string) string {
	return "https://www.youtube.com/watch?v=" + videoID
}

// baseArgs are the flags every yt-dlp invocation wants.
func (s *Service) baseArgs() []string {
	args := []string{"--no-warnings", "--socket-timeout", "15"}
	if s.jsRuntime != "" {
		args = append(args, "--js-runtimes", "deno:"+s.jsRuntime)
	}
	if s.cookiesFile != "" {
		args = append(args, "--cookies", s.cookiesFile)
	}
	return args
}

func (s *Service) run(ctx context.Context, args []string) (lines []string, lastLine string, err error) {
	rerr := s.runner.Run(ctx, s.binary, args, func(line string) {
		line = strings.TrimSpace(line)
		if line == "" {
			return
		}
		lastLine = line
		lines = append(lines, line)
	})
	return lines, lastLine, rerr
}

func (s *Service) persistVideoID(ctx context.Context, source, externalID, videoID string) {
	if s.store == nil {
		return
	}
	// A cache write that fails only costs a repeat of the search next time.
	_ = s.store.UpsertExtstreamVideoID(ctx, db.UpsertExtstreamVideoIDParams{
		Source: source, ExternalID: externalID, VideoID: videoID, UpdatedAt: s.now().Unix(),
	})
}

func (s *Service) persistURL(ctx context.Context, source, externalID, videoID, url string) {
	if s.store == nil {
		return
	}
	_ = s.store.UpsertExtstreamURL(ctx, db.UpsertExtstreamURLParams{
		Source: source, ExternalID: externalID, VideoID: videoID, Url: url,
		UrlExpiresAt: urlExpiry(url, s.now()).Unix(), UpdatedAt: s.now().Unix(),
	})
}

// urlExpiry reads the expiry the signed URL carries in its `expire` parameter.
// Trusting the URL's own deadline is what lets a stored one be reused for the
// hours it stays valid, rather than for a guess. An unreadable or already-past
// expiry falls back to a conservative window.
func urlExpiry(rawURL string, now time.Time) time.Time {
	u, err := neturl.Parse(rawURL)
	if err == nil {
		if secs, perr := strconv.ParseInt(u.Query().Get("expire"), 10, 64); perr == nil {
			if t := time.Unix(secs, 0); t.After(now) {
				return t
			}
		}
	}
	return now.Add(fallbackURLLifetime)
}
