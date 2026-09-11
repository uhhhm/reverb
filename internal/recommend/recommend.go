// Package recommend gathers recommendation candidates from the configured
// sources and hands them to the surfaces that show them.
//
// Each device generates its own recommendations (ADR 0001): results are cached
// here in memory and never written to the change log. A source that fails or
// is slow yields an empty result rather than an error, so a recommendation
// section can disappear without taking the page around it down.
package recommend

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/search"
)

const (
	defaultTimeout = 8 * time.Second
	// cacheTTL bounds how stale a cached result can get. Similarity data moves
	// slowly, and reopening a page must not re-query the source.
	cacheTTL = 24 * time.Hour
)

// Library names a library artist so it can be found in a search source.
// library.LibraryAdapter satisfies it.
type Library interface {
	GetArtist(ctx context.Context, id string) (core.Artist, error)
}

// Option configures a Service.
type Option func(*Service)

// WithLibrary supplies the live library adapter, read per request so it
// survives adapter reloads. Without it, library artists get no results.
func WithLibrary(lib func() Library) Option { return func(s *Service) { s.library = lib } }

// WithTimeout bounds one lookup against the sources.
func WithTimeout(d time.Duration) Option { return func(s *Service) { s.timeout = d } }

// WithClock replaces time.Now (test seam).
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithSleep replaces the wait between rate-limited source calls (test seam).
func WithSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(s *Service) { s.sleep = sleep }
}

// WithTrackSource registers the source similar tracks come from. Without one,
// similar tracks are unavailable.
func WithTrackSource(src TrackSimilarity) Option { return func(s *Service) { s.tracks = src } }

// Matcher decides whether a track is already in the library.
// *matching.Service satisfies it.
type Matcher interface {
	Match(ctx context.Context, ext core.ExternalResult) (core.MatchResult, error)
}

// WithMatcher supplies the live library matcher, read per request so it
// follows a library reload.
func WithMatcher(m func() Matcher) Option { return func(s *Service) { s.matcher = m } }

// WithCatalogIDs maps library backend track ids to catalog ids.
func WithCatalogIDs(ids func(ctx context.Context, backendIDs []string) map[string]string) Option {
	return func(s *Service) { s.catalogIDs = ids }
}

// Exclusions is what must never be recommended: the owner's Not interested
// marks. notinterested.Set satisfies it.
type Exclusions interface {
	Track(ext core.ExternalResult) bool
	Artist(name string) bool
}

// WithExclusions loads the current marks, per request, so a mark takes effect
// on results that were cached before it was made.
func WithExclusions(load func(ctx context.Context) (Exclusions, error)) Option {
	return func(s *Service) { s.exclusions = load }
}

type Service struct {
	exclusions  func(context.Context) (Exclusions, error)
	recentPlays func(context.Context, time.Time) ([]TrackCandidate, error)
	sources     func() []search.SearchSource
	library     func() Library
	tracks      TrackSimilarity
	trackGate   gate
	matcher     func() Matcher
	catalogIDs  func(context.Context, []string) map[string]string
	timeout     time.Duration
	now         func() time.Time
	sleep       func(context.Context, time.Duration) error
	cache       *cache
}

// New builds a Service. sources returns the live search sources, read per
// request so an adapter reload takes effect without rebuilding the service.
func New(sources func() []search.SearchSource, opts ...Option) *Service {
	s := &Service{sources: sources, timeout: defaultTimeout, now: time.Now, sleep: sleepContext}
	for _, opt := range opts {
		opt(s)
	}
	s.cache = &cache{now: s.now, entries: map[string]cacheEntry{}}
	return s
}

func (s *Service) liveSources() []search.SearchSource {
	if s == nil || s.sources == nil {
		return nil
	}
	return s.sources()
}

// excluded returns the current marks, or nil when there are none to apply.
func (s *Service) excluded(ctx context.Context) Exclusions {
	if s.exclusions == nil {
		return nil
	}
	ex, err := s.exclusions(ctx)
	if err != nil {
		log.Printf("recommend: reading not-interested marks: %v", err)
		return nil
	}
	return ex
}

// withoutMarkedArtists filters into a new slice: artists may be a cached list.
func (s *Service) withoutMarkedArtists(ctx context.Context, artists []core.ExternalArtist) []core.ExternalArtist {
	ex := s.excluded(ctx)
	out := make([]core.ExternalArtist, 0, len(artists))
	for _, a := range artists {
		if ex == nil || !ex.Artist(a.Name) {
			out = append(out, a)
		}
	}
	return out
}

// withoutMarkedTracks filters into a new slice: tracks may be a cached list.
func (s *Service) withoutMarkedTracks(ctx context.Context, tracks []core.ExternalResult) []core.ExternalResult {
	ex := s.excluded(ctx)
	out := make([]core.ExternalResult, 0, len(tracks))
	for _, t := range tracks {
		if ex == nil || !ex.Track(t) {
			out = append(out, t)
		}
	}
	return out
}

func (s *Service) liveMatcher() Matcher {
	if s.matcher == nil {
		return nil
	}
	return s.matcher()
}

func (s *Service) liveLibrary() Library {
	if s.library == nil {
		return nil
	}
	return s.library()
}

type cacheEntry struct {
	value   any
	expires time.Time
}

// cache holds successful results only: a failure is retried on the next
// request instead of being remembered as "nothing similar".
type cache struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]cacheEntry
}

func (c *cache) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || c.now().After(e.expires) {
		delete(c.entries, key)
		return nil, false
	}
	return e.value, true
}

func (c *cache) put(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{value: value, expires: c.now().Add(cacheTTL)}
}
