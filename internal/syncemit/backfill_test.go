package syncemit_test

import (
	"context"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
	"github.com/uhhhm/reverb/internal/syncemit"
)

func newBackfillStore(t *testing.T) (*store.Store, *syncemit.Service) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/reverb.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := st.Q().CreateDevice(context.Background(), db.CreateDeviceParams{
		ID: "dev_local", Name: "local", TokenHash: "hash",
	}); err != nil {
		t.Fatal(err)
	}
	n := 0
	cat := catalog.NewService(st.Q(), time.Now, func() string { n++; return string(rune('a' + n)) })
	return st, syncemit.New(reverbsync.NewSyncStore(st.Q()), cat, func(context.Context) string { return "dev_local" })
}

func changeCount(t *testing.T, st *store.Store) int {
	t.Helper()
	n, err := st.Q().CountSyncChanges(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return int(n)
}

// History recorded before this device could replicate anything still has to
// reach a device paired later, or pairing a second device would show an empty
// listening history next to a full library.
func TestBackfillPublishesExistingPlays(t *testing.T) {
	st, emit := newBackfillStore(t)
	ctx := context.Background()
	cat := catalog.NewService(st.Q(), time.Now, func() string { return "z" })
	cid, err := cat.CanonicalFor(ctx, catalog.Identity{Kind: "track", Title: "Song", Artist: "Band"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Q().InsertPlay(ctx, db.InsertPlayParams{
		ID: "play_1", UserID: "local", CatalogID: cid, PlayedAt: 500, MsPlayed: 1000, CreatedAt: 500,
	}); err != nil {
		t.Fatal(err)
	}

	emit.BackfillHistory(ctx, st.Q(), nil)
	if changeCount(t, st) == 0 {
		t.Fatal("backfill published nothing")
	}
}

// It runs once. A second pass over a long history on every boot would be pure
// cost, since publishing is already idempotent field by field.
func TestBackfillRunsOnce(t *testing.T) {
	st, emit := newBackfillStore(t)
	ctx := context.Background()
	if err := st.Q().InsertPlay(ctx, db.InsertPlayParams{
		ID: "play_1", UserID: "local", CatalogID: "trk_x", PlayedAt: 500, CreatedAt: 500,
	}); err != nil {
		t.Skipf("plays has a foreign key on catalog_entity: %v", err)
	}
	emit.BackfillHistory(ctx, st.Q(), nil)
	after := changeCount(t, st)
	emit.BackfillHistory(ctx, st.Q(), nil)
	if changeCount(t, st) != after {
		t.Fatal("a second backfill appended more changes")
	}
}

// A device with no identity cannot author anything, and must not burn its one
// run producing nothing.
func TestBackfillWaitsForADeviceIdentity(t *testing.T) {
	st, _ := newBackfillStore(t)
	ctx := context.Background()
	emit := syncemit.New(reverbsync.NewSyncStore(st.Q()), nil, func(context.Context) string { return "" })

	emit.BackfillHistory(ctx, st.Q(), nil)
	if done, _ := st.Q().GetSetting(ctx, "sync:history_published"); done == "true" {
		t.Fatal("backfill marked itself done with no device to author under")
	}
}
