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
	"encoding/json"
	"fmt"

	"github.com/uhhhm/reverb/internal/crop"
	"github.com/uhhhm/reverb/internal/override"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// Fields carried for a track. Renames and crops are per-field LWW, so each
// arrives independently and must be merged into the existing row rather than
// replacing it — a title that arrived alone must not wipe the artist.
const (
	FieldTitle       = "title"
	FieldArtist      = "artist"
	FieldCropStartMs = "cropStartMs"
	FieldCropEndMs   = "cropEndMs"
	// FieldDeleted is the tombstone sentinel, owned by the deletion path.
	FieldDeleted = "__deleted"
)

// EntityTrack is the entity type track metadata syncs under. The id is the
// catalog id: backend track ids are local to one library backend, so two
// devices would never agree on them.
const EntityTrack = "track"

type Service struct {
	overrides *override.Service
	crops     *crop.Service
}

func New(overrides *override.Service, crops *crop.Service) *Service {
	return &Service{overrides: overrides, crops: crops}
}

// Apply writes one accepted change into the table it belongs to. Unknown
// entity types and fields are ignored, not errors: a peer on a newer version
// may legitimately send fields this one does not understand yet, and the change
// stays in the log to be materialized after an upgrade.
func (s *Service) Apply(ctx context.Context, ch reverbsync.SyncChange) error {
	if s == nil || ch.EntityType != EntityTrack || ch.EntityID == "" {
		return nil
	}
	switch ch.Field {
	case FieldTitle, FieldArtist:
		return s.applyName(ctx, ch)
	case FieldCropStartMs, FieldCropEndMs:
		return s.applyCrop(ctx, ch)
	default:
		return nil
	}
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
