package metadata

import (
	"context"
	"errors"
	"github.com/uhhhm/reverb/internal/crop"
	"github.com/uhhhm/reverb/internal/override"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
	"github.com/uhhhm/reverb/internal/syncemit"
	"testing"
)

type memoryNames struct {
	name override.Name
	fail error
}

func (n *memoryNames) Get(context.Context, string) (override.Name, error) { return n.name, nil }
func (n *memoryNames) Set(_ context.Context, _ string, v override.Name) error {
	if n.fail != nil {
		return n.fail
	}
	n.name = v
	return nil
}
func (*memoryNames) CatalogIDForTrack(context.Context, string) string { return "trk_catalog" }

type memoryCrops struct {
	points    crop.Points
	catalogID string
}

func (c *memoryCrops) Set(_ context.Context, _ string, p crop.Points) error { c.points = p; return nil }
func (c *memoryCrops) Clear(context.Context, string) error                  { c.points = crop.Points{}; return nil }
func (c *memoryCrops) CatalogIDForTrack(context.Context, string) string     { return c.catalogID }

type recordingLog struct {
	changes []reverbsync.SyncChange
	fail    error
}

func (*recordingLog) ListLatestForEntity(context.Context, string, string) ([]reverbsync.SyncChange, error) {
	return nil, nil
}
func (l *recordingLog) AppendChange(_ context.Context, _ string, c reverbsync.SyncChange) (int64, error) {
	l.changes = append(l.changes, c)
	return 0, l.fail
}

func TestRenamePreservesOmissionsAndLocalEditOnPublicationFailure(t *testing.T) {
	names := &memoryNames{name: override.Name{Title: "old", Artist: "artist", Album: "album"}}
	log := &recordingLog{fail: errors.New("log unavailable")}
	edits := New(names, nil, syncemit.New(log, nil, func(context.Context) string { return "device" }))
	clear := ""
	got, err := edits.Rename(context.Background(), BackendID("backend"), NamePatch{Title: &clear})
	if err != nil || got.Title != "" || got.Artist != "artist" || got.Album != "album" {
		t.Fatalf("%+v %v", got, err)
	}
	if len(log.changes) != 3 {
		t.Fatalf("published %d fields", len(log.changes))
	}
	for _, ch := range log.changes {
		if ch.EntityID != "trk_catalog" {
			t.Fatal("published backend ID")
		}
	}
	names.fail = errors.New("local write failed")
	_, err = edits.Rename(context.Background(), BackendID("backend"), NamePatch{})
	if !errors.Is(err, names.fail) || len(log.changes) != 3 {
		t.Fatal("failed local edit must not publish")
	}
}
func TestCropAndClearPublishCatalogFields(t *testing.T) {
	crops := &memoryCrops{catalogID: "trk_catalog"}
	log := &recordingLog{}
	edits := New(nil, crops, syncemit.New(log, nil, func(context.Context) string { return "device" }))
	if err := edits.SetCrop(context.Background(), BackendID("backend"), crop.Points{StartMs: 10, EndMs: 100}); err != nil {
		t.Fatal(err)
	}
	if err := edits.ClearCrop(context.Background(), BackendID("backend")); err != nil {
		t.Fatal(err)
	}
	if len(log.changes) != 4 || crops.points != (crop.Points{}) {
		t.Fatal("crop/clear not applied")
	}
	if log.changes[2].Value != 0 || log.changes[3].Value != 0 {
		t.Fatal("clear must publish zero endpoints")
	}
	crops.catalogID = ""
	if err := edits.SetCrop(context.Background(), BackendID("unbound"), crop.Points{EndMs: 200}); err != nil {
		t.Fatal(err)
	}
	if len(log.changes) != 4 || crops.points.EndMs != 200 {
		t.Fatal("unbound edit must remain local")
	}
}
