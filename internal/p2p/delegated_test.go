package p2p

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store/db"
)

func TestDelegatedStreamCarriesLibraryAudioForPairedPeer(t *testing.T) {
	ctx := context.Background()
	server, client := newLinkedHosts(t)
	store := newTrustStore(t)
	if err := store.CreateDevice(ctx, db.CreateDeviceParams{ID: "phone-device", Name: "Phone", TokenHash: "phone-token"}); err != nil {
		t.Fatal(err)
	}
	guard := NewGuard(store)
	if err := guard.Trust(ctx, client.ID(), "phone-device", "Phone"); err != nil {
		t.Fatal(err)
	}
	RegisterDelegatedHandler(server, guard, func(_ context.Context, catalogID string, opts core.StreamOpts, byteRange string) (core.StreamHandle, error) {
		if catalogID != "trk_song" {
			t.Fatalf("catalog id = %q", catalogID)
		}
		if opts.TimeOffsetSec != 12 || opts.Format != "mp3" {
			t.Fatalf("opts = %+v", opts)
		}
		if byteRange != "bytes=3-" {
			t.Fatalf("range = %q", byteRange)
		}
		return core.StreamHandle{
			Body:          io.NopCloser(strings.NewReader("audio bytes")),
			ContentType:   "audio/mpeg",
			ContentLength: 11,
			AcceptRanges:  "bytes",
			ContentRange:  "bytes 3-13/14",
			StatusCode:    206,
		}, nil
	}, nil)

	handle, err := RequestDelegatedStream(ctx, client, server.ID(), "trk_song", core.StreamOpts{Format: "mp3", TimeOffsetSec: 12}, "bytes=3-", false)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Body.Close()
	got, err := io.ReadAll(handle.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "audio bytes" || handle.StatusCode != 206 || handle.ContentType != "audio/mpeg" || handle.ContentRange != "bytes 3-13/14" {
		t.Fatalf("handle = %+v body=%q", handle, got)
	}
}

func TestDelegatedStreamRefusesUnpairedPeer(t *testing.T) {
	server, client := newLinkedHosts(t)
	RegisterDelegatedHandler(server, NewGuard(newTrustStore(t)), func(context.Context, string, core.StreamOpts, string) (core.StreamHandle, error) {
		t.Fatal("unpaired request reached the library")
		return core.StreamHandle{}, nil
	}, nil)

	if _, err := RequestDelegatedStream(context.Background(), client, server.ID(), "trk_song", core.StreamOpts{}, "", false); err == nil {
		t.Fatal("unpaired delegated request succeeded")
	}
}

func TestDelegatedCoverUsesCatalogIdentityAndTrust(t *testing.T) {
	ctx := context.Background()
	server, client := newLinkedHosts(t)
	store := newTrustStore(t)
	RegisterDelegatedHandler(server, NewGuard(store), func(context.Context, string, core.StreamOpts, string) (core.StreamHandle, error) {
		return core.StreamHandle{}, core.ErrLibraryItemNotFound
	}, func(_ context.Context, catalogID string, size int) (core.CoverArt, error) {
		if catalogID != "trk_cover" || size != 300 {
			t.Errorf("cover request = %q size %d", catalogID, size)
		}
		return core.CoverArt{Body: io.NopCloser(strings.NewReader("JPEG")), ContentType: "image/jpeg"}, nil
	})
	if _, err := RequestDelegatedCover(ctx, client, server.ID(), "trk_cover", 300); err == nil {
		t.Fatal("unpaired peer received artwork")
	}
	if err := store.CreateDevice(ctx, db.CreateDeviceParams{ID: "phone-device", Name: "Phone", TokenHash: "phone-token"}); err != nil {
		t.Fatal(err)
	}
	if err := NewGuard(store).Trust(ctx, client.ID(), "phone-device", "Phone"); err != nil {
		t.Fatal(err)
	}
	art, err := RequestDelegatedCover(ctx, client, server.ID(), "trk_cover", 300)
	if err != nil {
		t.Fatal(err)
	}
	defer art.Body.Close()
	data, err := io.ReadAll(art.Body)
	if err != nil || string(data) != "JPEG" || art.ContentType != "image/jpeg" {
		t.Fatalf("cover = %q, %q, %v", data, art.ContentType, err)
	}
}

func TestDelegatorPrefersServerThenMostRecentlyReachedPeer(t *testing.T) {
	ctx := context.Background()
	client, server, recent := newThreeLinkedHosts(t)
	clientStore := newTrustStore(t)
	for _, device := range []db.CreateDeviceParams{
		{ID: "server-device", Name: "Server", TokenHash: "server-token", IsServer: 1},
		{ID: "recent-device", Name: "Laptop", TokenHash: "recent-token"},
	} {
		if err := clientStore.CreateDevice(ctx, device); err != nil {
			t.Fatal(err)
		}
	}
	clientGuard := NewGuard(clientStore)
	if err := clientGuard.Trust(ctx, recent.ID(), "recent-device", "Laptop"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := clientGuard.Trust(ctx, server.ID(), "server-device", "Server"); err != nil {
		t.Fatal(err)
	}

	serve := func(h host.Host, label string) {
		q := newTrustStore(t)
		if err := q.CreateDevice(ctx, db.CreateDeviceParams{ID: "client-device", Name: "Phone", TokenHash: label + "-token"}); err != nil {
			t.Fatal(err)
		}
		guard := NewGuard(q)
		if err := guard.Trust(ctx, client.ID(), "client-device", "Phone"); err != nil {
			t.Fatal(err)
		}
		RegisterDelegatedHandler(h, guard, func(context.Context, string, core.StreamOpts, string) (core.StreamHandle, error) {
			return core.StreamHandle{Body: io.NopCloser(strings.NewReader(label)), ContentLength: int64(len(label)), StatusCode: 200}, nil
		}, nil)
	}
	serve(server, "server")
	serve(recent, "recent")

	d := NewDelegator(func() host.Host { return client }, func() *Guard { return clientGuard }, clientStore)
	handle, err := d.Stream(ctx, "trk_song", core.StreamOpts{}, "")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(handle.Body)
	_ = handle.Body.Close()
	if string(got) != "server" {
		t.Fatalf("picked %q, want server", got)
	}
}

func newThreeLinkedHosts(t *testing.T) (host.Host, host.Host, host.Host) {
	t.Helper()
	first, second := newLinkedHosts(t)
	third, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = third.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := first.Connect(ctx, peer.AddrInfo{ID: third.ID(), Addrs: third.Addrs()}); err != nil {
		t.Fatal(err)
	}
	return first, second, third
}

func TestDelegatorCachesPlayableProbes(t *testing.T) {
	ctx := context.Background()
	server, client := newLinkedHosts(t)
	serverStore := newTrustStore(t)
	if err := serverStore.CreateDevice(ctx, db.CreateDeviceParams{ID: "phone-device", Name: "Phone", TokenHash: "phone-token"}); err != nil {
		t.Fatal(err)
	}
	serverGuard := NewGuard(serverStore)
	if err := serverGuard.Trust(ctx, client.ID(), "phone-device", "Phone"); err != nil {
		t.Fatal(err)
	}
	var opens atomic.Int32
	RegisterDelegatedHandler(server, serverGuard, func(context.Context, string, core.StreamOpts, string) (core.StreamHandle, error) {
		opens.Add(1)
		return core.StreamHandle{Body: io.NopCloser(strings.NewReader("")), StatusCode: 200}, nil
	}, nil)
	clientStore := newTrustStore(t)
	if err := clientStore.CreateDevice(ctx, db.CreateDeviceParams{ID: "server-device", Name: "Server", TokenHash: "server-token", IsServer: 1}); err != nil {
		t.Fatal(err)
	}
	clientGuard := NewGuard(clientStore)
	if err := clientGuard.Trust(ctx, server.ID(), "server-device", "Server"); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	d := NewDelegator(func() host.Host { return client }, func() *Guard { return clientGuard }, clientStore)
	d.now = func() time.Time { return now }

	if !d.Playable(ctx, "trk_song") || !d.Playable(ctx, "trk_song") {
		t.Fatal("track not playable")
	}
	if got := opens.Load(); got != 1 {
		t.Fatalf("probes = %d, want 1 within the cache window", got)
	}
	now = now.Add(playableProbeTTL)
	if !d.Playable(ctx, "trk_song") || opens.Load() != 2 {
		t.Fatalf("expired answer was not re-probed (probes = %d)", opens.Load())
	}
}

func TestDelegatorSkipsProbesWhileNoPeerIsReachable(t *testing.T) {
	store := newTrustStore(t)
	client, _ := newLinkedHosts(t)
	now := time.Unix(1000, 0)
	d := NewDelegator(func() host.Host { return client }, func() *Guard { return NewGuard(store) }, store)
	d.now = func() time.Time { return now }

	if d.Playable(context.Background(), "trk_song") {
		t.Fatal("playable with no paired device")
	}
	if want := now.Add(unreachableProbeTTL); !d.unreachableUntil.Equal(want) {
		t.Fatalf("unreachableUntil = %v, want %v", d.unreachableUntil, want)
	}
}
