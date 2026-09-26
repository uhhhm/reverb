package offlineset_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/offlineset"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
)

func newTestStoreOff(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/offline.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

func createDeviceOff(t *testing.T, st *store.Store, id string, isServer int64) {
	t.Helper()
	if err := st.Q().CreateDevice(context.Background(), db.CreateDeviceParams{
		ID:        id,
		Name:      id,
		TokenHash: "hash_" + id,
		IsServer:  isServer,
	}); err != nil {
		t.Fatalf("create device %s: %v", id, err)
	}
}

func createPlaylistOff(t *testing.T, st *store.Store, id, name string) {
	t.Helper()
	_, err := st.Q().UpsertSyncedPlaylist(context.Background(), db.UpsertSyncedPlaylistParams{
		ID:         id,
		Source:     "spotify",
		ExternalID: "ext-" + id,
		Name:       name,
		CoverUrl:   "",
		TracksJson: "[]",
		Mode:       "once",
		CreatedAt:  time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("create playlist %s: %v", id, err)
	}
	// stamp last_synced_at
	_ = st.Q().UpdateSyncedPlaylistTracks(context.Background(), db.UpdateSyncedPlaylistTracksParams{
		Name:         name,
		CoverUrl:     "",
		TracksJson:   "[]",
		LastSyncedAt: time.Now().Unix(),
		ID:           id,
	})
}

// selection is an Offline set for a store whose server device is dev1, with a
// notifier that records what the set held at each notification.
type selection struct {
	*offlineset.Service
	notified [][]offlineset.Entry
}

func newSelection(t *testing.T, q offlineset.Querier) *selection {
	t.Helper()
	sel := &selection{}
	deviceID := func(context.Context) (string, error) { return "dev1", nil }
	sel.Service = offlineset.NewService(q, deviceID, func() {
		// The keeper reads the set when it hears the change, so the change
		// must already be stored.
		entries, err := sel.List(context.Background())
		if err != nil {
			t.Errorf("list at notification: %v", err)
		}
		sel.notified = append(sel.notified, entries)
	})
	return sel
}

// failingWrites is a store whose offline-set writes fail.
type failingWrites struct{ offlineset.Querier }

var errDiskFull = errors.New("disk full")

func (failingWrites) UpsertOfflineSet(context.Context, db.UpsertOfflineSetParams) error {
	return errDiskFull
}
func (failingWrites) DeleteOfflineSetEntry(context.Context, db.DeleteOfflineSetEntryParams) error {
	return errDiskFull
}

func TestSetKeepsAPlaylistOfflineAndWakesTheKeeper(t *testing.T) {
	st := newTestStoreOff(t)
	ctx := context.Background()
	createDeviceOff(t, st, "dev1", 1)
	createPlaylistOff(t, st, "pl1", "Road Trip")
	sel := newSelection(t, st.Q())

	e, err := sel.Set(ctx, "pl1", true)
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if e.DeviceID != "dev1" || e.PlaylistID != "pl1" || !e.Enabled || e.PlaylistName != "Road Trip" {
		t.Fatalf("Set returned %+v", e)
	}
	if delta := time.Now().UnixMilli() - e.UpdatedAt; delta < 0 || delta > 5000 {
		t.Fatalf("updatedAt %d is not now", e.UpdatedAt)
	}
	if len(sel.notified) != 1 || len(sel.notified[0]) != 1 || !sel.notified[0][0].Enabled {
		t.Fatalf("keeper saw %+v, want one notification holding the enabled playlist", sel.notified)
	}

	e, err = sel.Set(ctx, "pl1", false)
	if err != nil || e.Enabled {
		t.Fatalf("disable returned %+v, %v", e, err)
	}
	if len(sel.notified) != 2 || sel.notified[1][0].Enabled {
		t.Fatalf("keeper did not see the playlist disabled: %+v", sel.notified)
	}
}

func TestListShowsThisDevicesSetWithPlaylistNames(t *testing.T) {
	st := newTestStoreOff(t)
	ctx := context.Background()
	createDeviceOff(t, st, "dev1", 1)
	createDeviceOff(t, st, "dev2", 0)
	createPlaylistOff(t, st, "plA", "Alpha")
	createPlaylistOff(t, st, "plB", "Beta")
	createPlaylistOff(t, st, "plC", "Gamma")
	sel := newSelection(t, st.Q())
	for _, id := range []string{"plB", "plA"} {
		if _, err := sel.Set(ctx, id, true); err != nil {
			t.Fatal(err)
		}
	}
	// Another Device's choice is not this Device's Offline set.
	if err := st.Q().UpsertOfflineSet(ctx, db.UpsertOfflineSetParams{DeviceID: "dev2", PlaylistID: "plC", Enabled: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}

	list, err := sel.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].PlaylistID != "plA" || list[0].PlaylistName != "Alpha" || list[1].PlaylistID != "plB" || list[1].PlaylistName != "Beta" {
		t.Fatalf("list = %+v, want plA Alpha then plB Beta", list)
	}
}

func TestRemoveDropsAPlaylistAndWakesTheKeeper(t *testing.T) {
	st := newTestStoreOff(t)
	ctx := context.Background()
	createDeviceOff(t, st, "dev1", 1)
	createPlaylistOff(t, st, "pl1", "P1")
	createPlaylistOff(t, st, "pl2", "P2")
	sel := newSelection(t, st.Q())
	for _, id := range []string{"pl1", "pl2"} {
		if _, err := sel.Set(ctx, id, true); err != nil {
			t.Fatal(err)
		}
	}

	if err := sel.Remove(ctx, "pl1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	last := sel.notified[len(sel.notified)-1]
	if len(sel.notified) != 3 || len(last) != 1 || last[0].PlaylistID != "pl2" {
		t.Fatalf("keeper saw %+v after remove, want only pl2", sel.notified)
	}
	// Removing a playlist that is not in the set is not an error.
	if err := sel.Remove(ctx, "pl1"); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
}

// Every way a selection change can fail leaves the set as it was and the
// keeper undisturbed.
func TestFailedSelectionChangesNeitherTheSetNorTheKeeper(t *testing.T) {
	st := newTestStoreOff(t)
	ctx := context.Background()
	createDeviceOff(t, st, "dev1", 1)
	createPlaylistOff(t, st, "pl1", "P1")
	createPlaylistOff(t, st, "pl2", "P2")
	seed := newSelection(t, st.Q())
	if _, err := seed.Set(ctx, "pl1", true); err != nil {
		t.Fatal(err)
	}

	noDevice := &selection{}
	noDevice.Service = offlineset.NewService(st.Q(),
		func(context.Context) (string, error) { return "", errors.New("no server device") },
		func() { noDevice.notified = append(noDevice.notified, nil) })
	broken := newSelection(t, failingWrites{st.Q()})
	healthy := newSelection(t, st.Q())

	cases := []struct {
		name    string
		sel     *selection
		change  func(*offlineset.Service) error
		wantErr error
	}{
		{"set unknown playlist", healthy, func(s *offlineset.Service) error { _, err := s.Set(ctx, "nope", true); return err }, offlineset.ErrPlaylistNotFound},
		{"remove unknown playlist", healthy, func(s *offlineset.Service) error { return s.Remove(ctx, "nope") }, offlineset.ErrPlaylistNotFound},
		{"set without a device", noDevice, func(s *offlineset.Service) error { _, err := s.Set(ctx, "pl2", true); return err }, nil},
		{"remove without a device", noDevice, func(s *offlineset.Service) error { return s.Remove(ctx, "pl1") }, nil},
		{"set write fails", broken, func(s *offlineset.Service) error { _, err := s.Set(ctx, "pl2", true); return err }, errDiskFull},
		{"remove write fails", broken, func(s *offlineset.Service) error { return s.Remove(ctx, "pl1") }, errDiskFull},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.change(c.sel.Service)
			if err == nil {
				t.Fatal("change succeeded")
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if len(c.sel.notified) != 0 {
				t.Fatal("keeper was notified of a failed change")
			}
			list, err := healthy.List(ctx)
			if err != nil || len(list) != 1 || list[0].PlaylistID != "pl1" || !list[0].Enabled {
				t.Fatalf("set changed to %+v (%v)", list, err)
			}
		})
	}
}

func TestDesktopSelectionNeedsNoKeeper(t *testing.T) {
	st := newTestStoreOff(t)
	createDeviceOff(t, st, "dev1", 1)
	createPlaylistOff(t, st, "pl1", "P1")
	svc := offlineset.NewService(st.Q(), func(context.Context) (string, error) { return "dev1", nil }, nil)
	if _, err := svc.Set(context.Background(), "pl1", true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Remove(context.Background(), "pl1"); err != nil {
		t.Fatal(err)
	}
}

func TestDeletedPlaylistLeavesTheSet(t *testing.T) {
	st := newTestStoreOff(t)
	ctx := context.Background()
	createDeviceOff(t, st, "dev1", 1)
	createPlaylistOff(t, st, "pl1", "P1")
	createPlaylistOff(t, st, "pl2", "P2")
	sel := newSelection(t, st.Q())
	for _, id := range []string{"pl1", "pl2"} {
		if _, err := sel.Set(ctx, id, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Q().DeleteSyncedPlaylist(ctx, "pl1"); err != nil {
		t.Fatal(err)
	}
	list, err := sel.List(ctx)
	if err != nil || len(list) != 1 || list[0].PlaylistID != "pl2" {
		t.Fatalf("after delete list = %+v (%v)", list, err)
	}
}

// The Offline set is local to each Device (ADR 0003): no change replicates.
func TestOfflineSetNoSyncEmission(t *testing.T) {
	st := newTestStoreOff(t)
	ctx := context.Background()
	q := st.Q()
	createDeviceOff(t, st, "dev1", 1)
	createPlaylistOff(t, st, "pl1", "P1")
	sel := newSelection(t, q)

	steps := []func() error{
		func() error { _, err := sel.Set(ctx, "pl1", true); return err },
		func() error { _, err := sel.Set(ctx, "pl1", false); return err },
		func() error { _, err := sel.List(ctx); return err },
		func() error { return sel.Remove(ctx, "pl1") },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if n, _ := q.CountSyncChanges(ctx); n != 0 {
			t.Fatalf("step %d emitted %d sync_change rows", i, n)
		}
	}
}
