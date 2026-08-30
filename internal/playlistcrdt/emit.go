package playlistcrdt

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/fracidx"
	"github.com/uhhhm/reverb/internal/playlistsync"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// Log is the change log a Service reads its own published state back out of and
// writes to. *sync.SyncStore satisfies it.
type Log interface {
	ListLatestForEntity(ctx context.Context, entityType, entityID string) ([]reverbsync.SyncChange, error)
	AppendChange(ctx context.Context, deviceID string, ch reverbsync.SyncChange) (int64, error)
}

// DeviceResolver names the identity changes are authored under. Publishing is
// skipped when it returns "": an unpaired device has nothing to publish to.
type DeviceResolver func(ctx context.Context) string

// Service publishes local playlist edits into the change log and projects
// accepted changes back onto the playlist store.
type Service struct {
	log     Log
	store   playlistsync.Store
	device  DeviceResolver
	now     func() int64
	catalog CatalogResolver
}

// New constructs a Service. Any of log, store, or device may be absent, in
// which case the corresponding half is a no-op — that is how a build without
// pairing, or a test without a log, stays working.
func New(l Log, store playlistsync.Store, device DeviceResolver) *Service {
	return &Service{log: l, store: store, device: device, now: func() int64 { return time.Now().UnixMilli() }}
}

// Publish brings the log up to date with the playlist as it now stands.
//
// It diffs rather than taking instructions, so every mutation — rename, add,
// remove, reorder, settings — funnels through one call site and cannot be
// forgotten. Only what actually changed is written: re-publishing an unchanged
// playlist appends nothing.
func (s *Service) Publish(ctx context.Context, id string) {
	if s == nil || s.log == nil || s.store == nil || s.device == nil || id == "" {
		return
	}
	device := s.device(ctx)
	if device == "" {
		return
	}
	row, err := s.store.Get(ctx, id)
	if err != nil {
		return
	}
	published, err := s.log.ListLatestForEntity(ctx, reverbsync.EntityPlaylist, id)
	if err != nil {
		log.Printf("sync playlist %q: read log: %v", id, err)
		return
	}
	st := readState(published)
	if st.deleted {
		// A tombstone is final; republishing under it would be rejected anyway.
		return
	}
	at := s.now()

	for field, want := range map[string]any{
		FieldName:            row.Name,
		FieldCoverURL:        row.CoverURL,
		FieldMode:            row.Mode,
		FieldSource:          row.Source,
		FieldExternalID:      row.ExternalID,
		FieldSyncEnabled:     row.SyncEnabled,
		FieldSyncIntervalSec: row.SyncIntervalSec,
		FieldAutoDownload:    row.AutoDownload,
		FieldCreatedAt:       row.CreatedAt,
	} {
		if !sameScalar(st.fields[field], want) {
			s.append(ctx, device, id, field, want, at)
		}
	}

	if !tracksReplicate(row.Mode) {
		return
	}
	s.publishMembers(ctx, device, id, st, decodeTracks(row.TracksJSON), at)
}

// publishMembers writes the membership and ordering diff. Additions and
// removals are one field each; positions are only rewritten for the tracks that
// actually moved, so a concurrent move of a different track on another device
// survives the merge.
func (s *Service) publishMembers(ctx context.Context, device, id string, st state, tracks []core.ExternalResult, at int64) {
	wantOrder := make([]string, 0, len(tracks))
	want := make(map[string]core.ExternalResult, len(tracks))
	for _, t := range tracks {
		k := MemberKey(t.Source, t.ExternalID)
		if _, dup := want[k]; dup {
			continue
		}
		want[k] = t
		wantOrder = append(wantOrder, k)
	}

	for k, m := range st.members {
		if !m.Present {
			continue
		}
		if _, kept := want[k]; !kept {
			s.append(ctx, device, id, k, member{Present: false}, at)
		}
	}

	moved := fracidx.Assign(st.orderKeys(), wantOrder)
	for _, k := range wantOrder {
		prev := st.members[k]
		order := prev.Order
		if o, ok := moved[k]; ok {
			order = o
		}
		entry := want[k]
		if prev.Present && prev.Order == order && sameJSON(prev.Entry, &entry) {
			continue
		}
		s.append(ctx, device, id, k, member{Present: true, Order: order, Entry: &entry}, at)
	}
}

func (s *Service) append(ctx context.Context, device, id, field string, value any, at int64) {
	if _, err := s.log.AppendChange(ctx, device, reverbsync.SyncChange{
		EntityType: reverbsync.EntityPlaylist,
		EntityID:   id,
		Field:      field,
		Value:      value,
		UpdatedAt:  at,
	}); err != nil {
		log.Printf("sync playlist %q field %q: %v", id, field, err)
	}
}

// sameScalar compares a value read back out of the log with the one the store
// holds. The log round-trips through JSON, so an int comes back as a float and
// the two cannot be compared directly.
func sameScalar(logged, want any) bool {
	if logged == nil {
		return false
	}
	return sameJSON(logged, want)
}

func sameJSON(a, b any) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(ab) == string(bb)
}
