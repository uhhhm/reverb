package p2p

import (
	"context"
	"encoding/json"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
	"strings"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/events"
)

// SyncNow brackets every round with sync.started / sync.finished so the UI can
// show an indicator, including when there is nothing to sync with.
func TestSyncNowPublishesStartedAndFinished(t *testing.T) {
	bus := events.New()
	started, unsubStarted := bus.Subscribe(TopicSyncStarted)
	defer unsubStarted()
	finished, unsubFinished := bus.Subscribe(TopicSyncFinished)
	defer unsubFinished()

	s := NewSyncer(nil, nil, nil, nil, "dev1")
	s.SetBus(bus)
	s.SyncNow(context.Background())

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("no sync.started event")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("no sync.finished event")
	}
}

// A Syncer without a bus must still run (the bus is optional wiring).
func TestSyncNowWithoutBusDoesNotPanic(t *testing.T) {
	NewSyncer(nil, nil, nil, nil, "dev1").SyncNow(context.Background())
}

// A refused exchange must reach the user, not just the process log.
func TestSyncNowReportsRefusedPeer(t *testing.T) {
	server, client := newLinkedHosts(t)
	server.SetStreamHandler("/reverb/sync/1.0.0", func(st network.Stream) {
		defer st.Close()
		_ = json.NewEncoder(st).Encode(reverbsync.SyncResponse{Error: "pairing required"})
	})
	q := newTrustStore(t)
	ctx := context.Background()
	if err := q.CreateDevice(ctx, db.CreateDeviceParams{ID: "remote", Name: "Laptop", TokenHash: "test"}); err != nil {
		t.Fatal(err)
	}
	guard := NewGuard(q)
	if err := guard.Trust(ctx, server.ID(), "remote", "Laptop"); err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	finished, unsubscribe := bus.Subscribe(TopicSyncFinished)
	defer unsubscribe()
	s := NewSyncer(client, reverbsync.NewSyncStore(q), guard, nil, "local")
	s.SetBus(bus)
	s.SyncNow(ctx)
	select {
	case ev := <-finished:
		data, _ := json.Marshal(ev.Payload)
		if !strings.Contains(string(data), "pairing required") {
			t.Fatalf("sync finished without reporting failure: %s", data)
		}
	case <-time.After(time.Second):
		t.Fatal("no completion")
	}
}

func TestRequestSyncCoalescesAndRetainsCompletion(t *testing.T) {
	server, client := newLinkedHosts(t)
	entered, release := make(chan struct{}), make(chan struct{})
	server.SetStreamHandler("/reverb/sync/1.0.0", func(st network.Stream) {
		defer st.Close()
		close(entered)
		<-release
		_ = json.NewEncoder(st).Encode(reverbsync.SyncResponse{})
	})
	q := newTrustStore(t)
	ctx := context.Background()
	if err := q.CreateDevice(ctx, db.CreateDeviceParams{ID: "remote", Name: "Laptop", TokenHash: "test"}); err != nil {
		t.Fatal(err)
	}
	guard := NewGuard(q)
	if err := guard.Trust(ctx, server.ID(), "remote", "Laptop"); err != nil {
		t.Fatal(err)
	}
	s := NewSyncer(client, reverbsync.NewSyncStore(q), guard, nil, "local")
	first := s.RequestSync(ctx)
	if first.State != "pending" {
		t.Fatalf("request = %+v", first)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("exchange not started")
	}
	second := s.RequestSync(ctx)
	close(release)
	if second.ID != first.ID || second.State != "running" {
		t.Fatalf("duplicate request = %+v", second)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		result := s.Status()
		if result.State == "completed" {
			if result.Peers != 1 || result.Succeeded != 1 || result.FinishedAt == 0 {
				t.Fatalf("result = %+v", result)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no retained completion: %+v", s.Status())
}
