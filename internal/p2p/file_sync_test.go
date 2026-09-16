package p2p

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

func TestDeletedFileIsRemovedAndCannotBePulledBack(t *testing.T) {
	ctx := context.Background()
	q := newTrustStore(t)
	mkDevice(t, q, "me")
	dir := t.TempDir()
	body := []byte("deleted audio")
	writeTrack(t, dir, "track.flac", body)
	fs := NewFileSyncer(q, "me", dir)
	if err := fs.ScanAndSync(ctx); err != nil {
		t.Fatal(err)
	}
	ss := syncpkg.NewSyncStore(q)
	if _, err := ss.AppendChange(ctx, "me", syncpkg.SyncChange{EntityType: "file", EntityID: hashOf(body), Field: "__deleted"}); err != nil {
		t.Fatal(err)
	}
	if err := fs.ScanAndSync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "track.flac")); !os.IsNotExist(err) {
		t.Fatalf("deleted file still exists: %v", err)
	}
	if n := manifestCount(t, fs); n != 0 {
		t.Fatalf("still advertising %d deleted files", n)
	}
}

func manifestCount(t *testing.T, f *FileSyncer) int {
	t.Helper()
	rows, err := f.store.ListFileManifests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

// A music dir that has gone away — an unmounted external or network volume —
// must not take the manifest with it. The whole library would otherwise be
// re-hashed on return, and peers would see every file as missing meanwhile.
func TestScanAndSync_UnreadableMusicDirKeepsManifests(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeTrack(t, dir, "Artist/Album/01.flac", []byte("AUDIO"))
	writeTrack(t, dir, "Artist/Album/02.flac", []byte("MORE AUDIO"))

	q := newTrustStore(t)
	mkDevice(t, q, "device-a")
	f := NewFileSyncer(q, "device-a", dir)
	if err := f.ScanAndSync(ctx); err != nil {
		t.Fatal(err)
	}
	if got := manifestCount(t, f); got != 2 {
		t.Fatalf("expected 2 manifests after the first scan, got %d", got)
	}

	// The volume vanishes entirely.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := f.ScanAndSync(ctx); err != nil {
		t.Fatalf("scan over a missing music dir must not fail: %v", err)
	}
	if got := manifestCount(t, f); got != 2 {
		t.Fatalf("manifests wiped by a missing music dir: %d left", got)
	}
}

// The common shape of an unmounted volume is the mount point still present on
// the boot disk as an empty directory: the walk succeeds and finds nothing.
func TestScanAndSync_EmptyMusicDirKeepsManifests(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeTrack(t, dir, "Artist/Album/01.flac", []byte("AUDIO"))

	q := newTrustStore(t)
	mkDevice(t, q, "device-a")
	f := NewFileSyncer(q, "device-a", dir)
	if err := f.ScanAndSync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "Artist")); err != nil {
		t.Fatal(err)
	}
	if err := f.ScanAndSync(ctx); err != nil {
		t.Fatal(err)
	}
	if got := manifestCount(t, f); got != 1 {
		t.Fatalf("manifests wiped by an empty music dir: %d left", got)
	}
}

// A file genuinely deleted from a library that still has files in it drops out
// of the manifest — the stale-delete pass still does its job.
func TestScanAndSync_DeletedFileDropsFromManifest(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeTrack(t, dir, "Artist/Album/01.flac", []byte("AUDIO"))
	writeTrack(t, dir, "Artist/Album/02.flac", []byte("MORE AUDIO"))

	q := newTrustStore(t)
	mkDevice(t, q, "device-a")
	f := NewFileSyncer(q, "device-a", dir)
	if err := f.ScanAndSync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "Artist", "Album", "02.flac")); err != nil {
		t.Fatal(err)
	}
	if err := f.ScanAndSync(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := f.store.ListFileManifests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RelPath != "Artist/Album/01.flac" {
		t.Fatalf("stale manifest not removed: %+v", rows)
	}
}
