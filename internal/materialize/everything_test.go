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
	"github.com/uhhhm/reverb/internal/play"
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

func TestPeerPlayCarriesRecommendationContextAndOlderPlayDefaultsToQualified(t *testing.T) {
	st, ss, _ := newPeerStore(t)
	ctx := context.Background()
	qualified := false
	receive(t, ss,
		reverbsync.SyncChange{EntityType: reverbsync.EntityCatalog, EntityID: "trk_remote", Field: syncemit.FieldIdentity, Value: peerTrack},
		reverbsync.SyncChange{EntityType: reverbsync.EntityPlay, EntityID: "play_skip", Field: syncemit.FieldRecord, Value: syncemit.Play{UserID: "local", CatalogID: "trk_remote", PlayedAt: 500, Origin: "radio", SessionID: "s1", Qualified: &qualified}},
		reverbsync.SyncChange{EntityType: reverbsync.EntityPlay, EntityID: "play_legacy", Field: syncemit.FieldRecord, Value: syncemit.Play{UserID: "local", CatalogID: "trk_remote", PlayedAt: 501}},
	)
	skip, _ := st.Q().GetPlay(ctx, "play_skip")
	legacy, _ := st.Q().GetPlay(ctx, "play_legacy")
	if skip.Origin != "radio" || skip.SessionID != "s1" || skip.Qualified != 0 || legacy.Qualified != 1 {
		t.Fatalf("skip=%+v legacy=%+v", skip, legacy)
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

func TestPeerPlayRetriesAfterCatalogArrivesInLaterRound(t *testing.T) {
	st, ss, _ := newPeerStore(t)
	receive(t, ss, reverbsync.SyncChange{EntityType: reverbsync.EntityPlay, EntityID: "play_late", Field: syncemit.FieldRecord, Value: syncemit.Play{UserID: "local", CatalogID: "trk_late", PlayedAt: 500}})
	receive(t, ss, reverbsync.SyncChange{EntityType: reverbsync.EntityCatalog, EntityID: "trk_late", Field: syncemit.FieldIdentity, Value: peerTrack})
	if _, err := st.Q().GetPlay(context.Background(), "play_late"); err != nil {
		t.Fatalf("play lost after catalog arrived: %v", err)
	}
}

func TestRecoverPlayHistoryAfterRestart(t *testing.T) {
	st, ss, cat := newPeerStore(t)
	ctx := context.Background()
	// The log committed, but the old process never projected the play.
	receive(t, ss, reverbsync.SyncChange{EntityType: reverbsync.EntityCatalog, EntityID: "trk_restart", Field: syncemit.FieldIdentity, Value: peerTrack})
	ss.SetMaterializer(nil)
	receive(t, ss, reverbsync.SyncChange{EntityType: reverbsync.EntityPlay, EntityID: "play_restart", Field: syncemit.FieldRecord, Value: syncemit.Play{UserID: "local", CatalogID: "trk_restart", PlayedAt: 500}})
	projector := materialize.New(nil, nil).WithCatalog(cat).WithTrackStore(st.Q())
	for i := 0; i < 2; i++ {
		if err := projector.RecoverPlays(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Q().GetPlay(ctx, "play_restart"); err != nil {
		t.Fatal(err)
	}
	plays, err := st.Q().ListAllPlays(ctx)
	if err != nil || len(plays) != 1 {
		t.Fatalf("plays = %d, %v", len(plays), err)
	}
}

func TestDeletedListeningHistoryReplicatesAndStaysDeletedAfterRecovery(t *testing.T) {
	ctx := context.Background()
	local, log, cat := newPeerStore(t)
	emitter := syncemit.New(log, cat, func(context.Context) string { return "dev_peer" })
	plays := play.NewService(local.Q(), cat, time.Now, func() string { return "deleted_play" }).WithEmitter(emitter)
	if err := plays.Record(ctx, "local", play.PlayInput{Title: "Song", Artist: "Band", MsPlayed: 60000}); err != nil {
		t.Fatal(err)
	}
	remote, remoteLog, remoteCat := newPeerStore(t)
	exchange := func() {
		t.Helper()
		changes, err := log.ListSince(ctx, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := remoteLog.Reconcile(ctx, "dev_peer", 0, changes); err != nil {
			t.Fatal(err)
		}
	}
	exchange()
	if _, err := remote.Q().GetPlay(ctx, "deleted_play"); err != nil {
		t.Fatal(err)
	}
	// A different owner cannot emit a tombstone for this play.
	if err := plays.Delete(ctx, "someone_else", "deleted_play"); err != nil {
		t.Fatal(err)
	}
	if tomb, _ := log.GetLatestForField(ctx, reverbsync.EntityPlay, "deleted_play", reverbsync.FieldDeleted); tomb != nil {
		t.Fatal("cross-owner tombstone")
	}
	if err := plays.Delete(ctx, "local", "deleted_play"); err != nil {
		t.Fatal(err)
	}
	exchange()
	for _, replica := range []struct {
		q   *db.Queries
		cat *catalog.Service
	}{{local.Q(), cat}, {remote.Q(), remoteCat}} {
		if err := materialize.New(nil, nil).WithCatalog(replica.cat).WithTrackStore(replica.q).RecoverPlays(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := replica.q.GetPlay(ctx, "deleted_play"); err != sql.ErrNoRows {
			t.Errorf("deleted play resurrected: %v", err)
		}
	}
}

// A peer can send a play whose value is not valid JSON. Stored, it made the
// deferred-play recovery query -- which selects by json_extract on value_json
// -- error for every catalog identity, so plays waiting on their identity were
// never projected and the retry loop logged the same failure forever. The sync
// boundary refuses the malformed change instead of storing it.
func TestMalformedPlayValueIsRefusedAndRecoveryStillDrains(t *testing.T) {
	st, ss, _ := newPeerStore(t)
	ctx := context.Background()

	_, _, rejected, err := ss.Reconcile(ctx, "dev_peer", 0, []reverbsync.SyncChange{{
		EntityType: reverbsync.EntityPlay, EntityID: "play_bad", Field: syncemit.FieldRecord,
		ValueJSON: `{"catalogId":"trk_late"`, UpdatedAt: 1000, DeviceID: "dev_peer",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 {
		t.Fatalf("rejected %d malformed changes, want 1", len(rejected))
	}
	if stored, err := ss.GetLatestForField(ctx, reverbsync.EntityPlay, "play_bad", syncemit.FieldRecord); err != nil {
		t.Fatal(err)
	} else if stored != nil {
		t.Fatalf("malformed change was stored: %+v", stored)
	}

	// A play whose catalog identity has not arrived yet waits for it, and is
	// projected when it does.
	receive(t, ss, reverbsync.SyncChange{EntityType: reverbsync.EntityPlay, EntityID: "play_late", Field: syncemit.FieldRecord, Value: syncemit.Play{UserID: "local", CatalogID: "trk_late", PlayedAt: 500}})
	receive(t, ss, reverbsync.SyncChange{EntityType: reverbsync.EntityCatalog, EntityID: "trk_late", Field: syncemit.FieldIdentity, Value: peerTrack})
	if _, err := st.Q().GetPlay(ctx, "play_late"); err != nil {
		t.Fatalf("deferred play never projected: %v", err)
	}

	if err := ss.RecoverProjection(ctx); err != nil {
		t.Fatalf("projection recovery: %v", err)
	}
	pending, err := st.Q().ListPendingSyncProjections(ctx, db.ListPendingSyncProjectionsParams{Revision: 0, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending projections = %d, want the queue drained", len(pending))
	}
}

// An earlier build stored malformed values before the boundary refused them.
// Recovery must skip such a row rather than fail the query that finds every
// other deferred play.
func TestMalformedPlayRowStoredByAnOlderBuildIsSkippedByRecovery(t *testing.T) {
	st, ss, cat := newPeerStore(t)
	ctx := context.Background()
	if _, err := st.Q().AppendSyncChangeWithHLC(ctx, db.AppendSyncChangeWithHLCParams{
		DeviceID: "dev_peer", EntityType: reverbsync.EntityPlay, EntityID: "play_legacy_bad",
		Field: syncemit.FieldRecord, ValueJson: `{"catalogId":"trk_late"`, UpdatedAt: 1000,
	}); err != nil {
		t.Fatal(err)
	}

	receive(t, ss, reverbsync.SyncChange{EntityType: reverbsync.EntityPlay, EntityID: "play_late", Field: syncemit.FieldRecord, Value: syncemit.Play{UserID: "local", CatalogID: "trk_late", PlayedAt: 500}})
	receive(t, ss, reverbsync.SyncChange{EntityType: reverbsync.EntityCatalog, EntityID: "trk_late", Field: syncemit.FieldIdentity, Value: peerTrack})
	if _, err := st.Q().GetPlay(ctx, "play_late"); err != nil {
		t.Fatalf("deferred play never projected past the malformed row: %v", err)
	}
	if err := materialize.New(nil, nil).WithCatalog(cat).WithTrackStore(st.Q()).RecoverPlays(ctx); err != nil {
		t.Fatalf("recover plays: %v", err)
	}
}

// Clearing an unreadable play out of the projection queue must not depend on
// the startup quarantine, which only runs once p2p signing is configured.
// Projection recovery alone drains it, so the retry loop stops reporting a
// failure that can never succeed.
func TestUnreadablePlayLeavesTheProjectionQueueWithoutTheStartupQuarantine(t *testing.T) {
	st, ss, _ := newPeerStore(t)
	ctx := context.Background()
	rev, err := st.Q().AppendSyncChangeWithHLC(ctx, db.AppendSyncChangeWithHLCParams{
		DeviceID: "dev_peer", EntityType: reverbsync.EntityPlay, EntityID: "play_legacy_bad",
		Field: syncemit.FieldRecord, ValueJson: `{"catalogId":"trk"`, UpdatedAt: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Q().MarkSyncProjectionPending(ctx, rev); err != nil {
		t.Fatal(err)
	}

	if err := ss.RecoverProjection(ctx); err != nil {
		t.Fatalf("projection recovery reports a failure that can never succeed: %v", err)
	}
	pending, err := st.Q().ListPendingSyncProjections(ctx, db.ListPendingSyncProjectionsParams{Revision: 0, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending projections = %d, want the queue drained", len(pending))
	}
}
