package tastesettings_test

import (
	"context"
	"errors"
	"testing"

	"github.com/uhhhm/reverb/internal/materialize"
	"github.com/uhhhm/reverb/internal/sync/pairtest"
	"github.com/uhhhm/reverb/internal/tastesettings"
)

// device is one running Reverb: its own database, change log and settings.
type device struct {
	*pairtest.Device
	settings *tastesettings.Service
}

func newDevice(t *testing.T, id string, peers ...string) *device {
	t.Helper()
	d := &device{}
	d.Device = pairtest.NewDevice(t, id, func(sd *pairtest.Device, m *materialize.Service) {
		d.settings = tastesettings.New(sd.Store.Q(), sd.Emitter())
		m.WithTasteSettings(d.settings)
	}, peers...)
	return d
}

func syncTo(t *testing.T, from, to *device) { pairtest.SyncTo(t, from.Device, to.Device) }

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
	pairtest.SyncToWithoutEcho(t, a.Device, b.Device)
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
