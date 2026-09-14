package recommend

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store/db"
)

// PlayedCount is how often something was played, or how many library tracks
// an artist has. Title is empty for an artist.
type PlayedCount struct {
	Artist string
	Title  string
	Count  int
}

// Listening summarises the household's plays and library for the surfaces
// that choose their own seeds (Home shelves and Mixes). Play reads are over
// replicated plays within explicit bounds, so devices holding the same plays
// pick the same seeds (ADR 0001). Library reads follow this device's own
// library bindings, which do not replicate, so seeds drawn from the library
// can differ between devices.
type Listening interface {
	// TopTracks returns recordings by qualified plays in [since, until), most
	// played first, ties by name.
	TopTracks(ctx context.Context, since, until time.Time, limit int) ([]PlayedCount, error)
	// TopArtists returns artists by qualified plays in [since, until).
	TopArtists(ctx context.Context, since, until time.Time, limit int) ([]PlayedCount, error)
	// LibraryArtists returns artists by the number of playable library tracks.
	LibraryArtists(ctx context.Context, limit int) ([]PlayedCount, error)
	// LibraryTracks returns playable library tracks by an artist, in album
	// and title order.
	LibraryTracks(ctx context.Context, artist string, limit int) ([]core.ExternalResult, error)
}

// WithListening supplies the play and library summaries shelves and Mixes are
// seeded from. Without it, those surfaces are empty.
func WithListening(l Listening) Option { return func(s *Service) { s.listening = l } }

// ListeningQuerier is the persistence seam for Listening.
type ListeningQuerier interface {
	TopPlayedTracksBetween(context.Context, db.TopPlayedTracksBetweenParams) ([]db.TopPlayedTracksBetweenRow, error)
	TopPlayedArtistsBetween(context.Context, db.TopPlayedArtistsBetweenParams) ([]db.TopPlayedArtistsBetweenRow, error)
	LibraryArtistTrackCounts(context.Context, int64) ([]db.LibraryArtistTrackCountsRow, error)
	LibraryTracksByArtist(context.Context, db.LibraryTracksByArtistParams) ([]db.LibraryTracksByArtistRow, error)
}

type storeListening struct{ q ListeningQuerier }

// NewListening reads Listening from the store.
func NewListening(q ListeningQuerier) Listening { return storeListening{q: q} }

func (l storeListening) TopTracks(ctx context.Context, since, until time.Time, limit int) ([]PlayedCount, error) {
	rows, err := l.q.TopPlayedTracksBetween(ctx, db.TopPlayedTracksBetweenParams{Since: since.Unix(), Until: until.Unix(), ResultLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]PlayedCount, len(rows))
	for i, r := range rows {
		out[i] = PlayedCount{Artist: r.Artist, Title: r.Title, Count: int(r.Plays)}
	}
	return out, nil
}

func (l storeListening) TopArtists(ctx context.Context, since, until time.Time, limit int) ([]PlayedCount, error) {
	rows, err := l.q.TopPlayedArtistsBetween(ctx, db.TopPlayedArtistsBetweenParams{Since: since.Unix(), Until: until.Unix(), ResultLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]PlayedCount, len(rows))
	for i, r := range rows {
		out[i] = PlayedCount{Artist: r.Artist, Count: int(r.Plays)}
	}
	return out, nil
}

func (l storeListening) LibraryArtists(ctx context.Context, limit int) ([]PlayedCount, error) {
	rows, err := l.q.LibraryArtistTrackCounts(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	out := make([]PlayedCount, len(rows))
	for i, r := range rows {
		out[i] = PlayedCount{Artist: r.Artist, Count: int(r.Tracks)}
	}
	return out, nil
}

func (l storeListening) LibraryTracks(ctx context.Context, artist string, limit int) ([]core.ExternalResult, error) {
	rows, err := l.q.LibraryTracksByArtist(ctx, db.LibraryTracksByArtistParams{Artist: artist, ResultLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]core.ExternalResult, len(rows))
	for i, r := range rows {
		out[i] = core.ExternalResult{
			Source: "library", ExternalID: r.BackendID, CanonicalID: r.ID,
			Title: r.Title, Artist: r.Artist, Album: r.Album,
			DurationMs: int(r.DurationMs), ISRC: r.Isrc, MBID: r.Mbid,
			CoverArtID: r.CoverArtID, Type: core.EntityTrack,
			RecommendationSources: []string{"local"},
			Match:                 &core.MatchResult{Status: core.MatchInLibrary, LibraryTrackID: r.BackendID, Method: core.MatchFuzzy, Confidence: 1},
		}
	}
	return out, nil
}

// Store keeps generated shelves and Mixes on this device between launches.
// The settings table satisfies it; nothing written here replicates.
type Store interface {
	GetSetting(ctx context.Context, key string) (string, error)
	UpsertSetting(ctx context.Context, arg db.UpsertSettingParams) error
}

// WithStore persists shelves and Mixes, so they render from cache at once
// after a restart. Without it they live in memory only.
func WithStore(st Store) Option { return func(s *Service) { s.store = st } }

// memoryStore stands in when no Store is configured.
type memoryStore struct {
	values map[string]string
}

func (m *memoryStore) GetSetting(_ context.Context, key string) (string, error) {
	v, ok := m.values[key]
	if !ok {
		return "", sql.ErrNoRows
	}
	return v, nil
}

func (m *memoryStore) UpsertSetting(_ context.Context, arg db.UpsertSettingParams) error {
	m.values[arg.Key] = arg.Value
	return nil
}

// load reads a stored value; false when absent or unreadable.
func (s *Service) load(ctx context.Context, key string, out any) bool {
	s.storeMu.Lock()
	raw, err := s.store.GetSetting(ctx, key)
	s.storeMu.Unlock()
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("recommend: reading %s: %v", key, err)
		}
		return false
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		log.Printf("recommend: decoding %s: %v", key, err)
		return false
	}
	return true
}

func (s *Service) save(ctx context.Context, key string, value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		log.Printf("recommend: encoding %s: %v", key, err)
		return
	}
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	if err := s.store.UpsertSetting(ctx, db.UpsertSettingParams{Key: key, Value: string(raw)}); err != nil {
		log.Printf("recommend: saving %s: %v", key, err)
	}
}
