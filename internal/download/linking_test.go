package download

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/events"
)

// orderingPlaylistAdder records playlist additions and, at each one, whether a
// completion for the job had already been published: the bus delivers
// synchronously, so an event waiting on the channel was published first.
type orderingPlaylistAdder struct {
	mu                 sync.Mutex
	completions        <-chan events.Event
	adds               int
	completedBeforeAdd bool
}

func (a *orderingPlaylistAdder) AddTracksToPlaylist(context.Context, string, []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.adds++
	select {
	case <-a.completions:
		a.completedBeforeAdd = true
	default:
	}
	return nil
}

// Linking a completed job to its library track has two triggers — the startup
// backfill and the post-download scan — and both must honour the same policy:
//   - nothing downstream happens for a link that was not persisted, so a failed
//     write is retried by the next trigger instead of announced twice;
//   - the playlist addition precedes the completion, so a listener that hears
//     the completion finds the track already in its playlist;
//   - the completion carries the canonical id minted at link time;
//   - a persisted link is not relinked.
func TestLinkingPolicyIsSharedByBothTriggers(t *testing.T) {
	triggers := map[string]func(*Manager){
		"backfill": func(m *Manager) { m.BackfillUnlinked() },
		"scan": func(m *Manager) {
			m.pending = true
			m.runScan()
		},
	}
	for name, trigger := range triggers {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := &completionWriteStore{memStore: newMemStore()}
			job := core.DownloadJob{
				ID: "j-link", DedupKey: "dk-link", Status: core.DownloadCompleted,
				DownloaderName: "dl", Source: "spotify", ExternalID: "ext-link",
				Title: "Blue in Green", Artist: "Miles Davis", Album: "Kind of Blue",
				AddToPlaylistID: "pl-jazz", Progress: 100,
			}
			if err := store.Insert(ctx, job, core.DownloadRequest{}); err != nil {
				t.Fatal(err)
			}
			bus := events.New()
			completions, unsub := bus.Subscribe(TopicComplete)
			defer unsub()
			adder := &orderingPlaylistAdder{completions: completions}
			minter := &fakeCanonicalMinter{retID: "trk_blue"}
			m := NewManager(
				Config{Workers: 1, DebounceWindow: time.Second, ScanPollEvery: time.Millisecond, ScanPollMax: time.Second, ScanSettleMax: 10 * time.Millisecond},
				wrapDownloaders(nil), store, bus, &fakeScanner{}, &fakeRematcher{trackID: "lib-blue", coverArtID: "cov-blue"},
				&fakeVersion{v: 1}, RealClock{}, adder, nil,
			)
			m.SetCanonicalMinter(minter)
			var linked []string
			m.SetLinkedHook(func(_ context.Context, cid string) { linked = append(linked, cid) })

			// The link cannot be written: nothing may be announced.
			store.failUpdate.Store(true)
			trigger(m)
			if n := len(completions); n != 0 {
				t.Fatalf("unpersisted link published %d completion(s)", n)
			}
			if adder.adds != 0 || minter.callCount() != 0 || len(linked) != 0 {
				t.Fatalf("unpersisted link had effects: playlist adds=%d mints=%d linked=%v", adder.adds, minter.callCount(), linked)
			}
			if got, _, _ := store.Get(ctx, job.ID); got.LibraryTrackID != "" {
				t.Fatalf("job linked to %q despite the failed write", got.LibraryTrackID)
			}

			// The store recovers: the same trigger links the job exactly once.
			store.failUpdate.Store(false)
			trigger(m)
			if adder.adds != 1 {
				t.Fatalf("playlist adds = %d, want 1", adder.adds)
			}
			if adder.completedBeforeAdd {
				t.Fatal("completion was published before the playlist addition")
			}
			if len(completions) != 1 {
				t.Fatalf("completions = %d, want 1", len(completions))
			}
			ev := (<-completions).Payload.(core.DownloadEvent)
			if ev.LibraryTrackID != "lib-blue" || ev.CoverArtID != "cov-blue" || ev.CanonicalID != "trk_blue" {
				t.Fatalf("completion = %+v, want library lib-blue, cover cov-blue, canonical trk_blue", ev)
			}
			if len(linked) != 1 || linked[0] != "trk_blue" {
				t.Fatalf("linked hook saw %v, want [trk_blue]", linked)
			}
			got, _, _ := store.Get(ctx, job.ID)
			if got.LibraryTrackID != "lib-blue" || got.CanonicalID != "trk_blue" {
				t.Fatalf("stored job = library %q canonical %q", got.LibraryTrackID, got.CanonicalID)
			}

			// A persisted link is final.
			trigger(m)
			if adder.adds != 1 || len(completions) != 0 || minter.callCount() != 1 {
				t.Fatalf("relinked: playlist adds=%d pending completions=%d mints=%d", adder.adds, len(completions), minter.callCount())
			}
		})
	}
}
