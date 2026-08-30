package api

import (
	"context"
	"log"
	"time"

	"github.com/uhhhm/reverb/internal/materialize"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// emitTrackFieldChange publishes one per-track metadata field to the sync log so
// paired devices receive it.
//
// The entity id is the CATALOG id, never the backend track id: a backend id is
// local to one library backend, so no two devices would agree on it. A track
// with no catalog binding yet simply does not sync — locally it still works,
// and it starts syncing once the binding exists.
//
// Best-effort by design: a device that is not paired has nothing to publish to,
// and a sync failure must never fail the user's edit.
func (s *Server) emitTrackFieldChange(ctx context.Context, catalogID, field string, value any) {
	if catalogID == "" {
		return
	}
	// The emitter also publishes the catalog entity the id names. Without that a
	// peer receives an edit to trk_… and has no way to tell which track it is.
	if s.deps.SyncEmit != nil {
		s.deps.SyncEmit.EmitTrackField(ctx, catalogID, field, value)
		return
	}
	if s.deps.SyncStore == nil {
		return
	}
	deviceID := s.resolveAuthorDeviceForSync(ctx)
	if deviceID == "" {
		return
	}
	if _, err := s.deps.SyncStore.AppendChange(ctx, deviceID, reverbsync.SyncChange{
		EntityType: materialize.EntityTrack,
		EntityID:   catalogID,
		Field:      field,
		Value:      value,
		UpdatedAt:  time.Now().UnixMilli(),
	}); err != nil {
		log.Printf("sync %s for track %q: %v", field, catalogID, err)
	}
}

// emitTrackQuality publishes a per-track quality override. An empty tier is how
// clearing one travels — there is no tombstone, because the track still exists.
func (s *Server) emitTrackQuality(ctx context.Context, trackID, quality string) {
	if s.deps.Overrides == nil {
		return
	}
	s.emitTrackFieldChange(ctx, s.deps.Overrides.CatalogIDForTrack(ctx, trackID), materialize.FieldQuality, quality)
}

// emitTrackLoudness publishes a measured playback gain. It is a property of the
// file rather than a preference, so sharing it spares every other device an
// ffmpeg pass over a track it already has.
func (s *Server) emitTrackLoudness(ctx context.Context, trackID string, gainDb float64) {
	if s.deps.Overrides == nil {
		return
	}
	s.emitTrackFieldChange(ctx, s.deps.Overrides.CatalogIDForTrack(ctx, trackID), materialize.FieldLoudnessGainDb, gainDb)
}

// emitTrackRename publishes a rename. Title and artist are separate LWW fields,
// so both are sent — clearing one is as much a change as setting it.
func (s *Server) emitTrackRename(ctx context.Context, trackID, title, artist string) {
	if s.deps.Overrides == nil {
		return
	}
	catalogID := s.deps.Overrides.CatalogIDForTrack(ctx, trackID)
	s.emitTrackFieldChange(ctx, catalogID, materialize.FieldTitle, title)
	s.emitTrackFieldChange(ctx, catalogID, materialize.FieldArtist, artist)
}

// emitTrackCrop publishes crop boundaries. Zero on both is how an uncrop
// travels: there is no tombstone for a crop, because the track itself still
// exists and the file was never modified.
func (s *Server) emitTrackCrop(ctx context.Context, trackID string, startMs, endMs int) {
	if s.deps.Crop == nil {
		return
	}
	catalogID := s.deps.Crop.CatalogIDForTrack(ctx, trackID)
	s.emitTrackFieldChange(ctx, catalogID, materialize.FieldCropStartMs, startMs)
	s.emitTrackFieldChange(ctx, catalogID, materialize.FieldCropEndMs, endMs)
}
