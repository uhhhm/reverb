// Package tastehistory imports the listening history of a linked Last.fm
// account as taste input: top tracks and artists, and the scrobbles from
// before the account was linked to Reverb. It gives a new install a rich
// taste profile at once.
//
// Imported history is taste input only. It never becomes plays and never
// replicates; it stays on the device that holds the Last.fm link, and
// unlinking deletes it. Scrobbles Reverb sent itself are already counted as
// plays, so they are subtracted from what is imported.
package tastehistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/matching"
	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/scrobble"
	"github.com/uhhhm/reverb/internal/store/db"
)

const (
	provider = scrobble.LastFM

	topTracks    = 200
	topArtists   = 100
	recentTracks = 1000

	// refreshEvery re-imports while linked: top lists keep growing with
	// scrobbles from other players.
	refreshEvery = 7 * 24 * time.Hour
	// retryEvery spaces out imports that failed.
	retryEvery   = time.Hour
	importedKey  = "tastehistory.lastfm.importedAt"
	importLimit  = 2 * time.Minute
	secondsInDay = 24 * 60 * 60
)

// Count is how many times a track, or an artist when Title is empty, was
// scrobbled.
type Count struct {
	Artist string
	Title  string
	Plays  int
}

// Scrobble is one dated listen.
type Scrobble struct {
	Artist string
	Title  string
	At     int64 // unix seconds
}

// Fetcher reads a user's history from Last.fm.
type Fetcher interface {
	TopTracks(ctx context.Context, user string, limit int) ([]Count, error)
	TopArtists(ctx context.Context, user string, limit int) ([]Count, error)
	// RecentTracks returns up to limit scrobbles dated before the given time.
	RecentTracks(ctx context.Context, user string, before int64, limit int) ([]Scrobble, error)
}

// Service imports, serves and removes the household's Last.fm history.
type Service struct {
	conn  *sql.DB
	q     *db.Queries
	fetch Fetcher
	now   func() time.Time
	// allowed reports whether online lookups are on; off, nothing is
	// fetched, and what was imported stays until the account is unlinked.
	allowed func(ctx context.Context) bool

	mu       sync.Mutex // serialises imports and removals
	lastTry  time.Time
	inFlight bool
	// again asks the running import to read once more: the link changed
	// while it was reading.
	again bool
}

// New builds the Service. allowed may be nil, meaning always.
func New(conn *sql.DB, fetch Fetcher, now func() time.Time, allowed func(ctx context.Context) bool) *Service {
	return &Service{conn: conn, q: db.New(conn), fetch: fetch, now: now, allowed: allowed}
}

// Signals returns the imported history as taste signals.
func (s *Service) Signals(ctx context.Context) ([]recommend.TasteSignal, error) {
	rows, err := s.q.ListTasteHistory(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]recommend.TasteSignal, len(rows))
	for i, r := range rows {
		out[i] = recommend.TasteSignal{Kind: recommend.SignalHistory, Artist: r.Artist, Title: r.Title, Plays: int(r.Plays), At: r.At}
	}
	return out, nil
}

// LinkChanged is the scrobble service's link hook: linking Last.fm starts an
// import in the background, and unlinking removes the history at once.
func (s *Service) LinkChanged(ctx context.Context, _ string, prov string, active bool) {
	if prov != provider {
		return
	}
	// Whatever was imported may belong to another account now, so it goes
	// at once; a new link imports afresh.
	if err := s.Remove(ctx); err != nil {
		log.Printf("tastehistory: removing Last.fm history: %v", err)
	}
	if !active {
		return
	}
	go func() {
		if err := s.Import(context.Background()); err != nil {
			log.Printf("tastehistory: importing Last.fm history: %v", err)
		}
	}()
}

