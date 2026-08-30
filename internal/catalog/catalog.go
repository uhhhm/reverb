package catalog

import (
	"context"
	"time"

	"github.com/uhhhm/reverb/internal/store/db"
)

// Identity holds all the metadata a caller knows about a library entity.
// Fields ISRC, MBID, Source, and ExternalID are optional.
// The JSON tags are part of the wire format: an Identity is what a peer is sent
// so it can make sense of a catalog id, so renaming a field renames it for
// every device.
type Identity struct {
	Kind       string `json:"kind"` // "track", "album", "artist"
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	ISRC       string `json:"isrc,omitempty"`
	MBID       string `json:"mbid,omitempty"`
	Source     string `json:"source,omitempty"`
	ExternalID string `json:"externalId,omitempty"`
	DurationMs int    `json:"durationMs"`
}

// Querier is the slice of generated *db.Queries methods that catalog needs.
// It includes the merge methods that Task 4 will use so Task 4 doesn't need to
// change this interface.
type Querier interface {
	GetAliasCatalogID(ctx context.Context, arg db.GetAliasCatalogIDParams) (string, error)
	InsertCatalogEntity(ctx context.Context, arg db.InsertCatalogEntityParams) error
	InsertCatalogAlias(ctx context.Context, arg db.InsertCatalogAliasParams) error
	GetCatalogEntity(ctx context.Context, id string) (db.CatalogEntity, error)
	// Merge methods — Task 4 uses these; include now so Task 4 doesn't change the interface.
	ListAliasesForCatalog(ctx context.Context, catalogID string) ([]db.ListAliasesForCatalogRow, error)
	RepointAliases(ctx context.Context, arg db.RepointAliasesParams) error
	RepointBindings(ctx context.Context, arg db.RepointBindingsParams) error
	DeleteCatalogEntity(ctx context.Context, id string) error
	// Binding methods for merge FK repoint (Task 4).
	GetBackendBinding(ctx context.Context, arg db.GetBackendBindingParams) (db.BackendBinding, error)
	UpsertBackendBinding(ctx context.Context, arg db.UpsertBackendBindingParams) error
	DeleteBindingsForCatalog(ctx context.Context, catalogID string) error
	// plays is the first stored consumer reference the foundation design anticipated:
	// repointing it on merge keeps listening history consolidated under the winner.
	RepointPlays(ctx context.Context, arg db.RepointPlaysParams) error
	// RepointDownloadJobs repoints download_jobs.canonical_id from loser → winner
	// so that job covers continue to resolve after a catalog merge (Task 3).
	RepointDownloadJobs(ctx context.Context, arg db.RepointDownloadJobsParams) error
}

// Service mints and resolves catalog IDs.
type Service struct {
	q     Querier
	now   func() time.Time
	idgen func() string // returns a uuid-ish token (no prefix)

	// emitter publishes newly minted entities to the sync log; nil when this
	// device replicates nothing.
	emitter Emitter
}

// NewService constructs a Service.
func NewService(q Querier, now func() time.Time, idgen func() string) *Service {
	return &Service{q: q, now: now, idgen: idgen}
}
