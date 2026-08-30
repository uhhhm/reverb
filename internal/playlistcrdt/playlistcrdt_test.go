package playlistcrdt_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/playlistcrdt"
	"github.com/uhhhm/reverb/internal/playlistsync"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
	"github.com/uhhhm/reverb/internal/wiring"
)

// device is one Reverb install: its own database, change log and playlist store.
type device struct {
	id    string
	st    *store.Store
	log   *reverbsync.SyncStore
	store playlistsync.Store
	crdt  *playlistcrdt.Service
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
	for _, p := range append([]string{id}, peers...) {
		if err := st.Q().CreateDevice(context.Background(), db.CreateDeviceParams{
			ID: p, Name: p, TokenHash: "hash_" + p,
		}); err != nil {
			t.Fatal(err)
		}
	}
	d := &device{id: id, st: st, log: reverbsync.NewSyncStore(st.Q()), store: wiring.NewSyncStore(st.Q())}
	d.crdt = playlistcrdt.New(d.log, d.store, func(context.Context) string { return id })
	d.log.SetMaterializer(materializer{d.crdt})
	return d
}

// materializer routes playlist changes into the projection, which is all these
// tests exercise.
type materializer struct{ p *playlistcrdt.Service }

func (m materializer) Apply(ctx context.Context, ch reverbsync.SyncChange) error {
	if ch.EntityType != reverbsync.EntityPlaylist {
		return nil
	}
	return m.p.Apply(ctx, ch.EntityID)
}

// save writes a playlist as a local edit would, then publishes it.
func (d *device) save(t *testing.T, id, name string, tracks []core.ExternalResult) {
	t.Helper()
	ctx := context.Background()
	encoded, err := json.Marshal(tracks)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.Upsert(ctx, core.SyncedPlaylist{
		ID: id, Source: "local", ExternalID: id, Name: name, Mode: "once",
	}, string(encoded), 1000); err != nil {
		t.Fatal(err)
	}
	if err := d.store.UpdateTracks(ctx, id, name, "", string(encoded), 1000); err != nil {
		t.Fatal(err)
	}
	d.crdt.Publish(ctx, id)
}

func (d *device) tracks(t *testing.T, id string) []string {
	t.Helper()
	row, err := d.store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get playlist %q on %s: %v", id, d.id, err)
	}
	var out []core.ExternalResult
	if err := json.Unmarshal([]byte(row.TracksJSON), &out); err != nil {
		t.Fatal(err)
	}
	titles := make([]string, 0, len(out))
	for _, tr := range out {
		titles = append(titles, tr.Title)
	}
	return titles
}

// push replicates everything from has been logged on from onto to.
func push(t *testing.T, from, to *device) {
	t.Helper()
	ctx := context.Background()
	changes, err := from.log.ListSince(ctx, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := to.log.Reconcile(ctx, from.id, 0, changes); err != nil {
		t.Fatalf("reconcile onto %s: %v", to.id, err)
	}
}

func track(title string) core.ExternalResult {
	return core.ExternalResult{Source: "library", ExternalID: title, Title: title, Type: core.EntityTrack}
}

func equal(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPlaylistReachesTheOtherDevice(t *testing.T) {
	a, b := newDevice(t, "dev_a", "dev_b"), newDevice(t, "dev_b", "dev_a")
	a.save(t, "pl1", "Roadtrip", []core.ExternalResult{track("one"), track("two")})
	push(t, a, b)

	if got := b.tracks(t, "pl1"); !equal(got, "one", "two") {
		t.Fatalf("tracks on B = %v, want [one two]", got)
	}
	row, _ := b.store.Get(context.Background(), "pl1")
	if row.Name != "Roadtrip" {
		t.Fatalf("name on B = %q, want Roadtrip", row.Name)
	}
}

// The case a whole-tracklist last-writer-wins scheme gets wrong: two devices
// each add a different track before they meet, and both additions must survive.
func TestConcurrentAdditionsBothSurvive(t *testing.T) {
	a, b := newDevice(t, "dev_a", "dev_b"), newDevice(t, "dev_b", "dev_a")
	base := []core.ExternalResult{track("one")}
	a.save(t, "pl1", "Roadtrip", base)
	push(t, a, b)

	a.save(t, "pl1", "Roadtrip", []core.ExternalResult{track("one"), track("from-a")})
	b.save(t, "pl1", "Roadtrip", []core.ExternalResult{track("one"), track("from-b")})
	push(t, a, b)
	push(t, b, a)

	for _, d := range []*device{a, b} {
		got := d.tracks(t, "pl1")
		if len(got) != 3 {
			t.Fatalf("tracks on %s = %v, want all three additions kept", d.id, got)
		}
		seen := map[string]bool{}
		for _, tr := range got {
			seen[tr] = true
		}
		if !seen["from-a"] || !seen["from-b"] {
			t.Fatalf("tracks on %s = %v, want both additions", d.id, got)
		}
	}
	if !equal(a.tracks(t, "pl1"), b.tracks(t, "pl1")...) {
		t.Fatalf("devices disagree on order: %v vs %v", a.tracks(t, "pl1"), b.tracks(t, "pl1"))
	}
}

func TestRemovalReplicates(t *testing.T) {
	a, b := newDevice(t, "dev_a", "dev_b"), newDevice(t, "dev_b", "dev_a")
	a.save(t, "pl1", "Roadtrip", []core.ExternalResult{track("one"), track("two")})
	push(t, a, b)

	a.save(t, "pl1", "Roadtrip", []core.ExternalResult{track("two")})
	push(t, a, b)

	if got := b.tracks(t, "pl1"); !equal(got, "two") {
		t.Fatalf("tracks on B = %v, want the removal applied", got)
	}
}

func TestReorderReplicates(t *testing.T) {
	a, b := newDevice(t, "dev_a", "dev_b"), newDevice(t, "dev_b", "dev_a")
	a.save(t, "pl1", "Roadtrip", []core.ExternalResult{track("one"), track("two"), track("three")})
	push(t, a, b)

	a.save(t, "pl1", "Roadtrip", []core.ExternalResult{track("three"), track("one"), track("two")})
	push(t, a, b)

	if got := b.tracks(t, "pl1"); !equal(got, "three", "one", "two") {
		t.Fatalf("tracks on B = %v, want [three one two]", got)
	}
}

// Republishing a playlist nobody touched must write nothing: an unchanged
// republish that appended would grow the log on every read.
func TestRepublishingAnUnchangedPlaylistIsSilent(t *testing.T) {
	a := newDevice(t, "dev_a")
	a.save(t, "pl1", "Roadtrip", []core.ExternalResult{track("one")})
	before, err := a.log.ListSince(context.Background(), 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	a.crdt.Publish(context.Background(), "pl1")
	after, err := a.log.ListSince(context.Background(), 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("republish appended %d changes, want none", len(after)-len(before))
	}
}

func TestDeletionRemovesThePlaylistOnThePeer(t *testing.T) {
	a, b := newDevice(t, "dev_a", "dev_b"), newDevice(t, "dev_b", "dev_a")
	a.save(t, "pl1", "Roadtrip", []core.ExternalResult{track("one")})
	push(t, a, b)

	ctx := context.Background()
	if _, err := reverbsync.NewDeletionService(a.log, a.st.Q()).DeletePlaylist(ctx, "dev_a", "pl1", 9000); err != nil {
		t.Fatal(err)
	}
	push(t, a, b)

	if _, err := b.store.Get(ctx, "pl1"); err == nil {
		t.Fatal("playlist still present on B after the tombstone replicated")
	}
}
