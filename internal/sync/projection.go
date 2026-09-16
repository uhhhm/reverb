package sync

import (
	"context"
	"log"
	"time"

	"github.com/uhhhm/reverb/internal/store/db"
)

// RecoverProjection retries accepted changes whose projection did not finish.
func (s *SyncStore) RecoverProjection(ctx context.Context) error {
	var revision int64
	var first error
	for {
		rows, err := s.q.ListPendingSyncProjections(ctx, db.ListPendingSyncProjectionsParams{Revision: revision, Limit: 1000})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return first
		}
		batch := make([]SyncChange, 0, len(rows))
		for _, row := range rows {
			batch = append(batch, dbToSyncChange(row))
			revision = row.Revision
		}
		if err := s.materialize(ctx, batch); err != nil && first == nil {
			first = err
		}
	}
}

// RunProjectionRecovery retries transient storage/dependency failures and
// resumes work whose log committed before an earlier process exited.
func (s *SyncStore) RunProjectionRecovery(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.RecoverProjection(ctx); err != nil && ctx.Err() == nil {
			log.Printf("sync: retry projection: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
