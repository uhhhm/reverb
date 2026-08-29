// Package crop stores non-destructive trim points for library tracks.
//
// Reverb never rewrites the audio file: a crop is a pair of playback
// boundaries, applied when the track is played. That is what makes a crop
// reversible — uncropping is deleting a row, and re-cropping later is just
// storing a new pair.
package crop

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store/db"
)

// Points are a track's playback boundaries in milliseconds. EndMs of 0 means
// "play to the end of the file".
type Points struct {
	StartMs int `json:"startMs"`
	EndMs   int `json:"endMs"`
}

// ErrInvalid marks boundaries that could not produce audible playback.
var ErrInvalid = errors.New("crop: end must be after start")

type Service struct {
	q *db.Queries
}

func New(q *db.Queries) *Service { return &Service{q: q} }

// CatalogIDForTrack resolves the stable catalog id a backend track is bound to,
// or "" when it has no binding yet. Callers that publish a crop need it: the
// backend track id changes when the library backend is swapped, so it is not an
// identity peers can agree on.
func (s *Service) CatalogIDForTrack(ctx context.Context, trackID string) string {
	if s == nil || s.q == nil || trackID == "" {
		return ""
	}
	if id, err := s.q.GetCatalogIDByBackendID(ctx, trackID); err == nil {
		return id
	}
	return ""
}

// SetByCatalogID applies a crop that arrived from a peer, which identifies the
// track by its catalog id. When this device has the track bound to a backend
// id, the row is keyed on that so ApplyTracks finds it; otherwise the row is
// parked under the catalog id until a binding exists.
func (s *Service) SetByCatalogID(ctx context.Context, catalogID string, p Points) error {
	if s == nil || s.q == nil {
		return errors.New("crop: no store")
	}
	trackID := catalogID
	if id, err := s.q.GetBackendIDByCatalogID(ctx, catalogID); err == nil && id != "" {
		trackID = id
	}
	return s.set(ctx, trackID, catalogID, p)
}

// GetByCatalogID returns the crop stored under a catalog id, or zero Points.
func (s *Service) GetByCatalogID(ctx context.Context, catalogID string) (Points, error) {
	if s == nil || s.q == nil {
		return Points{}, nil
	}
	row, err := s.q.GetTrackCropByCatalogID(ctx, sql.NullString{String: catalogID, Valid: true})
	if errors.Is(err, sql.ErrNoRows) {
		return Points{}, nil
	}
	if err != nil {
		return Points{}, err
	}
	return pointsFrom(row), nil
}

// ClearByCatalogID removes a crop that a peer removed.
func (s *Service) ClearByCatalogID(ctx context.Context, catalogID string) error {
	if s == nil || s.q == nil {
		return errors.New("crop: no store")
	}
	if err := s.q.DeleteTrackCropByCatalogID(ctx, sql.NullString{String: catalogID, Valid: true}); err != nil {
		return err
	}
	// Legacy rows predate catalog binding and are keyed on the id alone.
	return s.q.DeleteTrackCrop(ctx, catalogID)
}

// Set records a crop. A crop that starts at 0 and runs to the end is not a
// crop at all, so it deletes the row rather than storing a no-op.
func (s *Service) Set(ctx context.Context, trackID string, p Points) error {
	if s == nil || s.q == nil {
		return errors.New("crop: no store")
	}
	if p.StartMs < 0 {
		p.StartMs = 0
	}
	if p.EndMs < 0 {
		p.EndMs = 0
	}
	if p.EndMs > 0 && p.EndMs <= p.StartMs {
		return ErrInvalid
	}
	return s.set(ctx, trackID, s.CatalogIDForTrack(ctx, trackID), p)
}

