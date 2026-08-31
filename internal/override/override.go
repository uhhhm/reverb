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
	Album  string `json:"album"`
}

type Service struct {
	q *db.Queries
}

func New(q *db.Queries) *Service { return &Service{q: q} }

// Set records a rename. Both fields blank deletes the override outright, so the
// table never accumulates rows that say nothing.
// For P2P, it also stores the catalog_id (stable) alongside the backend track_id (volatile)
// so the rename survives backend swaps and syncs to peers via catalog_id.
func (s *Service) Set(ctx context.Context, trackID string, n Name) error {
	if s == nil || s.q == nil {
		return errors.New("override: no store")
	}
	title := strings.TrimSpace(n.Title)
	artist := strings.TrimSpace(n.Artist)
	album := strings.TrimSpace(n.Album)
	if title == "" && artist == "" && album == "" {
		return s.q.DeleteTrackOverride(ctx, trackID)
	}
	// Try to resolve catalog_id for P2P stability.
	catalogID := s.catalogIDForTrack(ctx, trackID)
	if catalogID != "" {
		return s.q.UpsertTrackOverrideByCatalogID(ctx, db.UpsertTrackOverrideByCatalogIDParams{
			TrackID:   trackID,
			Title:     title,
			Artist:    artist,
			Album:     album,
			UpdatedAt: time.Now().Unix(),
			CatalogID: sql.NullString{String: catalogID, Valid: true},
		})
	}
	return s.q.UpsertTrackOverride(ctx, db.UpsertTrackOverrideParams{
		TrackID:   trackID,
		Title:     title,
		Artist:    artist,
		Album:     album,
		UpdatedAt: time.Now().Unix(),
	})
}

// SetByCatalogID applies a rename that arrived from a peer, which identifies
// the track by its catalog id. When this device has the track bound to a
// backend id the row is keyed on that, so ApplyTracks finds it; otherwise it is
// parked under the catalog id until a binding exists.
func (s *Service) SetByCatalogID(ctx context.Context, catalogID string, n Name) error {
	if s == nil || s.q == nil {
		return errors.New("override: no store")
	}
	title := strings.TrimSpace(n.Title)
	artist := strings.TrimSpace(n.Artist)
	album := strings.TrimSpace(n.Album)
	trackID := catalogID
	if id, err := s.q.GetBackendIDByCatalogID(ctx, catalogID); err == nil && id != "" {
		trackID = id
	}
	if title == "" && artist == "" && album == "" {
		if err := s.q.DeleteTrackOverrideByCatalogID(ctx, sql.NullString{String: catalogID, Valid: true}); err != nil {
			return err
		}
		return s.q.DeleteTrackOverride(ctx, trackID)
	}
	return s.q.UpsertTrackOverrideByCatalogID(ctx, db.UpsertTrackOverrideByCatalogIDParams{
		TrackID:   trackID,
		Title:     title,
		Artist:    artist,
		Album:     album,
		UpdatedAt: time.Now().Unix(),
		CatalogID: sql.NullString{String: catalogID, Valid: true},
	})
}

// GetByCatalogID returns the rename stored under a catalog id, or a zero Name.
func (s *Service) GetByCatalogID(ctx context.Context, catalogID string) (Name, error) {
	if s == nil || s.q == nil {
		return Name{}, nil
	}
	row, err := s.q.GetTrackOverrideByCatalogID(ctx, sql.NullString{String: catalogID, Valid: true})
	if errors.Is(err, sql.ErrNoRows) {
		return Name{}, nil
	}
	if err != nil {
		return Name{}, err
	}
	return Name{Title: row.Title, Artist: row.Artist, Album: row.Album}, nil
}

// CatalogIDForTrack resolves the stable catalog id a backend track is bound to,
// or "" when it has no binding yet. Callers publishing a rename need it: the
// backend track id changes when the library backend is swapped, so it is not an
// identity peers can agree on.
func (s *Service) CatalogIDForTrack(ctx context.Context, trackID string) string {
	return s.catalogIDForTrack(ctx, trackID)
}

// catalogIDForTrack tries to resolve the stable catalog_id for a backend trackID
// via backend_binding. Returns "" if not found (legacy or not yet bound).
func (s *Service) catalogIDForTrack(ctx context.Context, trackID string) string {
	if s == nil || s.q == nil || trackID == "" {
		return ""
	}
	// Direct query: SELECT catalog_id FROM backend_binding WHERE backend_id = ? LIMIT 1
	// Uses the generated GetCatalogIDByBackendID if available, otherwise fallback to raw query.
	if id, err := s.q.GetCatalogIDByBackendID(ctx, trackID); err == nil && id != "" {
		return id
	}
	return ""
}

