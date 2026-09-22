package offlineset_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/offlineset"
	"github.com/uhhhm/reverb/internal/p2p"
	"github.com/uhhhm/reverb/internal/store/db"
)

// Pruning gives back the space of what the offline set fetched once no offline
// playlist names it, and never touches a file that reached the folder any
// other way, even one no playlist names.
func TestPruneRemovesOnlyWhatTheOfflineSetFetched(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreOff(t)
	q := st.Q()
	createDeviceOff(t, st, "phone", 1)
	createPlaylistOff(t, st, "pl1", "Commute")
	if err := q.UpdateSyncedPlaylistTracks(ctx, db.UpdateSyncedPlaylistTracksParams{
		ID: "pl1", Name: "Commute", LastSyncedAt: time.Now().Unix(),
		TracksJson: `[{"source":"deezer","externalId":"1","title":"Fetched","artist":"Band","album":"Record","type":"track"}]`,
	}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Band/Record/Placed.mp3")
	files := p2p.NewFileSyncer(q, "phone", dir)
	if err := files.ScanAndSync(ctx); err != nil {
		t.Fatal(err)
	}
	k := offlineset.NewKeeper(offlineset.KeeperConfig{
		Store:        q,
		DeviceID:     func(context.Context) (string, error) { return "phone", nil },
		FileDeviceID: "phone",
		MusicDir:     dir,
		FreeSpace:    func(string) (int64, error) { return 1 << 40, nil },
		Rescan:       files.ScanAndSync,
	})
	svc := offlineset.NewService(q)
	if err := svc.Set(ctx, "phone", "pl1", true); err != nil {
		t.Fatal(err)
	}
	offer := p2p.FileManifest{RelPath: "Band/Record/Fetched.mp3", ContentHash: hashOf("Band/Record/Fetched.mp3"), Size: 23, Title: "Fetched", Artist: "Band", Album: "Record"}
	got := k.Select(ctx, "desktop", []p2p.FileManifest{offer}, []p2p.FileManifest{offer})
	if len(got) != 1 || got[0].RelPath != offer.RelPath {
		t.Fatalf("selected %+v, want the offline playlist's track", got)
	}
	// A second peer's round, running at the same time, leaves it alone.
	if again := k.Select(ctx, "server", []p2p.FileManifest{offer}, []p2p.FileManifest{offer}); len(again) != 0 {
		t.Fatalf("a second round selected %+v while the first has it", again)
	}
	write("Band/Record/Fetched.mp3")
	if err := files.ScanAndSync(ctx); err != nil {
		t.Fatal(err)
	}
	k.Fetched(ctx, offer, nil)
	if err := k.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		return err == nil
	}
	if !exists("Band/Record/Fetched.mp3") || !exists("Band/Record/Placed.mp3") {
		t.Fatal("prune removed a file while its playlist is still offline")
	}
	status, err := k.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Playlists) != 1 || status.Playlists[0].Tracks[0].State != offlineset.StateReady {
		t.Fatalf("status = %+v", status)
	}

	if err := svc.Remove(ctx, "phone", "pl1"); err != nil {
		t.Fatal(err)
	}
	if err := k.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if exists("Band/Record/Fetched.mp3") {
		t.Fatal("the fetched file stayed after its playlist left the offline set")
	}
	if !exists("Band/Record/Placed.mp3") {
		t.Fatal("prune removed a file the offline set never fetched")
	}
	if left, _ := q.ListOfflineFiles(ctx); len(left) != 0 {
		t.Fatalf("offline files still recorded: %+v", left)
	}
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// A phone that cannot read its free space fetches nothing, since it cannot
// keep the reserve, and says so rather than claiming to be full.
func TestUnknownFreeSpaceFetchesNothing(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreOff(t)
	q := st.Q()
	createDeviceOff(t, st, "phone", 1)
	createPlaylistOff(t, st, "pl1", "Commute")
	if err := q.UpdateSyncedPlaylistTracks(ctx, db.UpdateSyncedPlaylistTracksParams{
		ID: "pl1", Name: "Commute", LastSyncedAt: time.Now().Unix(),
		TracksJson: `[{"source":"deezer","externalId":"1","title":"Song","artist":"Band","album":"Record","type":"track"}]`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := offlineset.NewService(q).Set(ctx, "phone", "pl1", true); err != nil {
		t.Fatal(err)
	}
	k := offlineset.NewKeeper(offlineset.KeeperConfig{
		Store:        q,
		DeviceID:     func(context.Context) (string, error) { return "phone", nil },
		FileDeviceID: "phone",
		MusicDir:     t.TempDir(),
		FreeSpace:    func(string) (int64, error) { return 0, os.ErrPermission },
	})
	offer := p2p.FileManifest{RelPath: "Band/Record/Song.mp3", ContentHash: "h", Size: 10, Title: "Song", Artist: "Band", Album: "Record"}
	if got := k.Select(ctx, "desktop", []p2p.FileManifest{offer}, []p2p.FileManifest{offer}); len(got) != 0 {
		t.Fatalf("selected %+v with the free space unknown", got)
	}
	status, err := k.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.SpaceUnknown || status.Full || status.AvailableBytes != 0 || status.Playlists[0].Tracks[0].State != offlineset.StateQueued {
		t.Fatalf("status = %+v", status)
	}
}
