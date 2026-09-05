package sync

import (
	"context"
	"database/sql"
	"github.com/uhhhm/reverb/internal/store/db"
)

// SyncQuerier is the persistence required by reconciliation and local authorship.
// SQL queries and test adapters must implement the same operations. UnderlyingDB
// and WithTx retain SQLite transaction support; pairing has a separate Querier.
type SyncQuerier interface {
	ServerDeviceQuerier
	AppendSyncChangeWithHLC(context.Context, db.AppendSyncChangeWithHLCParams) (int64, error)
	ListSyncChangesSince(context.Context, db.ListSyncChangesSinceParams) ([]db.SyncChange, error)
	GetLatestSyncChangeForField(context.Context, db.GetLatestSyncChangeForFieldParams) (db.SyncChange, error)
	GetSyncCursor(context.Context, string) (db.SyncCursor, error)
	UpsertSyncCursor(context.Context, db.UpsertSyncCursorParams) error
	GetSyncVector(context.Context, string) (db.SyncVector, error)
	UpsertSyncVector(context.Context, db.UpsertSyncVectorParams) error
	ListUnsignedSyncChangesForDevice(context.Context, string) ([]db.SyncChange, error)
	UpdateSyncChangeSig(context.Context, db.UpdateSyncChangeSigParams) error
	GetMaxSyncRevision(context.Context) (interface{}, error)
	GetMaxHLC(context.Context) (interface{}, error)
	ListSyncChangesSinceHLC(context.Context, db.ListSyncChangesSinceHLCParams) ([]db.SyncChange, error)
	ListSyncVectors(context.Context) ([]db.SyncVector, error)
	UnderlyingDB() db.DBTX
	WithTx(*sql.Tx) *db.Queries
}

var _ SyncQuerier = (*db.Queries)(nil)
