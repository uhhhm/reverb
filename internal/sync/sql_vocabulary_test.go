package sync_test

import (
	"context"
	"slices"
	"testing"

	"github.com/uhhhm/reverb/internal/store/db"
	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

// sqlc cannot take Go constants, so the queries that read the log by entity
// and field spell the names out. Each case writes the log through the
// constants and checks a query's rows, so a constant renamed without its SQL
// (or the reverse) turns one of these rows wrong instead of silently matching
// nothing.
func TestQueriesReadTheLogInItsVocabulary(t *testing.T) {
	st := newTestStoreSync(t)
	ctx := context.Background()
	q := st.Q()
	ss := syncpkg.NewSyncStore(q)
	createDevice(t, st, "dev_a", "a", 0)
	appendChange := func(entity, id, field string, value any) {
		t.Helper()
		if _, err := ss.AppendChange(ctx, "dev_a", syncpkg.SyncChange{
			EntityType: entity, EntityID: id, Field: field, Value: value, UpdatedAt: 1000,
		}); err != nil {
			t.Fatalf("append %s/%s %s: %v", entity, id, field, err)
		}
	}

	t.Run("ListDeletedFileHashes", func(t *testing.T) {
		appendChange(syncpkg.EntityFile, "hash-gone", syncpkg.FieldDeleted, nil)
		got, err := q.ListDeletedFileHashes(ctx)
		if err != nil || !slices.Equal(got, []string{"hash-gone"}) {
			t.Fatalf("got %v, %v; want the file tombstoned through the constants", got, err)
		}
	})

	t.Run("ListUnprojectedPlays", func(t *testing.T) {
		record := map[string]any{"catalogId": "trk_x", "userId": "u", "playedAt": 1}
		appendChange(syncpkg.EntityPlay, "play-kept", syncpkg.FieldRecord, record)
		appendChange(syncpkg.EntityPlay, "play-deleted", syncpkg.FieldRecord, record)
		appendChange(syncpkg.EntityPlay, "play-deleted", syncpkg.FieldDeleted, nil)
		rows, err := q.ListUnprojectedPlays(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, r := range rows {
			ids = append(ids, r.EntityID)
		}
		if !slices.Equal(ids, []string{"play-kept"}) {
			t.Fatalf("got %v; want the recorded play and not the tombstoned one", ids)
		}
	})

	t.Run("ListDeletedPlays", func(t *testing.T) {
		// Search-minted, so it stays out of household browsing below.
		if err := q.InsertCatalogEntity(ctx, db.InsertCatalogEntityParams{ID: "trk_x", Kind: "track", Title: "x", Source: "deezer"}); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"held-kept", "held-deleted"} {
			if err := q.InsertPlay(ctx, db.InsertPlayParams{ID: id, UserID: "u", CatalogID: "trk_x", PlayedAt: 1, CreatedAt: 1}); err != nil {
				t.Fatal(err)
			}
		}
		appendChange(syncpkg.EntityPlay, "held-deleted", syncpkg.FieldDeleted, nil)
		got, err := q.ListDeletedPlays(ctx)
		if err != nil || !slices.Equal(got, []string{"held-deleted"}) {
			t.Fatalf("got %v, %v; want the play tombstoned through the constants", got, err)
		}
	})

	t.Run("ListBrowsableCatalogTracks", func(t *testing.T) {
		// A search-minted identity is absent until the log says otherwise, a
		// library-minted one present until it says otherwise.
		for _, e := range []struct{ id, source string }{
			{"trk_search_added", "deezer"},
			{"trk_library_removed", ""},
			{"trk_library_tombstoned", ""},
			{"trk_library_untouched", ""},
		} {
			if err := q.InsertCatalogEntity(ctx, db.InsertCatalogEntityParams{ID: e.id, Kind: "track", Title: e.id, Source: e.source}); err != nil {
				t.Fatal(err)
			}
		}
		appendChange(syncpkg.EntityTrack, "trk_search_added", syncpkg.FieldLibraryPresent, true)
		appendChange(syncpkg.EntityTrack, "trk_library_removed", syncpkg.FieldLibraryPresent, false)
		appendChange(syncpkg.EntityTrack, "trk_library_tombstoned", syncpkg.FieldDeleted, nil)
		rows, err := q.ListBrowsableCatalogTracks(ctx, db.ListBrowsableCatalogTracksParams{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		slices.Sort(ids)
		if want := []string{"trk_library_untouched", "trk_search_added"}; !slices.Equal(ids, want) {
			t.Fatalf("got %v, want %v", ids, want)
		}
	})
}
