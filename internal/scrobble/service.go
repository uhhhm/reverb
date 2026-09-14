package scrobble

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/uhhhm/reverb/internal/store/db"
)

// maxAttempts is the number of attempts before a queue row is permanently failed.
const maxAttempts = 6

// provider is the canonical provider name for Last.fm in this service.
const provider = LastFM

// ----------------------------------------------------------------------------
// Querier — the subset of db.Queries used by Service.
// db.Queries satisfies this interface automatically.
// ----------------------------------------------------------------------------

// Querier is the minimal data-layer interface consumed by Service.
// It is implemented by *db.Queries (the real store) and by fakes in tests.
type Querier interface {
	// link operations
	UpsertScrobbleLink(ctx context.Context, arg db.UpsertScrobbleLinkParams) error
	GetScrobbleLink(ctx context.Context, arg db.GetScrobbleLinkParams) (db.ScrobbleLink, error)
	ListScrobbleLinks(ctx context.Context, userID string) ([]db.ScrobbleLink, error)
	DeleteScrobbleLink(ctx context.Context, arg db.DeleteScrobbleLinkParams) error
	SetScrobbleLinkStatus(ctx context.Context, arg db.SetScrobbleLinkStatusParams) error

	// queue operations
	InsertScrobbleQueue(ctx context.Context, arg db.InsertScrobbleQueueParams) error
	DeletePendingScrobbles(ctx context.Context, arg db.DeletePendingScrobblesParams) error
	SelectDueScrobbles(ctx context.Context, arg db.SelectDueScrobblesParams) ([]db.ScrobbleQueue, error)
	MarkScrobbleDone(ctx context.Context, id string) error
	MarkScrobbleRetry(ctx context.Context, arg db.MarkScrobbleRetryParams) error
	MarkScrobbleFailed(ctx context.Context, id string) error
}

// ----------------------------------------------------------------------------
// Link — public view of a scrobble link (no SessionKey)
// ----------------------------------------------------------------------------

// Link is the public representation of a provider link. SessionKey is
// intentionally omitted to prevent accidental leakage.
type Link struct {
	Provider string
	Username string
	Status   string
}

// ----------------------------------------------------------------------------
// Service
// ----------------------------------------------------------------------------

// Service orchestrates scrobbling per-user: auth, queueing, and the
// background worker that drains the queue. Last.fm is always registered;
// token providers such as ListenBrainz are added with WithTokenProvider. A
// play is queued once for every provider the user has an active link to, and
// each row is submitted to its own provider, so every provider retries
// offline the same way.
type Service struct {
	q      Querier
	sc     Scrobbler
	tokens map[string]TokenProvider
	cfg    func() Creds // returns app-level Creds{APIKey, APISecret}
	now    func() time.Time
	idgen  func() string
	hooks  []LinkHook
}

// LinkHook is told when a user's link to a provider becomes active or is
// removed.
type LinkHook func(ctx context.Context, userID, provider string, active bool)

// NewService constructs a Service.
//
//   - q: data layer (real db.Queries or test fake)
//   - sc: Scrobbler adapter (e.g. lastfm.Adapter)
//   - cfg: returns the app-level Creds (APIKey + APISecret only; SessionKey is per-user)
//   - now: clock function (use time.Now in production; override in tests)
//   - idgen: ID generator for queue rows (use uuid.NewString; override in tests)
func NewService(q Querier, sc Scrobbler, cfg func() Creds, now func() time.Time, idgen func() string) *Service {
	return &Service{q: q, sc: sc, tokens: map[string]TokenProvider{}, cfg: cfg, now: now, idgen: idgen}
}

// WithTokenProvider registers a provider linked by a user token under name.
// Call before the worker starts.
func (s *Service) WithTokenProvider(name string, p TokenProvider) *Service {
	s.tokens[name] = p
	return s
}

// OnLinkChange registers a hook run after a link is stored or removed. Call
// before serving requests.
func (s *Service) OnLinkChange(h LinkHook) { s.hooks = append(s.hooks, h) }

func (s *Service) linkChanged(ctx context.Context, userID, prov string, active bool) {
	for _, h := range s.hooks {
		h(ctx, userID, prov, active)
	}
}

