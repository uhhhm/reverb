// Package materialize projects accepted sync changes onto the tables they
// describe.
//
// The change log replicates facts ("track X's title is Y"); nothing in it is
// visible until something writes those facts into the tables the app reads.
// This is that step, and it is deliberately one-way: it writes through the
// domain services WITHOUT appending new changes, so applying what a peer sent
// cannot echo back to that peer as a fresh change.
package materialize

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/crop"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
	"github.com/uhhhm/reverb/internal/syncemit"
)

// Fields carried for a track. Renames and crops are per-field LWW, so each
// arrives independently and must be merged into the existing row rather than
// replacing it — a title that arrived alone must not wipe the artist.
const (
	FieldTitle       = reverbsync.FieldTitle
	FieldArtist      = reverbsync.FieldArtist
	FieldCropStartMs = reverbsync.FieldCropStartMs
	FieldCropEndMs   = reverbsync.FieldCropEndMs
	// FieldQuality is the tier a track is (re-)fetched at.
	FieldQuality = reverbsync.FieldQuality
	// FieldLoudnessGainDb is the measured playback gain. It is a measurement of
	// the file, not a preference, so replicating it saves every other device an
	// ffmpeg pass over a track it already has.
	FieldLoudnessGainDb = reverbsync.FieldLoudnessGainDb
	// FieldDeleted is the tombstone sentinel, owned by the deletion path.
	FieldDeleted = reverbsync.FieldDeleted
)

// EntityTrack is the entity type track metadata syncs under. The id is the
// catalog id: backend track ids are local to one library backend, so two
// devices would never agree on them.
const EntityTrack = reverbsync.EntityTrack

// Catalog adopts entities a peer minted and translates the ids they were minted
// under. *catalog.Service satisfies it.
type Catalog interface {
	Adopt(ctx context.Context, remoteID string, id catalog.Identity) (string, error)
	Resolve(ctx context.Context, catalogID string) string
}

// Playlists rebuilds one playlist from the log. *playlistcrdt.Service satisfies it.
type Playlists interface {
	Apply(ctx context.Context, playlistID string) error
}

// TrackStore writes the per-track tables that are keyed on a catalog id.
// *db.Queries satisfies it.
type TrackStore interface {
	GetBackendIDByCatalogID(ctx context.Context, catalogID string) (string, error)
	UpsertTrackQualityOverrideByCatalogID(ctx context.Context, arg db.UpsertTrackQualityOverrideByCatalogIDParams) error
	DeleteTrackQualityOverrideByCatalogID(ctx context.Context, catalogID sql.NullString) error
	UpsertTrackLoudnessByCatalogID(ctx context.Context, arg db.UpsertTrackLoudnessByCatalogIDParams) error
	InsertPlayIfAbsent(ctx context.Context, arg db.InsertPlayIfAbsentParams) error
}

type Service struct {
	overrides *override.Service
	crops     *crop.Service
	catalog   Catalog
	playlists Playlists
	tracks    TrackStore
}

func New(overrides *override.Service, crops *crop.Service) *Service {
	return &Service{overrides: overrides, crops: crops}
}

// WithCatalog attaches catalog adoption. Without it, catalog entities still
// replicate but land nowhere, and every catalog id a peer sends is taken at
// face value.
func (s *Service) WithCatalog(c Catalog) *Service { s.catalog = c; return s }

// WithPlaylists attaches the playlist projection.
func (s *Service) WithPlaylists(p Playlists) *Service { s.playlists = p; return s }

// WithTrackStore attaches the per-track and play tables.
func (s *Service) WithTrackStore(t TrackStore) *Service { s.tracks = t; return s }

