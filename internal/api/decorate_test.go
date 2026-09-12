package api

import (
	"context"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/cover"
	"github.com/uhhhm/reverb/internal/crop"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
)

// decorateTracks is the read path that carries canonical recording identifiers
// onto adapter tracks, so a recommendation seed taken from an API result keeps
// the MBID the catalog already knows.
func TestDecorateTracksProjectsCatalogIdentities(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/decorate.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}

	if err := st.Q().InsertCatalogEntity(ctx, db.InsertCatalogEntityParams{
		ID: "cat_track", Kind: "track", Title: "One More Time", Artist: "Daft Punk",
		Isrc: "USX1234", Mbid: "recording-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Q().UpsertBackendBinding(ctx, db.UpsertBackendBindingParams{
		CatalogID: "cat_track", LibraryIdentity: "lib", BackendID: "backend_1",
	}); err != nil {
		t.Fatal(err)
	}
	// An artist binding carries its own MBID, but recording identities must not
	// be projected onto it.
	if err := st.Q().InsertCatalogEntity(ctx, db.InsertCatalogEntityParams{
		ID: "cat_artist", Kind: "artist", Title: "Daft Punk", Mbid: "artist-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Q().UpsertBackendBinding(ctx, db.UpsertBackendBindingParams{
		CatalogID: "cat_artist", LibraryIdentity: "lib", BackendID: "backend_artist",
	}); err != nil {
		t.Fatal(err)
	}

	srv := NewServer(Deps{
		Catalog:   st.Q(),
		Covers:    cover.New(st.Q(), t.TempDir()),
		Entities:  override.NewEntities(st.Q()),
		Overrides: override.New(st.Q()),
		Crop:      crop.New(st.Q()),
	})

	tracks := []core.Track{
		{ID: "backend_1"},
		{ID: "backend_1", ISRC: "existing-isrc", MBID: "existing-mbid"},
		{ID: "backend_unknown"},
		{ID: "backend_artist"},
	}
	srv.decorateTracks(ctx, tracks)

	if tracks[0].ISRC != "USX1234" || tracks[0].MBID != "recording-1" {
		t.Fatalf("identity = isrc %q mbid %q, want catalog values", tracks[0].ISRC, tracks[0].MBID)
	}
	if tracks[1].ISRC != "existing-isrc" || tracks[1].MBID != "existing-mbid" {
		t.Fatalf("existing identity was overwritten: isrc %q mbid %q", tracks[1].ISRC, tracks[1].MBID)
	}
	if tracks[2].ISRC != "" || tracks[2].MBID != "" {
		t.Fatalf("unbound track gained identity: isrc %q mbid %q", tracks[2].ISRC, tracks[2].MBID)
	}
	if tracks[3].ISRC != "" || tracks[3].MBID != "" {
		t.Fatalf("artist binding leaked a recording identity: isrc %q mbid %q", tracks[3].ISRC, tracks[3].MBID)
	}
}

// A server with no catalog (tests/legacy wiring) still decorates without one.
func TestDecorateTracksWithoutCatalog(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/decorate_nil.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(Deps{
		Covers:    cover.New(st.Q(), t.TempDir()),
		Entities:  override.NewEntities(st.Q()),
		Overrides: override.New(st.Q()),
		Crop:      crop.New(st.Q()),
	})

	tracks := []core.Track{{ID: "backend_1"}}
	srv.decorateTracks(ctx, tracks)
	if tracks[0].ISRC != "" || tracks[0].MBID != "" {
		t.Fatalf("no catalog should leave identity empty, got isrc %q mbid %q", tracks[0].ISRC, tracks[0].MBID)
	}
}
