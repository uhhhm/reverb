package p2p

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/store/db"
)

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func mkDevice(t *testing.T, q *db.Queries, id string) {
	t.Helper()
	if err := q.CreateDevice(context.Background(), db.CreateDeviceParams{ID: id, Name: id, TokenHash: id + "-hash"}); err != nil {
		t.Fatal(err)
	}
}

func writeTrack(t *testing.T, dir, rel string, body []byte) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMissingFilesSkipsHeldContent(t *testing.T) {
	local := []FileManifest{{ContentHash: "aaa", RelPath: "A/1.flac"}}
	remote := []FileManifest{
		{ContentHash: "aaa", RelPath: "Other/1.flac"},
		{ContentHash: "bbb", RelPath: "B/2.flac"},
	}
	got := MissingFiles(local, remote, "")
	if len(got) != 1 || got[0].ContentHash != "bbb" {
		t.Fatalf("want only bbb, got %+v", got)
	}
}

func TestMissingFilesRefusesToClobberDifferentLocalContent(t *testing.T) {
	local := []FileManifest{{ContentHash: "aaa", RelPath: "A/1.flac"}}
	remote := []FileManifest{{ContentHash: "zzz", RelPath: "A/1.flac"}}
	if got := MissingFiles(local, remote, ""); len(got) != 0 {
		t.Fatalf("must not overwrite a different local file at the same path, got %+v", got)
	}
}

func TestMissingFilesSkipsUntrackedFileOnDisk(t *testing.T) {
	dir := t.TempDir()
	writeTrack(t, dir, "A/1.flac", []byte("local bytes"))
	remote := []FileManifest{{ContentHash: "zzz", RelPath: "A/1.flac"}}
	if got := MissingFiles(nil, remote, dir); len(got) != 0 {
		t.Fatalf("must not overwrite an unmanifested local file, got %+v", got)
	}
}

func TestMissingFilesRejectsTraversalPaths(t *testing.T) {
	remote := []FileManifest{{ContentHash: "zzz", RelPath: "../../etc/passwd"}}
	if got := MissingFiles(nil, remote, ""); len(got) != 0 {
		t.Fatalf("traversal path must be refused, got %+v", got)
	}
}

func TestMissingFilesDeduplicatesByHash(t *testing.T) {
	remote := []FileManifest{
		{ContentHash: "aaa", RelPath: "A/1.flac"},
		{ContentHash: "aaa", RelPath: "B/1.flac"},
	}
	if got := MissingFiles(nil, remote, ""); len(got) != 1 {
		t.Fatalf("want one entry per hash, got %+v", got)
	}
}

func TestManifestHandlerRejectsUnpairedPeer(t *testing.T) {
	q := newTrustStore(t)
	guard := NewGuard(q)
	server, client := newLinkedHosts(t)
	RegisterManifestHandler(server, q, "server-device", guard)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := RequestManifest(ctx, client, server.ID()); err == nil {
		t.Fatal("unpaired peer must not receive the library manifest")
	}
}

// TestPullReplicatesPeerLibrary is the end-to-end shape: a paired peer's files
// land in the local music dir without any manual fetch.
func TestPullReplicatesPeerLibrary(t *testing.T) {
	ctx := context.Background()
	serverDir, clientDir := t.TempDir(), t.TempDir()
	body := []byte("AUDIO BYTES")
	writeTrack(t, serverDir, "Artist/Album/01.flac", body)

	serverQ := newTrustStore(t)
	clientQ := newTrustStore(t)
	serverHost, clientHost := newLinkedHosts(t)
	mkDevice(t, serverQ, "server-device")
	mkDevice(t, clientQ, "client-device")

	serverGuard := NewGuard(serverQ)
	if err := serverGuard.Trust(ctx, clientHost.ID(), "", "client"); err != nil {
		t.Fatal(err)
	}
	clientGuard := NewGuard(clientQ)
	if err := clientGuard.Trust(ctx, serverHost.ID(), "", "server"); err != nil {
		t.Fatal(err)
	}

	// Server side: hash its library, then serve manifest + files.
	serverFS := NewFileSyncer(serverQ, "server-device", serverDir)
	if err := serverFS.ScanAndSync(ctx); err != nil {
		t.Fatal(err)
	}
	RegisterManifestHandler(serverHost, serverQ, "server-device", serverGuard)
	RegisterFileHandler(serverHost, serverDir, serverGuard)

	clientFS := NewFileSyncer(clientQ, "client-device", clientDir)
	puller := NewPuller(clientHost, clientQ, clientFS, clientGuard, "client-device", clientDir)
	puller.pullAll(ctx)

	got, err := os.ReadFile(filepath.Join(clientDir, "Artist", "Album", "01.flac"))
	if err != nil {
		t.Fatalf("file was not replicated: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("replicated content mismatch: %q", got)
	}
	// And the local manifest now knows about it under this device's ID.
	rows, err := clientQ.ListFileManifests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.RelPath == "Artist/Album/01.flac" && r.DeviceID == "client-device" && r.ContentHash == hashOf(body) {
			found = true
		}
	}
	if !found {
		t.Fatalf("pulled file missing from local manifest: %+v", rows)
	}
}

func TestManifestHandlerOnlyAdvertisesLocalDeviceFiles(t *testing.T) {
	ctx := context.Background()
	q := newTrustStore(t)
	guard := NewGuard(q)
	server, client := newLinkedHosts(t)
	mkDevice(t, q, "me")
	mkDevice(t, q, "other")
	if err := guard.Trust(ctx, client.ID(), "", "client"); err != nil {
		t.Fatal(err)
	}
	for _, row := range []db.UpsertFileManifestParams{
		{CanonicalID: "me:A/1.flac", ContentHash: "aaa", Size: 1, RelPath: "A/1.flac", DeviceID: "me"},
		{CanonicalID: "other:B/2.flac", ContentHash: "bbb", Size: 1, RelPath: "B/2.flac", DeviceID: "other"},
	} {
		if err := q.UpsertFileManifest(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	RegisterManifestHandler(server, q, "me", guard)

	resp, err := RequestManifest(ctx, client, server.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 1 || resp.Files[0].DeviceID != "me" {
		t.Fatalf("want only locally-authored rows, got %+v", resp.Files)
	}
}
