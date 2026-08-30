package materialize_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/crop"
	"github.com/uhhhm/reverb/internal/materialize"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
	"github.com/uhhhm/reverb/internal/syncemit"
)

func newPeerStore(t *testing.T) (*store.Store, *reverbsync.SyncStore, *catalog.Service) {
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
		ID: "dev_peer", Name: "peer", TokenHash: "hash",
	}); err != nil {
		t.Fatal(err)
	}
	n := 0
	cat := catalog.NewService(st.Q(), time.Now, func() string { n++; return string(rune('a' + n)) })
	ss := reverbsync.NewSyncStore(st.Q())
	ss.SetMaterializer(materialize.New(override.New(st.Q()), crop.New(st.Q())).
		WithCatalog(cat).
		WithTrackStore(st.Q()))
	return st, ss, cat
}

var peerTrack = catalog.Identity{Kind: "track", Title: "Song", Artist: "Band", Album: "Record", DurationMs: 180000}

func receive(t *testing.T, ss *reverbsync.SyncStore, changes ...reverbsync.SyncChange) {
	t.Helper()
	for i := range changes {
		changes[i].DeviceID = "dev_peer"
		if changes[i].UpdatedAt == 0 {
			changes[i].UpdatedAt = 1000
		}
	}
	_, _, rejected, err := ss.Reconcile(context.Background(), "dev_peer", 0, changes)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 0 {
		t.Fatalf("rejected %v", rejected)
	}
}

// A play is only useful if the receiving device can say which track it was, so
// the entity has to be adopted before the play that names it.
func TestPeerPlayLandsInHistory(t *testing.T) {
	st, ss, _ := newPeerStore(t)
	ctx := context.Background()

	receive(t, ss,
		reverbsync.SyncChange{EntityType: reverbsync.EntityPlay, EntityID: "play_1", Field: syncemit.FieldRecord, Value: syncemit.Play{
			UserID: "local", CatalogID: "trk_remote", PlayedAt: 500, MsPlayed: 120000, Completed: true, CreatedAt: 500,
		}},
		reverbsync.SyncChange{EntityType: reverbsync.EntityCatalog, EntityID: "trk_remote", Field: syncemit.FieldIdentity, Value: peerTrack},
	)

	row, err := st.Q().GetPlay(ctx, "play_1")
	if err != nil {
		t.Fatalf("the peer's play was not recorded: %v", err)
	}
	if row.CatalogID != "trk_remote" || row.MsPlayed != 120000 {
		t.Fatalf("play = %+v, want the peer's catalog id and duration", row)
	}
}

// The same play arriving twice is one play. Re-sending a log is normal, and
// double-counting it would corrupt the listening stats it feeds.
func TestPeerPlayIsNotCountedTwice(t *testing.T) {
	st, ss, _ := newPeerStore(t)
	ctx := context.Background()
	rec := reverbsync.SyncChange{EntityType: reverbsync.EntityPlay, EntityID: "play_1", Field: syncemit.FieldRecord, Value: syncemit.Play{
		UserID: "local", CatalogID: "trk_remote", PlayedAt: 500, MsPlayed: 120000, CreatedAt: 500,
	}}
	entity := reverbsync.SyncChange{EntityType: reverbsync.EntityCatalog, EntityID: "trk_remote", Field: syncemit.FieldIdentity, Value: peerTrack}
	receive(t, ss, entity, rec)

	// A repeat loses the merge, so it never reaches the projection at all —
	// and even if it did, the insert ignores an id already present.
	_, _, _, err := ss.Reconcile(ctx, "dev_peer", 0, []reverbsync.SyncChange{rec})
	if err != nil {
		t.Fatal(err)
	}
	count, err := st.Q().CountPlaysByCatalog(ctx, db.CountPlaysByCatalogParams{UserID: "local", CatalogID: "trk_remote"})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("play count = %d, want 1", count)
	}
}

