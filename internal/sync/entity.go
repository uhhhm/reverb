package sync

import (
	"context"
	"fmt"

	"github.com/uhhhm/reverb/internal/store/db"
)

// ListLatestForEntity returns the current winning change for every field of one
// entity — the entity's whole state as the log knows it.
//
// A projection that is a function of several fields at once (a playlist's name,
// settings and every track membership) cannot be rebuilt from the single change
// that just arrived, so it reads the entity back instead.
func (s *SyncStore) ListLatestForEntity(ctx context.Context, entityType, entityID string) ([]SyncChange, error) {
	arg := db.ListLatestSyncFieldsForEntityParams{EntityType: entityType, EntityID: entityID}
	var rows []db.SyncChange
	var err error
	switch qq := any(s.q).(type) {
	case interface {
		ListLatestSyncFieldsForEntity(context.Context, db.ListLatestSyncFieldsForEntityParams) ([]db.SyncChange, error)
	}:
		rows, err = qq.ListLatestSyncFieldsForEntity(ctx, arg)
	default:
		return nil, fmt.Errorf("querier does not support ListLatestSyncFieldsForEntity")
	}
	if err != nil {
		return nil, err
	}
	out := make([]SyncChange, 0, len(rows))
	for _, r := range rows {
		out = append(out, dbToSyncChange(r))
	}
	return out, nil
}