// Apply writes one accepted change into the table it belongs to. Unknown
// entity types and fields are ignored, not errors: a peer on a newer version
// may legitimately send fields this one does not understand yet, and the change
// stays in the log to be materialized after an upgrade.
func (s *Service) Apply(ctx context.Context, ch reverbsync.SyncChange) error {
	if s == nil || ch.EntityID == "" {
		return nil
	}
	switch ch.EntityType {
	case reverbsync.EntityCatalog:
		return s.applyCatalogEntity(ctx, ch)
	case reverbsync.EntityPlaylist:
		if s.playlists == nil {
			return nil
		}
		return s.playlists.Apply(ctx, ch.EntityID)
	case reverbsync.EntityPlay:
		return s.applyPlay(ctx, ch)
	case EntityTrack:
		return s.applyTrack(ctx, ch)
	default:
		return nil
	}
}

func (s *Service) applyTrack(ctx context.Context, ch reverbsync.SyncChange) error {
	// The id was minted on the device that sent it. Translating it here is what
	// lets an edit made on one device land on the right track on another.
	ch.EntityID = s.resolveCatalogID(ctx, ch.EntityID)
	switch ch.Field {
	case FieldTitle, FieldArtist:
		return s.applyName(ctx, ch)
	case FieldCropStartMs, FieldCropEndMs:
		return s.applyCrop(ctx, ch)
	case FieldQuality:
		return s.applyQuality(ctx, ch)
	case FieldLoudnessGainDb:
		return s.applyLoudness(ctx, ch)
	default:
		return nil
	}
}

func (s *Service) resolveCatalogID(ctx context.Context, id string) string {
	if s.catalog == nil {
		return id
	}
	return s.catalog.Resolve(ctx, id)
}

// applyCatalogEntity records a catalog entity a peer minted. Everything else
// keyed on a catalog id is meaningless until this has run, which is why the
// change log applies catalog entities ahead of the rest of a batch.
func (s *Service) applyCatalogEntity(ctx context.Context, ch reverbsync.SyncChange) error {
	if s.catalog == nil || ch.Field != reverbsync.FieldIdentity {
		return nil
	}
	var id catalog.Identity
	if err := decodeValue(ch, &id); err != nil {
		return err
	}
	_, err := s.catalog.Adopt(ctx, ch.EntityID, id)
	return err
}

// applyPlay records a play a peer reported. Plays are immutable facts under
// unique ids, so a repeat is the same play arriving twice and is ignored rather
// than double-counted.
func (s *Service) applyPlay(ctx context.Context, ch reverbsync.SyncChange) error {
	if s.tracks == nil || ch.Field != reverbsync.FieldRecord {
		return nil
	}
	var p syncemit.Play
	if err := decodeValue(ch, &p); err != nil {
		return err
	}
	cid := s.resolveCatalogID(ctx, p.CatalogID)
	if cid == "" {
		return nil
	}
	completed := int64(0)
	if p.Completed {
		completed = 1
	}
	createdAt := p.CreatedAt
	if createdAt == 0 {
		createdAt = time.Now().Unix()
	}
	return s.tracks.InsertPlayIfAbsent(ctx, db.InsertPlayIfAbsentParams{
		ID:        ch.EntityID,
		UserID:    p.UserID,
		CatalogID: cid,
		PlayedAt:  p.PlayedAt,
		MsPlayed:  int64(p.MsPlayed),
		Completed: completed,
		CreatedAt: createdAt,
	})
}

// backendIDFor returns the library track id a catalog id is bound to, falling
// back to the catalog id itself. The per-track tables are keyed on the backend
// id so the read paths find them; a track with no binding yet is parked under
// its catalog id until one exists.
func (s *Service) backendIDFor(ctx context.Context, catalogID string) string {
	if id, err := s.tracks.GetBackendIDByCatalogID(ctx, catalogID); err == nil && id != "" {
		return id
	}
	return catalogID
}

