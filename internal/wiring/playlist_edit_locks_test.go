package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/playlistsync"
	"github.com/uhhhm/reverb/internal/registry"
)

// emptySearchLib is a library that finds nothing, so Detail reports every
// added track as not in the library.
type emptySearchLib struct{ stubLib }

func (*emptySearchLib) Search(context.Context, string, []core.EntityType) (core.SearchResults, error) {
	return core.SearchResults{}, nil
}

// TestRebuiltSyncServicesShareEditLocks races managed-playlist edits across two
// bundles from one Builder, as an adapter reload leaves an old and a new
// playlistsync.Service alive at once, against the real SQLite store. Every
// add must survive, and the renames must not write back a stale tracklist.
func TestRebuiltSyncServicesShareEditLocks(t *testing.T) {
	st := newTestStore(t)
	addInstance(t, st, "library", "subsonic", `{"url":"http://x"}`)
	addInstance(t, st, "downloader", "spotdl", `{"output_dir":"/music"}`)
	b := newTestBuilder(t, st)
	b.libraryReg.Register("subsonic", func() registry.Plugin { return &emptySearchLib{} })
	ctx := context.Background()

	build := func() *playlistsync.Service {
		bundle, err := b.Build(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if bundle.Sync == nil {
			t.Fatal("expected a sync service")
		}
		t.Cleanup(bundle.Manager.Stop)
		return bundle.Sync
	}
	before, after := build(), build()

	det, err := before.CreateManaged(ctx, "Reloaded")
	if err != nil {
		t.Fatal(err)
	}
	id := det.ID

	const adds = 30
	start := make(chan struct{})
	errs := make(chan error, adds+2)
	var wg sync.WaitGroup
	for i := 0; i < adds; i++ {
		svc := before
		if i%2 == 1 {
			svc = after
		}
		entry := core.ExternalResult{Source: "spotify", ExternalID: fmt.Sprintf("t%d", i), Title: "T", Type: core.EntityTrack}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := svc.AddTracks(ctx, id, []core.ExternalResult{entry}, false)
			errs <- err
		}()
	}
	for _, svc := range []*playlistsync.Service{before, after} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.Rename(ctx, id, "Renamed")
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	row, err := st.Q().GetSyncedPlaylist(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var tracks []core.ExternalResult
	if err := json.Unmarshal([]byte(row.TracksJson), &tracks); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, tr := range tracks {
		seen[tr.ExternalID]++
	}
	for i := 0; i < adds; i++ {
		if n := seen[fmt.Sprintf("t%d", i)]; n != 1 {
			t.Errorf("t%d present %d times", i, n)
		}
	}
	if len(tracks) != adds {
		t.Errorf("tracklist has %d entries, want %d", len(tracks), adds)
	}
	if row.Name != "Renamed" {
		t.Errorf("name = %q, want Renamed", row.Name)
	}
}
