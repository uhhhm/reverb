// Package syncemit publishes locally-made changes into the sync log.
//
// It exists so that the services making those changes — catalog, play,
// playlists — do not each have to know how a device is identified, when a
// change is worth writing, or that a catalog id is meaningless to a peer until
// the entity behind it has been sent. They call a narrow interface; this
// package is the one place that knows the log.
package syncemit

import (
	"context"
	"log"
	"time"

	"github.com/uhhhm/reverb/internal/catalog"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// FieldIdentity carries a catalog entity's metadata. One field, not one per
// attribute: an entity's identity is what it is, and half of it arriving is not
// a partial track, it is a different track.
const FieldIdentity = reverbsync.FieldIdentity

// Log is the change log this package writes to. *sync.SyncStore satisfies it.
type Log interface {
	ListLatestForEntity(ctx context.Context, entityType, entityID string) ([]reverbsync.SyncChange, error)
	AppendChange(ctx context.Context, deviceID string, ch reverbsync.SyncChange) (int64, error)
}

// Catalog reads back an entity this device already minted, so a change keyed on
// an old catalog id can still publish the entity it names.
type Catalog interface {
	EntityIdentity(ctx context.Context, catalogID string) (catalog.Identity, error)
}

// DeviceResolver names the identity changes are authored under, or "" when this
// device has no identity yet and nothing can be published.
type DeviceResolver func(ctx context.Context) string

type Service struct {
	log    Log
	cat    Catalog
	device DeviceResolver
	now    func() int64
}

func New(l Log, cat Catalog, device DeviceResolver) *Service {
	return &Service{log: l, cat: cat, device: device, now: func() int64 { return time.Now().UnixMilli() }}
}

func (s *Service) ready() bool {
	return s != nil && s.log != nil && s.device != nil
}

// EmitCatalogEntity publishes a freshly minted catalog entity. It satisfies
// catalog.Emitter.
func (s *Service) EmitCatalogEntity(ctx context.Context, catalogID string, id catalog.Identity) {
	if !s.ready() || catalogID == "" {
		return
	}
	device := s.device(ctx)
	if device == "" {
		return
	}
	s.append(ctx, device, reverbsync.EntityCatalog, catalogID, FieldIdentity, id)
}

// EnsureCatalogEntity publishes the entity behind a catalog id if the log does
// not already carry it.
//
// Every other catalog-id-keyed change depends on this: the id is a random local
// token, so a peer receiving "track trk_xyz was renamed" has no way to work out
// which track that is. Entities minted before this device ever paired were
// never published, so a caller cannot assume minting already did it.
func (s *Service) EnsureCatalogEntity(ctx context.Context, catalogID string) {
	if !s.ready() || s.cat == nil || catalogID == "" {
		return
	}
	device := s.device(ctx)
	if device == "" {
		return
	}
	existing, err := s.log.ListLatestForEntity(ctx, reverbsync.EntityCatalog, catalogID)
	if err != nil {
		return
	}
	for _, ch := range existing {
		if ch.Field == FieldIdentity {
			return
		}
	}
	id, err := s.cat.EntityIdentity(ctx, catalogID)
	if err != nil {
		return
	}
	s.append(ctx, device, reverbsync.EntityCatalog, catalogID, FieldIdentity, id)
}

// Play is one play event as it travels. The user id travels with it because the
// log is not scoped to a user, and history that landed under a different id
// would never be read back.
type Play struct {
	UserID    string `json:"userId"`
	CatalogID string `json:"catalogId"`
	PlayedAt  int64  `json:"playedAt"`
	MsPlayed  int    `json:"msPlayed"`
	Completed bool   `json:"completed"`
	CreatedAt int64  `json:"createdAt"`
}

// EmitPlay publishes one play. Plays are immutable facts under unique ids, so
// they never conflict — the only thing replication has to get right is that the
// track they name is one the receiving device can identify.
func (s *Service) EmitPlay(ctx context.Context, playID string, p Play) {
	if !s.ready() || playID == "" {
		return
	}
	device := s.device(ctx)
	if device == "" {
		return
	}
	s.EnsureCatalogEntity(ctx, p.CatalogID)
	s.append(ctx, device, reverbsync.EntityPlay, playID, FieldRecord, p)
}

// FieldRecord is the single field a play travels under.
const FieldRecord = reverbsync.FieldRecord

// EmitTrackField publishes one per-track metadata field under a catalog id,
// making sure the entity it names has been published first.
func (s *Service) EmitTrackField(ctx context.Context, catalogID, field string, value any) {
	if !s.ready() || catalogID == "" {
		return
	}
	device := s.device(ctx)
	if device == "" {
		return
	}
	s.EnsureCatalogEntity(ctx, catalogID)
	s.append(ctx, device, reverbsync.EntityTrack, catalogID, field, value)
}

func (s *Service) append(ctx context.Context, device, entityType, entityID, field string, value any) {
	if _, err := s.log.AppendChange(ctx, device, reverbsync.SyncChange{
		EntityType: entityType,
		EntityID:   entityID,
		Field:      field,
		Value:      value,
		UpdatedAt:  s.now(),
	}); err != nil {
		log.Printf("sync %s/%s %s: %v", entityType, entityID, field, err)
	}
}
