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
	if p.StartMs == 0 && p.EndMs == 0 {
		return s.q.DeleteTrackCrop(ctx, trackID)
	}
	end := sql.NullInt64{}
	if p.EndMs > 0 {
		end = sql.NullInt64{Int64: int64(p.EndMs), Valid: true}
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
	byID := make(map[string]Points, len(rows))
	for _, r := range rows {
		byID[r.TrackID] = pointsFrom(r)
	}
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
	byID := make(map[string]Points, len(dbRows))
	for _, r := range dbRows {
		byID[r.TrackID] = pointsFrom(r)
	}
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

func pointsFrom(r db.TrackCrop) Points {
	p := Points{StartMs: int(r.StartMs)}
	if r.EndMs.Valid {
		p.EndMs = int(r.EndMs.Int64)
	}
	return p
}
