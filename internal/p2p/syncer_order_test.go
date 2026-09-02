package p2p

import (
	"testing"

	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// A round's changes are reconciled per author, so a play authored by one
// device would otherwise be projected before the catalog entity another device
// minted for the same track -- and the play then fails the plays.catalog_id
// foreign key with the log already committed, so nothing retries it.
func TestFilterCatalogSplitsEntitiesFromTheRowsNamingThem(t *testing.T) {
	batch := []reverbsync.SyncChange{
		{EntityType: "play", EntityID: "play_1"},
		{EntityType: reverbsync.EntityCatalog, EntityID: "cat_1"},
		{EntityType: "track", EntityID: "cat_1", Field: "title"},
		{EntityType: reverbsync.EntityCatalog, EntityID: "cat_2"},
	}
	entities := filterCatalog(batch, true)
	if len(entities) != 2 || entities[0].EntityID != "cat_1" || entities[1].EntityID != "cat_2" {
		t.Fatalf("catalog half = %v, want both entities in order", entities)
	}
	rest := filterCatalog(batch, false)
	if len(rest) != 2 || rest[0].EntityType != "play" || rest[1].EntityType != "track" {
		t.Fatalf("remainder = %v, want the play and the rename in order", rest)
	}
}