func (s *Service) applyQuality(ctx context.Context, ch reverbsync.SyncChange) error {
	if s.tracks == nil {
		return nil
	}
	quality, err := stringValue(ch.Value)
	if err != nil {
		return err
	}
	catalogID := sql.NullString{String: ch.EntityID, Valid: true}
	if quality == "" {
		return s.tracks.DeleteTrackQualityOverrideByCatalogID(ctx, catalogID)
	}
	return s.tracks.UpsertTrackQualityOverrideByCatalogID(ctx, db.UpsertTrackQualityOverrideByCatalogIDParams{
		TrackID:   s.backendIDFor(ctx, ch.EntityID),
		Quality:   quality,
		UpdatedAt: time.Now().Unix(),
		CatalogID: catalogID,
	})
}

func (s *Service) applyLoudness(ctx context.Context, ch reverbsync.SyncChange) error {
	if s.tracks == nil {
		return nil
	}
	gain, ok := ch.Value.(float64)
	if !ok {
		return fmt.Errorf("materialize: expected a number, got %T", ch.Value)
	}
	return s.tracks.UpsertTrackLoudnessByCatalogID(ctx, db.UpsertTrackLoudnessByCatalogIDParams{
		TrackID:   s.backendIDFor(ctx, ch.EntityID),
		GainDb:    gain,
		UpdatedAt: time.Now().Unix(),
		CatalogID: sql.NullString{String: ch.EntityID, Valid: true},
	})
}

func (s *Service) applyName(ctx context.Context, ch reverbsync.SyncChange) error {
	if s.overrides == nil {
		return nil
	}
	value, err := stringValue(ch.Value)
	if err != nil {
		return err
	}
	current, err := s.overrides.GetByCatalogID(ctx, ch.EntityID)
	if err != nil {
		return err
	}
	if ch.Field == FieldTitle {
		current.Title = value
	} else {
		current.Artist = value
	}
	return s.overrides.SetByCatalogID(ctx, ch.EntityID, current)
}

func (s *Service) applyCrop(ctx context.Context, ch reverbsync.SyncChange) error {
	if s.crops == nil {
		return nil
	}
	value, err := intValue(ch.Value)
	if err != nil {
		return err
	}
	current, err := s.crops.GetByCatalogID(ctx, ch.EntityID)
	if err != nil {
		return err
	}
	if ch.Field == FieldCropStartMs {
		current.StartMs = value
	} else {
		current.EndMs = value
	}
	// Both boundaries back at the ends of the file is an uncrop, not a crop of
	// the whole track, so it removes the row.
	if current.StartMs <= 0 && current.EndMs <= 0 {
		return s.crops.ClearByCatalogID(ctx, ch.EntityID)
	}
	return s.crops.SetByCatalogID(ctx, ch.EntityID, current)
}

// stringValue reads a change's value as a string. Values round-trip through
// JSON, so a value that arrived as raw JSON bytes is decoded rather than
// stringified into `"x"`.
func stringValue(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case json.RawMessage:
		var out string
		if err := json.Unmarshal(t, &out); err != nil {
			return "", err
		}
		return out, nil
	default:
		return "", fmt.Errorf("materialize: expected a string, got %T", v)
	}
}

// intValue reads a change's value as an int. JSON decoding yields float64 for
// every number, so that is the common case rather than an oddity.
func intValue(v any) (int, error) {
	switch t := v.(type) {
	case nil:
		return 0, nil
	case float64:
		return int(t), nil
	case int:
		return t, nil
	case int64:
		return int(t), nil
	case json.Number:
		n, err := t.Int64()
		return int(n), err
	default:
		return 0, fmt.Errorf("materialize: expected a number, got %T", v)
	}
}

// decodeValue reads a structured change value into target.
//
// A change that came off the wire carries the exact bytes its signature covers,
// and those are preferred. One appended locally has only the Go value, so it is
// re-encoded — the projection has to work for both, because a device
// materializes its own writes on the same path as a peer's.
func decodeValue(ch reverbsync.SyncChange, target any) error {
	raw := []byte(ch.ValueJSON)
	if len(raw) == 0 {
		encoded, err := json.Marshal(ch.Value)
		if err != nil {
			return err
		}
		raw = encoded
	}
	return json.Unmarshal(raw, target)
}
