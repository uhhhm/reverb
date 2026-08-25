package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/maxjb-xyz/reverb/internal/store/db"
)

// SyncStore provides changelog helpers around Querier.
// Querier is the pairing Querier (minimal seam); sync methods are accessed via
// type assertion on the underlying *db.Queries so we avoid extending Querier
// and keep device.go untouched. This handles the historical GetMaxSyncRevision
// return type (interface{} in generated code) transparently.
type SyncStore struct {
	q      Querier
	policy MergePolicy
}

// NewSyncStore creates a store with default LWWPolicy.
func NewSyncStore(q Querier) *SyncStore {
	return &SyncStore{q: q, policy: LWWPolicy{}}
}

// NewSyncStoreWithPolicy creates a store with a custom merge policy (for tests).
func NewSyncStoreWithPolicy(q Querier, p MergePolicy) *SyncStore {
	if p == nil {
		p = LWWPolicy{}
	}
	return &SyncStore{q: q, policy: p}
}

func marshalValue(ch SyncChange) (string, error) {
	if ch.Field == "__deleted" {
		return "true", nil
	}
	if ch.Value == nil {
		return "null", nil
	}
	b, err := json.Marshal(ch.Value)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalValue(field, valueJSON string) any {
	if field == "__deleted" {
		return nil
	}
	if valueJSON == "" || valueJSON == "null" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(valueJSON), &v); err != nil {
		return valueJSON
	}
	return v
}

func dbToSyncChange(row db.SyncChange) SyncChange {
	return SyncChange{
		EntityType: row.EntityType,
		EntityID:   row.EntityID,
		Field:      row.Field,
		Value:      unmarshalValue(row.Field, row.ValueJson),
		UpdatedAt:  row.UpdatedAt,
		DeviceID:   row.DeviceID,
		Revision:   row.Revision,
	}
}

// --- helpers that reach sync methods via type assertion ---

func (s *SyncStore) appendSyncChange(ctx context.Context, arg db.AppendSyncChangeParams) (int64, error) {
	if qq, ok := any(s.q).(interface {
		AppendSyncChange(context.Context, db.AppendSyncChangeParams) (int64, error)
	}); ok {
		return qq.AppendSyncChange(ctx, arg)
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		return dbq.AppendSyncChange(ctx, arg)
	}
	return 0, fmt.Errorf("querier does not support AppendSyncChange")
}

func (s *SyncStore) listSyncChangesSince(ctx context.Context, arg db.ListSyncChangesSinceParams) ([]db.SyncChange, error) {
	if qq, ok := any(s.q).(interface {
		ListSyncChangesSince(context.Context, db.ListSyncChangesSinceParams) ([]db.SyncChange, error)
	}); ok {
		return qq.ListSyncChangesSince(ctx, arg)
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		return dbq.ListSyncChangesSince(ctx, arg)
	}
	return nil, fmt.Errorf("querier does not support ListSyncChangesSince")
}

func (s *SyncStore) getMaxSyncRevision(ctx context.Context) (int64, error) {
	if qq, ok := any(s.q).(interface {
		GetMaxSyncRevision(context.Context) (int64, error)
	}); ok {
		return qq.GetMaxSyncRevision(ctx)
	}
	if qq, ok := any(s.q).(interface {
		GetMaxSyncRevision(context.Context) (interface{}, error)
	}); ok {
		v, err := qq.GetMaxSyncRevision(ctx)
		if err != nil {
			return 0, err
		}
		switch vv := v.(type) {
		case int64:
			return vv, nil
		case int:
			return int64(vv), nil
		case int32:
			return int64(vv), nil
		case int16:
			return int64(vv), nil
		case float64:
			return int64(vv), nil
		case nil:
			return 0, nil
		case string:
			// shouldn't happen
			return 0, nil
		default:
			return 0, fmt.Errorf("unexpected GetMaxSyncRevision type %T", v)
		}
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		// db.Queries currently returns interface{}
		v, err := dbq.GetMaxSyncRevision(ctx)
		if err != nil {
			return 0, err
		}
		switch vv := v.(type) {
		case int64:
			return vv, nil
		case int:
			return int64(vv), nil
		case nil:
			return 0, nil
		default:
			// try via reflection? fallback
			return 0, fmt.Errorf("unexpected GetMaxSyncRevision type %T", v)
		}
	}
	return 0, fmt.Errorf("querier does not support GetMaxSyncRevision")
}

