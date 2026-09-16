package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/uhhhm/reverb/internal/store/db"
)

// RecoverUnusableChanges quarantines rows that cannot be used: copies corrupted
// by old receivers (which rewrote signed HLCs), and play records whose value is
// not valid JSON, stored by builds that accepted them before the sync boundary
// refused them. It never signs on behalf of a remote author or accepts an
// invalid signature.
//
// The two arms part company over what to ask for afterwards. Resetting a
// corrupt copy's vector asks its author, or a relay holding a valid copy, to
// retransmit the authentic facts; a malformed value has no authentic copy to
// ask for, so it is dropped without a reset. That arm is also the one that
// covers locally-authored rows: such a row can only come from a bug here, and
// it wedges the same reads a peer's would.
func (s *SyncStore) RecoverUnusableChanges(ctx context.Context) (int, error) {
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
			malformed := malformedPlayRecord(row.EntityType, row.Field, row.ValueJson)
			if !malformed {
				if row.DeviceID == s.localDeviceID || row.Sig == "" {
					continue
				}
				if !errors.Is(ss.VerifyChangeAuthorship(ctx, dbToSyncChange(row)), ErrBadSignature) {
					continue
				}
			}
			payload, err := json.Marshal(row)
			if err != nil {
				return 0, err
			}
			if malformed {
				err = q.QuarantineMalformedSyncChange(ctx, row.Revision, string(payload))
			} else {
				err = q.QuarantineSyncChange(ctx, row.Revision, string(payload), row.DeviceID)
			}
			if err != nil {
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

// malformedPlayRecord reports whether a stored change is a play that can never
// be read back: its projection decodes a struct, and the SQL that finds
// deferred plays calls json_extract on its value and raises. No retry changes
// that.
//
// It is deliberately narrower than the boundary check in reconcileInternal,
// which refuses any unparseable value, because this one decides what to
// delete. A stored row of another kind still projects through unmarshalValue's
// raw-string fallback -- a legacy unquoted title, say -- and dropping it would
// let an older value win instead.
func malformedPlayRecord(entityType, field, valueJSON string) bool {
	return entityType == EntityPlay && field == FieldRecord && malformedValueJSON(valueJSON)
}