// Run keeps the history current until ctx ends: it imports once a Last.fm
// link has none, re-imports weekly, and removes history whose link is gone.
func (s *Service) Run(ctx context.Context, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		s.check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) check(ctx context.Context) {
	links, err := s.q.ListActiveScrobbleLinksByProvider(ctx, provider)
	if err != nil {
		log.Printf("tastehistory: reading Last.fm links: %v", err)
		return
	}
	if len(links) == 0 {
		// A broken link keeps its history until it is unlinked.
		if s.linked(ctx) {
			return
		}
		if err := s.Remove(ctx); err != nil {
			log.Printf("tastehistory: removing Last.fm history: %v", err)
		}
		return
	}
	imported, _ := s.importedAt(ctx)
	if s.now().Sub(imported) < refreshEvery {
		return
	}
	s.mu.Lock()
	recent := s.now().Sub(s.lastTry) < retryEvery
	s.mu.Unlock()
	if recent {
		return
	}
	if err := s.Import(ctx); err != nil {
		log.Printf("tastehistory: importing Last.fm history: %v", err)
	}
}

// linked reports whether any Last.fm link exists, active or not.
func (s *Service) linked(ctx context.Context) bool {
	n, err := s.q.CountScrobbleLinksByProvider(ctx, provider)
	// Unsure: keep what is there.
	return err != nil || n > 0
}

func (s *Service) importedAt(ctx context.Context) (time.Time, error) {
	v, err := s.q.GetSetting(ctx, importedKey)
	if err != nil {
		return time.Time{}, err
	}
	sec, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0), nil
}

// Import replaces the stored history with a fresh read of the household's
// Last.fm account. With no active link it removes the history instead. A
// call made while an import runs, as when the account is relinked, makes the
// running import read again rather than being dropped.
func (s *Service) Import(ctx context.Context) error {
	s.mu.Lock()
	if s.inFlight {
		s.again = true
		s.mu.Unlock()
		return nil
	}
	s.inFlight = true
	s.mu.Unlock()
	for {
		s.mu.Lock()
		s.again, s.lastTry = false, s.now()
		s.mu.Unlock()
		// Each read gets its own time limit, so a rerun asked for late in a
		// slow read still has all of it.
		runCtx, cancel := context.WithTimeout(ctx, importLimit)
		err := s.importOnce(runCtx)
		cancel()
		s.mu.Lock()
		if !s.again || ctx.Err() != nil {
			s.inFlight = false
			s.mu.Unlock()
			return err
		}
		s.mu.Unlock()
	}
}

func (s *Service) importOnce(ctx context.Context) error {
	links, err := s.q.ListActiveScrobbleLinksByProvider(ctx, provider)
	if err != nil {
		return err
	}
	if len(links) == 0 {
		return s.Remove(ctx)
	}
	if s.allowed != nil && !s.allowed(ctx) {
		return nil
	}
	// One household: the first account linked stands for it.
	link := links[0]
	rows, err := s.read(ctx, link.Username, link.CreatedAt)
	if err != nil {
		return err
	}
	return s.replace(ctx, link, rows)
}

// read fetches the account's history and takes Reverb's own scrobbles out.
// Top lists are dated when the account was linked: they stand for listening
// before Reverb, which Reverb's own plays then take over from.
func (s *Service) read(ctx context.Context, user string, linkedAt int64) ([]db.InsertTasteHistoryParams, error) {
	tracks, err := s.fetch.TopTracks(ctx, user, topTracks)
	if err != nil {
		return nil, fmt.Errorf("top tracks: %w", err)
	}
	artists, err := s.fetch.TopArtists(ctx, user, topArtists)
	if err != nil {
		return nil, fmt.Errorf("top artists: %w", err)
	}
	recent, err := s.fetch.RecentTracks(ctx, user, linkedAt, recentTracks)
	if err != nil {
		return nil, fmt.Errorf("recent tracks: %w", err)
	}
	sent, err := s.q.ListSentScrobbles(ctx, provider)
	if err != nil {
		return nil, fmt.Errorf("sent scrobbles: %w", err)
	}
	return historyRows(tracks, artists, recent, sent, linkedAt), nil
}