func (s *SyncStore) getLatestSyncChangeForField(ctx context.Context, arg db.GetLatestSyncChangeForFieldParams) (db.SyncChange, error) {
	if qq, ok := any(s.q).(interface {
		GetLatestSyncChangeForField(context.Context, db.GetLatestSyncChangeForFieldParams) (db.SyncChange, error)
	}); ok {
		return qq.GetLatestSyncChangeForField(ctx, arg)
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		return dbq.GetLatestSyncChangeForField(ctx, arg)
	}
	return db.SyncChange{}, fmt.Errorf("querier does not support GetLatestSyncChangeForField")
}

func (s *SyncStore) getSyncCursor(ctx context.Context, deviceID string) (db.SyncCursor, error) {
	if qq, ok := any(s.q).(interface {
		GetSyncCursor(context.Context, string) (db.SyncCursor, error)
	}); ok {
		return qq.GetSyncCursor(ctx, deviceID)
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		return dbq.GetSyncCursor(ctx, deviceID)
	}
	return db.SyncCursor{}, fmt.Errorf("querier does not support GetSyncCursor")
}

func (s *SyncStore) upsertSyncCursor(ctx context.Context, arg db.UpsertSyncCursorParams) error {
	if qq, ok := any(s.q).(interface {
		UpsertSyncCursor(context.Context, db.UpsertSyncCursorParams) error
	}); ok {
		return qq.UpsertSyncCursor(ctx, arg)
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		return dbq.UpsertSyncCursor(ctx, arg)
	}
	return fmt.Errorf("querier does not support UpsertSyncCursor")
}

// AppendChange appends a single change for deviceID. Value is marshaled to value_json.
func (s *SyncStore) AppendChange(ctx context.Context, deviceID string, ch SyncChange) (int64, error) {
	valueJSON, err := marshalValue(ch)
	if err != nil {
		return 0, err
	}
	return s.appendSyncChange(ctx, db.AppendSyncChangeParams{
		DeviceID:   deviceID,
		EntityType: ch.EntityType,
		EntityID:   ch.EntityID,
		Field:      ch.Field,
		ValueJson:  valueJSON,
		UpdatedAt:  ch.UpdatedAt,
	})
}

// ListSince returns changes with revision > since, ordered by revision ASC.
func (s *SyncStore) ListSince(ctx context.Context, since int64, limit int64) ([]SyncChange, error) {
	if limit <= 0 {
		limit = 10000
	}
	rows, err := s.listSyncChangesSince(ctx, db.ListSyncChangesSinceParams{
		Revision: since,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]SyncChange, 0, len(rows))
	for _, r := range rows {
		out = append(out, dbToSyncChange(r))
	}
	if out == nil {
		out = []SyncChange{}
	}
	return out, nil
}

// GetMaxRevision returns the current global revision (0 if none).
func (s *SyncStore) GetMaxRevision(ctx context.Context) (int64, error) {
	return s.getMaxSyncRevision(ctx)
}

