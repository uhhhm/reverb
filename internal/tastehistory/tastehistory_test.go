package tastehistory

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
)

const linkedAt = 1_000_000

type fakeFetcher struct {
	tracks, artists []Count
	recent          []Scrobble
	err             error
	before          int64
	calls           int
	users           []string
	during          func()
}

func (f *fakeFetcher) TopTracks(_ context.Context, user string, _ int) ([]Count, error) {
	f.calls++
	f.users = append(f.users, user)
	return f.tracks, f.err
}
func (f *fakeFetcher) TopArtists(context.Context, string, int) ([]Count, error) {
	return f.artists, f.err
}
func (f *fakeFetcher) RecentTracks(_ context.Context, _ string, before int64, _ int) ([]Scrobble, error) {
	f.before = before
	if f.during != nil {
		f.during()
	}
	return f.recent, f.err
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "reverb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

func link(t *testing.T, q *db.Queries) {
	t.Helper()
	if err := q.UpsertScrobbleLink(context.Background(), db.UpsertScrobbleLinkParams{
		UserID: "owner", Provider: "lastfm", SessionKey: "sk", Username: "alice", Status: "active", CreatedAt: linkedAt,
	}); err != nil {
		t.Fatal(err)
	}
}

// sent records a scrobble Reverb itself delivered to Last.fm.
func sent(t *testing.T, q *db.Queries, id, artist, title string, at int64) {
	t.Helper()
	if err := q.InsertScrobbleQueue(context.Background(), db.InsertScrobbleQueueParams{
		ID: id, UserID: "owner", Provider: "lastfm", Title: title, Artist: artist, PlayedAt: at, Status: "done", CreatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
}

func newService(st *store.Store, f Fetcher) *Service {
	return New(st.DB(), f, func() time.Time { return time.Unix(2_000_000, 0) }, nil)
}

func TestImportCountsEveryListenOnceAndLeavesOutReverbsOwn(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	q := st.Q()
	link(t, q)
	// Reverb sent Song 2 under an earlier link, and Song 1 since linking.
	sent(t, q, "s1", "Artist B", "Song 2", 500_000)
	sent(t, q, "s2", "Artist A; Guest", "Song 1", 1_100_000)
	f := &fakeFetcher{
		tracks:  []Count{{Artist: "Artist A", Title: "Song 1", Plays: 10}, {Artist: "Artist B", Title: "Song 2", Plays: 1}},
		artists: []Count{{Artist: "Artist A", Plays: 12}, {Artist: "Artist B", Plays: 1}},
		recent: []Scrobble{
			{Artist: "Artist A", Title: "Song 1", At: 900_000},
			{Artist: "artist a", Title: "song 1", At: 900_100},
			{Artist: "Artist B", Title: "Song 2", At: 500_000},
		},
	}
	svc := newService(st, f)

	if err := svc.Import(ctx); err != nil {
		t.Fatal(err)
	}
	if f.before != linkedAt {
		t.Fatalf("recent scrobbles read before %d, want the link time %d", f.before, linkedAt)
	}
	got, err := svc.Signals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	day := int64(900_000 / secondsInDay * secondsInDay)
	want := []recommend.TasteSignal{
		// Two dated scrobbles of Song 1 on one day.
		{Kind: recommend.SignalHistory, Artist: "Artist A", Title: "Song 1", Plays: 2, At: day},
		// Ten listens less the two dated and the one Reverb sent.
		{Kind: recommend.SignalHistory, Artist: "Artist A", Title: "Song 1", Plays: 7, At: linkedAt},
		// Twelve less Reverb's one. Song 2 and Artist B were only Reverb's.
		{Kind: recommend.SignalHistory, Artist: "Artist A", Plays: 11, At: linkedAt},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("signals =\n%+v\nwant\n%+v", got, want)
	}
	if n, err := q.CountPlays(ctx); err != nil || n != 0 {
		t.Fatalf("imported history created %d plays (%v)", n, err)
	}
}

func TestReimportReplacesAndUnlinkRemoves(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	link(t, st.Q())
	f := &fakeFetcher{artists: []Count{{Artist: "A", Plays: 3}}}
	svc := newService(st, f)
	for range 2 {
		if err := svc.Import(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := svc.Signals(ctx); len(got) != 1 {
		t.Fatalf("after two imports %d signals, want 1", len(got))
	}

	svc.LinkChanged(ctx, "owner", "listenbrainz", false)
	if got, _ := svc.Signals(ctx); len(got) != 1 {
		t.Fatal("unlinking another provider removed Last.fm history")
	}
	svc.LinkChanged(ctx, "owner", "lastfm", false)
	if got, _ := svc.Signals(ctx); len(got) != 0 {
		t.Fatalf("after unlinking %d signals remain", len(got))
	}
	if _, err := svc.importedAt(ctx); err == nil {
		t.Fatal("import time kept after unlinking")
	}
}

func TestLinkingAnotherAccountDropsTheOldHistoryAtOnce(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	link(t, st.Q())
	online := true
	f := &fakeFetcher{artists: []Count{{Artist: "A", Plays: 3}}}
	svc := New(st.DB(), f, time.Now, func(context.Context) bool { return online })
	if err := svc.Import(ctx); err != nil {
		t.Fatal(err)
	}
	// With lookups off the new account's import reads nothing, so the old
	// history must not stand in for it.
	online = false
	svc.LinkChanged(ctx, "owner", "lastfm", true)
	if got, _ := svc.Signals(ctx); len(got) != 0 {
		t.Fatalf("old account's history kept: %+v", got)
	}
	if _, err := svc.importedAt(ctx); err == nil {
		t.Fatal("old import time kept, which would delay the next import a week")
	}
}

func TestImportDiscardedWhenUnlinkedWhileReading(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	link(t, st.Q())
	f := &fakeFetcher{artists: []Count{{Artist: "A", Plays: 3}}}
	f.during = func() {
		_ = st.Q().DeleteScrobbleLink(ctx, db.DeleteScrobbleLinkParams{UserID: "owner", Provider: "lastfm"})
	}
	svc := newService(st, f)
	if err := svc.Import(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Signals(ctx); len(got) != 0 {
		t.Fatalf("history stored for a removed link: %+v", got)
	}
}

func TestImportNeedsOnlineLookupsAndALink(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	f := &fakeFetcher{artists: []Count{{Artist: "A", Plays: 3}}}
	if err := newService(st, f).Import(ctx); err != nil || f.calls != 0 {
		t.Fatalf("import without a link: %v after %d calls", err, f.calls)
	}
	link(t, st.Q())
	offline := New(st.DB(), f, time.Now, func(context.Context) bool { return false })
	if err := offline.Import(ctx); err != nil || f.calls != 0 {
		t.Fatalf("import with online lookups off: %v after %d calls", err, f.calls)
	}
}

func TestFailedImportKeepsThePreviousHistory(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	link(t, st.Q())
	f := &fakeFetcher{artists: []Count{{Artist: "A", Plays: 3}}}
	svc := newService(st, f)
	if err := svc.Import(ctx); err != nil {
		t.Fatal(err)
	}
	f.err = errors.New("last.fm is down")
	if err := svc.Import(ctx); err == nil {
		t.Fatal("want the fetch error")
	}
	if got, _ := svc.Signals(ctx); len(got) != 1 {
		t.Fatalf("failed import left %d signals, want the previous 1", len(got))
	}
}

func TestRelinkingWhileImportingReadsTheNewAccount(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	link(t, st.Q())
	f := &fakeFetcher{artists: []Count{{Artist: "A", Plays: 3}}}
	svc := newService(st, f)
	f.during = func() {
		f.during = nil
		// Relinked to another account while alice's history is read; the
		// link hook asks for an import, which the running one picks up.
		if err := st.Q().UpsertScrobbleLink(ctx, db.UpsertScrobbleLinkParams{
			UserID: "owner", Provider: "lastfm", SessionKey: "sk2", Username: "bob", Status: "active", CreatedAt: linkedAt + 5,
		}); err != nil {
			t.Error(err)
		}
		if err := svc.Import(ctx); err != nil {
			t.Error(err)
		}
	}

	if err := svc.Import(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.users, []string{"alice", "bob"}) {
		t.Fatalf("read accounts %v, want alice then bob", f.users)
	}
	if got, _ := svc.Signals(ctx); len(got) != 1 {
		t.Fatalf("signals %+v, want bob's one row", got)
	}
}
