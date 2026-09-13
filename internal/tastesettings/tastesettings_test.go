package tastesettings_test

import (
	"context"
	"errors"
	"testing"

	"github.com/uhhhm/reverb/internal/crop"
	"github.com/uhhhm/reverb/internal/materialize"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
	"github.com/uhhhm/reverb/internal/syncemit"
	"github.com/uhhhm/reverb/internal/tastesettings"
)

// device is one running Reverb: its own database, change log and settings.
type device struct {
	id       string
	log      *reverbsync.SyncStore
	settings *tastesettings.Service
}

func newDevice(t *testing.T, id string, peers ...string) *device {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/reverb.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i, d := range append([]string{id}, peers...) {
		isServer := int64(0)
		if i == 0 {
			isServer = 1
		}
		if err := st.Q().CreateDevice(ctx, db.CreateDeviceParams{ID: d, Name: d, TokenHash: "hash_" + d, IsServer: isServer}); err != nil {
			t.Fatal(err)
		}
	}
	log := reverbsync.NewSyncStore(st.Q())
	settings := tastesettings.New(st.Q(), syncemit.New(log, nil, func(context.Context) string { return id }))
	log.SetMaterializer(materialize.New(override.New(st.Q()), crop.New(st.Q())).WithTasteSettings(settings))
	return &device{id: id, log: log, settings: settings}
}

func syncTo(t *testing.T, from, to *device) {
	t.Helper()
	changes, err := from.log.ListSince(context.Background(), 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := to.log.Reconcile(context.Background(), from.id, 0, changes); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, d *device) tastesettings.Settings {
	t.Helper()
	got, err := d.settings.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDefaultsUntilChanged(t *testing.T) {
	if got := get(t, newDevice(t, "dev_a")); got != tastesettings.Defaults() || !got.OnlineRecommendations || got.Adventurousness != 50 {
		t.Fatalf("got %+v, want defaults", got)
	}
}

func TestUpdateChangesOnlyWhatIsSet(t *testing.T) {
	d := newDevice(t, "dev_a")
	off := false
	if _, err := d.settings.Update(context.Background(), tastesettings.Patch{OnlineRecommendations: &off}); err != nil {
		t.Fatal(err)
	}
	n := 80
	got, err := d.settings.Update(context.Background(), tastesettings.Patch{Adventurousness: &n})
	if err != nil {
		t.Fatal(err)
	}
	if want := (tastesettings.Settings{Adventurousness: 80, OnlineRecommendations: false}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestAdventurousnessOutOfRangeIsRejected(t *testing.T) {
	d := newDevice(t, "dev_a")
	for _, n := range []int{-1, 101} {
		if _, err := d.settings.Update(context.Background(), tastesettings.Patch{Adventurousness: &n}); !errors.Is(err, tastesettings.ErrInvalid) {
			t.Fatalf("adventurousness %d: err = %v, want ErrInvalid", n, err)
		}
	}
}

func TestSettingsReachPairedDevices(t *testing.T) {
	a := newDevice(t, "dev_a", "dev_b")
	b := newDevice(t, "dev_b", "dev_a")
	n, off := 20, false
	if _, err := a.settings.Update(context.Background(), tastesettings.Patch{Adventurousness: &n, OnlineRecommendations: &off}); err != nil {
		t.Fatal(err)
	}
	syncTo(t, a, b)
	if got, want := get(t, b), (tastesettings.Settings{Adventurousness: 20, OnlineRecommendations: false}); got != want {
		t.Fatalf("peer has %+v, want %+v", got, want)
	}

	// A later change on the peer wins back on the first device.
	on := true
	if _, err := b.settings.Update(context.Background(), tastesettings.Patch{OnlineRecommendations: &on}); err != nil {
		t.Fatal(err)
	}
	syncTo(t, b, a)
	if got := get(t, a); !got.OnlineRecommendations || got.Adventurousness != 20 {
		t.Fatalf("first device has %+v after the peer switched online back on", got)
	}
}