// submitter returns the provider's Submitter and the credentials for a link
// to it, or false when the provider is not registered.
func (s *Service) submitter(link db.ScrobbleLink) (Submitter, Creds, bool) {
	if link.Provider == provider {
		c := s.cfg()
		c.SessionKey = link.SessionKey
		return s.sc, c, true
	}
	if p, ok := s.tokens[link.Provider]; ok {
		return p, Creds{SessionKey: link.SessionKey}, true
	}
	return nil, Creds{}, false
}

// activeLinks returns the user's active links to registered providers.
func (s *Service) activeLinks(ctx context.Context, userID string) ([]db.ScrobbleLink, error) {
	rows, err := s.q.ListScrobbleLinks(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []db.ScrobbleLink
	for _, r := range rows {
		if _, _, ok := s.submitter(r); ok && r.Status == "active" {
			out = append(out, r)
		}
	}
	return out, nil
}

// ConnectToken links a token provider: the token is checked with the
// provider, then stored like a Last.fm session key and never returned or
// logged. A rejected token wraps ErrAuth and stores nothing.
func (s *Service) ConnectToken(ctx context.Context, userID, prov, token string) (username string, err error) {
	p, ok := s.tokens[prov]
	if !ok {
		return "", ErrUnknownProvider
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("%w: empty token", ErrAuth)
	}
	username, err = p.ValidateToken(ctx, token)
	if err != nil {
		return "", fmt.Errorf("scrobble: validate %s token: %w", prov, err)
	}
	if err := s.q.UpsertScrobbleLink(ctx, db.UpsertScrobbleLinkParams{
		UserID:     userID,
		Provider:   prov,
		SessionKey: token,
		Username:   username,
		Status:     "active",
		CreatedAt:  s.now().Unix(),
	}); err != nil {
		return "", fmt.Errorf("scrobble: store link: %w", err)
	}
	s.linkChanged(ctx, userID, prov, true)
	return username, nil
}

// ----------------------------------------------------------------------------
// Auth passthroughs
// ----------------------------------------------------------------------------

// IsConfigured reports whether the app-level API key and secret are both set.
// Used by the /scrobble/links handler to populate the "configured" field.
func (s *Service) IsConfigured() bool {
	c := s.cfg()
	return c.APIKey != "" && c.APISecret != ""
}

// AuthURL starts the OAuth-style token flow for the provider.
// Returns an error if the app API key or secret are not configured.
func (s *Service) AuthURL(ctx context.Context) (authURL, token string, err error) {
	c := s.cfg()
	if c.APIKey == "" || c.APISecret == "" {
		return "", "", fmt.Errorf("lastfm not configured: api_key and api_secret required")
	}
	return s.sc.AuthURL(ctx, c)
}

// CompleteAuth exchanges the approved token for a session key, stores the link
// as "active", and returns the provider username.
func (s *Service) CompleteAuth(ctx context.Context, userID, token string) (username string, err error) {
	c := s.cfg()
	sessionKey, user, err := s.sc.CompleteAuth(ctx, c, token)
	if err != nil {
		return "", fmt.Errorf("scrobble: complete auth: %w", err)
	}
	if err := s.q.UpsertScrobbleLink(ctx, db.UpsertScrobbleLinkParams{
		UserID:     userID,
		Provider:   provider,
		SessionKey: sessionKey,
		Username:   user,
		Status:     "active",
		CreatedAt:  s.now().Unix(),
	}); err != nil {
		return "", fmt.Errorf("scrobble: store link: %w", err)
	}
	s.linkChanged(ctx, userID, provider, true)
	return user, nil
}

// Links returns the provider links for userID, omitting SessionKey.
func (s *Service) Links(ctx context.Context, userID string) ([]Link, error) {
	rows, err := s.q.ListScrobbleLinks(ctx, userID)
	if err != nil {
		return nil, err
	}
	links := make([]Link, 0, len(rows))
	for _, r := range rows {
		links = append(links, Link{
			Provider: r.Provider,
			Username: r.Username,
			Status:   r.Status,
		})
	}
	return links, nil
}

// Unlink deletes the link for (userID, provider) and its pending uploads, so
// nothing more is sent once the user disconnects.
func (s *Service) Unlink(ctx context.Context, userID, prov string) error {
	// Pending uploads go first: if removing the link then fails, nothing
	// more is sent, and a retry removes the link.
	if err := s.q.DeletePendingScrobbles(ctx, db.DeletePendingScrobblesParams{UserID: userID, Provider: prov}); err != nil {
		return err
	}
	if err := s.q.DeleteScrobbleLink(ctx, db.DeleteScrobbleLinkParams{
		UserID:   userID,
		Provider: prov,
	}); err != nil {
		return err
	}
	s.linkChanged(ctx, userID, prov, false)
	return nil
}

// ----------------------------------------------------------------------------
// NowPlaying (fire-and-forget)
// ----------------------------------------------------------------------------

// NowPlaying updates the "now playing" status on every provider userID has an
// active link to. Errors are logged and swallowed — callers must not depend on
// this succeeding.
func (s *Service) NowPlaying(ctx context.Context, userID string, t Track) {
	links, err := s.activeLinks(ctx, userID)
	if err != nil {
		log.Printf("scrobble: NowPlaying: links user=%s: %v", userID, err)
		return
	}
	for _, link := range links {
		sub, c, _ := s.submitter(link)
		if err := sub.NowPlaying(ctx, c, t); err != nil {
			log.Printf("scrobble: NowPlaying: %s error user=%s: %v", link.Provider, userID, err)
		}
	}
}

// ----------------------------------------------------------------------------
// Enqueue
// ----------------------------------------------------------------------------

// Enqueue inserts a pending queue row for the play for every provider the
// user has an active link to. With no active link Enqueue is a no-op
// (returns nil).
func (s *Service) Enqueue(ctx context.Context, userID string, p ScrobblePlay) error {
	links, err := s.activeLinks(ctx, userID)
	if err != nil {
		return fmt.Errorf("scrobble: enqueue: links: %w", err)
	}
	now := s.now().Unix()
	for _, link := range links {
		if err := s.q.InsertScrobbleQueue(ctx, db.InsertScrobbleQueueParams{
			ID:            s.idgen(),
			UserID:        userID,
			Provider:      link.Provider,
			CatalogID:     "",
			Title:         p.Title,
			Artist:        p.Artist,
			Album:         p.Album,
			DurationMs:    int64(p.DurationMs),
			PlayedAt:      p.PlayedAt,
			Status:        "pending",
			Attempts:      0,
			NextAttemptAt: now,
			CreatedAt:     now,
		}); err != nil {
			return fmt.Errorf("scrobble: enqueue %s: %w", link.Provider, err)
		}
	}
	return nil
}

// ----------------------------------------------------------------------------
// Worker
// ----------------------------------------------------------------------------

// RunWorker runs a background ticker that calls drainOnce on every tick until
// ctx is done. tick is typically 30s–1m in production.
func (s *Service) RunWorker(ctx context.Context, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.drainOnce(ctx, 50); err != nil {
				log.Printf("scrobble: worker: drainOnce: %v", err)
			}
		}
	}
}

