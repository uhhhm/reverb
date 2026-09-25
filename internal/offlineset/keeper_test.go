package offlineset_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
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

// pendingKeeper is a phone keeper over a folder holding one Download.
func pendingKeeper(t *testing.T) (k *offlineset.Keeper, q *db.Queries, dir string, files *p2p.FileSyncer) {
	t.Helper()
	st := newTestStoreOff(t)
	q = st.Q()
	createDeviceOff(t, st, "phone", 1)
	dir = t.TempDir()
	p := filepath.Join(dir, "Band", "Downloaded.m4a")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("downloaded"), 0o644); err != nil {
		t.Fatal(err)
	}
	files = p2p.NewFileSyncer(q, "phone", dir)
	if err := files.ScanAndSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	k = offlineset.NewKeeper(offlineset.KeeperConfig{
		Store:        q,
		DeviceID:     func(context.Context) (string, error) { return "phone", nil },
		FileDeviceID: "phone",
		MusicDir:     dir,
		FreeSpace:    func(string) (int64, error) { return 1 << 40, nil },
		Rescan:       files.ScanAndSync,
	})
	if err := k.AddPending(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return k, q, dir, files
}

func TestAddPendingRejectsDownloaderDirectoryFallback(t *testing.T) {
	k, _, dir, _ := pendingKeeper(t)
	err := k.AddPending(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("AddPending(directory) error = %v, want a directory error", err)
	}
}

// A Download made on the phone stays until a paired device's manifest shows
// it holds the same bytes, and is then removed, since no offline playlist
// names it.
func TestPendingUploadLeavesOnceAPeerHoldsIt(t *testing.T) {
	ctx := context.Background()
	k, q, dir, _ := pendingKeeper(t)
	downloaded := filepath.Join(dir, "Band", "Downloaded.m4a")

	pending, err := k.PendingUploads(ctx)
	if err != nil || len(pending) != 1 || pending[0].RelPath != "Band/Downloaded.m4a" || pending[0].SizeBytes != int64(len("downloaded")) {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	// A peer that holds other files confirms nothing.
	other := p2p.FileManifest{RelPath: "Band/Other.m4a", ContentHash: hashOf("other"), Size: 5}
	k.Select(ctx, "desktop", []p2p.FileManifest{other}, nil)
	if err := k.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(downloaded); err != nil {
		t.Fatal("a Download no peer holds was pruned")
	}

	held := p2p.FileManifest{RelPath: "Band/Downloaded.m4a", ContentHash: hashOf("downloaded"), Size: 10}
	k.Select(ctx, "desktop", []p2p.FileManifest{other, held}, nil)
	if err := k.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(downloaded); !os.IsNotExist(err) {
		t.Fatal("the Download stayed after a peer confirmed it holds it")
	}
	if left, _ := q.ListPendingUploads(ctx); len(left) != 0 {
		t.Fatalf("still pending: %+v", left)
	}
}

// The offline set never prunes a Download a peer has not confirmed, even when
// it recorded a file at the same path with the same bytes.
func TestOfflineSetDoesNotPruneAPendingUpload(t *testing.T) {
	ctx := context.Background()
	k, q, dir, _ := pendingKeeper(t)
	if err := q.UpsertOfflineFile(ctx, db.UpsertOfflineFileParams{
		RelPath: "Band/Downloaded.m4a", ContentHash: hashOf("downloaded"), FetchedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := k.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Band", "Downloaded.m4a")); err != nil {
		t.Fatal("the offline set pruned a pending upload")
	}
}

// A Download removed from the folder by other means is no longer pending.
func TestPendingUploadThatIsGoneIsForgotten(t *testing.T) {
	ctx := context.Background()
	k, q, dir, files := pendingKeeper(t)
	if err := os.Remove(filepath.Join(dir, "Band", "Downloaded.m4a")); err != nil {
		t.Fatal(err)
	}
	if err := files.ScanAndSync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := k.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if left, _ := q.ListPendingUploads(ctx); len(left) != 0 {
		t.Fatalf("a missing file is still pending: %+v", left)
	}
}
