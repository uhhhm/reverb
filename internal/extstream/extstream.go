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
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/uhhhm/reverb/internal/core"
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

// TrackLookup fetches a durable source track by id. *search.Aggregator fits.
type TrackLookup interface {
	GetTrack(ctx context.Context, source, externalID string) (core.ExternalResult, error)
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
	ttl         time.Duration
	now         func() time.Time

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
	source, externalID = strings.TrimSpace(source), strings.TrimSpace(externalID)
	if source == "" || externalID == "" {
		return "", fmt.Errorf("extstream: source and id are required")
	}
	key := source + ":" + externalID

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
		url, rerr := s.resolveUncached(detached, source, externalID)
		if rerr != nil {
			return "", rerr
		}
		s.mu.Lock()
		s.cache[key] = entry{url: url, expiresAt: s.now().Add(s.ttl)}
		s.mu.Unlock()
		return url, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

// Invalidate drops any cached URL for a track. Called when a proxied stream is
// rejected upstream, so the next play re-resolves instead of replaying a URL
// that has expired early.
func (s *Service) Invalidate(source, externalID string) {
	s.mu.Lock()
	delete(s.cache, strings.TrimSpace(source)+":"+strings.TrimSpace(externalID))
	s.mu.Unlock()
}

func (s *Service) resolveUncached(ctx context.Context, source, externalID string) (string, error) {
	if s.lookup == nil {
		return "", fmt.Errorf("extstream: no search source configured")
	}
	track, err := s.lookup.GetTrack(ctx, source, externalID)
	if err != nil {
		return "", fmt.Errorf("extstream: looking up %s:%s: %w", source, externalID, err)
	}
	query := strings.TrimSpace(strings.TrimSpace(track.Artist) + " - " + strings.TrimSpace(track.Title))
	if query == "-" || strings.TrimSpace(track.Title) == "" {
		return "", fmt.Errorf("extstream: %s:%s has no title to search for", source, externalID)
	}

	runCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	// -g prints the direct media URL instead of downloading. bestaudio keeps the
	// proxy off video bytes; --no-playlist stops a search hit that belongs to a
	// playlist from expanding into one.
	args := []string{"-f", "bestaudio", "--no-playlist", "-g"}
	if s.cookiesFile != "" {
		args = append(args, "--cookies", s.cookiesFile)
	}
	args = append(args, "ytsearch1:"+query)

	var urls []string
	var lastLine string
	rerr := s.runner.Run(runCtx, s.binary, args, func(line string) {
		line = strings.TrimSpace(line)
		if line == "" {
			return
		}
		lastLine = line
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			urls = append(urls, line)
		}
	})
	if rerr != nil {
		return "", fmt.Errorf("extstream: yt-dlp resolve for %q failed: %w (%s)", query, rerr, lastLine)
	}
	if len(urls) == 0 {
		return "", fmt.Errorf("extstream: no playable source found for %q (%s)", query, lastLine)
	}
	return urls[0], nil
}
