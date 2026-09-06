package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/uhhhm/reverb/internal/store/db"
)

// RecoverInvalidRemoteChanges quarantines copies corrupted by old receivers
// (which rewrote signed HLCs). It never signs on behalf of a remote author or
// accepts an invalid signature. Resetting the affected vector asks that author
// or a relay with a valid copy to retransmit the authentic facts.
func (s *SyncStore) RecoverInvalidRemoteChanges(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conn, ok := s.q.UnderlyingDB().(*sql.DB)
	if !ok || s.localDeviceID == "" {
		return 0, nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	ss := NewSyncStore(q)
	var revision int64
	count := 0
	for {
		rows, err := q.ListSyncChangesSince(ctx, db.ListSyncChangesSinceParams{Revision: revision, Limit: 1000})
		if err != nil {
			return 0, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			revision = row.Revision
			if row.DeviceID == s.localDeviceID || row.Sig == "" {
				continue
			}
			if !errors.Is(ss.VerifyChangeAuthorship(ctx, dbToSyncChange(row)), ErrBadSignature) {
				continue
			}
			payload, err := json.Marshal(row)
			if err != nil {
				return 0, err
			}
			if err := q.QuarantineSyncChange(ctx, row.Revision, string(payload), row.DeviceID); err != nil {
				return 0, err
			}
			count++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}