// drainOnce selects up to batch due queue rows, groups them by user_id and
// provider, and submits each group to that provider.
//
// Outcomes per user:
//   - success           → MarkScrobbleDone for each row
//   - ErrAuth           → SetScrobbleLinkStatus("broken") + MarkScrobbleRetry
//     with a long backoff to avoid a hot loop
//   - transient error   → MarkScrobbleRetry(attempts+1, backoffAt); if
//     attempts+1 >= maxAttempts → MarkScrobbleFailed
func (s *Service) drainOnce(ctx context.Context, batch int) error {
	now := s.now()
	rows, err := s.q.SelectDueScrobbles(ctx, db.SelectDueScrobblesParams{
		NextAttemptAt: now.Unix(),
		Limit:         int64(batch),
	})
	if err != nil {
		return fmt.Errorf("scrobble: drainOnce: select: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	// Group rows by user and provider.
	type group struct{ userID, provider string }
	var order []group
	byGroup := make(map[group][]db.ScrobbleQueue)
	for _, r := range rows {
		g := group{r.UserID, r.Provider}
		if _, ok := byGroup[g]; !ok {
			order = append(order, g)
		}
		byGroup[g] = append(byGroup[g], r)
	}

	for _, g := range order {
		if err := s.submitGroup(ctx, g.userID, g.provider, byGroup[g], now); err != nil {
			// Log and continue to other users and providers.
			log.Printf("scrobble: drainOnce: user=%s provider=%s: %v", g.userID, g.provider, err)
		}
	}
	return nil
}

// submitGroup handles all due rows for a single user and provider.
func (s *Service) submitGroup(ctx context.Context, userID, prov string, rows []db.ScrobbleQueue, now time.Time) error {
	// Look up the user's active link.
	link, err := s.q.GetScrobbleLink(ctx, db.GetScrobbleLinkParams{
		UserID:   userID,
		Provider: prov,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// User has no link; leave rows as-is (they'll be re-examined next tick).
			return nil
		}
		return fmt.Errorf("get link: %w", err)
	}
	if link.Status != "active" {
		// Link is broken — don't submit; rows will stay pending with their
		// existing next_attempt_at values (which drainOnce set to far-future on
		// ErrAuth, so they won't be immediately re-selected).
		return nil
	}

	// Build the plays slice.
	plays := make([]ScrobblePlay, 0, len(rows))
	for _, r := range rows {
		plays = append(plays, ScrobblePlay{
			Track: Track{
				Title:      r.Title,
				Artist:     r.Artist,
				Album:      r.Album,
				DurationMs: int(r.DurationMs),
			},
			PlayedAt: r.PlayedAt,
		})
	}

	sub, c, ok := s.submitter(link)
	if !ok {
		// A provider no longer registered: leave the rows for a later build.
		return nil
	}
	_, scrobbleErr := sub.Scrobble(ctx, c, plays)

	if scrobbleErr == nil {
		// Mark all rows done.
		for _, r := range rows {
			if err := s.q.MarkScrobbleDone(ctx, r.ID); err != nil {
				log.Printf("scrobble: MarkScrobbleDone %s: %v", r.ID, err)
			}
		}
		return nil
	}

	// Handle ErrAuth: break the link and push all rows' next_attempt_at far
	// into the future to avoid a hot loop.
	if errors.Is(scrobbleErr, ErrAuth) {
		if err := s.q.SetScrobbleLinkStatus(ctx, db.SetScrobbleLinkStatusParams{
			Status:   "broken",
			UserID:   userID,
			Provider: prov,
		}); err != nil {
			log.Printf("scrobble: SetScrobbleLinkStatus broken user=%s: %v", userID, err)
		}
		// Push rows far out so they're not hot-looping.
		farFuture := now.Add(24 * time.Hour).Unix()
		for _, r := range rows {
			newAttempts := r.Attempts + 1
			if err := s.q.MarkScrobbleRetry(ctx, db.MarkScrobbleRetryParams{
				Attempts:      newAttempts,
				NextAttemptAt: farFuture,
				ID:            r.ID,
			}); err != nil {
				log.Printf("scrobble: MarkScrobbleRetry(auth) %s: %v", r.ID, err)
			}
		}
		return fmt.Errorf("user=%s: %w", userID, scrobbleErr)
	}

	// Transient error: retry each row individually with backoff.
	for _, r := range rows {
		newAttempts := r.Attempts + 1
		if newAttempts >= maxAttempts {
			if err := s.q.MarkScrobbleFailed(ctx, r.ID); err != nil {
				log.Printf("scrobble: MarkScrobbleFailed %s: %v", r.ID, err)
			}
			continue
		}
		nextAt := now.Add(backoff(int(newAttempts))).Unix()
		if err := s.q.MarkScrobbleRetry(ctx, db.MarkScrobbleRetryParams{
			Attempts:      newAttempts,
			NextAttemptAt: nextAt,
			ID:            r.ID,
		}); err != nil {
			log.Printf("scrobble: MarkScrobbleRetry %s: %v", r.ID, err)
		}
	}
	return fmt.Errorf("transient scrobble error user=%s: %w", userID, scrobbleErr)
}

// backoff returns min(1h, 60s * 2^(n-1)) for attempt number n (1-based).
func backoff(n int) time.Duration {
	const base = 60 * time.Second
	const cap = 1 * time.Hour
	d := base
	for i := 1; i < n; i++ {
		d *= 2
		if d > cap {
			return cap
		}
	}
	return d
}
