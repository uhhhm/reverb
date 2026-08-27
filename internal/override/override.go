// Package override stores user-supplied display names for library tracks.
//
// Reverb never rewrites file tags: a rename is recorded here and applied when
// tracks are read back out, so the underlying library (Navidrome and any other
// Subsonic client) is left untouched.
package override

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store/db"
)

// Name is a rename for one track. An empty field means "keep what the library
// says" — clearing a field is how a rename is undone.
type Name struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
}

type Service struct {
	q *db.Queries
}

func New(q *db.Queries) *Service { return &Service{q: q} }

// Set records a rename. Both fields blank deletes the override outright, so the
// table never accumulates rows that say nothing.
func (s *Service) Set(ctx context.Context, trackID string, n Name) error {
	if s == nil || s.q == nil {
		return errors.New("override: no store")
	}
	title := strings.TrimSpace(n.Title)
	artist := strings.TrimSpace(n.Artist)
	if title == "" && artist == "" {
		return s.q.DeleteTrackOverride(ctx, trackID)
	}
	return s.q.UpsertTrackOverride(ctx, db.UpsertTrackOverrideParams{
		TrackID:   trackID,
		Title:     title,
		Artist:    artist,
		UpdatedAt: time.Now().Unix(),
	})
}

// Get returns the rename for one track, or a zero Name when there is none.
func (s *Service) Get(ctx context.Context, trackID string) (Name, error) {
	if s == nil || s.q == nil {
		return Name{}, nil
	}
	row, err := s.q.GetTrackOverride(ctx, trackID)
	if errors.Is(err, sql.ErrNoRows) {
		return Name{}, nil
	}
	if err != nil {
		return Name{}, err
	}
	return Name{Title: row.Title, Artist: row.Artist}, nil
}

// all loads every override keyed by track id. Overrides are few (only what the
// user has renamed by hand), so one read beats a query per track.
func (s *Service) all(ctx context.Context) (map[string]Name, error) {
	rows, err := s.q.ListTrackOverrides(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Name, len(rows))
	for _, r := range rows {
		out[r.TrackID] = Name{Title: r.Title, Artist: r.Artist}
	}
	return out, nil
}

// ApplyTracks rewrites titles and artists in place. A read failure is not fatal
// — the caller gets the library's own names, which is the correct fallback.
func (s *Service) ApplyTracks(ctx context.Context, tracks []core.Track) {
	if s == nil || s.q == nil || len(tracks) == 0 {
		return
	}
	m, err := s.all(ctx)
	if err != nil || len(m) == 0 {
		return
	}
	for i := range tracks {
		applyTo(&tracks[i], m[tracks[i].ID])
	}
}

// ApplyTrack rewrites one track in place.
func (s *Service) ApplyTrack(ctx context.Context, t *core.Track) {
	if s == nil || s.q == nil || t == nil {
		return
	}
	n, err := s.Get(ctx, t.ID)
	if err != nil {
		return
	}
	applyTo(t, n)
}

func applyTo(t *core.Track, n Name) {
	if n.Title != "" {
		t.Title = n.Title
	}
	if n.Artist != "" {
		t.Artist = n.Artist
	}
}

// ApplyDetailTracks rewrites album- and playlist-detail rows in place. Both the
// row's own display fields and the embedded LibraryTrack are updated, keyed on
// the library track id — missing (unowned) rows have no id and are left alone.
func (s *Service) ApplyDetailTracks(ctx context.Context, rows []core.AlbumDetailTrack) {
	if s == nil || s.q == nil || len(rows) == 0 {
		return
	}
	m, err := s.all(ctx)
	if err != nil || len(m) == 0 {
		return
	}
	for i := range rows {
		lt := rows[i].LibraryTrack
		if lt == nil {
			continue
		}
		n, ok := m[lt.ID]
		if !ok {
			continue
		}
		applyTo(lt, n)
		if n.Title != "" {
			rows[i].Title = n.Title
		}
		if n.Artist != "" {
			rows[i].Artist = n.Artist
		}
	}
}
