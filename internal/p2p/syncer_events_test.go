package p2p

import (
	"context"
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
