package app

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/linkadd"
)

// Failure modes: a replacement manager loses catalog minting or completion
// hooks, a replacement playlist service stops emitting, or live consumers keep
// using the retired services. Exercise the composition root and public APIs.
func TestReloadPreservesDownloadIdentityAndPlaylistChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a runtime and downloads through yt-dlp")
	}
	d := newDownloadingPhone(t)
	var pl core.SyncedPlaylistDetail
	d.must(http.MethodPost, "/playlists", map[string]string{"name": "Before reload"}, &pl, http.StatusCreated)
	old := d.rt.Reloader.Current().Downloads
	d.must(http.MethodPost, "/adapters", map[string]any{"type": "search", "name": "deezer", "enabled": false}, nil, http.StatusCreated)
	if d.rt.Reloader.Current().Downloads == old {
		t.Fatal("adapter mutation did not replace download services")
	}
	job := d.download("After reload")
	if job.CanonicalID == "" {
		t.Error("download after reload has no catalog id")
	}
	if pending := d.pendingUploads(); len(pending) != 1 {
		t.Errorf("completion hook lost: pending uploads = %+v", pending)
	}
	d.must(http.MethodPut, "/playlists/"+pl.ID, map[string]string{"name": "After reload"}, nil, http.StatusOK)
	changes, err := d.rt.Deps.SyncStore.ListSince(context.Background(), 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range changes {
		if ch.EntityID == pl.ID && ch.Field == "name" && ch.Value == "After reload" {
			return
		}
	}
	t.Fatal("playlist rename after reload is absent from the change log")
}

// LinkAdd is also called outside HTTP; it must see an unavailable live manager
// without a handler mutating the planner first.
func TestLinkAddFollowsRemovedLibrary(t *testing.T) {
	d := newSyncDevice(t, "desktop")
	d.must(http.MethodPut, "/settings", map[string]string{"libraryBackendMode": "external"}, nil, http.StatusOK)
	var adapters []struct{ ID, Type string }
	d.must(http.MethodGet, "/adapters", nil, &adapters, http.StatusOK)
	for _, a := range adapters {
		if a.Type == "library" {
			d.must(http.MethodDelete, "/adapters/"+a.ID, nil, nil, http.StatusOK)
		}
	}
	if d.rt.Reloader.Current().Downloads != nil {
		t.Fatal("library removal did not disable downloads")
	}
	_, err := d.rt.Deps.LinkAdd.Add(context.Background(), linkadd.AddOptions{URL: "https://open.spotify.com/track/reload"})
	if !errors.Is(err, linkadd.ErrNoDownloader) {
		t.Fatalf("Add after library removal: %v, want no downloader", err)
	}
}

func TestTwoDevicesReplicatePlaylistAfterAdapterReload(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	a := newDownloadingPhone(t)
	b := newSyncDevice(t, "desktop")
	pair(t, a, b)
	var pl core.SyncedPlaylistDetail
	a.must(http.MethodPost, "/playlists", map[string]string{"name": "Before reload"}, &pl, http.StatusCreated)
	a.must(http.MethodPost, "/playlists/"+pl.ID+"/tracks", trackBody(1), nil, http.StatusOK)
	converge(t, a, b, "initial playlist", func() bool { p, ok := b.playlist(pl.ID); return ok && len(p.Tracks) == 1 })

	// Creating and removing an adapter replaces the services twice. The existing
	// playlist and pairing must keep working through both replacements.
	var adapter struct{ Data struct{ ID string } }
	a.must(http.MethodPost, "/adapters", map[string]any{"type": "search", "name": "deezer", "enabled": false}, &adapter, http.StatusCreated)
	a.must(http.MethodDelete, "/adapters/"+adapter.Data.ID, nil, nil, http.StatusOK)
	job := a.download("Reloaded song")
	if job.CanonicalID == "" {
		t.Fatal("download lost catalog identity after repeated reloads")
	}
	a.must(http.MethodPut, "/playlists/"+pl.ID, map[string]string{"name": "After two reloads"}, nil, http.StatusOK)
	a.must(http.MethodPost, "/playlists/"+pl.ID+"/tracks", map[string]any{
		"source": "library", "externalId": job.LibraryTrackID, "title": "Reloaded song", "artist": "Band", "album": "Record", "download": false,
	}, nil, http.StatusOK)
	converge(t, a, b, "rename and downloaded track reach paired device", func() bool {
		p, ok := b.playlist(pl.ID)
		return ok && p.Name == "After two reloads" && len(p.Tracks) == 2 && p.Tracks[1].CanonicalID == job.CanonicalID
	})
	b.restart()
	b.must(http.MethodPost, "/playlists/"+pl.ID+"/tracks", trackBody(3), nil, http.StatusOK)
	converge(t, a, b, "peer edit returns to reloaded device after peer restart", func() bool {
		p, ok := a.playlist(pl.ID)
		return ok && len(p.Tracks) == 3
	})
	t.Logf("verified playlist %s: two adapter reloads, download catalog id %s, replicated rename and membership, peer restart and return edit", pl.ID, job.CanonicalID)
}
