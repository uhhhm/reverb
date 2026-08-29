package materialize

import (
	"context"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/crop"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/store"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

func newService(t *testing.T) (*Service, *override.Service, *crop.Service) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/materialize.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	o, c := override.New(st.Q()), crop.New(st.Q())
	return New(o, c), o, c
}

func change(field string, value any) reverbsync.SyncChange {
	return reverbsync.SyncChange{EntityType: EntityTrack, EntityID: "cat_1", Field: field, Value: value, UpdatedAt: 1000}
}

// A rename that arrives from a peer has to become visible, which means writing
// it into track_override — the log alone changes nothing the app reads.
func TestApplyRenameBecomesVisible(t *testing.T) {
	svc, overrides, _ := newService(t)
	ctx := context.Background()

	if err := svc.Apply(ctx, change(FieldTitle, "Real Title")); err != nil {
		t.Fatal(err)
	}
	got, err := overrides.GetByCatalogID(ctx, "cat_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Real Title" {
		t.Fatalf("title = %q", got.Title)
	}
}

// Title and artist are separate LWW fields, so one arriving alone must merge
// into the row rather than blanking the other.
func TestApplyRenameMergesFields(t *testing.T) {
	svc, overrides, _ := newService(t)
	ctx := context.Background()

	if err := svc.Apply(ctx, change(FieldTitle, "Title")); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(ctx, change(FieldArtist, "Artist")); err != nil {
		t.Fatal(err)
	}
	got, _ := overrides.GetByCatalogID(ctx, "cat_1")
	if got.Title != "Title" || got.Artist != "Artist" {
		t.Fatalf("override = %+v, want both fields kept", got)
	}
}

func TestApplyCropBecomesVisible(t *testing.T) {
	svc, _, crops := newService(t)
	ctx := context.Background()

	// Values arrive JSON-decoded, so numbers are float64.
	if err := svc.Apply(ctx, change(FieldCropStartMs, float64(5000))); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(ctx, change(FieldCropEndMs, float64(90000))); err != nil {
		t.Fatal(err)
	}
	got, err := crops.GetByCatalogID(ctx, "cat_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.StartMs != 5000 || got.EndMs != 90000 {
		t.Fatalf("crop = %+v", got)
	}
}

// An uncrop travels as both boundaries going back to zero — there is no
// tombstone, because the track still exists and the file was never modified.
func TestApplyUncropRemovesTheCrop(t *testing.T) {
	svc, _, crops := newService(t)
	ctx := context.Background()

	if err := svc.Apply(ctx, change(FieldCropStartMs, float64(5000))); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(ctx, change(FieldCropEndMs, float64(90000))); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(ctx, change(FieldCropStartMs, float64(0))); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(ctx, change(FieldCropEndMs, float64(0))); err != nil {
		t.Fatal(err)
	}
	got, _ := crops.GetByCatalogID(ctx, "cat_1")
	if got != (crop.Points{}) {
		t.Fatalf("crop = %+v, want cleared", got)
	}
}

// A peer on a newer version may send fields this one has never heard of. That
// is not an error: the change stays in the log and materializes after upgrade.
func TestApplyIgnoresUnknownFieldsAndEntities(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()

	if err := svc.Apply(ctx, change("somethingNew", "x")); err != nil {
		t.Fatalf("unknown field must be ignored, got %v", err)
	}
	if err := svc.Apply(ctx, reverbsync.SyncChange{EntityType: "playlist", EntityID: "p1", Field: "name", Value: "x"}); err != nil {
		t.Fatalf("unknown entity must be ignored, got %v", err)
	}
	// Tombstones belong to the deletion path, not here.
	if err := svc.Apply(ctx, change(FieldDeleted, nil)); err != nil {
		t.Fatalf("tombstone must be ignored, got %v", err)
	}
}

func TestApplyRejectsAMistypedValue(t *testing.T) {
	svc, _, _ := newService(t)
	if err := svc.Apply(context.Background(), change(FieldCropStartMs, "not a number")); err == nil {
		t.Fatal("want an error for a non-numeric crop boundary")
	}
}

// Crops apply onto the tracks the app reads, keyed by catalog id when the
// backend binding is not known yet.
func TestAppliedCropReachesTrackPayloads(t *testing.T) {
	svc, _, crops := newService(t)
	ctx := context.Background()
	if err := svc.Apply(ctx, change(FieldCropStartMs, float64(4000))); err != nil {
		t.Fatal(err)
	}
	tracks := []core.Track{{ID: "cat_1"}}
	crops.ApplyTracks(ctx, tracks)
	if tracks[0].CropStartMs != 4000 {
		t.Fatalf("track crop = %d, want 4000", tracks[0].CropStartMs)
	}
}
