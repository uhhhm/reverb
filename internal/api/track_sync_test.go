package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/auth"
	"github.com/uhhhm/reverb/internal/crop"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

// trackSyncServer wires the edit paths against a real sync store, so the test
// observes what a paired device would actually receive.
func trackSyncServer(t *testing.T) (*Server, *store.Store, *http.Cookie) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/track_sync.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewService(st.Q(), time.Now)
	if err := authSvc.EnsureSeed(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := syncpkg.EnsureServerDevice(context.Background(), st.Q()); err != nil {
		t.Fatal(err)
	}
	_, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{AllowedHosts: testAllowedHosts,
		Auth:         authSvc,
		Search:       registry.NewRegistry("search"),
		Downloader:   registry.NewRegistry("downloader"),
		Overrides:    override.New(st.Q()),
		Crop:         crop.New(st.Q()),
		SyncStore:    syncpkg.NewSyncStore(st.Q()),
		PairingStore: st.Q(),
		OfflineSet:   st.Q(),
	})
	return srv, st, &http.Cookie{Name: sessionCookie, Value: tok}
}

// bindTrack gives a backend track id a stable catalog id, which is the identity
// per-track metadata syncs under.
func bindTrack(t *testing.T, st *store.Store, catalogID, backendID string) {
	t.Helper()
	ctx := context.Background()
	if err := st.Q().InsertCatalogEntity(ctx, db.InsertCatalogEntityParams{
		ID: catalogID, Kind: "track", Title: "T", Artist: "A",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Q().UpsertBackendBinding(ctx, db.UpsertBackendBindingParams{
		CatalogID:       catalogID,
		LibraryIdentity: "lib",
		BackendID:       backendID,
	}); err != nil {
		t.Fatal(err)
	}
}

func changesFor(t *testing.T, st *store.Store, field string) []syncpkg.SyncChange {
	t.Helper()
	ss := syncpkg.NewSyncStore(st.Q())
	all, err := ss.ListSince(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []syncpkg.SyncChange
	for _, c := range all {
		if c.Field == field {
			out = append(out, c)
		}
	}
	return out
}

// A rename has to reach paired devices, and it travels under the CATALOG id:
// the backend track id is local to one library backend.
func TestRenamePublishesASyncChange(t *testing.T) {
	srv, st, cookie := trackSyncServer(t)
	bindTrack(t, st, "cat_1", "backend_1")

	if rec := do(t, srv, cookie, http.MethodPut, "/api/v1/library/track/backend_1/name", `{"title":"Real Title","artist":"Real Artist"}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	titles := changesFor(t, st, "title")
	if len(titles) != 1 {
		t.Fatalf("title changes = %d, want 1", len(titles))
	}
	if titles[0].EntityID != "cat_1" {
		t.Errorf("entity id = %q, want the catalog id", titles[0].EntityID)
	}
	if titles[0].Value != "Real Title" {
		t.Errorf("value = %v", titles[0].Value)
	}
	if len(changesFor(t, st, "artist")) != 1 {
		t.Error("artist must be published too — it is a separate LWW field")
	}
}

func TestCropPublishesASyncChange(t *testing.T) {
	srv, st, cookie := trackSyncServer(t)
	bindTrack(t, st, "cat_1", "backend_1")

	if rec := do(t, srv, cookie, http.MethodPut, "/api/v1/library/track/backend_1/crop", `{"startMs":5000,"endMs":90000}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	starts := changesFor(t, st, "cropStartMs")
	if len(starts) != 1 || starts[0].EntityID != "cat_1" {
		t.Fatalf("crop start changes = %v", starts)
	}
	if len(changesFor(t, st, "cropEndMs")) != 1 {
		t.Error("crop end must be published too")
	}
}

// An uncrop travels as both boundaries returning to zero — there is no
// tombstone, because the track still exists and the file was never modified.
func TestUncropPublishesZeroedBoundaries(t *testing.T) {
	srv, st, cookie := trackSyncServer(t)
	bindTrack(t, st, "cat_1", "backend_1")

	if rec := do(t, srv, cookie, http.MethodPut, "/api/v1/library/track/backend_1/crop", `{"startMs":5000,"endMs":90000}`); rec.Code != http.StatusOK {
		t.Fatalf("crop status = %d", rec.Code)
	}
	if rec := do(t, srv, cookie, http.MethodDelete, "/api/v1/library/track/backend_1/crop", ""); rec.Code != http.StatusOK {
		t.Fatalf("uncrop status = %d", rec.Code)
	}
	starts := changesFor(t, st, "cropStartMs")
	last := starts[len(starts)-1]
	if v, ok := last.Value.(float64); !ok || v != 0 {
		t.Fatalf("last crop start = %v, want 0", last.Value)
	}
}

// A track with no catalog binding still works locally; it just has no identity
// peers could agree on, so nothing is published.
func TestUnboundTrackPublishesNothing(t *testing.T) {
	srv, st, cookie := trackSyncServer(t)

	if rec := do(t, srv, cookie, http.MethodPut, "/api/v1/library/track/backend_1/crop", `{"startMs":5000}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := changesFor(t, st, "cropStartMs"); len(got) != 0 {
		t.Fatalf("published %v for an unbound track", got)
	}
}
