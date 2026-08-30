package syncemit

import (
	"context"
	"database/sql"
	"log"

	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// backfillKey guards the one-time publish. Replication only ever carried per-track
// renames and crops before, so a library built up over months would otherwise
// arrive at a newly paired device empty and only start filling from the next
// edit onwards.
const backfillKey = "sync:history_published"

// BackfillStore is everything the one-time publish reads. *db.Queries satisfies it.
type BackfillStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	UpsertSetting(ctx context.Context, arg db.UpsertSettingParams) error
	ListAllPlays(ctx context.Context) ([]db.Play, error)
	ListTrackCrops(ctx context.Context) ([]db.TrackCrop, error)
	ListTrackQualityOverrides(ctx context.Context) ([]db.TrackQualityOverride, error)
	ListTrackLoudness(ctx context.Context) ([]db.TrackLoudness, error)
	ListSyncedPlaylists(ctx context.Context) ([]db.SyncedPlaylist, error)
}

// Publisher publishes one playlist's current state. *playlistcrdt.Service
// satisfies it.
type Publisher interface {
	Publish(ctx context.Context, playlistID string)
}

// BackfillHistory publishes everything this device already had before it could
// replicate it. It runs at most once, and does nothing until the device has an
// identity to author under — an install that has never paired has nothing to
// say and would otherwise burn its one run producing nothing.
//
// Publishing is idempotent field by field, so a second pass would be harmless;
// the flag is there to keep boot cheap on a large history, not for correctness.
func (s *Service) BackfillHistory(ctx context.Context, store BackfillStore, playlists Publisher) {
	if !s.ready() || store == nil {
		return
	}
	if s.device(ctx) == "" {
		return
	}
	if done, _ := store.GetSetting(ctx, backfillKey); done == "true" {
		return
	}

	if playlists != nil {
		rows, err := store.ListSyncedPlaylists(ctx)
		if err != nil {
			log.Printf("sync backfill: list playlists: %v", err)
		}
		for _, row := range rows {
			playlists.Publish(ctx, row.ID)
		}
	}

	plays, err := store.ListAllPlays(ctx)
	if err != nil {
		log.Printf("sync backfill: list plays: %v", err)
	}
	for _, p := range plays {
		s.EmitPlay(ctx, p.ID, Play{
			UserID:    p.UserID,
			CatalogID: p.CatalogID,
			PlayedAt:  p.PlayedAt,
			MsPlayed:  int(p.MsPlayed),
			Completed: p.Completed != 0,
			CreatedAt: p.CreatedAt,
		})
	}

	// Rows still keyed only on a backend track id are skipped: without a catalog
	// id there is no identity a peer could match them against.
	crops, err := store.ListTrackCrops(ctx)
	if err != nil {
		log.Printf("sync backfill: list crops: %v", err)
	}
	for _, c := range crops {
		if cid := valid(c.CatalogID); cid != "" {
			s.EmitTrackField(ctx, cid, reverbsync.FieldCropStartMs, c.StartMs)
			s.EmitTrackField(ctx, cid, reverbsync.FieldCropEndMs, c.EndMs)
		}
	}
	quality, err := store.ListTrackQualityOverrides(ctx)
	if err != nil {
		log.Printf("sync backfill: list quality overrides: %v", err)
	}
	for _, q := range quality {
		if cid := valid(q.CatalogID); cid != "" {
			s.EmitTrackField(ctx, cid, reverbsync.FieldQuality, q.Quality)
		}
	}
	gains, err := store.ListTrackLoudness(ctx)
	if err != nil {
		log.Printf("sync backfill: list loudness: %v", err)
	}
	for _, g := range gains {
		if cid := valid(g.CatalogID); cid != "" {
			s.EmitTrackField(ctx, cid, reverbsync.FieldLoudnessGainDb, g.GainDb)
		}
	}

	if err := store.UpsertSetting(ctx, db.UpsertSettingParams{Key: backfillKey, Value: "true"}); err != nil {
		log.Printf("sync backfill: set flag: %v", err)
	}
	log.Printf("sync: published %d play(s) and existing playlists to paired devices", len(plays))
}

func valid(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}
