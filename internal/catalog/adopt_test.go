package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/store"
)

func newAdoptStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

func ids() func() string {
	n := 0
	return func() string {
		n++
		return string(rune('a' + n))
	}
}

var song = catalog.Identity{Kind: "track", Title: "Song", Artist: "Band", Album: "Record", DurationMs: 180000}

// A catalog id is minted from a random token, so the id a peer sends names an
// entity this device has never heard of. Adopting it has to make that id
// resolvable, or every change keyed on it is undeliverable.
func TestAdoptMakesARemoteIDResolvable(t *testing.T) {
	st := newAdoptStore(t)
	svc := catalog.NewService(st.Q(), time.Now, ids())
	ctx := context.Background()

	got, err := svc.Adopt(ctx, "trk_remote", song)
	if err != nil {
		t.Fatal(err)
	}
	if got != "trk_remote" {
		t.Fatalf("Adopt = %q, want the peer's id kept when the track is new here", got)
	}
	if resolved := svc.Resolve(ctx, "trk_remote"); resolved != "trk_remote" {
		t.Fatalf("Resolve = %q, want trk_remote", resolved)
	}
}

// The same track already known here under a locally minted id must not become a
// second entity — and the peer's id has to keep resolving afterwards, because
// the peer goes on sending changes keyed on it.
func TestAdoptFusesWithATrackAlreadyKnown(t *testing.T) {
	st := newAdoptStore(t)
	svc := catalog.NewService(st.Q(), time.Now, ids())
	ctx := context.Background()

	local, err := svc.CanonicalFor(ctx, song)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Adopt(ctx, "trk_remote", song)
	if err != nil {
		t.Fatal(err)
	}
	if got != local {
		t.Fatalf("Adopt = %q, want it fused onto the local entity %q", got, local)
	}
	if resolved := svc.Resolve(ctx, "trk_remote"); resolved != local {
		t.Fatalf("Resolve(trk_remote) = %q, want %q", resolved, local)
	}
	if resolved := svc.Resolve(ctx, local); resolved != local {
		t.Fatalf("Resolve(%q) = %q, want it to resolve to itself", local, resolved)
	}
}

// Adopting twice is what happens when a peer re-sends its log, and must not
// fork the entity or change the answer.
func TestAdoptIsIdempotent(t *testing.T) {
	st := newAdoptStore(t)
	svc := catalog.NewService(st.Q(), time.Now, ids())
	ctx := context.Background()

	first, err := svc.Adopt(ctx, "trk_remote", song)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Adopt(ctx, "trk_remote", song)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("Adopt returned %q then %q", first, second)
	}
}

// An id this device has never seen resolves to itself: a caller must always get
// a usable key back, not an empty one.
func TestResolveFallsBackToTheIDItself(t *testing.T) {
	st := newAdoptStore(t)
	svc := catalog.NewService(st.Q(), time.Now, ids())
	if got := svc.Resolve(context.Background(), "trk_unknown"); got != "trk_unknown" {
		t.Fatalf("Resolve = %q, want trk_unknown", got)
	}
}

// A newly minted entity has to be published, or a peer receiving a change keyed
// on its id cannot tell which track it means.
func TestMintingPublishesTheEntity(t *testing.T) {
	st := newAdoptStore(t)
	rec := &recorder{}
	svc := catalog.NewService(st.Q(), time.Now, ids()).WithEmitter(rec)
	ctx := context.Background()

	cid, err := svc.CanonicalFor(ctx, song)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.ids) != 1 || rec.ids[0] != cid {
		t.Fatalf("emitted %v, want just %q", rec.ids, cid)
	}
	// A second call resolves the existing entity; there is nothing new to say.
	if _, err := svc.CanonicalFor(ctx, song); err != nil {
		t.Fatal(err)
	}
	if len(rec.ids) != 1 {
		t.Fatalf("emitted %v, want no repeat for an entity that already existed", rec.ids)
	}
}

type recorder struct{ ids []string }

func (r *recorder) EmitCatalogEntity(_ context.Context, id string, _ catalog.Identity) {
	r.ids = append(r.ids, id)
}