// A play whose track this device already knows under its own id has to be
// filed under that id, not forked onto the peer's.
func TestPeerPlayIsFiledUnderTheLocalCatalogID(t *testing.T) {
	st, ss, cat := newPeerStore(t)
	ctx := context.Background()
	local, err := cat.CanonicalFor(ctx, peerTrack)
	if err != nil {
		t.Fatal(err)
	}

	receive(t, ss,
		reverbsync.SyncChange{EntityType: reverbsync.EntityCatalog, EntityID: "trk_remote", Field: syncemit.FieldIdentity, Value: peerTrack},
		reverbsync.SyncChange{EntityType: reverbsync.EntityPlay, EntityID: "play_1", Field: syncemit.FieldRecord, Value: syncemit.Play{
			UserID: "local", CatalogID: "trk_remote", PlayedAt: 500, MsPlayed: 1000, CreatedAt: 500,
		}},
	)

	row, err := st.Q().GetPlay(ctx, "play_1")
	if err != nil {
		t.Fatal(err)
	}
	if row.CatalogID != local {
		t.Fatalf("play filed under %q, want the local entity %q", row.CatalogID, local)
	}
}

func TestPeerQualityOverrideLands(t *testing.T) {
	st, ss, _ := newPeerStore(t)
	receive(t, ss,
		reverbsync.SyncChange{EntityType: reverbsync.EntityCatalog, EntityID: "trk_remote", Field: syncemit.FieldIdentity, Value: peerTrack},
		reverbsync.SyncChange{EntityType: reverbsync.EntityTrack, EntityID: "trk_remote", Field: materialize.FieldQuality, Value: "lossless"},
	)
	row, err := st.Q().GetTrackQualityOverrideByCatalogID(context.Background(), nullString("trk_remote"))
	if err != nil {
		t.Fatalf("the peer's quality override was not applied: %v", err)
	}
	if row.Quality != "lossless" {
		t.Fatalf("quality = %q, want lossless", row.Quality)
	}
}

// Loudness is a measurement of the file, so a peer that has already measured it
// saves this device the ffmpeg pass.
func TestPeerLoudnessLands(t *testing.T) {
	st, ss, _ := newPeerStore(t)
	receive(t, ss,
		reverbsync.SyncChange{EntityType: reverbsync.EntityCatalog, EntityID: "trk_remote", Field: syncemit.FieldIdentity, Value: peerTrack},
		reverbsync.SyncChange{EntityType: reverbsync.EntityTrack, EntityID: "trk_remote", Field: materialize.FieldLoudnessGainDb, Value: -3.5},
	)
	row, err := st.Q().GetTrackLoudnessByCatalogID(context.Background(), nullString("trk_remote"))
	if err != nil {
		t.Fatalf("the peer's loudness was not applied: %v", err)
	}
	if row.GainDb != -3.5 {
		t.Fatalf("gain = %v, want -3.5", row.GainDb)
	}
}

// A rename keyed on a catalog id this device knows under a different id must
// land on the track it means, not on a stray key nothing reads.
func TestPeerRenameFollowsTheCatalogID(t *testing.T) {
	st, ss, cat := newPeerStore(t)
	ctx := context.Background()
	local, err := cat.CanonicalFor(ctx, peerTrack)
	if err != nil {
		t.Fatal(err)
	}
	receive(t, ss,
		reverbsync.SyncChange{EntityType: reverbsync.EntityCatalog, EntityID: "trk_remote", Field: syncemit.FieldIdentity, Value: peerTrack},
		reverbsync.SyncChange{EntityType: reverbsync.EntityTrack, EntityID: "trk_remote", Field: materialize.FieldTitle, Value: "Renamed"},
	)
	name, err := override.New(st.Q()).GetByCatalogID(ctx, local)
	if err != nil {
		t.Fatal(err)
	}
	if name.Title != "Renamed" {
		t.Fatalf("title under %q = %q, want Renamed", local, name.Title)
	}
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
