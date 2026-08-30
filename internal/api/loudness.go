package api

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uhhhm/reverb/internal/library/loudness"
	"github.com/uhhhm/reverb/internal/store/db"
)

// handleTrackGain reports the playback gain, in dB, that brings a track to
// Reverb's reference level.
//
// Measurement is lazy and cached: it runs ffmpeg over the file the first time a
// track is asked about, then stores the result. Measuring the whole library up
// front would spend hours of CPU on tracks nobody plays, and the player only
// needs the gain for the track it is about to start.
//
// 204 means "no gain available" — a remote library with no file to inspect, or
// no ffmpeg — and the player then plays the track unmodified.
func (s *Server) handleTrackGain(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.deps.Loudness != nil {
		if row, err := s.deps.Loudness.GetTrackLoudness(r.Context(), id); err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"gainDb": row.GainDb})
			return
		}
	}
	path, ok := s.localPathFor(r.Context(), id)
	if !ok || path == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	gain, err := loudness.Measure(r.Context(), "ffmpeg", path)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.deps.Loudness != nil {
		if err := s.storeTrackGain(r.Context(), id, gain); err != nil {
			// A cache write failure only costs a re-measure next time.
			log.Printf("WARNING: could not cache track loudness for %s: %v", id, err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"gainDb": gain})
}

// storeTrackGain caches a measured gain and shares it with paired devices.
//
// It is stored under the catalog id as well as the backend track id: the gain
// describes the recording, so a device that has the same track under a
// different backend id can use the measurement instead of repeating it.
func (s *Server) storeTrackGain(ctx context.Context, trackID string, gain float64) error {
	catalogID := ""
	if s.deps.Overrides != nil {
		catalogID = s.deps.Overrides.CatalogIDForTrack(ctx, trackID)
	}
	var err error
	if catalogID != "" {
		err = s.deps.Loudness.UpsertTrackLoudnessByCatalogID(ctx, db.UpsertTrackLoudnessByCatalogIDParams{
			TrackID:   trackID,
			GainDb:    gain,
			UpdatedAt: time.Now().Unix(),
			CatalogID: sql.NullString{String: catalogID, Valid: true},
		})
	} else {
		err = s.deps.Loudness.UpsertTrackLoudness(ctx, db.UpsertTrackLoudnessParams{
			TrackID:   trackID,
			GainDb:    gain,
			UpdatedAt: time.Now().Unix(),
		})
	}
	if err != nil {
		return err
	}
	s.emitTrackLoudness(ctx, trackID, gain)
	return nil
}
