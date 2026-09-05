// Package metadata owns local rename and crop commands. Peer projection uses
// override.SetByCatalogID and crop.SetByCatalogID directly and never emits.
package metadata

import (
	"context"
	"github.com/uhhhm/reverb/internal/crop"
	"github.com/uhhhm/reverb/internal/override"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// BackendID addresses a track on this device; it must be resolved before emission.
type BackendID string

// Emitter accepts catalog IDs, never backend track IDs. Publication is best effort.
type Emitter interface {
	EmitTrackField(context.Context, string, string, any)
}

type Names interface {
	Get(context.Context, string) (override.Name, error)
	Set(context.Context, string, override.Name) error
	CatalogIDForTrack(context.Context, string) string
}
type Crops interface {
	Set(context.Context, string, crop.Points) error
	Clear(context.Context, string) error
	CatalogIDForTrack(context.Context, string) string
}

// NamePatch distinguishes omitted fields from explicit clears.
type NamePatch struct {
	Title  *string `json:"title"`
	Artist *string `json:"artist"`
	Album  *string `json:"album"`
}

type Edits struct {
	names Names
	crops Crops
	emit  Emitter
}

func New(names Names, crops Crops, emit Emitter) *Edits { return &Edits{names, crops, emit} }

func (e *Edits) Rename(ctx context.Context, id BackendID, p NamePatch) (override.Name, error) {
	cur, err := e.names.Get(ctx, string(id))
	if err != nil {
		return override.Name{}, err
	}
	if p.Title != nil {
		cur.Title = *p.Title
	}
	if p.Artist != nil {
		cur.Artist = *p.Artist
	}
	if p.Album != nil {
		cur.Album = *p.Album
	}
	if err := e.names.Set(ctx, string(id), cur); err != nil {
		return override.Name{}, err
	}
	name, err := e.names.Get(ctx, string(id))
	if err != nil {
		return override.Name{}, err
	}
	cid := e.names.CatalogIDForTrack(ctx, string(id))
	e.publish(ctx, cid, reverbsync.FieldTitle, name.Title)
	e.publish(ctx, cid, reverbsync.FieldArtist, name.Artist)
	e.publish(ctx, cid, reverbsync.FieldAlbum, name.Album)
	return name, nil
}
func (e *Edits) SetCrop(ctx context.Context, id BackendID, p crop.Points) error {
	if err := e.crops.Set(ctx, string(id), p); err != nil {
		return err
	}
	e.publishCrop(ctx, id, p)
	return nil
}
func (e *Edits) ClearCrop(ctx context.Context, id BackendID) error {
	if err := e.crops.Clear(ctx, string(id)); err != nil {
		return err
	}
	e.publishCrop(ctx, id, crop.Points{})
	return nil
}
func (e *Edits) publishCrop(ctx context.Context, id BackendID, p crop.Points) {
	cid := e.crops.CatalogIDForTrack(ctx, string(id))
	e.publish(ctx, cid, reverbsync.FieldCropStartMs, p.StartMs)
	e.publish(ctx, cid, reverbsync.FieldCropEndMs, p.EndMs)
}
func (e *Edits) publish(ctx context.Context, catalogID, field string, value any) {
	if catalogID != "" && e.emit != nil {
		e.emit.EmitTrackField(ctx, catalogID, field, value)
	}
}