// historyRows counts every listen once. A dated scrobble becomes a row for
// its track and day; a top track keeps only the listens neither dated nor
// sent by Reverb; a top artist keeps only those not sent by Reverb. Artist
// weight comes from artist rows alone, so a track row does not repeat it.
func historyRows(tracks, artists []Count, recent []Scrobble, sent []db.ListSentScrobblesRow, linkedAt int64) []db.InsertTasteHistoryParams {
	type dated struct {
		key string
		at  int64
	}
	ownAt := map[dated]int{}
	ownTrack := map[string]int{}
	ownArtist := map[string]int{}
	for _, r := range sent {
		ownAt[dated{trackKey(r.Artist, r.Title), r.PlayedAt}]++
		ownTrack[trackKey(r.Artist, r.Title)]++
		ownArtist[artistKey(r.Artist)]++
	}

	var out []db.InsertTasteHistoryParams
	type day struct {
		key string
		day int64
	}
	dayRow := map[day]int{}
	datedTrack := map[string]int{}
	for _, sc := range recent {
		key := trackKey(sc.Artist, sc.Title)
		// Reverb's own scrobble (sent under an earlier link): skipped here,
		// and still subtracted from the track's top count below.
		if d := (dated{key, sc.At}); ownAt[d] > 0 {
			ownAt[d]--
			continue
		}
		datedTrack[key]++
		d := day{key, sc.At / secondsInDay}
		if i, ok := dayRow[d]; ok {
			out[i].Plays++
			continue
		}
		dayRow[d] = len(out)
		out = append(out, db.InsertTasteHistoryParams{Provider: provider, Artist: sc.Artist, Title: sc.Title, Plays: 1, At: d.day * secondsInDay})
	}
	for _, t := range tracks {
		key := trackKey(t.Artist, t.Title)
		if n := t.Plays - datedTrack[key] - ownTrack[key]; n > 0 {
			out = append(out, db.InsertTasteHistoryParams{Provider: provider, Artist: t.Artist, Title: t.Title, Plays: int64(n), At: linkedAt})
		}
	}
	for _, a := range artists {
		if n := a.Plays - ownArtist[artistKey(a.Artist)]; n > 0 {
			out = append(out, db.InsertTasteHistoryParams{Provider: provider, Artist: a.Artist, Plays: int64(n), At: linkedAt})
		}
	}
	return out
}

// replace swaps the stored history in one transaction, unless the link was
// removed or relinked to another account while Last.fm was being read.
func (s *Service) replace(ctx context.Context, link db.ScrobbleLink, rows []db.InsertTasteHistoryParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	current, err := q.GetScrobbleLink(ctx, db.GetScrobbleLinkParams{UserID: link.UserID, Provider: provider})
	if errors.Is(err, sql.ErrNoRows) || err == nil && (current.Username != link.Username || current.CreatedAt != link.CreatedAt) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := q.DeleteTasteHistory(ctx, provider); err != nil {
		return err
	}
	for _, r := range rows {
		if err := q.InsertTasteHistory(ctx, r); err != nil {
			return err
		}
	}
	if err := q.UpsertSetting(ctx, db.UpsertSettingParams{Key: importedKey, Value: strconv.FormatInt(s.now().Unix(), 10)}); err != nil {
		return err
	}
	return tx.Commit()
}

// Remove deletes the imported history, taking its contribution out of the
// taste profile on the next build.
func (s *Service) Remove(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	if err := q.DeleteTasteHistory(ctx, provider); err != nil {
		return err
	}
	if err := q.DeleteSetting(ctx, importedKey); err != nil {
		return err
	}
	return tx.Commit()
}

func trackKey(artist, title string) string {
	return artistKey(artist) + "\x1f" + matching.Normalize(title)
}

// artistKey matches Last.fm's names to Reverb's credits: Reverb scrobbles a
// composite credit as its primary artist.
func artistKey(artist string) string {
	return matching.Normalize(matching.PrimaryArtist(artist))
}
