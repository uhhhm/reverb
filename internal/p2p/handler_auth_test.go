package p2p

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// newLinkedHosts returns two connected in-process libp2p hosts.
func newLinkedHosts(t *testing.T) (host.Host, host.Host) {
	t.Helper()
	mk := func() host.Host {
		h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
		if err != nil {
			t.Fatalf("new host: %v", err)
		}
		t.Cleanup(func() { _ = h.Close() })
		return h
	}
	server, client := mk(), mk()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Connect(ctx, peer.AddrInfo{ID: server.ID(), Addrs: server.Addrs()}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return server, client
}

// requestFile performs the /reverb/file/1.0.0 exchange and returns the bytes
// the server was willing to hand over.
func requestFile(t *testing.T, client host.Host, serverID peer.ID, relPath string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := client.NewStream(ctx, serverID, "/reverb/file/1.0.0")
	if err != nil {
		return nil, err
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(10 * time.Second))
	if err := json.NewEncoder(s).Encode(fileRequest{RelPath: relPath}); err != nil {
		return nil, err
	}
	_ = s.CloseWrite()
	return io.ReadAll(s)
}

// TestFileHandlerRejectsUnpairedPeer is the regression test for the original
// finding: any peer that could open a stream could read the whole music
// library, because the handler never looked at who was calling.
func TestFileHandlerRejectsUnpairedPeer(t *testing.T) {
	musicDir := t.TempDir()
	secret := filepath.Join(musicDir, "Artist", "Album")
	if err := os.MkdirAll(secret, 0o755); err != nil {
		t.Fatal(err)
	}
	trackPath := filepath.Join(secret, "01.flac")
	if err := os.WriteFile(trackPath, []byte("PRIVATE AUDIO"), 0o644); err != nil {
		t.Fatal(err)
	}

	q := newTrustStore(t)
	guard := NewGuard(q)
	server, client := newLinkedHosts(t)
	RegisterFileHandler(server, musicDir, guard)

	got, err := requestFile(t, client, server.ID(), "Artist/Album/01.flac")
	if err == nil && len(got) > 0 {
		t.Fatalf("unpaired peer received %d bytes of library content: %q", len(got), got)
	}
}

// TestFileHandlerServesPairedPeer confirms the gate does not break the feature.
func TestFileHandlerServesPairedPeer(t *testing.T) {
	ctx := context.Background()
	musicDir := t.TempDir()
	dir := filepath.Join(musicDir, "Artist", "Album")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := []byte("PRIVATE AUDIO")
	if err := os.WriteFile(filepath.Join(dir, "01.flac"), want, 0o644); err != nil {
		t.Fatal(err)
	}

	q := newTrustStore(t)
	if err := q.CreateDevice(ctx, db.CreateDeviceParams{ID: "dev_a", Name: "a", TokenHash: "h1"}); err != nil {
		t.Fatal(err)
	}
	guard := NewGuard(q)
	server, client := newLinkedHosts(t)
	if err := guard.Trust(ctx, client.ID(), "dev_a", "laptop"); err != nil {
		t.Fatal(err)
	}
	RegisterFileHandler(server, musicDir, guard)

	got, err := requestFile(t, client, server.ID(), "Artist/Album/01.flac")
	if err != nil {
		t.Fatalf("paired peer refused: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("want %q, got %q", want, got)
	}
}

// TestFileHandlerRefusesSymlinkEscape covers the containment hardening: a
// symlink inside musicDir must not become a window onto the rest of the disk.
func TestFileHandlerRefusesSymlinkEscape(t *testing.T) {
	ctx := context.Background()
	outside := t.TempDir()
	secretPath := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("TOP SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	musicDir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(musicDir, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	q := newTrustStore(t)
	if err := q.CreateDevice(ctx, db.CreateDeviceParams{ID: "dev_a", Name: "a", TokenHash: "h1"}); err != nil {
		t.Fatal(err)
	}
	guard := NewGuard(q)
	server, client := newLinkedHosts(t)
	if err := guard.Trust(ctx, client.ID(), "dev_a", "laptop"); err != nil {
		t.Fatal(err)
	}
	RegisterFileHandler(server, musicDir, guard)

	got, _ := requestFile(t, client, server.ID(), "escape/secret.txt")
	if string(got) == "TOP SECRET" {
		t.Fatal("symlink escaped musicDir containment")
	}
}

// The sync handler answers a failure with {"error": ...}. That has to arrive as
// a failure: decoded into a SyncResponse with no error field it looked like a
// successful empty round, so an unpaired device, a device-id mismatch or a
// store error left no signal anywhere and the peer retried forever in silence.
func TestSyncPeerSurfacesErrorReply(t *testing.T) {
	server, client := newLinkedHosts(t)
	server.SetStreamHandler("/reverb/sync/1.0.0", func(s network.Stream) {
		defer s.Close()
		_ = json.NewEncoder(s).Encode(reverbsync.SyncResponse{Error: "unknown deviceId: pairing required"})
	})

	q := newTrustStore(t)
	syncer := NewSyncer(client, reverbsync.NewSyncStore(q), NewGuard(q), nil, "dev_local")

	err := syncer.syncPeer(context.Background(), server.ID())
	if err == nil {
		t.Fatal("syncPeer returned nil: the peer's error reply was read as a successful empty sync")
	}
	if !strings.Contains(err.Error(), "pairing required") {
		t.Fatalf("error = %v, want it to carry the peer's message", err)
	}
}