// Get returns the rename for one track, or a zero Name when there is none.
// It prefers the catalog_id (P2P stable) over the backend track_id (volatile).
func (s *Service) Get(ctx context.Context, trackID string) (Name, error) {
	if s == nil || s.q == nil {
		return Name{}, nil
	}
	if cid := s.catalogIDForTrack(ctx, trackID); cid != "" {
		if row, err := s.q.GetTrackOverrideByCatalogID(ctx, sql.NullString{String: cid, Valid: true}); err == nil {
			return Name{Title: row.Title, Artist: row.Artist, Album: row.Album}, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return Name{}, err
		}
	}
	row, err := s.q.GetTrackOverride(ctx, trackID)
	if errors.Is(err, sql.ErrNoRows) {
		return Name{}, nil
	}
	if err != nil {
		return Name{}, err
	}
	return Name{Title: row.Title, Artist: row.Artist, Album: row.Album}, nil
}

// all loads every override keyed by track id and by catalog_id.
// Overrides are few, so one read beats a query per track. For P2P, we index
// by both track_id (legacy) and catalog_id (stable) so ApplyTracks can find
// renames even after a backend swap.
func (s *Service) all(ctx context.Context) (map[string]Name, error) {
	rows, err := s.q.ListTrackOverrides(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Name, len(rows)*2)
	for _, r := range rows {
		out[r.TrackID] = Name{Title: r.Title, Artist: r.Artist, Album: r.Album}
		if r.CatalogID.Valid && r.CatalogID.String != "" {
			out[r.CatalogID.String] = Name{Title: r.Title, Artist: r.Artist, Album: r.Album}
		}
	}
	return out, nil
}

// CatalogIDsForTracks resolves the stable catalog id for a batch of backend
// track ids, skipping the ones with no binding yet.
func (s *Service) CatalogIDsForTracks(ctx context.Context, trackIDs []string) map[string]string {
	if s == nil || s.q == nil {
		return map[string]string{}
	}
	return s.catalogIDsForTracks(ctx, trackIDs)
}

// catalogIDsForTracks resolves catalog_ids for a batch of backend trackIDs via backend_binding.
func (s *Service) catalogIDsForTracks(ctx context.Context, trackIDs []string) map[string]string {
	if len(trackIDs) == 0 {
		return map[string]string{}
	}
	if rows, err := s.q.ListCatalogIDsByBackendIDs(ctx, trackIDs); err == nil {
		out := make(map[string]string, len(rows))
		for _, r := range rows {
			if r.CatalogID != "" {
				out[r.BackendID] = r.CatalogID
			}
		}
		return out
	}
	// Fallback to per-item (should not happen in prod, but keeps tests with mocks working).
	out := make(map[string]string, len(trackIDs))
	for _, tid := range trackIDs {
		if cid := s.catalogIDForTrack(ctx, tid); cid != "" {
			out[tid] = cid
		}
	}
	return out
}

// ApplyTracks rewrites titles and artists in place. A read failure is not fatal
// — the caller gets the library's own names, which is the correct fallback.
// For P2P, it tries catalog_id first (stable) then backend track_id (legacy).
func (s *Service) ApplyTracks(ctx context.Context, tracks []core.Track) {
	if s == nil || s.q == nil || len(tracks) == 0 {
		return
	}
	m, err := s.all(ctx)
	if err != nil || len(m) == 0 {
		return
	}
	// Build catalog_id map for this batch to prefer stable overrides.
	trackIDs := make([]string, 0, len(tracks))
	for _, t := range tracks {
		trackIDs = append(trackIDs, t.ID)
	}
	catalogMap := s.catalogIDsForTracks(ctx, trackIDs)
	for i := range tracks {
		if cid, ok := catalogMap[tracks[i].ID]; ok {
			if n, ok := m[cid]; ok && !n.empty() {
				applyTo(&tracks[i], n)
				continue
			}
		}
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

// empty reports whether a Name says nothing, meaning the library's own names stand.
func (n Name) empty() bool { return n.Title == "" && n.Artist == "" && n.Album == "" }

func applyTo(t *core.Track, n Name) {
	if n.Title != "" {
		t.Title = n.Title
	}
	if n.Artist != "" {
		t.Artist = n.Artist
	}
	if n.Album != "" {
		t.Album = n.Album
	}
}

// ApplyDetailTracks rewrites album- and playlist-detail rows in place. Both the
// row's own display fields and the embedded LibraryTrack are updated, keyed on
// the library track id — missing (unowned) rows have no id and are left alone.
// For P2P, catalog_id is preferred.
func (s *Service) ApplyDetailTracks(ctx context.Context, rows []core.AlbumDetailTrack) {
	if s == nil || s.q == nil || len(rows) == 0 {
		return
	}
	m, err := s.all(ctx)
	if err != nil || len(m) == 0 {
		return
	}
	// Batch resolve catalog_ids for this page.
	trackIDs := make([]string, 0, len(rows))
	for i := range rows {
		if rows[i].LibraryTrack != nil {
			trackIDs = append(trackIDs, rows[i].LibraryTrack.ID)
		}
	}
	catalogMap := s.catalogIDsForTracks(ctx, trackIDs)
	for i := range rows {
		lt := rows[i].LibraryTrack
		if lt == nil {
			continue
		}
		var n Name
		var ok bool
		if cid, hasCID := catalogMap[lt.ID]; hasCID {
			n, ok = m[cid]
		}
		if !ok {
			n, ok = m[lt.ID]
		}
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
