// Package pairtest runs two paired devices in one test, so a replicated fact
// can be written on one, synced the way a sync round does, and read back on
// the other.
//
// Each device has its own database, change log and materializer. The helpers
// fail loudly on the ways a replication test can pass while replication is
// broken: syncing a device onto its own store, calling a one-way sync
// convergence, a peer that re-emits what it applied, and a no-echo check that
// passes only because the materializer never projected anything.
//
// Only tests import it, so it is never linked into a product binary.
package pairtest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/crop"
	"github.com/uhhhm/reverb/internal/materialize"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
	"github.com/uhhhm/reverb/internal/syncemit"
)

// logLimit is larger than any test's change log.
const logLimit = 100000

// Device is one Reverb install.
type Device struct {
	ID    string
	Store *store.Store
	Log   *reverbsync.SyncStore
}

// Projector builds the services a device projects into and attaches them to
// its materializer, which already carries track overrides and crops.
type Projector func(d *Device, m *materialize.Service)

// NewPair returns two devices, dev_a and dev_b, each knowing the other as a
// paired peer and each projecting through project.
func NewPair(t testing.TB, project Projector) (a, b *Device) {
	t.Helper()
	return NewDevice(t, "dev_a", project, "dev_b"), NewDevice(t, "dev_b", project, "dev_a")
}

// NewDevice returns a device that is its own server and knows peers.
func NewDevice(t testing.TB, id string, project Projector, peers ...string) *Device {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/reverb.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	for i, p := range append([]string{id}, peers...) {
		isServer := int64(0)
		if i == 0 {
			isServer = 1
		}
		if err := st.Q().CreateDevice(context.Background(), db.CreateDeviceParams{
			ID: p, Name: p, TokenHash: "hash_" + p, IsServer: isServer,
		}); err != nil {
			t.Fatal(err)
		}
	}
	d := &Device{ID: id, Store: st, Log: reverbsync.NewSyncStore(st.Q())}
	m := materialize.New(override.New(st.Q()), crop.New(st.Q()))
	project(d, m)
	d.Log.SetMaterializer(m)
	return d
}

// Emitter publishes local edits into this device's log, authored by it.
func (d *Device) Emitter() *syncemit.Service {
	return syncemit.New(d.Log, nil, d.Resolve)
}

// Resolve names this device as the author of its edits.
func (d *Device) Resolve(context.Context) string { return d.ID }

// Changes returns every change in the device's log.
func (d *Device) Changes(t testing.TB) []reverbsync.SyncChange {
	t.Helper()
	changes, err := d.Log.ListSince(context.Background(), 0, logLimit)
	if err != nil {
		t.Fatal(err)
	}
	return changes
}

// SyncTo sends everything in from's log to to, the way a sync round does.
func SyncTo(t testing.TB, from, to *Device) {
	t.Helper()
	if from == to || from.Store == to.Store || from.Log == to.Log {
		t.Fatalf("syncing %s onto the same store it came from proves nothing", from.ID)
	}
	_, _, rejected, err := to.Log.Reconcile(context.Background(), from.ID, 0, from.Changes(t))
	if err != nil {
		t.Fatalf("reconcile %s onto %s: %v", from.ID, to.ID, err)
	}
	// Duplicates and changes that lose to a tombstone are rejected in normal
	// operation. An author the receiver does not know is a harness mistake
	// that would silently drop the fact under test.
	for _, ch := range rejected {
		if err := to.Log.ValidateDevice(context.Background(), ch.DeviceID); err != nil {
			t.Fatalf("%s does not know %s, the author of %+v: %v", to.ID, ch.DeviceID, ch, err)
		}
	}
}

// SyncToWithoutEcho syncs from onto to and fails if to authored any change
// while applying them: applying a peer's change must not publish it again.
// It also fails if the sync projected nothing on to, since then there was
// nothing that could have echoed.
func SyncToWithoutEcho(t testing.TB, from, to *Device) {
	t.Helper()
	authored := authoredBy(to.Changes(t), to.ID)
	before := projection(t, to)
	SyncTo(t, from, to)
	if echoed := authoredBy(to.Changes(t), to.ID); len(echoed) > len(authored) {
		t.Fatalf("applying %s's changes made %s echo %+v", from.ID, to.ID, echoed[len(authored):])
	}
	if projection(t, to) == before {
		t.Fatalf("sync %s -> %s projected nothing; is the service under test attached to the materializer?", from.ID, to.ID)
	}
}

// Converge syncs both ways and asserts both logs hold the same changes.
func Converge(t testing.TB, a, b *Device) {
	t.Helper()
	SyncTo(t, a, b)
	SyncTo(t, b, a)
	AssertConverged(t, a, b)
}

// AssertConverged fails unless both logs hold the same changes.
func AssertConverged(t testing.TB, a, b *Device) {
	t.Helper()
	onA, onB := changeKeys(a.Changes(t)), changeKeys(b.Changes(t))
	if !slices.Equal(onA, onB) {
		t.Fatalf("logs diverged: %s has %d changes, %s has %d\n%s: %v\n%s: %v",
			a.ID, len(onA), b.ID, len(onB), a.ID, onA, b.ID, onB)
	}
}

func authoredBy(changes []reverbsync.SyncChange, device string) []reverbsync.SyncChange {
	var out []reverbsync.SyncChange
	for _, ch := range changes {
		if ch.DeviceID == device {
			out = append(out, ch)
		}
	}
	return out
}

// changeKeys identifies each change independently of the revision a log
// stored it under, sorted so two logs compare as sets.
func changeKeys(changes []reverbsync.SyncChange) []string {
	keys := make([]string, 0, len(changes))
	for _, ch := range changes {
		keys = append(keys, fmt.Sprintf("%s/%s/%s/%s@%d:%s", ch.DeviceID, ch.EntityType, ch.EntityID, ch.Field, ch.UpdatedAt, ch.ValueJSON))
	}
	slices.Sort(keys)
	return keys
}

// projection fingerprints every table a materializer can write: everything
// except the change log's own bookkeeping and the device rows.
func projection(t testing.TB, d *Device) [sha256.Size]byte {
	t.Helper()
	ctx := context.Background()
	conn := d.Store.DB()
	rows, err := conn.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table'
		AND name NOT LIKE 'sqlite\_%' ESCAPE '\' AND name NOT LIKE 'sync\_%' ESCAPE '\'
		AND name NOT LIKE 'goose\_%' ESCAPE '\' AND name != 'device' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for _, table := range tables {
		fmt.Fprintf(h, "%s\n", table)
		for _, row := range tableRows(t, conn, table) {
			fmt.Fprintf(h, "%s\n", row)
		}
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func tableRows(t testing.TB, conn *sql.DB, table string) []string {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), `SELECT * FROM "`+table+`"`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = fmt.Sprintf("%v", v)
		}
		out = append(out, strings.Join(parts, "\x1f"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}