// GetLatestForField returns the latest change for entityType+entityId+field, or nil if none.
func (s *SyncStore) GetLatestForField(ctx context.Context, entityType, entityID, field string) (*SyncChange, error) {
	row, err := s.getLatestSyncChangeForField(ctx, db.GetLatestSyncChangeForFieldParams{
		EntityType: entityType,
		EntityID:   entityID,
		Field:      field,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	sc := dbToSyncChange(row)
	return &sc, nil
}

// GetCursor returns the stored cursor revision for deviceID (0 if none).
func (s *SyncStore) GetCursor(ctx context.Context, deviceID string) (int64, error) {
	cur, err := s.getSyncCursor(ctx, deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return cur.Revision, nil
}

// SetCursor upserts the cursor for deviceID.
func (s *SyncStore) SetCursor(ctx context.Context, deviceID string, rev int64) error {
	return s.upsertSyncCursor(ctx, db.UpsertSyncCursorParams{
		DeviceID: deviceID,
		Revision: rev,
	})
}

// isServer checks if deviceID belongs to a server device; lookup failure => false.
func (s *SyncStore) isServer(ctx context.Context, deviceID string) bool {
	if deviceID == "" {
		return false
	}
	dev, err := s.q.GetDeviceByID(ctx, deviceID)
	if err != nil {
		return false
	}
	return dev.IsServer == 1
}

// Reconcile applies inbound changes per-field LWW with delete-wins and deterministic tie-breakers,
// then returns outbound changes (revision > sinceRev) and new revision.
// Delete-wins: a __deleted tombstone always wins over a concurrent field edit, irrespective of UpdatedAt.
// When both sides are __deleted, LWW decides. Otherwise LWW via MergePolicy.
// Tie on UpdatedAt -> server wins, then deviceId lex order.
func (s *SyncStore) Reconcile(ctx context.Context, deviceID string, sinceRev int64, inbound []SyncChange) (outbound []SyncChange, newRev int64, rejected []SyncChange, err error) {
	policy := s.policy
	if policy == nil {
		policy = LWWPolicy{}
	}

	prevLookup := deviceIsServerLookup
	deviceIsServerLookup = func(id string) bool {
		return s.isServer(ctx, id)
	}
	defer func() { deviceIsServerLookup = prevLookup }()

	for _, inc := range inbound {
		originDevice := inc.DeviceID
		if originDevice == "" {
			originDevice = deviceID
		}
		inc.DeviceID = originDevice

		if inc.Field != "__deleted" {
			tomb, terr := s.GetLatestForField(ctx, inc.EntityType, inc.EntityID, "__deleted")
			if terr != nil {
				return nil, 0, nil, terr
			}
			if tomb != nil {
				rejected = append(rejected, inc)
				continue
			}
		}

		existing, err := s.GetLatestForField(ctx, inc.EntityType, inc.EntityID, inc.Field)
		if err != nil {
			return nil, 0, nil, err
		}
		if existing == nil {
			if _, aerr := s.AppendChange(ctx, originDevice, inc); aerr != nil {
				return nil, 0, nil, aerr
			}
			continue
		}

		isExistingDeleted := existing.Field == "__deleted"
		isIncomingDeleted := inc.Field == "__deleted"

		if isExistingDeleted && !isIncomingDeleted {
			rejected = append(rejected, inc)
			continue
		}
		if !isExistingDeleted && isIncomingDeleted {
			if _, aerr := s.AppendChange(ctx, originDevice, inc); aerr != nil {
				return nil, 0, nil, aerr
			}
			continue
		}
		if existing.UpdatedAt == inc.UpdatedAt {
			exSrv := s.isServer(ctx, existing.DeviceID)
			incSrv := s.isServer(ctx, inc.DeviceID)
			if exSrv != incSrv {
				if exSrv {
					rejected = append(rejected, inc)
					continue
				}
				if _, aerr := s.AppendChange(ctx, originDevice, inc); aerr != nil {
					return nil, 0, nil, aerr
				}
				continue
			}
		}
		if policy.PickWinner(*existing, inc) {
			if _, aerr := s.AppendChange(ctx, originDevice, inc); aerr != nil {
				return nil, 0, nil, aerr
			}
		} else {
			rejected = append(rejected, inc)
		}
	}

	outbound, err = s.ListSince(ctx, sinceRev, 10000)
	if err != nil {
		return nil, 0, nil, err
	}
	newRev, err = s.GetMaxRevision(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	if cerr := s.SetCursor(ctx, deviceID, newRev); cerr != nil {
		return nil, 0, nil, cerr
	}
	if rejected == nil {
		rejected = []SyncChange{}
	}
	return outbound, newRev, rejected, nil
}