// set is the shared write path. It stores the catalog id alongside the backend
// track id so the crop survives a backend swap and can be matched to changes
// arriving from peers.
func (s *Service) set(ctx context.Context, trackID, catalogID string, p Points) error {
	if p.StartMs < 0 {
		p.StartMs = 0
	}
	if p.EndMs < 0 {
		p.EndMs = 0
	}
	// A peer can hand us a start and an end that cross, because the two are
	// separate LWW fields and may arrive in either order. Treat the end as open
	// rather than storing a window with nothing in it — the matching update
	// corrects it moments later.
	if p.EndMs > 0 && p.EndMs <= p.StartMs {
		p.EndMs = 0
	}
	if p.StartMs == 0 && p.EndMs == 0 {
		if catalogID != "" {
			if err := s.q.DeleteTrackCropByCatalogID(ctx, sql.NullString{String: catalogID, Valid: true}); err != nil {
				return err
			}
		}
		return s.q.DeleteTrackCrop(ctx, trackID)
	}
	end := sql.NullInt64{}
	if p.EndMs > 0 {
		end = sql.NullInt64{Int64: int64(p.EndMs), Valid: true}
	}
	if catalogID != "" {
		return s.q.UpsertTrackCropByCatalogID(ctx, db.UpsertTrackCropByCatalogIDParams{
			TrackID:   trackID,
			StartMs:   int64(p.StartMs),
			EndMs:     end,
			UpdatedAt: time.Now().Unix(),
			CatalogID: sql.NullString{String: catalogID, Valid: true},
		})
	}
	return s.q.UpsertTrackCrop(ctx, db.UpsertTrackCropParams{
		TrackID:   trackID,
		StartMs:   int64(p.StartMs),
		EndMs:     end,
		UpdatedAt: time.Now().Unix(),
	})
}

// Get returns a track's crop, or zero Points when it has none.
func (s *Service) Get(ctx context.Context, trackID string) (Points, error) {
	if s == nil || s.q == nil {
		return Points{}, nil
	}
	if cid := s.CatalogIDForTrack(ctx, trackID); cid != "" {
		if row, err := s.q.GetTrackCropByCatalogID(ctx, sql.NullString{String: cid, Valid: true}); err == nil {
			return pointsFrom(row), nil
		}
	}
	row, err := s.q.GetTrackCrop(ctx, trackID)
	if err != nil {
		return Points{}, nil
	}
	return pointsFrom(row), nil
}

// Clear removes a track's crop, restoring full-length playback.
func (s *Service) Clear(ctx context.Context, trackID string) error {
	if s == nil || s.q == nil {
		return errors.New("crop: no store")
	}
	if cid := s.CatalogIDForTrack(ctx, trackID); cid != "" {
		if err := s.q.DeleteTrackCropByCatalogID(ctx, sql.NullString{String: cid, Valid: true}); err != nil {
			return err
		}
	}
	return s.q.DeleteTrackCrop(ctx, trackID)
}

// ApplyTracks stamps crop points onto tracks in place. A read failure is not
// fatal — the caller gets uncropped tracks, which is the correct fallback.
func (s *Service) ApplyTracks(ctx context.Context, tracks []core.Track) {
	if s == nil || s.q == nil || len(tracks) == 0 {
		return
	}
	rows, err := s.q.ListTrackCrops(ctx)
	if err != nil || len(rows) == 0 {
		return
	}
	byID := cropsByID(rows)
	for i := range tracks {
		if p, ok := byID[tracks[i].ID]; ok {
			tracks[i].CropStartMs = p.StartMs
			tracks[i].CropEndMs = p.EndMs
		}
	}
}

// ApplyDetailTracks stamps crop points onto the embedded library tracks of
// album- and playlist-detail rows. Unowned rows have no library track and are
// left alone.
func (s *Service) ApplyDetailTracks(ctx context.Context, rows []core.AlbumDetailTrack) {
	if s == nil || s.q == nil || len(rows) == 0 {
		return
	}
	dbRows, err := s.q.ListTrackCrops(ctx)
	if err != nil || len(dbRows) == 0 {
		return
	}
	byID := cropsByID(dbRows)
	for i := range rows {
		lt := rows[i].LibraryTrack
		if lt == nil {
			continue
		}
		if p, ok := byID[lt.ID]; ok {
			lt.CropStartMs = p.StartMs
			lt.CropEndMs = p.EndMs
		}
	}
}

// cropsByID indexes crops under both the backend track id and the catalog id,
// so a crop still matches after a backend swap and one that arrived from a peer
// matches before this device has bound the track.
func cropsByID(rows []db.TrackCrop) map[string]Points {
	out := make(map[string]Points, len(rows)*2)
	for _, r := range rows {
		p := pointsFrom(r)
		out[r.TrackID] = p
		if r.CatalogID.Valid && r.CatalogID.String != "" {
			out[r.CatalogID.String] = p
		}
	}
	return out
}

func pointsFrom(r db.TrackCrop) Points {
	p := Points{StartMs: int(r.StartMs)}
	if r.EndMs.Valid {
		p.EndMs = int(r.EndMs.Int64)
	}
	return p
}
