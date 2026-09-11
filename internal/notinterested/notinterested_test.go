package notinterested_test

import (
	"context"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/crop"
	"github.com/uhhhm/reverb/internal/materialize"
	"github.com/uhhhm/reverb/internal/notinterested"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
	"github.com/uhhhm/reverb/internal/syncemit"
)

// device is one running Reverb: its own database, change log and marks.
type device struct {
	id    string
	st    *store.Store
	log   *reverbsync.SyncStore
	marks *notinterested.Service
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
	marks := notinterested.New(st.Q(), syncemit.New(log, nil, func(context.Context) string { return id }))
	log.SetMaterializer(materialize.New(override.New(st.Q()), crop.New(st.Q())).WithNotInterested(marks))
	return &device{id: id, st: st, log: log, marks: marks}
}

// syncTo sends every change in from's log to to, the way a sync round does.
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

func list(t *testing.T, d *device) []notinterested.Mark {
	t.Helper()
	marks, err := d.marks.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return marks
}

func mark(t *testing.T, d *device, m notinterested.Mark) notinterested.Mark {
	t.Helper()
	got, err := d.marks.Mark(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestMarkAndUndo(t *testing.T) {
	d := newDevice(t, "dev_a")
	m := mark(t, d, notinterested.TrackMark("deezer", "3135556", "Harder, Better, Faster, Stronger", "Daft Punk"))
	marks := list(t, d)
	if len(marks) != 1 || marks[0].Key != m.Key || marks[0].Kind != notinterested.KindTrack || marks[0].Title != "Harder, Better, Faster, Stronger" {
		t.Fatalf("marks = %+v", marks)
	}
	if err := d.marks.Undo(context.Background(), m.Key); err != nil {
		t.Fatal(err)
	}
	if marks := list(t, d); len(marks) != 0 {
		t.Fatalf("after undo: %+v", marks)
	}
}

// A library track is keyed on its metadata, not on the backend id or the
// catalog id either device minted, so the same recording is one mark.
func TestSameRecordingIsOneMark(t *testing.T) {
	a := notinterested.LibraryTrackMark("One More Time", "Daft Punk", "Discovery", 320000)
	b := notinterested.LibraryTrackMark("One More Time", "Daft Punk; Romanthony", "Discovery", 321500)
	if a.Key != b.Key {
		t.Fatalf("keys differ for one recording: %s vs %s", a.Key, b.Key)
	}
	live := notinterested.LibraryTrackMark("One More Time (Live)", "Daft Punk", "Alive 2007", 320000)
	if live.Key == a.Key {
		t.Fatal("a live version must be a different mark")
	}
	if notinterested.ArtistMark("Daft Punk").Key != notinterested.ArtistMark("daft punk").Key {
		t.Fatal("artist keys must ignore case")
	}
}

func TestSetExcludesMarkedTracksAndArtists(t *testing.T) {
	d := newDevice(t, "dev_a")
	mark(t, d, notinterested.TrackMark("deezer", "1", "D.A.N.C.E.", "Justice"))
	mark(t, d, notinterested.ArtistMark("Stardust"))
	set, err := d.marks.Set(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		ext  core.ExternalResult
		want bool
	}{
		{"the marked result", core.ExternalResult{Source: "deezer", ExternalID: "1", Title: "D.A.N.C.E.", Artist: "Justice"}, true},
		{"the same recording from another source", core.ExternalResult{Source: "spotify", ExternalID: "x", Title: "D.A.N.C.E.", Artist: "Justice"}, true},
		{"the owned copy", core.ExternalResult{Source: "library", ExternalID: "lib-1", Title: "D.A.N.C.E.", Artist: "Justice"}, true},
		{"a track by a marked artist", core.ExternalResult{Source: "deezer", ExternalID: "2", Title: "Music Sounds Better with You", Artist: "Stardust"}, true},
		{"a composite credit led by a marked artist", core.ExternalResult{Source: "deezer", ExternalID: "3", Title: "Other", Artist: "Stardust feat. Someone"}, true},
		{"another track by the marked track's artist", core.ExternalResult{Source: "deezer", ExternalID: "4", Title: "Genesis", Artist: "Justice"}, false},
	} {
		if got := set.Track(tc.ext); got != tc.want {
			t.Errorf("%s: excluded = %v, want %v", tc.name, got, tc.want)
		}
	}
	if !set.Artist("stardust") || set.Artist("Justice") {
		t.Fatal("artist exclusion must follow artist marks only")
	}
}

func TestMarkReplicatesWithoutReemitting(t *testing.T) {
	a, b := newDevice(t, "dev_a", "dev_b"), newDevice(t, "dev_b", "dev_a")
	m := mark(t, a, notinterested.ArtistMark("Stardust"))
	syncTo(t, a, b)

	marks := list(t, b)
	if len(marks) != 1 || marks[0].Key != m.Key || marks[0].Artist != "Stardust" {
		t.Fatalf("peer marks = %+v", marks)
	}
	changes, err := b.log.ListSince(context.Background(), 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range changes {
		if ch.DeviceID == "dev_b" {
			t.Fatalf("applying a peer's mark emitted %+v", ch)
		}
	}
}

func TestUndoRemovesMarkOnEveryDevice(t *testing.T) {
	a, b := newDevice(t, "dev_a", "dev_b"), newDevice(t, "dev_b", "dev_a")
	m := mark(t, a, notinterested.TrackMark("deezer", "1", "D.A.N.C.E.", "Justice"))
	syncTo(t, a, b)
	if err := b.marks.Undo(context.Background(), m.Key); err != nil {
		t.Fatal(err)
	}
	syncTo(t, b, a)
	if marks := list(t, a); len(marks) != 0 {
		t.Fatalf("undo on the peer left %+v", marks)
	}
}

// One device undoes a mark while another re-marks it, neither having seen the
// other's change. After they sync both ways they must agree.
func TestConcurrentMarkAndUndoConverge(t *testing.T) {
	a, b := newDevice(t, "dev_a", "dev_b"), newDevice(t, "dev_b", "dev_a")
	m := mark(t, a, notinterested.TrackMark("deezer", "1", "D.A.N.C.E.", "Justice"))
	syncTo(t, a, b)

	if err := a.marks.Undo(context.Background(), m.Key); err != nil {
		t.Fatal(err)
	}
	// Clocks tick in milliseconds; a tie would fall to the device-id
	// tie-break instead of showing which write came later.
	time.Sleep(5 * time.Millisecond)
	mark(t, b, notinterested.TrackMark("deezer", "1", "D.A.N.C.E.", "Justice"))
	syncTo(t, a, b)
	syncTo(t, b, a)

	onA, onB := list(t, a), list(t, b)
	if len(onA) != len(onB) {
		t.Fatalf("diverged: a has %d marks, b has %d", len(onA), len(onB))
	}
	// The later write was b's re-mark, so it wins on both.
	if len(onA) != 1 {
		t.Fatalf("want the later re-mark to win, got %+v", onA)
	}
}
