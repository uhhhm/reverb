package pairtest_test

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/uhhhm/reverb/internal/materialize"
	"github.com/uhhhm/reverb/internal/sync/pairtest"
	"github.com/uhhhm/reverb/internal/tastesettings"
)

// Each test here is one way a replication test could pass while replication
// is broken. The harness must turn every one into a failure.

// recorder stands in for the test, so a harness failure can be observed rather
// than failing this test. Fatal stops the helper the way testing.T does.
type recorder struct {
	testing.TB
	mu     sync.Mutex
	failed []string
}

func (r *recorder) Helper() {}
func (r *recorder) Fatal(args ...any) {
	r.fail(fmt.Sprint(args...))
}
func (r *recorder) Fatalf(format string, args ...any) {
	r.fail(fmt.Sprintf(format, args...))
}
func (r *recorder) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = append(r.failed, fmt.Sprintf(format, args...))
}
func (r *recorder) fail(msg string) {
	r.mu.Lock()
	r.failed = append(r.failed, msg)
	r.mu.Unlock()
	runtime.Goexit()
}

// failure runs fn against a recorder and returns what it reported.
func failure(t *testing.T, fn func(tb testing.TB)) string {
	t.Helper()
	r := &recorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(r)
	}()
	<-done
	return strings.Join(r.failed, "; ")
}

// settings builds the real taste-settings projection, the smallest replicated
// fact the product has.
func settings(svc *map[string]*tastesettings.Service) pairtest.Projector {
	return func(d *pairtest.Device, m *materialize.Service) {
		s := tastesettings.New(d.Store.Q(), d.Emitter())
		m.WithTasteSettings(s)
		(*svc)[d.ID] = s
	}
}

func setAdventurousness(t *testing.T, s *tastesettings.Service, n int) {
	t.Helper()
	if _, err := s.Update(context.Background(), tastesettings.Patch{Adventurousness: &n}); err != nil {
		t.Fatal(err)
	}
}

func TestSyncToWithoutEchoPassesForAProjectionThatDoesNotEmit(t *testing.T) {
	svc := map[string]*tastesettings.Service{}
	a, b := pairtest.NewPair(t, settings(&svc))
	setAdventurousness(t, svc[a.ID], 80)

	if msg := failure(t, func(tb testing.TB) { pairtest.SyncToWithoutEcho(tb, a, b) }); msg != "" {
		t.Fatalf("a correct projection failed the harness: %s", msg)
	}
	got, err := svc[b.ID].Get(context.Background())
	if err != nil || got.Adventurousness != 80 {
		t.Fatalf("b settings = %+v, %v", got, err)
	}
}

// echoing re-publishes what it applies, the bug materialize exists to prevent.
type echoing struct{ *tastesettings.Service }

func (e echoing) Apply(ctx context.Context, field string, decode func(any) error) error {
	var n int
	if err := decode(&n); err != nil {
		return err
	}
	_, err := e.Update(ctx, tastesettings.Patch{Adventurousness: &n})
	return err
}

func TestPeerThatReemitsWhatItAppliedFails(t *testing.T) {
	svc := map[string]*tastesettings.Service{}
	a, b := pairtest.NewPair(t, func(d *pairtest.Device, m *materialize.Service) {
		s := tastesettings.New(d.Store.Q(), d.Emitter())
		m.WithTasteSettings(echoing{s})
		svc[d.ID] = s
	})
	setAdventurousness(t, svc[a.ID], 80)

	msg := failure(t, func(tb testing.TB) { pairtest.SyncToWithoutEcho(tb, a, b) })
	if !strings.Contains(msg, "echo") {
		t.Fatalf("an echoing peer passed; reported %q", msg)
	}
}

func TestMaterializerMissingTheServiceUnderTestFails(t *testing.T) {
	svc := map[string]*tastesettings.Service{}
	a, b := pairtest.NewPair(t, func(d *pairtest.Device, m *materialize.Service) {
		// Built, but never attached to the materializer.
		svc[d.ID] = tastesettings.New(d.Store.Q(), d.Emitter())
	})
	setAdventurousness(t, svc[a.ID], 80)

	msg := failure(t, func(tb testing.TB) { pairtest.SyncToWithoutEcho(tb, a, b) })
	if !strings.Contains(msg, "projected nothing") {
		t.Fatalf("a sync that projected nothing passed the no-echo check; reported %q", msg)
	}
}

func TestSyncingADeviceOntoItselfFails(t *testing.T) {
	svc := map[string]*tastesettings.Service{}
	a, _ := pairtest.NewPair(t, settings(&svc))
	setAdventurousness(t, svc[a.ID], 80)

	msg := failure(t, func(tb testing.TB) { pairtest.SyncTo(tb, a, a) })
	if !strings.Contains(msg, "same store") {
		t.Fatalf("syncing a device onto its own store passed; reported %q", msg)
	}
}

func TestOneWaySyncIsNotConvergence(t *testing.T) {
	svc := map[string]*tastesettings.Service{}
	a, b := pairtest.NewPair(t, settings(&svc))
	setAdventurousness(t, svc[a.ID], 80)
	off := false
	if _, err := svc[b.ID].Update(context.Background(), tastesettings.Patch{OnlineRecommendations: &off}); err != nil {
		t.Fatal(err)
	}
	pairtest.SyncTo(t, a, b)

	msg := failure(t, func(tb testing.TB) { pairtest.AssertConverged(tb, a, b) })
	if !strings.Contains(msg, "diverged") {
		t.Fatalf("a one-way sync counted as convergence; reported %q", msg)
	}

	if msg := failure(t, func(tb testing.TB) { pairtest.Converge(tb, a, b) }); msg != "" {
		t.Fatalf("converging both ways failed: %s", msg)
	}
	for _, d := range []*pairtest.Device{a, b} {
		got, err := svc[d.ID].Get(context.Background())
		if err != nil || got.Adventurousness != 80 || got.OnlineRecommendations {
			t.Fatalf("%s settings after converge = %+v, %v", d.ID, got, err)
		}
	}
}

// A device whose log is not a separate database would let a test read back the
// fact it wrote and call it replicated.
func TestPairedDevicesShareNothing(t *testing.T) {
	a, b := pairtest.NewPair(t, func(*pairtest.Device, *materialize.Service) {})
	if a.ID == b.ID || a.Store == b.Store || a.Log == b.Log {
		t.Fatalf("paired devices share state: %+v %+v", a, b)
	}
	changes, err := b.Log.ListSince(context.Background(), 0, 10)
	if err != nil || len(changes) != 0 {
		t.Fatalf("a fresh device has changes %+v, %v", changes, err)
	}
}
